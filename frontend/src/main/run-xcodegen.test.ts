import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { findProjectSpecDirs, type ReadDir, runXcodegen, shouldSkipDir } from "./run-xcodegen";

// A minimal Dirent-like entry — findProjectSpecDirs only needs name + isDirectory().
const dirent = (name: string, isDir: boolean) => ({ name, isDirectory: () => isDir });

// Build an injectable readdir from a { dirPath: [entries] } map. Unknown dirs read
// as empty so a walk into a skipped dir (which shouldn't happen) surfaces nothing.
function fakeReaddir(tree: Record<string, ReturnType<typeof dirent>[]>): ReadDir {
	return async (dir: string) => tree[dir] ?? [];
}

describe("shouldSkipDir", () => {
	it("skips dot-directories (.git, .claude worktrees, etc.)", () => {
		expect(shouldSkipDir(".git")).toBe(true);
		expect(shouldSkipDir(".claude")).toBe(true);
		expect(shouldSkipDir(".build")).toBe(true);
	});

	it("skips heavy build/dependency directories", () => {
		expect(shouldSkipDir("node_modules")).toBe(true);
		expect(shouldSkipDir("Pods")).toBe(true);
		expect(shouldSkipDir("Carthage")).toBe(true);
		expect(shouldSkipDir("DerivedData")).toBe(true);
		expect(shouldSkipDir("build")).toBe(true);
	});

	it("keeps normal source directories", () => {
		expect(shouldSkipDir("NterApp")).toBe(false);
		expect(shouldSkipDir("Sources")).toBe(false);
		expect(shouldSkipDir("packages")).toBe(false);
	});
});

describe("findProjectSpecDirs", () => {
	it("finds a nested project.yml and ignores noise directories", async () => {
		// Mirrors the real demo-ios-app: the spec is one level down in NterApp/,
		// with copies buried in .git/.claude/node_modules/Pods that must be skipped.
		const readdir = fakeReaddir({
			"/root": [
				dirent("NterApp", true),
				dirent(".git", true),
				dirent(".claude", true),
				dirent("node_modules", true),
				dirent("Pods", true),
				dirent("README.md", false),
			],
			"/root/NterApp": [dirent("project.yml", false), dirent("Sources", true)],
			"/root/NterApp/Sources": [],
			// These would each yield a spec if the walk failed to skip them.
			"/root/.git": [dirent("project.yml", false)],
			"/root/.claude": [dirent("project.yml", false)],
			"/root/node_modules": [dirent("project.yml", false)],
			"/root/Pods": [dirent("project.yml", false)],
		});

		const dirs = await findProjectSpecDirs("/root", readdir);
		expect(dirs).toEqual(["/root/NterApp"]);
	});

	it("finds every directory that contains a project.yml (multi-module)", async () => {
		const readdir = fakeReaddir({
			"/multi": [dirent("project.yml", false), dirent("ModuleA", true), dirent("ModuleB", true)],
			"/multi/ModuleA": [dirent("project.yml", false)],
			"/multi/ModuleB": [dirent("Nested", true)],
			"/multi/ModuleB/Nested": [dirent("project.yml", false)],
		});

		const dirs = await findProjectSpecDirs("/multi", readdir);
		expect(dirs).toEqual(["/multi", "/multi/ModuleA", "/multi/ModuleB/Nested"]);
	});

	it("does not treat a directory named project.yml as a spec", async () => {
		const readdir = fakeReaddir({
			"/root": [dirent("project.yml", true)],
			"/root/project.yml": [],
		});
		expect(await findProjectSpecDirs("/root", readdir)).toEqual([]);
	});

	it("returns an empty list when there is no project.yml anywhere", async () => {
		const readdir = fakeReaddir({
			"/root": [dirent("src", true), dirent("package.json", false)],
			"/root/src": [dirent("index.ts", false)],
		});
		expect(await findProjectSpecDirs("/root", readdir)).toEqual([]);
	});
});

describe("runXcodegen", () => {
	const env = { PATH: "/opt/homebrew/bin:/usr/bin" };

	it("runs xcodegen in every spec directory and aggregates per-dir results", async () => {
		const readdir = fakeReaddir({
			"/root": [dirent("project.yml", false), dirent("ModuleA", true)],
			"/root/ModuleA": [dirent("project.yml", false)],
		});
		const runOne = vi.fn(async (dir: string) => {
			if (dir === "/root/ModuleA") return { ok: false, exitCode: 1, output: "boom" };
			return { ok: true, exitCode: 0, output: "Created project at /root/App.xcodeproj" };
		});

		const result = await runXcodegen("/root", { env, readdir, runOne });

		expect(result).toEqual({
			status: "ran",
			root: "/root",
			results: [
				{ dir: ".", ok: true, exitCode: 0, output: "Created project at /root/App.xcodeproj" },
				{ dir: "ModuleA", ok: false, exitCode: 1, output: "boom" },
			],
		});
		expect(runOne).toHaveBeenCalledTimes(2);
	});

	it("reports no-specs when no project.yml is found", async () => {
		const readdir = fakeReaddir({ "/root": [dirent("README.md", false)] });
		const runOne = vi.fn();

		const result = await runXcodegen("/root", { env, readdir, runOne });

		expect(result).toEqual({ status: "no-specs", root: "/root" });
		expect(runOne).not.toHaveBeenCalled();
	});

	it("reports not-installed and stops when xcodegen is not on PATH", async () => {
		const readdir = fakeReaddir({
			"/root": [dirent("project.yml", false), dirent("ModuleA", true)],
			"/root/ModuleA": [dirent("project.yml", false)],
		});
		const runOne = vi.fn(async () => ({ notFound: true }) as const);

		const result = await runXcodegen("/root", { env, readdir, runOne });

		expect(result).toEqual({ status: "not-installed" });
		// Aborts after the first missing-binary signal rather than retrying each dir.
		expect(runOne).toHaveBeenCalledTimes(1);
	});

	it("passes the searched-dir path through to runOne", async () => {
		const readdir = fakeReaddir({ "/root": [dirent("project.yml", false)] });
		const runOne = vi.fn(async () => ({ ok: true, exitCode: 0, output: "" }));

		await runXcodegen("/root", { env, readdir, runOne });

		expect(runOne).toHaveBeenCalledWith("/root", env);
	});
});

// Documents the relative-path labelling used in the results ("." for the root).
describe("result dir labelling", () => {
	it("labels the searched root as '.'", () => {
		expect(path.relative("/root", "/root") || ".").toBe(".");
		expect(path.relative("/root", "/root/NterApp")).toBe("NterApp");
	});
});
