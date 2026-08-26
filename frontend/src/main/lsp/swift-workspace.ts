import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

/**
 * Everything Xcode-shaped about running SourceKit-LSP, kept out of the process
 * supervisor so it can be tested without spawning anything.
 *
 * 🗝 THE WHOLE POINT OF THIS FILE is that an UNCONFIGURED sourcekit-lsp looks
 * completely healthy while answering nothing. Pointed at a real `.xcodeproj`
 * with no build settings it initializes in ~60 ms, publishes diagnostics and
 * answers `documentSymbol` - and returns 0 hits for every ⌘click and 0 results
 * for every symbol query, with no error anywhere. So this module's job is to
 * decide BEFORE a process is spawned whether there is anything to serve, and to
 * hand back a reason a person can act on when there is not.
 */

/** Where a language server is rooted, and where its documents must be addressed. */
export type SwiftWorkspace =
	| {
			kind: "buildServer";
			/** The shadow root under the AO data dir: cwd and `rootUri`. */
			lspRoot: string;
			/** Documents must be addressed under HERE, not under the checkout. */
			documentRoot: string;
			detail: string;
			/** Set when there are compile args but no index: ⌘click works, symbols will not. */
			warning?: string;
	  }
	| { kind: "swiftpm"; lspRoot: string; documentRoot: string; detail: string; warning: string }
	| { kind: "unconfigured"; reason: string };

export type SwiftWorkspaceOptions = {
	/** The session's worktree, absolute. */
	workspaceRoot: string;
	/** `AO_DATA_DIR`. Everything this module writes lands under it. */
	dataDir: string;
	env: NodeJS.ProcessEnv;
	/** Overridable so tests can point at a fake tree. */
	derivedDataDir?: string;
};

/** The symlink inside the shadow root that points at the user's checkout. */
export const SHADOW_LINK = "wt";

/** Everything Swift caches lives under `<dataDir>/lsp/swift/…`. AO hard rule. */
export const SWIFT_SUBDIR = path.join("lsp", "swift");

/**
 * 🗝 `xcode-build-server` hardcodes `~/Library/Caches/xcode-build-server` and has
 * NO env override (`server.py:390`), then hands that path to sourcekit-lsp as
 * `indexDatabasePath` - measured at 211 MB per workspace, in an OS app-data
 * location this app may not touch. The editor spike's §3.5 table records
 * "nothing written anywhere", which is simply wrong, and the spike left 422 MB
 * there.
 *
 * The fix is to scope HOME to the BSP CHILD, through the `argv` in the
 * buildServer.json we write ourselves. Scoping it to sourcekit-lsp instead would
 * be wrong: sourcekit-lsp legitimately reads `~/Library/Developer` for the
 * toolchain and the index store.
 */
export const XBS_HOME_SUBDIR = "xbs-home";

/** `<configHome>/sourcekit-lsp/config.json` is where sourcekit-lsp looks. */
export const SKL_CONFIG_SUBDIR = "config";

/**
 * sourcekit-lsp's own config, and the one knob on it that is not optional.
 *
 * 🗝 `backgroundIndexing: false` is a HARD-RULE fix, not a performance choice.
 * On a SwiftPM package, sourcekit-lsp's background indexer runs a real build and
 * writes `.build/index-build/` INTO THE PACKAGE - measured, and it ignores
 * `--scratch-path` and `swiftPM.scratchPath` while doing it. AO may not write
 * into a user's checkout, so it is turned off; verified that the package then
 * stays clean and cross-file ⌘click still resolves in under a second.
 *
 * The price is stated rather than hidden: a Swift PACKAGE gets ⌘click and no
 * symbol search, because symbol search is what the background index was for. An
 * Xcode project is unaffected - its index comes from DerivedData, and all four
 * ⌘click targets plus symbol search were verified with this setting on.
 *
 * ⚠️ And this file CANNOT carry `index.indexDatabasePath`: measured on the real
 * iOS app, the build server's value wins and 39 MB landed in
 * `~/Library/Caches/xcode-build-server` anyway. That is what the HOME shim
 * above is for. The editor spike found the same thing for a compilation-database
 * workspace; it holds for a build-server workspace too.
 */
export function writeSourcekitConfig(dataDir: string): string {
	const configHome = path.join(dataDir, SWIFT_SUBDIR, SKL_CONFIG_SUBDIR);
	const dir = path.join(configHome, "sourcekit-lsp");
	fs.mkdirSync(dir, { recursive: true });
	fs.writeFileSync(path.join(dir, "config.json"), `${JSON.stringify({ backgroundIndexing: false }, null, 1)}\n`);
	return configHome;
}

/** Where `XDG_CONFIG_HOME` must point for the config above to be read. */
export function sourcekitConfigHome(dataDir: string): string {
	return path.join(dataDir, SWIFT_SUBDIR, SKL_CONFIG_SUBDIR);
}

export const SWIFTPM_NO_SYMBOLS =
	"Symbol search is off for Swift packages: sourcekit-lsp only indexes them by building into the package itself, and AO does not write to your checkout. Go to definition works.";

function shadowRootFor(dataDir: string, workspaceRoot: string): string {
	// Hashed rather than path-mangled: an AO worktree path is long enough that
	// `-`-joining it produces a directory name near the filesystem's limit.
	const id = createHash("sha1").update(workspaceRoot).digest("hex").slice(0, 16);
	return path.join(dataDir, SWIFT_SUBDIR, id);
}

function readdirSafe(dir: string): string[] {
	try {
		return fs.readdirSync(dir);
	} catch {
		return [];
	}
}

/**
 * The Xcode container to bind to. A `.xcworkspace` wins over a `.xcodeproj`:
 * with CocoaPods the project alone does not know about the Pods targets, so
 * binding it produces exactly the half-configured server this module exists to
 * prevent.
 */
export function findXcodeContainer(workspaceRoot: string): string | null {
	const entries = readdirSafe(workspaceRoot);
	const workspace = entries.filter((e) => e.endsWith(".xcworkspace")).sort()[0];
	if (workspace) return path.join(workspaceRoot, workspace);
	const project = entries.filter((e) => e.endsWith(".xcodeproj")).sort()[0];
	return project ? path.join(workspaceRoot, project) : null;
}

/** The user's DerivedData, unless a test points somewhere else. */
export function defaultDerivedDataDir(env: NodeJS.ProcessEnv): string {
	return path.join(env.HOME ?? "", "Library", "Developer", "Xcode", "DerivedData");
}

/**
 * The DerivedData directory Xcode built THIS checkout into.
 *
 * 🗝 Resolved by `info.plist`'s `WorkspacePath`, not by "the newest
 * `.xcactivitylog` on this machine". The editor spike used the latter and it
 * picked a DIFFERENT branch's checkout (#246 §1.1) - which on a machine like
 * this one, with a dozen AO worktrees of the same iOS app each with its own
 * DerivedData, is not an edge case but the normal state.
 */
export function findBuildRoot(containerPath: string, derivedDataDir: string): string | null {
	for (const name of readdirSafe(derivedDataDir)) {
		const dir = path.join(derivedDataDir, name);
		let plist: string;
		try {
			plist = fs.readFileSync(path.join(dir, "info.plist"), "utf8");
		} catch {
			continue;
		}
		const match = /<key>WorkspacePath<\/key>\s*<string>([^<]*)<\/string>/.exec(plist);
		if (match?.[1] === containerPath) return dir;
	}
	return null;
}

/**
 * The BSP binary. `xcode-build-server` is a separate MIT project (pure Python,
 * ~1 800 lines of accumulated `.xcactivitylog` edge cases); this app depends on
 * it rather than reimplementing it, so its absence is a first-class, ACTIONABLE
 * state rather than a server that spawns and answers nothing.
 */
export function findXcodeBuildServer(env: NodeJS.ProcessEnv): string | null {
	const override = env.AO_LSP_XCODE_BUILD_SERVER?.trim();
	if (override) return fs.existsSync(override) ? override : null;
	const candidates = [
		...(env.PATH ?? "")
			.split(":")
			.filter(Boolean)
			.map((dir) => path.join(dir, "xcode-build-server")),
		"/opt/homebrew/bin/xcode-build-server",
		"/usr/local/bin/xcode-build-server",
		"/opt/local/bin/xcode-build-server",
	];
	for (const candidate of candidates) {
		try {
			if (fs.statSync(candidate).isFile()) return candidate;
		} catch {
			// not there; try the next one
		}
	}
	return null;
}

export const INSTALL_XCODE_BUILD_SERVER =
	"Swift needs xcode-build-server to read this project's build settings. Install it with `brew install xcode-build-server`.";

/**
 * Create (or repair) the shadow root, and return the directory documents must be
 * addressed under.
 *
 * 🗝 THE SYMLINK IS LOAD-BEARING AND SO IS USING IT. sourcekit-lsp will not
 * serve a document that lies outside its workspace root - a shadow root whose
 * sources are referenced by absolute path gets 0 hits on every ⌘click while
 * `workspace/symbol` keeps working perfectly, because symbols come from the
 * index store and never touch the document's URI. Measured on the real iOS app,
 * both halves. The link puts the document INSIDE the root; sourcekit-lsp
 * resolves it for its replies, so definitions come back as real paths and
 * nothing has to be translated on the way in.
 */
export function writeShadowRoot(input: {
	shadowRoot: string;
	workspaceRoot: string;
	containerPath: string;
	buildRoot: string;
	buildServerCommand: string;
	xbsHome: string;
}): string {
	const { shadowRoot, workspaceRoot, containerPath, buildRoot, buildServerCommand, xbsHome } = input;
	fs.mkdirSync(shadowRoot, { recursive: true });
	fs.mkdirSync(xbsHome, { recursive: true });

	const link = path.join(shadowRoot, SHADOW_LINK);
	// Repaired rather than assumed: a worktree can be deleted and recreated at the
	// same path, and a dangling link is the one failure here that would look like
	// a language-server problem rather than a filesystem one.
	let current: string | null = null;
	try {
		current = fs.readlinkSync(link);
	} catch {
		current = null;
	}
	if (current !== workspaceRoot) {
		try {
			fs.unlinkSync(link);
		} catch {
			// nothing there yet
		}
		fs.symlinkSync(workspaceRoot, link);
	}

	fs.writeFileSync(
		path.join(shadowRoot, "buildServer.json"),
		`${JSON.stringify(
			{
				name: "xcode build server",
				version: "1.3.0",
				bspVersion: "2.2.0",
				languages: ["c", "cpp", "objective-c", "objective-cpp", "swift"],
				// See XBS_HOME_SUBDIR: the only way to keep the BSP's 200 MB index
				// database out of ~/Library/Caches.
				argv: ["/usr/bin/env", `HOME=${xbsHome}`, buildServerCommand],
				// 🗝 `xcode` rather than `manual`: the BSP finds and parses the newest
				// .xcactivitylog itself, derives indexStorePath from build_root, and
				// RE-PARSES when the user next builds in Xcode. `manual` would freeze
				// the compile args at whatever the build was when the editor opened.
				// It also means this shadow root holds no `.compile` and no filelists -
				// the 10 MB of rewritten paths the spike prescribed turned out to be
				// unnecessary (sourcekit-lsp resolves the link before asking the BSP).
				kind: "xcode",
				workspace: containerPath,
				build_root: buildRoot,
			},
			null,
			1,
		)}\n`,
	);
	return path.join(shadowRoot, SHADOW_LINK);
}

/**
 * What kind of Swift workspace this is, and whether it can be served at all.
 *
 * Every `unconfigured` reason is a sentence a person can act on, because the
 * alternative - spawning anyway - produces a server that looks healthy and
 * answers nothing.
 */
export function resolveSwiftWorkspace(options: SwiftWorkspaceOptions): SwiftWorkspace {
	const { workspaceRoot, dataDir, env } = options;
	const derivedDataDir = options.derivedDataDir ?? defaultDerivedDataDir(env);

	const container = findXcodeContainer(workspaceRoot);
	if (!container) {
		// No Xcode container: a plain SwiftPM package is the other shape
		// sourcekit-lsp serves natively, with no build server and no shadow root.
		if (fs.existsSync(path.join(workspaceRoot, "Package.swift"))) {
			writeSourcekitConfig(dataDir);
			return {
				kind: "swiftpm",
				lspRoot: workspaceRoot,
				documentRoot: workspaceRoot,
				detail: "SwiftPM package",
				warning: SWIFTPM_NO_SYMBOLS,
			};
		}
		return {
			kind: "unconfigured",
			reason: "No .xcworkspace, .xcodeproj or Package.swift here, so there are no Swift build settings to read.",
		};
	}

	const buildServerCommand = findXcodeBuildServer(env);
	if (!buildServerCommand) return { kind: "unconfigured", reason: INSTALL_XCODE_BUILD_SERVER };

	const buildRoot = findBuildRoot(container, derivedDataDir);
	if (!buildRoot) {
		return {
			kind: "unconfigured",
			reason:
				`Xcode has never built ${path.basename(container)} from this worktree, so there are no compile settings to read. ` +
				"Build it in Xcode once and reopen this file.",
		};
	}

	writeSourcekitConfig(dataDir);
	const shadowRoot = shadowRootFor(dataDir, workspaceRoot);
	const documentRoot = writeShadowRoot({
		shadowRoot,
		workspaceRoot,
		containerPath: container,
		buildRoot,
		buildServerCommand,
		xbsHome: path.join(dataDir, SWIFT_SUBDIR, XBS_HOME_SUBDIR),
	});

	// A build with no index store is a REAL and reachable half-state: ⌘click needs
	// compile arguments, symbol search needs the index, and they are produced by
	// different halves of a build. Saying which half is missing beats an empty
	// symbol list.
	const hasIndex = fs.existsSync(path.join(buildRoot, "Index.noindex", "DataStore"));
	return {
		kind: "buildServer",
		lspRoot: shadowRoot,
		documentRoot,
		detail: `Xcode build settings from ${path.basename(buildRoot)}`,
		warning: hasIndex
			? undefined
			: "This Xcode build produced no index, so symbol search will find nothing until the project is built again.",
	};
}
