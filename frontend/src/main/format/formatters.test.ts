import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterAll, describe, expect, it } from "vitest";
import { firstProblem, findSwiftFormat, type FormatRequest, type RunTool, runFormatter } from "./formatters";
import type { Fs } from "./project-config";

const noFiles: Fs = { readText: async () => null, exists: async () => false };

function request(overrides: Partial<FormatRequest> = {}): FormatRequest {
	return {
		languageId: "go",
		filePath: "/repo/main.go",
		workspaceRoot: "/repo",
		text: "package main\n",
		insertSpaces: false,
		tabSize: 4,
		...overrides,
	};
}

describe("runFormatter's contract: formatted text, or a reason and no text", () => {
	it("returns what the tool printed", async () => {
		const run: RunTool = async () => ({ kind: "exited", code: 0, stdout: "package main\n", stderr: "" });
		expect(await runFormatter(request(), { env: {}, fs: noFiles, run })).toEqual({
			ok: true,
			text: "package main\n",
			formatter: "gofmt",
		});
	});

	it("refuses a tool that printed nothing for a non-empty file", async () => {
		const run: RunTool = async () => ({ kind: "exited", code: 0, stdout: "", stderr: "" });
		const result = await runFormatter(request(), { env: {}, fs: noFiles, run });
		expect(result).toMatchObject({ ok: false, reason: "failed" });
		expect("text" in result).toBe(false);
	});

	it("names the line a syntax error is on, with the file's own name", async () => {
		const run: RunTool = async () => ({
			kind: "exited",
			code: 2,
			stdout: "",
			stderr: "<standard input>:3:1: expected '}', found 'EOF'\n",
		});
		expect(await runFormatter(request(), { env: {}, fs: noFiles, run })).toEqual({
			ok: false,
			reason: "failed",
			formatter: "gofmt",
			message: "main.go:3:1: expected '}', found 'EOF'",
		});
	});

	it("says the tool is missing rather than failing", async () => {
		const run: RunTool = async () => ({ kind: "missing" });
		expect(await runFormatter(request(), { env: {}, fs: noFiles, run })).toMatchObject({
			ok: false,
			reason: "unavailable",
			message: "gofmt is not installed - it ships with Go.",
		});
	});

	it("reports a timeout as a failure", async () => {
		const run: RunTool = async () => ({ kind: "timeout" });
		expect(await runFormatter(request(), { env: {}, fs: noFiles, run })).toMatchObject({ ok: false, reason: "failed" });
	});

	it("hands swift-format the editor's indentation only when the project has no .swift-format", async () => {
		const calls: string[][] = [];
		const run: RunTool = async (_command, args) => {
			calls.push(args);
			return { kind: "exited", code: 0, stdout: "x\n", stderr: "" };
		};
		const swift = request({
			languageId: "swift",
			filePath: "/repo/A.swift",
			insertSpaces: true,
			tabSize: 4,
			text: "x\n",
		});
		const findSwiftFormat = async () => "/xcode/swift-format";
		await runFormatter(swift, { env: {}, fs: noFiles, run, findSwiftFormat });
		expect(calls[0]).toEqual([
			"format",
			"--assume-filename",
			"/repo/A.swift",
			"--configuration",
			JSON.stringify({ version: 1, indentation: { spaces: 4 }, tabWidth: 4 }),
			"-",
		]);
		const withConfig: Fs = { readText: async () => null, exists: async (f) => f === "/repo/.swift-format" };
		await runFormatter(swift, { env: {}, fs: withConfig, run, findSwiftFormat });
		expect(calls[1]).toEqual(["format", "--assume-filename", "/repo/A.swift", "-"]);
	});

	it("runs Prettier only when the project configures it, and says so when it is not installed", async () => {
		const run: RunTool = async () => ({ kind: "exited", code: 0, stdout: "x;\n", stderr: "" });
		const ts = request({ languageId: "typescript", filePath: "/repo/a.ts", text: "x\n" });
		expect(await runFormatter(ts, { env: {}, fs: noFiles, run })).toMatchObject({ ok: false, reason: "unavailable" });
		const configured: Fs = {
			readText: async (f) => (f === "/repo/.prettierrc" ? "{}" : null),
			exists: async () => false,
		};
		expect(await runFormatter(ts, { env: {}, fs: configured, run })).toMatchObject({ ok: false, reason: "failed" });
		const installed: Fs = {
			readText: configured.readText,
			exists: async (f) => f === "/repo/node_modules/.bin/prettier",
		};
		expect(await runFormatter(ts, { env: {}, fs: installed, run })).toEqual({
			ok: true,
			text: "x;\n",
			formatter: "prettier",
		});
	});

	it("has nothing to say about a language without a formatter", async () => {
		expect(await runFormatter(request({ languageId: "python" }), { env: {}, fs: noFiles })).toMatchObject({
			ok: false,
			reason: "unavailable",
		});
	});
});

describe("firstProblem", () => {
	it("shortens a swift-format diagnostic and skips its deprecation warning", () => {
		const stderr =
			"<unknown>: warning: Running swift-format without input paths is deprecated and will be removed in the future.\n" +
			"/Users/me/app/Sources/A.swift:1:8: error: expected ')' to end parameter clause\n";
		expect(firstProblem(stderr, "A.swift", "swift-format")).toBe(
			"A.swift:1:8: error: expected ')' to end parameter clause",
		);
	});

	it("drops Prettier's [error] tag", () => {
		expect(firstProblem("[error] a.ts: SyntaxError: ';' expected. (3:5)\n", "a.ts", "prettier")).toBe(
			"a.ts: SyntaxError: ';' expected. (3:5)",
		);
	});
});

/**
 * The real tools, on this Mac. SKIPPED, not failed, where they are missing -
 * CI has neither gofmt's toolchain guaranteed nor Xcode.
 */
function has(command: string, args: string[], input = ""): boolean {
	try {
		execFileSync(command, args, { input, stdio: ["pipe", "ignore", "ignore"] });
		return true;
	} catch {
		return false;
	}
}

const scratch = mkdtempSync(path.join(os.tmpdir(), "ao-format-"));
afterAll(() => rmSync(scratch, { recursive: true, force: true }));

describe.skipIf(!has("gofmt", [], "package x\n"))("gofmt, for real", () => {
	it("formats, and refuses a syntax error without returning text", async () => {
		const env = process.env;
		const file = path.join(scratch, "main.go");
		const ok = await runFormatter(
			request({ filePath: file, workspaceRoot: scratch, text: "package main\nfunc main(){\nx:=1\n_=x}\n" }),
			{ env },
		);
		expect(ok).toEqual({ ok: true, text: "package main\n\nfunc main() {\n\tx := 1\n\t_ = x\n}\n", formatter: "gofmt" });
		const bad = await runFormatter(
			request({ filePath: file, workspaceRoot: scratch, text: "package main\nfunc main() {\n" }),
			{ env },
		);
		expect(bad).toMatchObject({ ok: false, reason: "failed", formatter: "gofmt" });
		expect(bad.ok ? "" : bad.message).toMatch(/^main\.go:\d+:\d+: /);
	});
});

describe.skipIf(process.platform !== "darwin" || !has("xcrun", ["--find", "swift-format"]))(
	"swift-format, for real",
	() => {
		it("uses the editor's indentation without a .swift-format, and the project's with one", async () => {
			const env = process.env;
			const file = path.join(scratch, "swift", "A.swift");
			const text = "func f() {\nif true {\nprint(1)\n}\n}\n";
			const four = await runFormatter(
				request({ languageId: "swift", filePath: file, workspaceRoot: scratch, text, insertSpaces: true, tabSize: 4 }),
				{ env, findSwiftFormat },
			);
			expect(four).toMatchObject({ ok: true, text: "func f() {\n    if true {\n        print(1)\n    }\n}\n" });
			writeFileSync(path.join(scratch, ".swift-format"), JSON.stringify({ version: 1, indentation: { spaces: 3 } }));
			const three = await runFormatter(
				request({ languageId: "swift", filePath: file, workspaceRoot: scratch, text, insertSpaces: true, tabSize: 4 }),
				{ env, findSwiftFormat },
			);
			expect(three).toMatchObject({ ok: true, text: "func f() {\n   if true {\n      print(1)\n   }\n}\n" });
		}, 30_000);
	},
);
