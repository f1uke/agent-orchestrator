import { describe, expect, test } from "vitest";
import { documentUriForPath, fileUriForPath, openTargetForUri, pathForFileUri } from "./lsp-uri";

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

describe("documentUriForPath", () => {
	const swift = { workspaceRoot: "/Users/x/nter-ios-app", documentRoot: "/Users/x/.ao/lsp/swift/abc/wt" };

	test("a workspace file is addressed THROUGH the shadow root's symlink", () => {
		// 🗝 Measured on a real iOS app: with the shadow root correct in every other
		// respect, addressing the same file by its real path made all four ⌘click
		// targets return 0 hits in 55-474 ms - no error, no diagnostic - while
		// workspace/symbol carried on returning the right answers. The failure does
		// not look like a failure; it looks like ⌘click not being implemented.
		expect(documentUriForPath("/Users/x/nter-ios-app/NterApp/AppDelegate.swift", swift)).toBe(
			"file:///Users/x/.ao/lsp/swift/abc/wt/NterApp/AppDelegate.swift",
		);
	});

	test("identity when the server is rooted where the files are", () => {
		const go = { workspaceRoot: "/w", documentRoot: "/w" };
		expect(documentUriForPath("/w/main.go", go)).toBe("file:///w/main.go");
	});

	test("a path outside the workspace is sent as itself", () => {
		// A definition in another checkout's Pods, or a knowledge-store note. It has
		// no place under the document root, and inventing one would address a file
		// that does not exist.
		expect(documentUriForPath("/usr/local/go/src/fmt/print.go", swift)).toBe("file:///usr/local/go/src/fmt/print.go");
	});

	test("a sibling whose name merely STARTS with the root is not inside it", () => {
		expect(documentUriForPath("/Users/x/nter-ios-app-old/A.swift", swift)).toBe(
			"file:///Users/x/nter-ios-app-old/A.swift",
		);
	});

	test("trailing slashes on either root do not double up", () => {
		expect(
			documentUriForPath("/Users/x/nter-ios-app/A.swift", {
				workspaceRoot: "/Users/x/nter-ios-app/",
				documentRoot: "/Users/x/.ao/lsp/swift/abc/wt/",
			}),
		).toBe("file:///Users/x/.ao/lsp/swift/abc/wt/A.swift");
	});

	test("escaping still happens after the rewrite", () => {
		expect(documentUriForPath("/Users/x/nter-ios-app/My View.swift", swift)).toBe(
			"file:///Users/x/.ao/lsp/swift/abc/wt/My%20View.swift",
		);
	});
});
