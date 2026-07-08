import { spawn } from "node:child_process";
import { readdir as fsReaddir } from "node:fs/promises";
import path from "node:path";

// xcodegen spec files are named exactly this. A project can be multi-module with
// several of them at different depths, so the whole tree is searched.
const SPEC_FILENAME = "project.yml";
const XCODEGEN_BIN = "xcodegen";

// Heavy build/dependency directories that never hold a hand-authored spec and
// would only slow the walk. Dot-directories (.git, .claude/worktrees, …) are
// skipped separately in shouldSkipDir.
export const IGNORED_DIR_NAMES = new Set(["node_modules", "Pods", "Carthage", "DerivedData", "build", ".build"]);

/** Whether the recursive search should skip a subdirectory by name. */
export function shouldSkipDir(name: string): boolean {
	return name.startsWith(".") || IGNORED_DIR_NAMES.has(name);
}

// Only the fields the walk needs; node's Dirent satisfies this.
export type ReadDir = (dir: string) => Promise<Array<{ name: string; isDirectory: () => boolean }>>;

const defaultReadDir: ReadDir = (dir) => fsReaddir(dir, { withFileTypes: true });

/**
 * Recursively find every directory under `root` that directly contains a
 * `project.yml`, skipping noise/heavy directories (see {@link shouldSkipDir}).
 * Unreadable directories are skipped quietly. Returns absolute paths in a stable
 * (sorted) order.
 */
export async function findProjectSpecDirs(root: string, readdir: ReadDir = defaultReadDir): Promise<string[]> {
	const found: string[] = [];

	async function walk(dir: string): Promise<void> {
		let entries: Awaited<ReturnType<ReadDir>>;
		try {
			entries = await readdir(dir);
		} catch {
			return;
		}
		const subdirs: string[] = [];
		let hasSpec = false;
		for (const entry of entries) {
			if (entry.isDirectory()) {
				if (!shouldSkipDir(entry.name)) subdirs.push(path.join(dir, entry.name));
			} else if (entry.name === SPEC_FILENAME) {
				hasSpec = true;
			}
		}
		if (hasSpec) found.push(dir);
		for (const sub of subdirs) await walk(sub);
	}

	await walk(root);
	found.sort();
	return found;
}

export type XcodegenDirResult = {
	/** Path relative to the searched root ("." for the root itself). */
	dir: string;
	ok: boolean;
	exitCode: number | null;
	/** Combined stdout+stderr, trimmed. */
	output: string;
};

export type RunXcodegenResult =
	| { status: "not-installed" }
	| { status: "no-specs"; root: string }
	| { status: "ran"; root: string; results: XcodegenDirResult[] };

// A single directory's outcome: either the binary was missing (ENOENT), or it
// ran and produced an exit code + output.
type RunOneOutcome = { notFound: true } | { ok: boolean; exitCode: number | null; output: string };

export type RunOne = (dir: string, env: NodeJS.ProcessEnv) => Promise<RunOneOutcome>;

export type RunXcodegenOptions = {
	/** Env for the spawned command; its PATH must resolve the `xcodegen` binary. */
	env: NodeJS.ProcessEnv;
	readdir?: ReadDir;
	runOne?: RunOne;
};

function isEnoent(error: unknown): boolean {
	return typeof error === "object" && error !== null && (error as { code?: string }).code === "ENOENT";
}

function errorText(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

// Spawn `xcodegen generate` in `dir`. Never rejects: resolves { notFound: true }
// when the binary is missing (ENOENT) so the caller can report a friendly
// "not installed" message, and otherwise resolves the exit code + captured
// output so a failed generate is a surfaced result rather than a crash.
function runXcodegenInDir(dir: string, env: NodeJS.ProcessEnv): Promise<RunOneOutcome> {
	return new Promise((resolve) => {
		let child: ReturnType<typeof spawn>;
		try {
			child = spawn(XCODEGEN_BIN, ["generate"], { cwd: dir, env });
		} catch (error) {
			resolve(isEnoent(error) ? { notFound: true } : { ok: false, exitCode: null, output: errorText(error) });
			return;
		}
		let output = "";
		child.stdout?.on("data", (chunk: Buffer) => {
			output += chunk.toString("utf8");
		});
		child.stderr?.on("data", (chunk: Buffer) => {
			output += chunk.toString("utf8");
		});
		child.once("error", (error) => {
			resolve(
				isEnoent(error)
					? { notFound: true }
					: { ok: false, exitCode: null, output: `${output}${errorText(error)}`.trim() },
			);
		});
		child.once("close", (code) => {
			resolve({ ok: code === 0, exitCode: code, output: output.trim() });
		});
	});
}

/**
 * Find every `project.yml` under `root` and run `xcodegen generate` in each
 * containing directory, sequentially. Returns a discriminated result:
 * `no-specs` when none are found, `not-installed` when the `xcodegen` binary is
 * not on PATH, otherwise `ran` with a per-directory outcome list.
 *
 * Runs are sequential because the specs' `postGenCommand` (e.g. `pod install`,
 * git-config writes) can contend when run concurrently; the typical case is
 * one or two specs.
 */
export async function runXcodegen(root: string, opts: RunXcodegenOptions): Promise<RunXcodegenResult> {
	const readdir = opts.readdir ?? defaultReadDir;
	const runOne = opts.runOne ?? runXcodegenInDir;

	const dirs = await findProjectSpecDirs(root, readdir);
	if (dirs.length === 0) return { status: "no-specs", root };

	const results: XcodegenDirResult[] = [];
	for (const dir of dirs) {
		const outcome = await runOne(dir, opts.env);
		if ("notFound" in outcome) return { status: "not-installed" };
		results.push({
			dir: path.relative(root, dir) || ".",
			ok: outcome.ok,
			exitCode: outcome.exitCode,
			output: outcome.output,
		});
	}
	return { status: "ran", root, results };
}
