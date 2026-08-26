import { describe, expect, test } from "vitest";
import { fileUriForPath, openTargetForUri, pathForFileUri } from "./lsp-uri";

describe("fileUriForPath / pathForFileUri", () => {
	test("round-trips a plain path", () => {
		expect(pathForFileUri(fileUriForPath("/a/b/c.go"))).toBe("/a/b/c.go");
	});

	test("round-trips spaces and non-ASCII", () => {
		// Go module cache paths carry `!` escapes and real projects carry spaces; a
		// naive `uri.slice(7)` corrupts both and ⌘click lands nowhere.
		for (const p of ["/a/My Project/c.go", "/a/配置/d.go", "/a/b!c/e.go", "/a/b#c/f.go", "/a/b?c/g.go"]) {
			expect(pathForFileUri(fileUriForPath(p))).toBe(p);
		}
	});

	test("accepts the file:/// form a server may send verbatim", () => {
		expect(pathForFileUri("file:///usr/local/go/src/fmt/print.go")).toBe("/usr/local/go/src/fmt/print.go");
	});
});

describe("openTargetForUri", () => {
	const workspaceRoot = "/Users/x/.ao/data/worktrees/proj/feature-a";

	test("inside the workspace: relative path, inWorkspace true", () => {
		expect(
			openTargetForUri({ uri: `file://${workspaceRoot}/backend/internal/x.go`, workspaceRoot, line: 12, column: 5 }),
		).toEqual({ path: "backend/internal/x.go", line: 12, column: 5, inWorkspace: true });
	});

	test("outside the workspace: absolute path, inWorkspace false", () => {
		// Go definitions land in GOROOT and the module cache constantly. Refusing
		// them would make ⌘click on fmt.Println a dead gesture, which is exactly
		// the answer-nothing failure this slice exists to kill.
		expect(openTargetForUri({ uri: "file:///usr/local/go/src/fmt/print.go", workspaceRoot, line: 3 })).toEqual({
			path: "/usr/local/go/src/fmt/print.go",
			line: 3,
			column: undefined,
			inWorkspace: false,
		});
	});

	test("a sibling directory sharing a prefix is NOT inside the workspace", () => {
		// `/…/feature-a-old` must not read as inside `/…/feature-a`. A bare
		// startsWith says it does.
		const target = openTargetForUri({ uri: `file://${workspaceRoot}-old/x.go`, workspaceRoot });
		expect(target.inWorkspace).toBe(false);
		expect(target.path).toBe(`${workspaceRoot}-old/x.go`);
	});

	test("the workspace root itself is not turned into an empty path", () => {
		const target = openTargetForUri({ uri: `file://${workspaceRoot}`, workspaceRoot });
		expect(target.path).toBe(workspaceRoot);
		expect(target.inWorkspace).toBe(false);
	});

	test("a trailing slash on the root does not change the verdict", () => {
		const target = openTargetForUri({ uri: `file://${workspaceRoot}/a.go`, workspaceRoot: `${workspaceRoot}/` });
		expect(target).toMatchObject({ path: "a.go", inWorkspace: true });
	});
});
