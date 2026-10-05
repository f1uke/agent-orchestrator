import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import {
	COMPILE_DATABASE,
	COMPILE_LEDGER,
	createCompileDatabase,
	NOT_BUILT_ANY,
	NOT_BUILT_FILE,
	type ParseLog,
	shellWords,
} from "./swift-compile-database";

/**
 * The compile database against a real filesystem, with the BSP's parser swapped
 * for one that does what `xcode-build-server parse -a` does: read a log's
 * entries and merge them in, replacing an entry by `module_name` or `file`.
 * Here a "log" is simply its entries as JSON.
 *
 * 🗝 The scenarios are the measured ones (2026-10-05): a full build, then an
 * incremental one that lists only the module it recompiled, then a no-op one
 * that lists nothing - after which `xcode-build-server`'s own `kind: "xcode"`
 * mode left the human's whole app on macOS guesses.
 */

type Item = { module_name?: string; file?: string; command: string; files?: string[]; fileLists?: string[] };

let tmp: string;
let checkout: string;
let buildRoot: string;
let shadowRoot: string;
let parsed: string[];

const mergingParser: ParseLog = async (log, output) => {
	parsed.push(path.basename(log));
	const incoming = JSON.parse(fs.readFileSync(log, "utf8")) as Item[];
	const existing = fs.existsSync(output) ? (JSON.parse(fs.readFileSync(output, "utf8")) as Item[]) : [];
	const id = (item: Item) => item.file ?? item.module_name;
	const replaced = new Map(incoming.map((item) => [id(item), item]));
	const merged = existing.map((item) => replaced.get(id(item)) ?? item);
	merged.push(...incoming.filter((item) => !existing.some((old) => id(old) === id(item))));
	fs.writeFileSync(output, JSON.stringify(merged));
};

let logClock = 1_700_000_000_000;
/** A build log, each one newer than the last, the way Xcode writes them. */
function writeLog(name: string, items: Item[] | string): string {
	const file = path.join(buildRoot, "Logs", "Build", `${name}.xcactivitylog`);
	fs.writeFileSync(file, typeof items === "string" ? items : JSON.stringify(items));
	logClock += 60_000;
	fs.utimesSync(file, new Date(logClock), new Date(logClock));
	return file;
}

/** A module the way Xcode records it: its sources in a `.SwiftFileList` under DerivedData. */
function swiftModule(
	name: string,
	sources: string[],
	command = `swiftc -module-name ${name} -sdk iPhoneSimulator.sdk`,
): Item {
	const list = path.join(buildRoot, "Build", `${name}.SwiftFileList`);
	fs.mkdirSync(path.dirname(list), { recursive: true });
	// Escaped the way Xcode escapes it: `Realised\ PL/...` in the real app.
	fs.writeFileSync(list, `${sources.map((s) => path.join(checkout, s).replaceAll(" ", "\\ ")).join("\n")}\n`);
	return { module_name: name, command, fileLists: [list] };
}

function source(rel: string): string {
	const file = path.join(checkout, rel);
	fs.mkdirSync(path.dirname(file), { recursive: true });
	fs.writeFileSync(file, "import UIKit\n");
	return file;
}

function database(parseLog: ParseLog = mergingParser) {
	return createCompileDatabase({ shadowRoot, buildRoot, parseLog });
}

function stored(): Item[] {
	return JSON.parse(fs.readFileSync(path.join(shadowRoot, COMPILE_DATABASE), "utf8")) as Item[];
}

beforeEach(() => {
	tmp = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "ao-compile-db-")));
	checkout = path.join(tmp, "checkout");
	buildRoot = path.join(tmp, "DerivedData", "Nter-abc");
	shadowRoot = path.join(tmp, "data", "lsp", "swift", "0123");
	fs.mkdirSync(path.join(checkout, ".git"), { recursive: true });
	fs.mkdirSync(path.join(buildRoot, "Logs", "Build"), { recursive: true });
	fs.mkdirSync(shadowRoot, { recursive: true });
	for (const rel of ["App/Chat/ChatNotice.swift", "App/Realised PL/Summary.swift", "Kit/KitBanner.swift"]) source(rel);
	parsed = [];
});

afterEach(() => {
	fs.rmSync(tmp, { recursive: true, force: true });
});

describe("accumulating build logs", () => {
	test("a no-op build after a full one takes nothing away", async () => {
		writeLog("1-full", [
			swiftModule("App", ["App/Chat/ChatNotice.swift", "App/Realised PL/Summary.swift"]),
			swiftModule("Kit", ["Kit/KitBanner.swift"]),
		]);
		writeLog("2-noop", []);
		const db = database();

		expect(await db.refresh()).toBe(true);

		// The human's case: the newest log compiled nothing. Read alone, as the
		// BSP's `xcode` mode reads it, it leaves every file on the macOS guess.
		for (const rel of ["App/Chat/ChatNotice.swift", "App/Realised PL/Summary.swift", "Kit/KitBanner.swift"]) {
			expect(db.statusOf(path.join(checkout, rel)), rel).toEqual({ built: true });
		}
	});

	test("an incremental build refreshes the module it compiled and keeps the others", async () => {
		writeLog("1-full", [
			swiftModule("App", ["App/Chat/ChatNotice.swift"], "swiftc -module-name App OLD"),
			swiftModule("Kit", ["Kit/KitBanner.swift"]),
		]);
		writeLog("2-incremental", [swiftModule("App", ["App/Chat/ChatNotice.swift"], "swiftc -module-name App NEW")]);
		const db = database();
		await db.refresh();

		expect(stored().map((item) => `${item.module_name}: ${item.command}`)).toEqual([
			"App: swiftc -module-name App NEW",
			"Kit: swiftc -module-name Kit -sdk iPhoneSimulator.sdk",
		]);
		expect(db.statusOf(path.join(checkout, "Kit/KitBanner.swift"))).toEqual({ built: true });
	});

	test("every log on disk is read, oldest first - registered in LogStoreManifest or not", async () => {
		// No manifest at all: plain xcodebuild never registered the logs it wrote.
		writeLog("b-newer", [swiftModule("App", ["App/Chat/ChatNotice.swift"], "NEW")]);
		const older = writeLog("a-older", [swiftModule("App", ["App/Chat/ChatNotice.swift"], "OLD")]);
		fs.utimesSync(older, new Date(1_600_000_000_000), new Date(1_600_000_000_000));

		await database().refresh();

		expect(parsed).toEqual(["a-older.xcactivitylog", "b-newer.xcactivitylog"]);
		expect(stored()[0].command).toBe("NEW");
	});

	test("each log is read exactly once, and a later build is read on the next refresh", async () => {
		writeLog("1-full", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		const db = database();
		await db.refresh();
		expect(await db.refresh()).toBe(false);
		expect(parsed).toEqual(["1-full.xcactivitylog"]);

		writeLog("2-kit", [swiftModule("Kit", ["Kit/KitBanner.swift"])]);
		expect(db.statusOf(path.join(checkout, "Kit/KitBanner.swift"))).toEqual({ built: false, reason: NOT_BUILT_FILE });
		expect(await db.refresh()).toBe(true);
		expect(parsed).toEqual(["1-full.xcactivitylog", "2-kit.xcactivitylog"]);
		expect(db.statusOf(path.join(checkout, "Kit/KitBanner.swift"))).toEqual({ built: true });
	});

	test("the ledger outlives the object: a new server for the same worktree does not re-read old logs", async () => {
		writeLog("1-full", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		await database().refresh();
		expect(await database().refresh()).toBe(false);
		expect(parsed).toHaveLength(1);
		expect(fs.existsSync(path.join(shadowRoot, COMPILE_LEDGER))).toBe(true);
	});

	test("an empty log is one Xcode has not written yet: skipped, then read once it has content", async () => {
		const log = writeLog("1-writing", "");
		const db = database();
		expect(await db.refresh()).toBe(false);
		expect(parsed).toEqual([]);

		fs.writeFileSync(log, JSON.stringify([swiftModule("App", ["App/Chat/ChatNotice.swift"])]));
		expect(await db.refresh()).toBe(true);
		expect(db.statusOf(path.join(checkout, "App/Chat/ChatNotice.swift"))).toEqual({ built: true });
	});

	test("a log the parser cannot read is not retried every pass, and does not lose what was there", async () => {
		writeLog("1-full", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		writeLog("2-broken", "not a log");
		const db = database();
		await db.refresh();
		expect(await db.refresh()).toBe(false);
		expect(parsed.filter((name) => name.startsWith("2-"))).toHaveLength(1);
		expect(db.statusOf(path.join(checkout, "App/Chat/ChatNotice.swift"))).toEqual({ built: true });
	});

	test("a different DerivedData starts over rather than keep arguments that name the old one", async () => {
		writeLog("1-full", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		await database().refresh();

		buildRoot = path.join(tmp, "DerivedData", "Nter-rebuilt");
		fs.mkdirSync(path.join(buildRoot, "Logs", "Build"), { recursive: true });
		const db = database();
		expect(await db.refresh()).toBe(true);
		expect(fs.existsSync(path.join(shadowRoot, COMPILE_DATABASE))).toBe(false);
		expect(db.statusOf(path.join(checkout, "App/Chat/ChatNotice.swift"))).toEqual({
			built: false,
			reason: NOT_BUILT_ANY,
		});
	});

	test("two refreshes at once read each log once", async () => {
		writeLog("1-full", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		const slow: ParseLog = async (log, output) => {
			await new Promise((r) => setTimeout(r, 20));
			await mergingParser(log, output);
		};
		await Promise.all([database(slow).refresh(), database(slow).refresh()]);
		expect(parsed).toEqual(["1-full.xcactivitylog"]);
	});

	test("the BSP never sees a half-written database: the merge happens in a copy", async () => {
		writeLog("1-full", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		await database().refresh();
		const before = fs.readFileSync(path.join(shadowRoot, COMPILE_DATABASE), "utf8");

		writeLog("2-kit", [swiftModule("Kit", ["Kit/KitBanner.swift"])]);
		const seen: { output: string; live: string }[] = [];
		await database(async (log, output) => {
			seen.push({ output, live: fs.readFileSync(path.join(shadowRoot, COMPILE_DATABASE), "utf8") });
			await mergingParser(log, output);
		}).refresh();

		expect(seen).toHaveLength(1);
		expect(seen[0].output).not.toBe(path.join(shadowRoot, COMPILE_DATABASE));
		expect(seen[0].live).toBe(before);
		expect(stored().map((item) => item.module_name)).toEqual(["App", "Kit"]);
	});
});

describe("which files have real settings", () => {
	test("nothing read yet, or nothing compiled yet: every file waits, and says no build compiled any Swift", async () => {
		const db = database();
		expect(db.statusOf(path.join(checkout, "App/Chat/ChatNotice.swift"))).toEqual({
			built: false,
			reason: NOT_BUILT_ANY,
		});
		writeLog("1-noop", []);
		await db.refresh();
		expect(db.statusOf(path.join(checkout, "App/Chat/ChatNotice.swift"))).toEqual({
			built: false,
			reason: NOT_BUILT_ANY,
		});
	});

	test("a file in a module no build compiled waits, with a reason about THIS file", async () => {
		writeLog("1-app", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		const db = database();
		await db.refresh();
		expect(db.statusOf(path.join(checkout, "Kit/KitBanner.swift"))).toEqual({ built: false, reason: NOT_BUILT_FILE });
	});

	test("a path with a space, escaped the way Xcode writes its file lists", async () => {
		writeLog("1-app", [swiftModule("App", ["App/Realised PL/Summary.swift"])]);
		const db = database();
		await db.refresh();
		expect(db.statusOf(path.join(checkout, "App/Realised PL/Summary.swift"))).toEqual({ built: true });
	});

	test("an Objective-C file entry and an inline `files` list count too", async () => {
		const header = source("Legacy/Bridge.m");
		writeLog("1-mixed", [
			{ file: header, command: "clang -c Bridge.m" },
			{ module_name: "Kit", command: "swiftc -module-name Kit", files: [path.join(checkout, "Kit/KitBanner.swift")] },
		]);
		const db = database();
		await db.refresh();
		expect(db.statusOf(header)).toEqual({ built: true });
		expect(db.statusOf(path.join(checkout, "Kit/KitBanner.swift"))).toEqual({ built: true });
	});

	test("a NEW Swift file borrows a compiled neighbour's settings, as the BSP does", async () => {
		writeLog("1-app", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		const db = database();
		await db.refresh();
		// Created since the last build: same directory, and a subdirectory.
		expect(db.statusOf(source("App/Chat/ChatNew.swift"))).toEqual({ built: true });
		expect(db.statusOf(source("App/Chat/Cells/ChatCell.swift"))).toEqual({ built: true });
	});

	test("the borrowing stops at the git root, as the BSP's does", async () => {
		writeLog("1-root", [{ module_name: "Root", command: "swiftc", files: [source("Root.swift")] }]);
		const db = database();
		await db.refresh();
		// The checkout's own Root.swift is one level above Kit/, so Kit borrows it...
		expect(db.statusOf(path.join(checkout, "Kit/KitBanner.swift"))).toEqual({ built: true });
		// ...but a nested repository's files never reach past their own root.
		fs.mkdirSync(path.join(checkout, "Vendor", "Lib", ".git"), { recursive: true });
		expect(db.statusOf(source("Vendor/Lib/Sources/Lib.swift"))).toEqual({ built: false, reason: NOT_BUILT_FILE });
	});

	test("matched by real path and without regard to case, the way the BSP keys files", async () => {
		writeLog("1-app", [swiftModule("App", ["App/Chat/ChatNotice.swift"])]);
		const db = database();
		await db.refresh();
		const link = path.join(tmp, "link-to-checkout");
		fs.symlinkSync(checkout, link);
		expect(db.statusOf(path.join(link, "App/Chat/ChatNotice.swift"))).toEqual({ built: true });
		expect(db.statusOf(path.join(checkout, "app/chat/chatnotice.swift"))).toEqual({ built: true });
	});
});

describe("shellWords", () => {
	test("splits the way Python's shlex.split does", () => {
		expect(shellWords("/a/Realised\\ PL/x.swift\n/b/y.swift\n")).toEqual(["/a/Realised PL/x.swift", "/b/y.swift"]);
		expect(shellWords(`'/a b/c.swift' "/d \\"e\\"/f.swift"`)).toEqual(["/a b/c.swift", '/d "e"/f.swift']);
		expect(shellWords("  ")).toEqual([]);
		expect(shellWords("''")).toEqual([""]);
	});
});
