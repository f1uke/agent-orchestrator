import path from "node:path";

/**
 * The language servers this app knows how to run.
 *
 * A TABLE, deliberately, so slice 5 (SourceKit-LSP) and slice 7 (tsserver) are
 * data rather than architecture. Only Go ships today: returning a spec we cannot
 * actually serve would spawn a process that answers nothing while looking
 * healthy, which is the failure mode this whole slice exists to make impossible.
 */
export type LanguageServerSpec = {
	languageId: string;
	command: string;
	args: string[];
	/** Lowercase, with the leading dot. */
	extensions: string[];
	/** Extra env for the child, merged over the resolved login-shell env. */
	env: (opts: { dataDir: string; env: NodeJS.ProcessEnv }) => Record<string, string>;
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

const SERVERS: LanguageServerSpec[] = [
	{
		languageId: "go",
		command: "gopls",
		args: ["-mode=stdio"],
		extensions: [".go"],
		env: ({ dataDir, env }) => ({
			// gopls defaults to ~/Library/Caches/gopls, an OS app-data location this
			// app may not touch. Verified in the spike: setting this moves every byte
			// under ~/.ao and leaves the default cache's mtime alone.
			GOPLSCACHE: path.join(dataDir, LSP_CACHE_SUBDIR, "gopls"),
			GOMEMLIMIT: env.AO_LSP_GOMEMLIMIT?.trim() || DEFAULT_GOMEMLIMIT,
		}),
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
	return { ...spec, command, args: args ? args.split(" ").filter(Boolean) : [] };
}

/** The language id for a path, or null when no server in the table claims it. */
export function languageIdForPath(filePath: string): string | null {
	const base = filePath.slice(filePath.lastIndexOf("/") + 1).toLowerCase();
	const dot = base.lastIndexOf(".");
	if (dot <= 0) return null;
	const ext = base.slice(dot);
	return SERVERS.find((s) => s.extensions.includes(ext))?.languageId ?? null;
}
