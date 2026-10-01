import { describe, expect, test } from "vitest";
import { buildInfillRequest, cleanCompletion, MAX_LINE_SUFFIX, PREFIX_LINES } from "./context";

const FILE = ["package main", "", "func main() {", "\tfmt.Pri", "}", ""];

describe("buildInfillRequest", () => {
	test("splits the cursor's line into prompt and the start of the suffix", () => {
		const r = buildInfillRequest({ lines: FILE, lineNumber: 4, column: 9 }, []);
		expect(r).toEqual({
			inputPrefix: "package main\n\nfunc main() {\n",
			prompt: "\tfmt.Pri",
			inputSuffix: "\n}\n",
			inputExtra: [],
			nIndent: 1,
		});
	});

	test("mid-line, the rest of the line leads the suffix", () => {
		const lines = ["x := foo()"];
		const r = buildInfillRequest({ lines, lineNumber: 1, column: 9 }, []);
		expect(r?.prompt).toBe("x := foo");
		expect(r?.inputSuffix.startsWith("()\n")).toBe(true);
	});

	test("does not ask when more than MAX_LINE_SUFFIX characters sit right of the cursor", () => {
		const line = `call(${"a".repeat(MAX_LINE_SUFFIX + 1)})`;
		expect(buildInfillRequest({ lines: [line], lineNumber: 1, column: 6 }, [])).toBeNull();
		// Closing punctuation and a short tail are still fine.
		expect(buildInfillRequest({ lines: ["call()"], lineNumber: 1, column: 6 }, [])).not.toBeNull();
	});

	test("sends at most PREFIX_LINES lines above", () => {
		const lines = Array.from({ length: PREFIX_LINES + 50 }, (_, i) => `l${i}`);
		const r = buildInfillRequest({ lines, lineNumber: lines.length, column: 1 }, []);
		expect(r?.inputPrefix.split("\n").length).toBe(PREFIX_LINES + 1);
		expect(r?.inputPrefix.startsWith("l49\n")).toBe(true);
	});
});

describe("cleanCompletion", () => {
	const view = (lines: string[], lineNumber: number, column: number) => ({ lines, lineNumber, column });

	test("drops trailing blank lines", () => {
		expect(cleanCompletion("ntln(x)\n\n  \n", view(FILE, 4, 9))).toBe("ntln(x)");
	});

	test("nothing but whitespace is nothing", () => {
		expect(cleanCompletion("   \n\t", view(FILE, 4, 9))).toBeNull();
		expect(cleanCompletion("", view(FILE, 4, 9))).toBeNull();
	});

	test("trailing lines that repeat the code already below the cursor are cut", () => {
		const lines = ["if ok {", "\treturn", "}", "next()"];
		// The model finished the block AND re-wrote the closing brace that exists.
		expect(cleanCompletion(" nil\n}", view(lines, 2, 8))).toBe(" nil");
	});

	test("a guess that only re-types the next line is dropped whole", () => {
		const lines = ["a()", "", "b()"];
		expect(cleanCompletion("b()", view(lines, 2, 1))).toBeNull();
	});

	test("a real multi-line guess survives", () => {
		const lines = ["func f() {", "\t", "}"];
		expect(cleanCompletion("x := 1\n\treturn x", view(lines, 2, 2))).toBe("x := 1\n\treturn x");
	});
});
