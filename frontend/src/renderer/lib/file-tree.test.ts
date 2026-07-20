import { describe, expect, it } from "vitest";
import { buildFileTree, flattenFileTree, matchesFileQuery, orderedFileItems } from "./file-tree";

type Item = { path: string };
const items = (...paths: string[]): Item[] => paths.map((path) => ({ path }));
const build = (...paths: string[]) => buildFileTree(items(...paths), (f) => f.path);

describe("buildFileTree", () => {
	it("nests files under their directories", () => {
		const tree = build("src/a.ts", "src/b.ts");
		expect(tree).toHaveLength(1);
		expect(tree[0]).toMatchObject({ kind: "dir", label: "src", key: "src" });
		expect(tree[0].kind === "dir" && tree[0].children.map((c) => c.label)).toEqual(["a.ts", "b.ts"]);
	});

	// The mechanism that makes a deep tree survive the rail's 280px floor: a
	// directory whose ONLY child is another directory merges into it, so
	// backend/internal/service/session is one row and one indent, not four.
	it("collapses a single-child directory chain into one row", () => {
		const tree = build("backend/internal/service/session/workspace_changes.go");
		expect(tree).toHaveLength(1);
		expect(tree[0].label).toBe("backend/internal/service/session");
		expect(tree[0].key).toBe("backend/internal/service/session");
		expect(tree[0].kind === "dir" && tree[0].children.map((c) => c.label)).toEqual(["workspace_changes.go"]);
	});

	// Collapsing must stop at a branch point, or sibling directories would be
	// swallowed into whichever chain was walked first.
	it("stops collapsing where a directory has more than one child", () => {
		const tree = build("a/b/c/one.ts", "a/b/d/two.ts");
		expect(tree.map((n) => n.label)).toEqual(["a/b"]);
		const ab = tree[0];
		expect(ab.kind === "dir" && ab.children.map((c) => c.label)).toEqual(["c", "d"]);
	});

	// GitLab keeps `Commons/` and the one file inside it as two rows; merging a
	// directory into its single FILE would hide that file's own row.
	it("does not collapse a directory whose single child is a file", () => {
		const tree = build("src/only.ts");
		expect(tree).toHaveLength(1);
		expect(tree[0].label).toBe("src");
		expect(tree[0].kind === "dir" && tree[0].children.map((c) => c.label)).toEqual(["only.ts"]);
	});

	it("keeps a root-level file at the top level", () => {
		const tree = build("README.md", "src/a.ts");
		expect(tree.map((n) => n.label)).toEqual(["src", "README.md"]);
	});

	it("sorts directories before files, each alphabetically and case-insensitively", () => {
		const tree = build("zeta.ts", "Alpha.md", "src/b.ts", "Docs/x.md");
		expect(tree.map((n) => n.label)).toEqual(["Docs", "src", "Alpha.md", "zeta.ts"]);
	});

	it("carries the original item on every file node", () => {
		const tree = build("src/a.ts");
		const file = tree[0].kind === "dir" ? tree[0].children[0] : tree[0];
		expect(file.kind === "file" && file.item).toEqual({ path: "src/a.ts" });
	});
});

describe("flattenFileTree", () => {
	it("emits every node with its depth, deepest-first order preserved", () => {
		const rows = flattenFileTree(build("a/b/one.ts", "a/c/two.ts"), new Set());
		expect(rows.map((r) => [r.node.label, r.depth])).toEqual([
			["a", 0],
			["b", 1],
			["one.ts", 2],
			["c", 1],
			["two.ts", 2],
		]);
	});

	it("hides the descendants of a collapsed directory but keeps the directory itself", () => {
		const tree = build("a/b/one.ts", "a/c/two.ts");
		const rows = flattenFileTree(tree, new Set(["a/b"]));
		expect(rows.map((r) => r.node.label)).toEqual(["a", "b", "c", "two.ts"]);
	});

	it("reports whether each directory row is expanded", () => {
		const tree = build("a/one.ts");
		expect(flattenFileTree(tree, new Set(["a"]))[0]).toMatchObject({ expanded: false });
		expect(flattenFileTree(tree, new Set())[0]).toMatchObject({ expanded: true });
	});
});

describe("orderedFileItems", () => {
	const order = (...paths: string[]) => orderedFileItems(items(...paths), (f) => f.path).map((f) => f.path);

	// The center pane stacks diffs in this order and the rail lists rows in it,
	// so reading down the tree and scrolling the diffs walk the same sequence.
	// Raw API order does NOT match, because the tree groups directories first.
	it("returns files in the order the fully-expanded tree renders them", () => {
		expect(order("root.ts", "src/b.ts", "src/a.ts", "docs/x.md")).toEqual([
			"docs/x.md",
			"src/a.ts",
			"src/b.ts",
			"root.ts",
		]);
	});

	it("keeps a directory's files together even when the input interleaves them", () => {
		expect(order("a/one.ts", "b/two.ts", "a/three.ts")).toEqual(["a/one.ts", "a/three.ts", "b/two.ts"]);
	});

	it("walks a merged chain in place, not at the end", () => {
		expect(order("zzz.ts", "deep/nested/only/file.go", "aaa.ts")).toEqual([
			"deep/nested/only/file.go",
			"aaa.ts",
			"zzz.ts",
		]);
	});

	it("returns every file exactly once", () => {
		const paths = ["a/b/c.ts", "a/d.ts", "e.ts", "f/g/h/i.ts"];
		expect(order(...paths).sort()).toEqual([...paths].sort());
	});
});

describe("matchesFileQuery", () => {
	it("matches anywhere in the path, not just its start", () => {
		// A previous iteration of this panel shipped prefix-only matching and had
		// to fix it; `fix` must find `hotfix/login-crash`.
		expect(matchesFileQuery("hotfix/login-crash.ts", "fix")).toBe(true);
		expect(matchesFileQuery("src/renderer/components/FilesPanel.tsx", "components")).toBe(true);
	});

	it("ignores case on both sides", () => {
		expect(matchesFileQuery("src/FilesPanel.tsx", "filespanel")).toBe(true);
		expect(matchesFileQuery("src/filespanel.tsx", "FilesPanel")).toBe(true);
	});

	it("matches everything for an empty or whitespace query", () => {
		expect(matchesFileQuery("anything.ts", "")).toBe(true);
		expect(matchesFileQuery("anything.ts", "   ")).toBe(true);
	});

	it("rejects a path that does not contain the query", () => {
		expect(matchesFileQuery("src/a.ts", "zzz")).toBe(false);
	});

	// GitLab's own placeholder advertises `*.vue`, so a query carrying glob
	// characters is treated as a whole-path glob rather than a literal.
	it("treats a query with wildcards as a glob over the whole path", () => {
		expect(matchesFileQuery("src/app/Main.vue", "*.vue")).toBe(true);
		expect(matchesFileQuery("src/app/Main.tsx", "*.vue")).toBe(false);
		expect(matchesFileQuery("src/app/Main.vue", "src/*/Main.vue")).toBe(true);
		expect(matchesFileQuery("src/a.ts", "a?.ts")).toBe(false);
		expect(matchesFileQuery("src/ab.ts", "*/a?.ts")).toBe(true);
	});

	// Regex metacharacters must not leak out of the glob translation: an
	// unescaped `.` would silently turn into "any character".
	it("treats a dot in a glob as a literal, not a wildcard", () => {
		expect(matchesFileQuery("srcXa.ts", "src.a*")).toBe(false);
		expect(matchesFileQuery("src.a.ts", "src.a*")).toBe(true);
		// `+` is likewise literal, and an unbalanced `(` would throw if it reached
		// the RegExp constructor raw.
		expect(matchesFileQuery("a+b/c.ts", "a+b/*")).toBe(true);
		expect(matchesFileQuery("weird(name)/c.ts", "weird(name)/*")).toBe(true);
	});
});
