import path from "node:path";
import { resolveSwiftWorkspace, sourcekitConfigHome } from "./swift-workspace";

/**
 * The language servers this app knows how to run.
 *
 * A TABLE, deliberately, so slice 7 (tsserver) stays data rather than
 * architecture. Returning a spec we cannot actually serve would spawn a process
 * that answers nothing while looking healthy, which is the failure mode this
 * whole area exists to make impossible - so a language is in this table only
 * once its server can be driven end to end.
 */

/**
 * Where a prepared server is rooted, and where its documents live.
 *
 * 🗝 `lspRoot` and `documentRoot` are the same directory for every language
 * except Swift, and separating them is not tidiness. sourcekit-lsp on an Xcode
 * project is rooted in a SHADOW directory under the AO data dir (the alternative
 * is writing `buildServer.json` into the user's checkout), and it will not serve
 * a document that lies outside its root - so documents are addressed through a
 * symlink inside that shadow root. Measured on the real iOS app: address them by
 * their real path instead and every ⌘click returns 0 hits in ~60 ms with no
 * error, while `workspace/symbol` keeps working perfectly.
 */
export type PreparedWorkspace = {
	lspRoot: string;
	documentRoot: string;
	/** Shown in the health panel and the editor's status pill. */
	detail?: string;
	/** Configured enough to start, but a feature will find nothing. Say which. */
	warning?: string;
};

export type WorkspacePreparation = ({ ok: true } & PreparedWorkspace) | { ok: false; reason: string };

/**
 * How this server says it has finished loading its index.
 *
 * `progress` - the server announces its work with `$/progress`, and readiness is
 * "no work outstanding". This is gopls.
 *
 * `synchronize` - the server announces NOTHING and has to be ASKED.
 * 🗝 Measured on sourcekit-lsp against the real iOS app: not one `$/progress`,
 * not one `window/workDoneProgress/create`, no `serverInfo`, in 45 s of
 * listening - while `workspace/symbol` returned one or two hits, none of them
 * the right one, for the first 3.6 s. A progress-driven gate is BLIND here.
 * `workspace/synchronize { index: true }` blocks until the index is loaded
 * (6.15 s on that project) and the first query after it is correct.
 */
export type IndexReadiness = "progress" | "synchronize";

export type LanguageServerSpec = {
	languageId: string;
	command: string;
	args: (opts: { dataDir: string }) => string[];
	/** Lowercase, with the leading dot. */
	extensions: string[];
	/** Extra env for the child, merged over the resolved login-shell env. */
	env: (opts: { dataDir: string; env: NodeJS.ProcessEnv }) => Record<string, string>;
	indexReadiness: IndexReadiness;
	/**
	 * An out-of-tree helper process this server's cost really lives in.
	 *
	 * 🗝 sourcekit-lsp does its type checking in a per-client `SourceKitService`
	 * XPC service that carries `ppid=1`, its own process group and session 0 - so
	 * it is invisible to any tree walk, and measuring only the server under-reports
	 * by ~3x (207 MB reported, 390 MB unreported, per server, on the real iOS app).
	 * The spike's published 246 MB counted the visible process alone.
	 */
	sidecarCommand?: string;
	/**
	 * Decide whether this language can be served in this workspace AT ALL, before
	 * anything is spawned, and shape the root if so. Absent means "the workspace
	 * root is the answer", which is every language but Swift.
	 */
	prepare?: (opts: { workspaceRoot: string; dataDir: string; env: NodeJS.ProcessEnv }) => WorkspacePreparation;
};

/** Everything a server caches lives under `<dataDir>/lsp/…`. AO hard rule. */
export const LSP_CACHE_SUBDIR = "lsp";

/**
 * Measured in the editor spike (§2.2): gopls holds ~880 MB of live types for
 * this repo's dependency closure but lets RSS drift to ~1.7 GB. GOMEMLIMIT does
 * not shrink the live data, it stops the Go runtime hoarding freed pages -
 * ~1.0 GB instead of ~1.7 GB, for ~2.5x the CPU.
 *
 * It MUST stay above the workspace's live heap. A limit below it is untested and
 * may thrash rather than degrade gracefully, which is why this is an env
 * override rather than a UI knob.
 */
export const DEFAULT_GOMEMLIMIT = "1GiB";

/**
 * 🗝 There is NO Swift equivalent of GOMEMLIMIT, and this is where to say so
 * rather than leave it looking like an oversight. sourcekit-lsp and
 * SourceKitService are Swift binaries with no heap-limit knob, and
 * `sourcekit-lsp --help` offers nothing of the kind. The only bound on Swift is
 * the registry's server cap plus the idle stop.
 *
 * That turns out not to make the ceiling worse. Measured per server, settled, on
 * the real iOS app: sourcekit-lsp 207 MB + xcode-build-server 19 MB +
 * SourceKitService 390 MB = ~620 MB, against gopls at ~1 790 MB WITH
 * GOMEMLIMIT=1GiB. Two servers is ~3.6 GB for two Go modules, ~2.4 GB for one of
 * each and ~1.24 GB for two Swift workspaces: Go still sets the ceiling.
 */
export const SWIFT_MEMORY_BOUND = null;

const SERVERS: LanguageServerSpec[] = [
	{
		languageId: "go",
		command: "gopls",
		args: () => ["-mode=stdio"],
		extensions: [".go"],
		indexReadiness: "progress",
		env: ({ dataDir, env }) => ({
			// gopls defaults to ~/Library/Caches/gopls, an OS app-data location this
			// app may not touch. Verified in the spike: setting this moves every byte
			// under ~/.ao and leaves the default cache's mtime alone.
			GOPLSCACHE: path.join(dataDir, LSP_CACHE_SUBDIR, "gopls"),
			GOMEMLIMIT: env.AO_LSP_GOMEMLIMIT?.trim() || DEFAULT_GOMEMLIMIT,
		}),
	},
	{
		languageId: "swift",
		command: "sourcekit-lsp",
		// Both of these default INTO the user's checkout (`<workspace>/.build` and
		// generated interfaces beside the sources). Verified in the spike that they
		// move, and verified here that nothing is written to the checkout while
		// they are set.
		args: ({ dataDir }) => [
			"--scratch-path",
			path.join(dataDir, LSP_CACHE_SUBDIR, "swift", "scratch"),
			"--generated-files-path",
			path.join(dataDir, LSP_CACHE_SUBDIR, "swift", "generated"),
		],
		extensions: [".swift"],
		// Objective-C is deliberately NOT here. sourcekit-lsp serves .m/.h too, but
		// the registry keys servers by languageId, so claiming them would spawn a
		// SECOND sourcekit-lsp for one workspace. Doing it properly needs the
		// catalogue to separate "which process" from "which documents", which is a
		// change for the slice that needs it. ⌘click INTO Objective-C from Swift
		// already works and opens the file read-only.
		indexReadiness: "synchronize",
		sidecarCommand: "SourceKitService",
		// The only way to reach sourcekit-lsp's own settings. `prepare` writes the
		// file; this points the server at it. See `writeSourcekitConfig` for why
		// the one setting in it is a hard-rule fix rather than a preference.
		env: ({ dataDir }) => ({ XDG_CONFIG_HOME: sourcekitConfigHome(dataDir) }),
		prepare: ({ workspaceRoot, dataDir, env }) => {
			const resolved = resolveSwiftWorkspace({ workspaceRoot, dataDir, env });
			if (resolved.kind === "unconfigured") return { ok: false, reason: resolved.reason };
			return {
				ok: true,
				lspRoot: resolved.lspRoot,
				documentRoot: resolved.documentRoot,
				detail: resolved.detail,
				warning: resolved.warning,
			};
		},
	},
];

/**
 * The spec for a language, or null when this app ships no server for it.
 *
 * `AO_LSP_COMMAND_<LANG>` / `AO_LSP_ARGS_<LANG>` override which binary is
 * spawned. The tests use it to exercise the registry's lifecycle policy against
 * a fake server on a machine with no gopls - but it is read from the resolved
 * LOGIN-SHELL env like everything else here, so a user with gopls somewhere
 * unusual can set it deliberately. That is the same trust level as `PATH`
 * already carries, and it is stated rather than described as test-only, because
 * nothing enforces test-only.
 */
export function serverForLanguage(languageId: string, env: NodeJS.ProcessEnv = {}): LanguageServerSpec | null {
	const spec = SERVERS.find((s) => s.languageId === languageId);
	if (!spec) return null;
	const upper = languageId.toUpperCase();
	const command = env[`AO_LSP_COMMAND_${upper}`];
	if (!command) return spec;
	const args = env[`AO_LSP_ARGS_${upper}`];
	const overridden = args ? args.split(" ").filter(Boolean) : [];
	return { ...spec, command, args: () => overridden };
}

/** The language id for a path, or null when no server in the table claims it. */
export function languageIdForPath(filePath: string): string | null {
	const base = filePath.slice(filePath.lastIndexOf("/") + 1).toLowerCase();
	const dot = base.lastIndexOf(".");
	if (dot <= 0) return null;
	const ext = base.slice(dot);
	return SERVERS.find((s) => s.extensions.includes(ext))?.languageId ?? null;
}

/** Every language this app can serve, for callers choosing one from a file index. */
export function servedLanguages(): { languageId: string; extensions: string[] }[] {
	return SERVERS.map((s) => ({ languageId: s.languageId, extensions: [...s.extensions] }));
}
