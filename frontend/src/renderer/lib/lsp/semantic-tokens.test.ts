import { describe, expect, it } from "vitest";
import {
	AO_SEMANTIC_LEGEND,
	decodeServerTokens,
	encodeMonacoTokens,
	type GrammarLines,
	paintSemanticTokens,
	roleAt,
	type ServerToken,
	scopeForToken,
} from "./semantic-tokens";

/**
 * The mapping is the deliverable of this slice; the transport around it is an
 * afternoon. So these pin the DECISIONS, against tokens copied out of the real
 * measurement on the iOS app rather than invented.
 */

/** sourcekit-lsp's legend, as its `initialize` reply actually sends it. */
const SOURCEKIT_LEGEND = {
	tokenTypes: [
		"namespace",
		"type",
		"class",
		"enum",
		"interface",
		"struct",
		"typeParameter",
		"parameter",
		"variable",
		"property",
		"enumMember",
		"event",
		"function",
		"method",
		"macro",
		"keyword",
		"modifier",
		"comment",
		"string",
		"number",
		"regexp",
		"operator",
		"decorator",
		"bracket",
		"label",
		"concept",
		"unknown",
		"identifier",
	],
	tokenModifiers: [
		"declaration",
		"definition",
		"readonly",
		"static",
		"deprecated",
		"abstract",
		"async",
		"modification",
		"documentation",
		"defaultLibrary",
		"deduced",
		"virtual",
		"dependentName",
		"usedAsMutableReference",
		"usedAsMutablePointer",
		"constructorOrDestructor",
		"userDefined",
		"functionScope",
		"classScope",
		"fileScope",
		"globalScope",
	],
};

function token(type: string, modifiers: string[] = []): ServerToken {
	return { line: 0, character: 0, length: 1, type, modifiers };
}

describe("scopeForToken", () => {
	it("splits a reference by project vs SDK, which is Xcode's own split", () => {
		expect(scopeForToken(token("property"), "viewModel", "--code-plain")).toBe("ao.value");
		expect(scopeForToken(token("property", ["defaultLibrary"]), "contentView", "--code-plain")).toBe("ao.value.system");
		expect(scopeForToken(token("class"), "SettingsViewModel", "--code-plain")).toBe("ao.type");
		expect(scopeForToken(token("class", ["defaultLibrary"]), "UILabel", "--code-type-ref")).toBe("ao.type.system");
	});

	it("treats every value kind alike, as Xcode does", () => {
		for (const kind of ["property", "variable", "parameter", "enumMember", "function", "method", "event"]) {
			expect(scopeForToken(token(kind), "name", "--code-plain"), kind).toBe("ao.value");
		}
		for (const kind of ["class", "struct", "enum", "interface", "type", "typeParameter", "namespace"]) {
			expect(scopeForToken(token(kind), "Name", "--code-plain"), kind).toBe("ao.type");
		}
	});

	/**
	 * The headline case: `@IBOutlet weak var titleLabel: UILabel!`. The grammar
	 * emits nothing for `titleLabel`, so it is plain today; sourcekit-lsp reports
	 * the declaration site as the bare type `identifier`.
	 */
	it("colours a declaration the grammar left plain", () => {
		expect(scopeForToken(token("identifier"), "titleLabel", "--code-plain")).toBe("ao.declaration");
	});

	/**
	 * …and does NOT touch one the grammar found. `identifier` cannot tell a type
	 * declaration from a value declaration; the grammar can, and Xcode paints
	 * them differently, so the grammar keeps those.
	 */
	it("leaves declarations the grammar already found alone", () => {
		expect(scopeForToken(token("identifier"), "LanguageSettingViewCell", "--code-type")).toBeNull();
		expect(scopeForToken(token("identifier"), "configure", "--code-declaration")).toBeNull();
	});

	/**
	 * 🗝 Xcode has NO operator role, and #255 made `=`, `->` and `??` plain here
	 * to match. sourcekit-lsp reports `??` as `operator.defaultLibrary` and `!`
	 * and `==` as `method.static.defaultLibrary`, so without the identifier-shape
	 * test the semantic layer would quietly paint them purple.
	 */
	it("never paints an operator", () => {
		expect(scopeForToken(token("operator", ["defaultLibrary"]), "??", "--code-plain")).toBeNull();
		expect(scopeForToken(token("method", ["static", "defaultLibrary"]), "==", "--code-plain")).toBeNull();
		expect(scopeForToken(token("method", ["static", "defaultLibrary"]), "!", "--code-plain")).toBeNull();
	});

	it("never overwrites the grammar's lexical layer", () => {
		// `.init(x:)` arrives as a static SDK method; Xcode paints `init` pink.
		expect(scopeForToken(token("method", ["static", "defaultLibrary"]), "init", "--code-keyword")).toBeNull();
		expect(scopeForToken(token("variable"), "$0", "--code-keyword")).toBeNull();
		expect(scopeForToken(token("property"), "note", "--code-comment")).toBeNull();
		expect(scopeForToken(token("number"), "1", "--code-number")).toBeNull();
	});

	it("does paint inside a string, because that is an interpolation", () => {
		// `"\(NTER.Web.baseURLString)/x"` - the Swift grammar calls the whole
		// interpolation a string; Xcode colours the expression inside it as code.
		expect(scopeForToken(token("property"), "baseURLString", "--code-string")).toBe("ao.value");
	});

	it("drops the kinds the grammar already gets right", () => {
		for (const kind of ["keyword", "comment", "string", "regexp", "bracket", "label", "unknown", "modifier"]) {
			expect(scopeForToken(token(kind), "word", "--code-plain"), kind).toBeNull();
		}
	});

	it("paints a macro with Xcode's preprocessor colour", () => {
		expect(scopeForToken(token("macro"), "Observable", "--code-plain")).toBe("ao.macro");
	});
});

describe("decodeServerTokens", () => {
	it("resolves LSP's relative encoding and names its indices", () => {
		// line 12 col 25 len 10 `property` + defaultLibrary, then col 40 on the
		// same line, then line 14 col 8.
		const property = SOURCEKIT_LEGEND.tokenTypes.indexOf("property");
		const identifier = SOURCEKIT_LEGEND.tokenTypes.indexOf("identifier");
		const defaultLibrary = 1 << SOURCEKIT_LEGEND.tokenModifiers.indexOf("defaultLibrary");
		const decoded = decodeServerTokens(
			{ data: [12, 25, 10, property, defaultLibrary, 0, 15, 4, identifier, 0, 2, 8, 3, property, 0] },
			SOURCEKIT_LEGEND,
		);
		expect(decoded).toEqual([
			{ line: 12, character: 25, length: 10, type: "property", modifiers: ["defaultLibrary"] },
			{ line: 12, character: 40, length: 4, type: "identifier", modifiers: [] },
			{ line: 14, character: 8, length: 3, type: "property", modifiers: [] },
		]);
	});

	it("is empty for an absent or empty answer rather than throwing", () => {
		expect(decodeServerTokens(null, SOURCEKIT_LEGEND)).toEqual([]);
		expect(decodeServerTokens({}, SOURCEKIT_LEGEND)).toEqual([]);
		expect(decodeServerTokens({ data: [] }, SOURCEKIT_LEGEND)).toEqual([]);
	});

	it("skips a type index the legend does not cover instead of mislabelling it", () => {
		expect(decodeServerTokens({ data: [0, 0, 3, 999, 0] }, SOURCEKIT_LEGEND)).toEqual([]);
	});
});

describe("encodeMonacoTokens", () => {
	it("round-trips through the same relative encoding Monaco reads", () => {
		const painted = [
			{ line: 3, character: 4, length: 5, scope: "ao.value" },
			{ line: 3, character: 12, length: 2, scope: "ao.value.system" },
			{ line: 9, character: 0, length: 7, scope: "ao.declaration" },
		] as const;
		const data = encodeMonacoTokens([...painted]);
		expect(
			decodeServerTokens({ data }, { tokenTypes: [...AO_SEMANTIC_LEGEND.tokenTypes], tokenModifiers: [] }),
		).toEqual(
			painted.map((p) => ({ line: p.line, character: p.character, length: p.length, type: p.scope, modifiers: [] })),
		);
	});
});

describe("roleAt", () => {
	const grammar: GrammarLines = [
		[
			{ offset: 0, scope: "keyword" },
			{ offset: 4, scope: "keyword.operator" },
			{ offset: 5, scope: "entity.name.type" },
		],
	];

	it("reads the scope in force at a column", () => {
		expect(roleAt(grammar, 0, 0)).toBe("--code-keyword");
		expect(roleAt(grammar, 0, 4)).toBe("--code-plain");
		expect(roleAt(grammar, 0, 9)).toBe("--code-type");
	});

	/**
	 * 🗝 Unstyled text does not come back with an empty scope: `--code-plain` is
	 * both the default foreground and `keyword.operator`'s colour, so shiki
	 * reverse-maps every unscoped token to `"keyword.operator"`. Both spellings
	 * have to mean plain, or the gap-filling rule stops firing.
	 */
	it("calls both spellings of unstyled text plain", () => {
		expect(roleAt([[{ offset: 0, scope: "" }]], 0, 0)).toBe("--code-plain");
		expect(roleAt([[{ offset: 0, scope: "keyword.operator" }]], 0, 0)).toBe("--code-plain");
	});

	it("is plain where the grammar pass produced nothing at all", () => {
		expect(roleAt([], 5, 3)).toBe("--code-plain");
	});
});

describe("paintSemanticTokens", () => {
	/** `@IBOutlet weak var titleLabel: UILabel!`, as both layers really see it. */
	const line = "    @IBOutlet weak var titleLabel: UILabel!";
	const grammar: GrammarLines = [
		[
			{ offset: 0, scope: "keyword.operator" },
			{ offset: 4, scope: "storage.modifier.attribute" },
			{ offset: 13, scope: "keyword" },
			{ offset: 22, scope: "keyword.operator" },
			{ offset: 34, scope: "keyword.operator" },
		],
	];

	it("fills the property name and re-tints the SDK type, and nothing else", () => {
		const property = SOURCEKIT_LEGEND.tokenTypes.indexOf("identifier");
		const cls = SOURCEKIT_LEGEND.tokenTypes.indexOf("class");
		const modifier = SOURCEKIT_LEGEND.tokenTypes.indexOf("modifier");
		const defaultLibrary = 1 << SOURCEKIT_LEGEND.tokenModifiers.indexOf("defaultLibrary");
		const painted = paintSemanticTokens({
			result: {
				data: [
					// `@IBOutlet` (the grammar's attribute colour is already right),
					0,
					4,
					9,
					modifier,
					0,
					// `titleLabel` at col 23 - plain today.
					0,
					19,
					10,
					property,
					0,
					// `UILabel` at col 35 - the grammar leaves it plain here.
					0,
					12,
					7,
					cls,
					defaultLibrary,
				],
			},
			legend: SOURCEKIT_LEGEND,
			grammar,
			lineText: () => line,
		});
		expect(painted.painted).toBe(2);
		expect(painted.dropped).toBe(1);
		expect(
			decodeServerTokens(
				{ data: painted.data },
				{ tokenTypes: [...AO_SEMANTIC_LEGEND.tokenTypes], tokenModifiers: [] },
			),
		).toEqual([
			{ line: 0, character: 23, length: 10, type: "ao.declaration", modifiers: [] },
			{ line: 0, character: 35, length: 7, type: "ao.type.system", modifiers: [] },
		]);
	});

	it("paints nothing when the server answers nothing", () => {
		const painted = paintSemanticTokens({ result: null, legend: SOURCEKIT_LEGEND, grammar, lineText: () => line });
		expect(painted.painted).toBe(0);
		expect(painted.data).toHaveLength(0);
	});
});
