import { execFile } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import type { BuildSettings } from "./language-servers";

/**
 * The compile database an Xcode worktree's Swift server reads, owned by AO and
 * accumulated across EVERY build log Xcode leaves, not just the newest one.
 *
 * 🗝 WHY AO OWNS IT. `xcode-build-server`'s `kind: "xcode"` mode reads only the
 * newest log in `Logs/Build/LogStoreManifest.plist`, and a log only records the
 * modules that build actually COMPILED. Measured on the human's worktree
 * (2026-10-05): a Run that compiled no Swift left the one log on disk, the BSP's
 * whole compile database was `[]`, and every file in the app fell through to the
 * BSP's last-resort guess - `[file, -sdk MacOSX.sdk]` - which is where "No such
 * module 'UIKit'" on a file that builds fine comes from. Reproduced on a two-module
 * fixture: after an incremental build only the untouched module's files went red,
 * after a no-op build all of them did.
 *
 * And the newest log is not even reliably there: plain `xcodebuild` (no
 * `-resultBundlePath`) wrote NO log for 2 of the 2 fixture builds that compiled
 * anything, and never registered the ones it did write. So this reads every
 * `.xcactivitylog` in the directory, registered or not, oldest first, and merges
 * each one exactly once with the BSP's own parser (`parse -a`, which replaces an
 * entry by module or file and keeps the rest). A no-op or incremental build can
 * then only ADD or REFRESH settings - never take them away.
 *
 * The BSP runs in `kind: "manual"` against the result (see `writeShadowRoot`),
 * reloads it when its mtime moves, and tells sourcekit-lsp - so a build that
 * finishes while a file is open fixes that file without reopening it.
 */

/** In the shadow root: the database the BSP reads in `manual` mode. Its name is the BSP's. */
export const COMPILE_DATABASE = ".compile";

/** In the shadow root: which logs are already in the database. */
export const COMPILE_LEDGER = "compile-ledger.json";

type LedgerEntry = { size: number; mtimeMs: number; failed?: true };
type Ledger = { buildRoot: string; logs: Record<string, LedgerEntry> };

/** Merge one `.xcactivitylog` into `output`, creating it when absent. */
export type ParseLog = (log: string, output: string) => Promise<void>;

export type CompileDatabaseOptions = {
	/** `~/.ao/data/lsp/swift/<id>`: everything this writes lands here. */
	shadowRoot: string;
	/** The DerivedData directory of THIS checkout. */
	buildRoot: string;
	parseLog: ParseLog;
};

export const NOT_BUILT_ANY =
	"No Xcode build of this worktree that AO has read compiled any Swift yet, so there are no compile settings and errors here would be wrong. They appear by themselves the next time a build compiles this file.";

export const NOT_BUILT_FILE =
	"No Xcode build of this worktree that AO has read compiled this file yet, so it has no compile settings and errors here would be wrong. They appear by themselves the next time a build compiles it.";

/** The real `xcode-build-server parse`, with HOME scoped the same way as the server it feeds. */
export function xcodeBuildServerParser(input: {
	buildServerCommand: string;
	xbsHome: string;
	env: NodeJS.ProcessEnv;
}): ParseLog {
	return (log, output) =>
		new Promise((resolve, reject) => {
			execFile(
				input.buildServerCommand,
				["parse", "-a", "-l", log, "-o", output],
				{ env: { ...input.env, HOME: input.xbsHome }, maxBuffer: 64 * 1024 * 1024 },
				(err, _stdout, stderr) => {
					if (err) reject(new Error(`${err.message}${stderr ? `: ${String(stderr).trim().split("\n").pop()}` : ""}`));
					else resolve();
				},
			);
		});
}

/**
 * One refresh at a time per shadow root, across every object made for it.
 * `prepare` runs again on every attach and on the setup poll, so two
 * `BuildSettings` for one worktree are normal - and two merges racing on one
 * `.compile.next` would lose a log.
 */
const refreshes = new Map<string, Promise<boolean>>();

export function createCompileDatabase(options: CompileDatabaseOptions): BuildSettings {
	const { shadowRoot, buildRoot } = options;
	const database = path.join(shadowRoot, COMPILE_DATABASE);
	const ledgerPath = path.join(shadowRoot, COMPILE_LEDGER);
	let coverage: { mtimeMs: number; index: Coverage } | null = null;

	async function refreshOnce(): Promise<boolean> {
		let ledger = readLedger(ledgerPath);
		let reset = false;
		if (!ledger || ledger.buildRoot !== buildRoot) {
			// A different DerivedData (deleted and rebuilt, or a worktree recreated at
			// the same path) names its file lists and outputs at ITS paths. Starting
			// over is the only way not to hand sourcekit-lsp arguments that point at
			// a directory that no longer exists.
			ledger = { buildRoot, logs: {} };
			reset = fs.existsSync(database) || fs.existsSync(ledgerPath);
		}
		const pending = buildLogs(buildRoot).filter((log) => {
			const seen = ledger.logs[log.name];
			return !seen || seen.size !== log.size || seen.mtimeMs !== log.mtimeMs;
		});
		if (pending.length === 0 && !reset) return false;

		// Merged into a copy and RENAMED into place: the BSP re-reads the database
		// the moment its mtime moves, and `parse -a` rewrites in place, so a reader
		// between its truncate and its last write would load half a JSON array.
		const next = `${database}.next`;
		fs.rmSync(next, { force: true });
		if (!reset && fs.existsSync(database)) fs.copyFileSync(database, next);
		for (const log of pending) {
			try {
				await options.parseLog(log.path, next);
				ledger.logs[log.name] = { size: log.size, mtimeMs: log.mtimeMs };
			} catch (err) {
				// Remembered, so a log the parser cannot read is not re-tried every
				// three seconds for ever. A log that is still being written changes size,
				// which makes it a new log as far as the ledger is concerned.
				ledger.logs[log.name] = { size: log.size, mtimeMs: log.mtimeMs, failed: true };
				console.warn(`[lsp:swift] could not read build log ${log.path}: ${(err as Error).message}`);
			}
		}
		if (fs.existsSync(next)) fs.renameSync(next, database);
		else if (reset) fs.rmSync(database, { force: true });
		fs.writeFileSync(ledgerPath, `${JSON.stringify(ledger, null, 1)}\n`);
		return true;
	}

	function currentCoverage(): Coverage | null {
		let mtimeMs: number;
		try {
			mtimeMs = fs.statSync(database).mtimeMs;
		} catch {
			return null;
		}
		if (coverage?.mtimeMs !== mtimeMs) coverage = { mtimeMs, index: readCoverage(database) };
		return coverage.index;
	}

	return {
		refresh() {
			const running = refreshes.get(shadowRoot);
			// Chained, never skipped: a caller asking AFTER a build finished must get
			// a refresh that started after it too, not the tail of an older one.
			const run = (running ?? Promise.resolve(false)).catch(() => false).then(refreshOnce);
			refreshes.set(shadowRoot, run);
			void run.finally(() => {
				if (refreshes.get(shadowRoot) === run) refreshes.delete(shadowRoot);
			});
			return run;
		},
		statusOf(filePath) {
			const index = currentCoverage();
			if (!index || index.files.size === 0) return { built: false, reason: NOT_BUILT_ANY };
			return covers(index, filePath) ? { built: true } : { built: false, reason: NOT_BUILT_FILE };
		},
	};
}

function readLedger(ledgerPath: string): Ledger | null {
	try {
		const parsed = JSON.parse(fs.readFileSync(ledgerPath, "utf8")) as Partial<Ledger>;
		if (typeof parsed.buildRoot !== "string" || typeof parsed.logs !== "object" || !parsed.logs) return null;
		return { buildRoot: parsed.buildRoot, logs: parsed.logs };
	} catch {
		return null;
	}
}

/**
 * Every build log on disk, oldest first, so a later build's entry for a module
 * replaces an earlier one's. An EMPTY file is a log Xcode has not written yet (or
 * never will) and is left for a later pass rather than recorded.
 */
function buildLogs(buildRoot: string): { name: string; path: string; size: number; mtimeMs: number }[] {
	const dir = path.join(buildRoot, "Logs", "Build");
	let names: string[];
	try {
		names = fs.readdirSync(dir);
	} catch {
		return [];
	}
	const logs: { name: string; path: string; size: number; mtimeMs: number }[] = [];
	for (const name of names) {
		if (!name.endsWith(".xcactivitylog")) continue;
		const file = path.join(dir, name);
		try {
			const stat = fs.statSync(file);
			if (stat.isFile() && stat.size > 0) logs.push({ name, path: file, size: stat.size, mtimeMs: stat.mtimeMs });
		} catch {
			// gone between readdir and stat
		}
	}
	return logs.sort((a, b) => a.mtimeMs - b.mtimeMs || a.name.localeCompare(b.name));
}

/** Lower-cased real paths, the BSP's own `filekey`, and the directories holding Swift among them. */
type Coverage = { files: Set<string>; swiftDirs: Set<string> };

/**
 * Which files the database gives REAL arguments to, by the BSP's own rules
 * (`compile_database.CompileFileInfo`): a module entry covers its `files` and
 * every path in its `fileLists`, a C entry its `file`. Keyed the way the BSP
 * keys them - real path, lower-cased - so a file AO calls built is a file the
 * BSP will actually find.
 */
function readCoverage(database: string): Coverage {
	const files = new Set<string>();
	let items: unknown;
	try {
		items = JSON.parse(fs.readFileSync(database, "utf8"));
	} catch {
		items = [];
	}
	for (const item of Array.isArray(items) ? items : []) {
		if (!item || typeof item !== "object") continue;
		const entry = item as { command?: unknown; files?: unknown; fileLists?: unknown; file?: unknown };
		if (!entry.command) continue;
		for (const file of stringList(entry.files)) files.add(fileKey(file));
		for (const list of stringList(entry.fileLists)) {
			let text: string;
			try {
				text = fs.readFileSync(list, "utf8");
			} catch {
				// The BSP skips a list that is gone (`os.path.isfile`), so does this.
				continue;
			}
			for (const file of shellWords(text)) files.add(fileKey(file));
		}
		if (typeof entry.file === "string") files.add(fileKey(entry.file));
	}
	const swiftDirs = new Set<string>();
	for (const key of files) if (key.endsWith(".swift")) swiftDirs.add(path.dirname(key));
	return { files, swiftDirs };
}

/**
 * Whether the BSP will answer `filePath` with a real build's arguments.
 *
 * A file in the database is. So is a NEW Swift file - one created since the last
 * build - next to built ones: the BSP borrows the arguments of a compiled Swift
 * file in the same directory or the nearest parent with one, stopping at the git
 * root (`CompileFileInfo.new_file`). Those are a real iOS build's arguments, so
 * calling that file "built" is honest; it is exactly what the editor did for new
 * files before.
 */
function covers(index: Coverage, filePath: string): boolean {
	const real = realPath(filePath);
	const key = real.toLowerCase();
	if (index.files.has(key)) return true;
	if (!key.endsWith(".swift")) return false;
	let dir = path.dirname(real);
	if (index.swiftDirs.has(dir.toLowerCase())) return true;
	while (dir !== path.dirname(dir)) {
		const parent = path.dirname(dir);
		if (index.swiftDirs.has(parent.toLowerCase())) return true;
		if (fs.existsSync(path.join(parent, ".git"))) return false;
		dir = parent;
	}
	return false;
}

function fileKey(file: string): string {
	return realPath(file).toLowerCase();
}

function realPath(file: string): string {
	try {
		return fs.realpathSync(file);
	} catch {
		return path.resolve(file);
	}
}

function stringList(value: unknown): string[] {
	return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
}

/**
 * POSIX shell word splitting, as Python's `shlex.split` does it - which is how the
 * BSP reads a `.SwiftFileList`. Xcode escapes a space in a path with a backslash
 * (`Realised\ PL/…` in the real app), so splitting on whitespace would cut that
 * file in two and call it unbuilt.
 */
export function shellWords(text: string): string[] {
	const words: string[] = [];
	let word = "";
	let inWord = false;
	let quote: "'" | '"' | null = null;
	for (let i = 0; i < text.length; i++) {
		const c = text[i];
		if (quote === "'") {
			if (c === "'") quote = null;
			else word += c;
			continue;
		}
		if (quote === '"') {
			if (c === '"') quote = null;
			else if (c === "\\" && i + 1 < text.length && '"\\$`\n'.includes(text[i + 1])) word += text[++i];
			else word += c;
			continue;
		}
		if (c === "\\" && i + 1 < text.length) {
			if (text[i + 1] !== "\n") word += text[i + 1];
			i++;
			inWord = true;
			continue;
		}
		if (c === "'" || c === '"') {
			quote = c;
			inWord = true;
			continue;
		}
		if (/\s/.test(c)) {
			if (inWord) words.push(word);
			word = "";
			inWord = false;
			continue;
		}
		word += c;
		inWord = true;
	}
	if (inWord) words.push(word);
	return words;
}
