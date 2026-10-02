import { describe, expect, it } from "vitest";
import { computeReindent, type IndentUnit, reindentDocument } from "./indent-engine";
import { indentProfileFor } from "./indent-profiles";

const TABS: IndentUnit = { insertSpaces: false, indentSize: 4 };
const FOUR: IndentUnit = { insertSpaces: true, indentSize: 4 };
const TWO: IndentUnit = { insertSpaces: true, indentSize: 2 };

/** Every line flush left, so the expected output is the engine's alone. */
function flatten(text: string): string {
	return text
		.split("\n")
		.map((line) => line.trimStart())
		.join("\n");
}

function reindent(language: string, text: string, unit: IndentUnit): string {
	const profile = indentProfileFor(language);
	if (!profile) throw new Error(`no profile for ${language}`);
	return reindentDocument(text, profile, unit);
}

/** The expected text survives being flattened and re-indented, byte for byte. */
function expectRoundTrip(language: string, expected: string, unit: IndentUnit): void {
	expect(reindent(language, flatten(expected), unit)).toBe(expected);
	// And re-indenting already-correct code changes nothing.
	expect(reindent(language, expected, unit)).toBe(expected);
}

describe("Go (gofmt's layout)", () => {
	it("indents blocks with one tab per level", () => {
		expectRoundTrip(
			"go",
			[
				"package main",
				"",
				"func main() {",
				"\tfor i := 0; i < 3; i++ {",
				"\t\tif i%2 == 0 {",
				"\t\t\tfmt.Println(i)",
				"\t\t} else {",
				"\t\t\tcontinue",
				"\t\t}",
				"\t}",
				"}",
			].join("\n"),
			TABS,
		);
	});

	it("puts case labels at the switch's level and their bodies one in", () => {
		expectRoundTrip(
			"go",
			[
				"func f(x int) {",
				"\tswitch x {",
				"\tcase 1,",
				"\t\t2:",
				"\t\tfoo()",
				"\tdefault:",
				"\t\tbar()",
				"\t}",
				"\tselect {",
				"\tcase <-done:",
				"\t\treturn",
				"\t}",
				"}",
			].join("\n"),
			TABS,
		);
	});

	it("indents a condition that runs over lines once, and its body from the statement", () => {
		expectRoundTrip(
			"go",
			[
				"func f() {",
				"\tif a &&",
				"\t\tb &&",
				"\t\tc {",
				"\t\tbody()",
				"\t}",
				"\tx := one +",
				"\t\ttwo",
				"\ty := 1",
				"}",
			].join("\n"),
			TABS,
		);
	});

	it("does not treat a composite literal's commas as continuations", () => {
		expectRoundTrip(
			"go",
			["var xs = []T{", "\t{", '\t\tName: "a",', "\t\tAge:  1,", "\t},", '\t{Name: "b"},', "}"].join("\n"),
			TABS,
		);
	});

	it("hangs a call's arguments off the call, and a closure's close off its opener", () => {
		expectRoundTrip(
			"go",
			[
				"func f() {",
				"\tdo(a,",
				"\t\tb)",
				"\terr := run(func() error {",
				"\t\treturn nil",
				"\t})",
				"\tfoo(",
				"\t\tx,",
				"\t\ty,",
				"\t)",
				"}",
			].join("\n"),
			TABS,
		);
	});

	it("never touches the inside of a raw string, and ignores brackets in strings and runes", () => {
		const text = [
			"func f() {",
			"\ts := `{",
			"   keep {{ this",
			"}`",
			"\tr := '{'",
			'\tq := "}}}"',
			"\treturn",
			"}",
		].join("\n");
		expect(reindent("go", text, TABS)).toBe(text);
		const messy = text.replace("\treturn", "return").replace("\tr :=", "r :=");
		expect(reindent("go", messy, TABS)).toBe(text);
	});

	it("keeps a block comment's own alignment while moving it with its first line", () => {
		expect(reindent("go", ["func f() {", "/* one {", " * two", " */", "x()", "}"].join("\n"), TABS)).toBe(
			["func f() {", "\t/* one {", "\t * two", "\t */", "\tx()", "}"].join("\n"),
		);
	});

	it("puts a label one level out, but not a composite literal's keys", () => {
		expectRoundTrip(
			"go",
			["func f() {", "outer:", "\tfor {", "\t\tbreak outer", "\t}", "\treturn T{", "\t\tName: x,", "\t}", "}"].join(
				"\n",
			),
			TABS,
		);
	});

	it("keeps a comment that is an empty case's whole body in that case", () => {
		expectRoundTrip("go", ["switch x {", "case a:", "\t// nothing to do", "case b:", "\tb()", "}"].join("\n"), TABS);
	});

	it("lines up a multi-line string argument's lines, as gofmt does", () => {
		expectRoundTrip(
			"go",
			["func f() {", '\tw.Write(a, "x" +', '\t\t"y" +', '\t\t"z")', "\treturn a,", "\t\tb", "}"].join("\n"),
			TABS,
		);
	});

	it("indents a trailing-dot method chain", () => {
		expectRoundTrip("go", ["func f() {", "\tv := x.", "\t\tFoo().", "\t\tBar()", "\tw := 1", "}"].join("\n"), TABS);
	});
});

describe("Swift (Xcode's layout)", () => {
	it("re-indents a type with methods, guard and if/else", () => {
		expectRoundTrip(
			"swift",
			[
				"final class Greeter {",
				"    private let name: String",
				"",
				"    func greet(_ who: String?) -> String {",
				"        guard let who else {",
				'            return "nobody"',
				"        }",
				"        if who.isEmpty {",
				"            return name",
				"        } else {",
				"            return who",
				"        }",
				"    }",
				"}",
			].join("\n"),
			FOUR,
		);
	});

	it("puts case labels at the switch's level, including @unknown default", () => {
		expectRoundTrip(
			"swift",
			[
				"switch value {",
				"case .a,",
				"     .b:",
				"    run()",
				"@unknown default:",
				"    break",
				"}",
				"enum Kind {",
				"    case one",
				"    case two",
				"}",
			].join("\n"),
			FOUR,
		);
	});

	it("indents a leading-dot chain once, and a closure on it from the chain line", () => {
		expectRoundTrip(
			"swift",
			["let doubled = items", "    .filter { $0 > 0 }", "    .map {", "        $0 * 2", "    }", "let next = 1"].join(
				"\n",
			),
			FOUR,
		);
	});

	it("lines arguments up under the first one when it follows the bracket", () => {
		expectRoundTrip(
			"swift",
			[
				"let view = UIView(frame: .zero,",
				"                  style: .plain)",
				"let list = [1, 2,",
				"            3]",
				"call(",
				"    a: 1,",
				"    b: 2",
				")",
			].join("\n"),
			FOUR,
		);
	});

	it("lines condition lists up under the first condition, and indents the body from the statement", () => {
		expectRoundTrip(
			"swift",
			[
				"func f() {",
				"    guard let a = x,",
				"          let b = y else {",
				"        return",
				"    }",
				"    if let c = z,",
				"       c > 1 {",
				"        use(c)",
				"    }",
				"}",
			].join("\n"),
			FOUR,
		);
	});

	it("lines SwiftUI modifiers up with the closing brace they follow", () => {
		expectRoundTrip(
			"swift",
			[
				"var body: some View {",
				"    VStack(spacing: 8) {",
				"        Text(title)",
				"            .font(.headline)",
				"            .padding()",
				"    }",
				"    .padding(.vertical, 24)",
				"    .frame(maxWidth: .infinity)",
				"}",
			].join("\n"),
			FOUR,
		);
	});

	it("steps a chain in after a closer line that already carries a call", () => {
		expectRoundTrip(
			"swift",
			[
				"func f() {",
				"    api.call(",
				"        a: 1",
				"    ).subscribe(onNext: { _ in })",
				"        .disposed(by: bag)",
				"}",
			].join("\n"),
			FOUR,
		);
	});

	it("finds the switch behind a multi-line subject", () => {
		expectRoundTrip(
			"swift",
			["switch (", "    a,", "    b", ") {", "case (true, false):", "    run()", "default:", "    break", "}"].join(
				"\n",
			),
			FOUR,
		);
	});

	it("leaves ⌘/-commented lines at column 0", () => {
		const text = ["func f() {", "    run()", "//    oldRun()", "}"].join("\n");
		expect(reindent("swift", text, FOUR)).toBe(text);
	});

	it("indents an argument whose value starts on the line below its label", () => {
		expectRoundTrip(
			"swift",
			[
				"view.addGradient(",
				"    colors:",
				"        [",
				"            .white,",
				"            .black",
				"        ],",
				"    angle: 90",
				")",
			].join("\n"),
			FOUR,
		);
	});

	it("hangs a trailing closure after a closing bracket off the call that opened it", () => {
		expectRoundTrip(
			"swift",
			[
				"UIView.animate(withDuration: 0.3, animations: {",
				"    view.alpha = 0",
				"}) { finished in",
				"    done(finished)",
				"}",
			].join("\n"),
			FOUR,
		);
	});

	it("leaves a multi-line string's body and closing delimiter exactly where they are", () => {
		const text = [
			"func f() -> String {",
			'    let s = """',
			"  { not code",
			"      }",
			'  """',
			"    return s",
			"}",
		].join("\n");
		expect(reindent("swift", text, FOUR)).toBe(text);
		expect(reindent("swift", text.replace("    return s", "return s"), FOUR)).toBe(text);
	});

	it("reads interpolation, quotes inside interpolation, and raw strings without losing count", () => {
		expectRoundTrip(
			"swift",
			[
				"func f() {",
				'    let a = "\\(dict["}"] ?? "{") and \\(g(1))"',
				'    let b = #"raw { \\" "#',
				'    let c = #"hole \\#(x) }"#',
				"    done()",
				"}",
			].join("\n"),
			FOUR,
		);
	});

	it("skips nested block comments, which Swift allows", () => {
		expectRoundTrip(
			"swift",
			["func f() {", "    /* outer { /* inner } */ still comment { */", "    x()", "}"].join("\n"),
			FOUR,
		);
	});

	it("does not read an optional type's trailing ? as a continuation", () => {
		expectRoundTrip("swift", ["struct S {", "    var a: Int?", "    var b: String", "}"].join("\n"), FOUR);
	});

	it("keeps #if directives with the code they guard", () => {
		expectRoundTrip("swift", ["func f() {", "    #if DEBUG", "    log()", "    #endif", "}"].join("\n"), FOUR);
	});
});

describe("TypeScript / JavaScript (Prettier's layout)", () => {
	it("indents case labels one level and their bodies two", () => {
		expectRoundTrip(
			"typescript",
			[
				"function f(x: number) {",
				"  switch (x) {",
				"    case 1:",
				"      a();",
				"      break;",
				"    case 2: {",
				"      b();",
				"      break;",
				"    }",
				"    default:",
				"      c();",
				"  }",
				"}",
			].join("\n"),
			TWO,
		);
	});

	it("ignores brackets in template literals, holes, regexes and quotes", () => {
		expectRoundTrip(
			"typescript",
			[
				"const a = `{ ${b ? `}` : '{'} text`;",
				"const re = /[{(]+/g;",
				"const s = '}' + \"{\";",
				"if (re.test(a)) {",
				"  run(a / 2);",
				"}",
			].join("\n"),
			TWO,
		);
	});

	it("never touches a template literal's lines", () => {
		const text = ["function f() {", "  return `", "line {", "    ${x}", "`;", "}"].join("\n");
		expect(reindent("typescript", text, TWO)).toBe(text);
	});

	it("indents object and array literals and an arrow's continuation", () => {
		expectRoundTrip(
			"typescript",
			[
				"const config = {",
				"  items: [",
				"    { id: 1 },",
				"    { id: 2 },",
				"  ],",
				"  pick: (x: Item) =>",
				"    x.id,",
				"};",
				"const chained = promise",
				"  .then((v) => {",
				"    return v;",
				"  })",
				"  .catch(fail);",
			].join("\n"),
			TWO,
		);
	});

	it("contains an apostrophe in JSX text to its own line", () => {
		expectRoundTrip(
			"typescript",
			["function C() {", "  return (", "    <p>Don't {stop}</p>", "  );", "}", "const after = 1;"].join("\n"),
			TWO,
		);
	});

	it("lays JSX out by element: children in, closing tags and `/>` back out", () => {
		const profile = indentProfileFor("typescript", "View.tsx");
		if (!profile) throw new Error("no tsx profile");
		const expected = [
			"export function View() {",
			"\treturn (",
			'\t\t<div className="x">',
			"\t\t\t<p>Don't stop // here</p>",
			"\t\t\t<Button",
			"\t\t\t\tonClick={() => {",
			"\t\t\t\t\trun();",
			"\t\t\t\t}}",
			"\t\t\t\tdisabled={",
			"\t\t\t\t\tbusy ||",
			"\t\t\t\t\tfailed",
			"\t\t\t\t}",
			"\t\t\t/>",
			"\t\t\t<>",
			"\t\t\t\t{items.map((item) => (",
			"\t\t\t\t\t<Row key={item.id} />",
			"\t\t\t\t))}",
			"\t\t\t</>",
			"\t\t</div>",
			"\t);",
			"}",
		].join("\n");
		expect(reindentDocument(flatten(expected), profile, TABS)).toBe(expected);
	});

	it("does not read a .tsx generic arrow, or a .ts type assertion, as a tag", () => {
		const tsx = indentProfileFor("typescript", "a.tsx");
		const ts = indentProfileFor("typescript", "a.ts");
		if (!tsx || !ts) throw new Error("no profile");
		const arrow = [
			"const edit =",
			"\t<T,>(set: (v: T) => void) =>",
			"\t(value: T) => {",
			"\t\tset(value);",
			"\t};",
		].join("\n");
		expect(reindentDocument(flatten(arrow), tsx, TABS)).toBe(arrow);
		const assertion = ["function f() {", "\tconst x = <Foo>bar;", "\treturn x;", "}"].join("\n");
		expect(reindentDocument(flatten(assertion), ts, TABS)).toBe(assertion);
	});

	it("follows Prettier's ternaries: branches stepped in, objects on a branch two in", () => {
		expectRoundTrip(
			"typescript",
			[
				"const failure =",
				"\tcaught instanceof Error",
				"\t\t? caught.failure",
				"\t\t: {",
				'\t\t\t\ttitle: "x",',
				"\t\t\t};",
				"const parsed = () =>",
				'\tkind === "list"',
				"\t\t? text",
				'\t\t\t\t.split(",")',
				"\t\t\t\t.filter(Boolean)",
				"\t\t: text;",
			].join("\n"),
			TABS,
		);
	});

	it("keeps a grouped condition's operands level and steps a call's in", () => {
		expectRoundTrip(
			"typescript",
			["if (", "\ta ||", "\tb ||", "\tc", ") {", "\trun(", "\t\tx +", "\t\t\ty,", "\t);", "}"].join("\n"),
			TABS,
		);
	});

	it("indents type arguments, union members and `as` continuations", () => {
		expectRoundTrip(
			"typescript",
			[
				"type Host = Pick<",
				"\tWebContents,",
				'\t| "goBack"',
				'\t| "reload"',
				">;",
				"type Kind =",
				"\t| {",
				'\t\t\tkind: "a";',
				"\t  }",
				'\t| { kind: "b" };',
				"const result = value as",
				"\tunknown[];",
				"export interface Props",
				"\textends Base {",
				"\tasChild?: boolean;",
				"}",
			].join("\n"),
			TABS,
		);
	});

	it("reads `?.` as a chain link and `/` inside a template hole as division", () => {
		expectRoundTrip(
			"typescript",
			[
				"const value = models",
				"\t.getModels()[0]",
				"\t?.getValue();",
				"const style = { width: `${(count / total) * 100}%` };",
				"if (style) {",
				"\tdone();",
				"}",
			].join("\n"),
			TABS,
		);
	});

	it("puts a comment where the code it introduces goes", () => {
		expectRoundTrip("typescript", ["type Mode =", '\t| "a"', "\t/** The default. */", '\t| "b";'].join("\n"), TABS);
	});

	it("indents a brace-less if body one level", () => {
		expectRoundTrip(
			"javascript",
			["function f(a) {", "  if (a)", "    return 1;", "  else", "    return 2;", "  done();", "}"].join("\n"),
			TWO,
		);
	});
});

describe("C family", () => {
	it("keeps preprocessor lines at column 0", () => {
		expectRoundTrip(
			"c",
			["int main(void) {", "#ifdef DEBUG", '    log("{");', "#endif", "    return 0;", "}"].join("\n"),
			FOUR,
		);
	});

	it("puts case labels at the switch's level (clang-format)", () => {
		expectRoundTrip(
			"cpp",
			["void f(int x) {", "    switch (x) {", "    case 1:", "        a();", "        break;", "    }", "}"].join("\n"),
			FOUR,
		);
	});
});

describe("ranges and blank lines", () => {
	const profile = indentProfileFor("go");
	if (!profile) throw new Error("no go profile");

	it("re-indents only the requested lines, hanging them off the real lines above", () => {
		const lines = ["func f() {", "  if x {", "y()", "z()", "  }", "}"];
		const changes = computeReindent({ lines, profile, unit: TABS, from: 2, to: 2 });
		// Anchored on the `if` line's REAL indentation (two spaces), not on what
		// gofmt would make of it: the range is all that may change.
		expect(changes).toEqual([{ line: 2, indent: "  \t", previous: "" }]);
	});

	it("leaves whitespace-only lines alone, except the ones asked to be indented", () => {
		const lines = ["func f() {", "\tx()", "   ", "", "}"];
		expect(computeReindent({ lines, profile, unit: TABS, from: 0, to: 4 })).toEqual([]);
		expect(computeReindent({ lines, profile, unit: TABS, from: 3, to: 3, indentBlank: new Set([3]) })).toEqual([
			{ line: 3, indent: "\t", previous: "" },
		]);
	});

	it("indents a blank caret line as a continuation after a trailing operator", () => {
		const lines = ["func f() {", "\tx := a +", "", "}"];
		expect(computeReindent({ lines, profile, unit: TABS, from: 2, to: 2, indentBlank: new Set([2]) })).toEqual([
			{ line: 2, indent: "\t\t", previous: "" },
		]);
	});

	it("survives unbalanced brackets above the range", () => {
		const lines = ["func f() {", "\tfoo(a", "}", "func g() {", "x()", "}"];
		const changes = computeReindent({ lines, profile, unit: TABS, from: 4, to: 4 });
		expect(changes).toEqual([{ line: 4, indent: "\t", previous: "" }]);
	});

	it("keeps CRLF line endings out of the indentation", () => {
		expect(reindent("go", "func f() {\r\nx()\r\n}\r\n", TABS)).toBe("func f() {\r\n\tx()\r\n}\r\n");
	});
});

describe("profiles", () => {
	it("has none for languages whose indentation is not their brackets", () => {
		for (const language of ["python", "yaml", "ruby", "shell", "markdown", "plaintext"]) {
			expect(indentProfileFor(language)).toBeNull();
		}
	});
});
