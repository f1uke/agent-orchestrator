import { describe, expect, it } from "vitest";
import { findFileLinks } from "./terminal-file-links";

// Helper: assert exactly one match whose text is `ref` and whose range slices
// back to `ref` (+ optional line suffix) out of the line.
function single(line: string) {
	const matches = findFileLinks(line);
	expect(matches).toHaveLength(1);
	return matches[0];
}

describe("findFileLinks — the three recognised shapes", () => {
	it("linkifies an absolute path (with a + in the basename)", () => {
		const path = "/Users/x/.ao/data/worktrees/proj/branch/Tests/Money+FormatTests.swift";
		const line = `  compiling ${path} ...`;
		const m = single(line);
		expect(m.ref).toBe(path);
		expect(line.slice(m.startIndex, m.endIndex)).toBe(path);
	});

	it("linkifies a workspace-relative path", () => {
		const ref = "MyApp/Sources/CheckoutRequest.swift";
		const m = single(`edited ${ref} today`);
		expect(m.ref).toBe(ref);
	});

	it("linkifies a bare filename with a code extension", () => {
		const m = single("see CheckoutRequest.swift for details");
		expect(m.ref).toBe("CheckoutRequest.swift");
	});
});

describe("findFileLinks — false-positive guards", () => {
	it("does NOT linkify a dotted symbol/method reference", () => {
		expect(findFileLinks("call Money.formatted on the value")).toHaveLength(0);
		expect(findFileLinks("result of obj.method() here")).toHaveLength(0);
	});

	it("does NOT linkify a package name", () => {
		expect(findFileLinks("import com.example.SomePackage")).toHaveLength(0);
	});

	it("does NOT linkify a version or bare number", () => {
		expect(findFileLinks("upgraded to v1.2.3 now")).toHaveLength(0);
		expect(findFileLinks("exit code 42")).toHaveLength(0);
	});

	it("does NOT linkify a slashed token with an unknown extension", () => {
		expect(findFileLinks("wrote src/foo.bar output")).toHaveLength(0);
	});

	it("does NOT linkify a path inside an http(s) URL", () => {
		expect(findFileLinks("https://github.com/o/r/blob/main/src/app.ts")).toHaveLength(0);
	});
});

describe("findFileLinks — line/column suffix + punctuation", () => {
	it("captures a :line:col suffix and excludes it from ref", () => {
		const m = single("error at src/app.go:42:7 unexpected token");
		expect(m.ref).toBe("src/app.go");
		expect(m.line).toBe(42);
	});

	it("strips trailing sentence punctuation from the path", () => {
		const m = single("please edit main.py.");
		expect(m.ref).toBe("main.py");
	});

	it("does not include a wrapping paren in the ref", () => {
		const m = single("(see index.tsx) for the entry");
		expect(m.ref).toBe("index.tsx");
	});
});

describe("findFileLinks — coexistence + multiplicity", () => {
	it("returns file matches without swallowing #/@ tokens", () => {
		// A line mixing an SCM ref (#123), a session ref (proj-1) and a file.
		const matches = findFileLinks("fixed in main.go per #123 and proj-1");
		expect(matches).toHaveLength(1);
		expect(matches[0].ref).toBe("main.go");
	});

	it("returns multiple file matches in order", () => {
		const matches = findFileLinks("touched a/one.ts then b/two.rs");
		expect(matches.map((m) => m.ref)).toEqual(["a/one.ts", "b/two.rs"]);
		expect(matches[0].startIndex).toBeLessThan(matches[1].startIndex);
	});
});
