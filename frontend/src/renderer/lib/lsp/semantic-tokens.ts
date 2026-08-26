import { grammarRole, SEMANTIC_SCOPES, type SemanticScope, type SyntaxRole } from "../monaco-theme";

/**
 * What the grammar cannot know, asked of the language server instead.
 *
 * shiki tokenises with TextMate grammars: regex over word shapes. So
 * `titleLabel` in `@IBOutlet weak var titleLabel: UILabel!` is plain text, and
 * `UILabel` is indistinguishable from a type you wrote yourself. Xcode colours
 * both, because it asks the compiler. `textDocument/semanticTokens` is that same
 * question, and this is the part of the answer we can paint.
 *
 * ─────────────────────────────────────────────────────────────────────────────
 * The rule the whole file follows:
 *
 *   The GRAMMAR knows where a name is DECLARED. The SERVER knows what a name
 *   REFERS TO. Each keeps what it knows.
 *
 * Reference kinds overrule the grammar - that is the point of being here at all.
 * Declaration sites do not: sourcekit-lsp reports every declared name as the
 * bare type `identifier`, which cannot tell `class Foo` from `var foo`, while
 * the grammar can and does. So `identifier` is applied only where the grammar
 * left the token plain, which is exactly the gap: properties, locals and
 * parameters. `class Foo` keeps Xcode's declaration-type cyan.
 * ─────────────────────────────────────────────────────────────────────────────
 *
 * Measured on the real iOS app (sourcekit-lsp, Xcode 26.3) before any of this
 * was written, on three files of 46, 120 and 1902 lines:
 *
 * - `semanticTokens/full` BLOCKS until the file is type-checked and then answers
 *   complete: 1.4-1.9 s cold, 66-113 ms warm, and the token count never moved
 *   again when re-asked at 1, 2, 4, 8 and 20 s. So the reader sees full TextMate
 *   colouring and then ONE enrichment, not colours crawling in.
 * - ~80% of what that enrichment does is give colour to text that was plain.
 *   Keywords, comments, strings and numbers never move, which is what keeps it
 *   from reading as the file re-colouring itself.
 * - The server never sends `workspace/semanticTokens/refresh` - 20 s of
 *   listening produced one `publishDiagnostics` and nothing else. One request
 *   per open is the whole mechanism; there is nothing to subscribe to.
 * - `full` is advertised as a BOOLEAN, so there is no delta support. Every
 *   refresh re-fetches the file. Fine on open and save; never on a keystroke.
 */

/** LSP's `SemanticTokens` result: `data` is 5 uint32 per token, all relative. */
export type SemanticTokensResult = { data?: number[] | Uint32Array; resultId?: string };

export type SemanticTokensLegend = { tokenTypes: string[]; tokenModifiers: string[] };

/** One decoded server token, in LSP's 0-based line/character coordinates. */
export type ServerToken = {
	line: number;
	character: number;
	length: number;
	type: string;
	modifiers: string[];
};

/**
 * Kinds that name a TYPE. `concept` and `actor` are in sourcekit-lsp's legend
 * for languages/versions this one may not produce; listing them costs nothing
 * and stops a future toolchain painting them plain.
 */
const TYPE_KINDS = new Set([
	"class",
	"struct",
	"enum",
	"interface",
	"type",
	"typeParameter",
	"namespace",
	"concept",
	"actor",
]);

/** Kinds that name a VALUE - Xcode's `identifier.function`/`.variable`/`.constant`. */
const VALUE_KINDS = new Set(["property", "variable", "parameter", "enumMember", "function", "method", "event"]);

/** Kinds Xcode paints with the preprocessor colour. */
const MACRO_KINDS = new Set(["macro", "decorator"]);

/**
 * Roles the semantic layer must not overwrite.
 *
 * `comment`/`keyword`/`number` are the grammar's lexical layer, which is right,
 * and which the server agrees with - re-sending them buys nothing and costs
 * churn. Measured examples of the harm: `.init(x:)` arrives as
 * `method.static.defaultLibrary` and would turn Xcode's pink `init` purple, and
 * `$0` arrives as `variable`.
 *
 * `--code-type` and `--code-declaration` are the grammar's DECLARATION finding,
 * which is the half of Xcode's split the server does not report.
 *
 * `--code-string` is deliberately absent: the only semantic tokens that land on
 * string-coloured text are inside `\(…)` interpolations, which Xcode paints as
 * code and the Swift grammar paints as string.
 */
const PROTECTED_ROLES = new Set<SyntaxRole>([
	"--code-comment",
	"--code-keyword",
	"--code-number",
	"--code-type",
	"--code-declaration",
]);

/**
 * Only identifier-shaped text is painted.
 *
 * 🗝 Xcode has NO operator role - `=`, `->`, `??` are plain, and #255 made them
 * plain here too. sourcekit-lsp reports `??` as `operator.defaultLibrary` and
 * `!`/`==` as `method.static.defaultLibrary`, so without this the semantic layer
 * would quietly undo that decision.
 */
const IDENTIFIER_START = /^[\p{L}_$]/u;

/** The legend this app asks Monaco to resolve against - see `SEMANTIC_SCOPES`. */
export const AO_SEMANTIC_LEGEND: { tokenTypes: SemanticScope[]; tokenModifiers: string[] } = {
	tokenTypes: SEMANTIC_SCOPES.map((s) => s.scope),
	// The `.system` half is spelled into the scope itself rather than carried as
	// a modifier: Monaco joins type and modifiers with a dot either way, and one
	// flat list is one thing to keep in step with the theme instead of two.
	tokenModifiers: [],
};

const SCOPE_INDEX = new Map<SemanticScope, number>(AO_SEMANTIC_LEGEND.tokenTypes.map((scope, i) => [scope, i]));

/**
 * The scope one server token should be painted with, or null to leave it to the
 * grammar. Pure, and the whole mapping: everything else here is transport.
 */
export function scopeForToken(token: ServerToken, text: string, grammar: SyntaxRole): SemanticScope | null {
	if (!IDENTIFIER_START.test(text)) return null;
	if (PROTECTED_ROLES.has(grammar)) return null;
	const system = token.modifiers.includes("defaultLibrary");
	if (MACRO_KINDS.has(token.type)) return "ao.macro";
	if (TYPE_KINDS.has(token.type)) return system ? "ao.type.system" : "ao.type";
	if (VALUE_KINDS.has(token.type)) return system ? "ao.value.system" : "ao.value";
	// A declaration site. The grammar owns those it can see; this is the rest.
	if (token.type === "identifier") return grammar === "--code-plain" ? "ao.declaration" : null;
	return null;
}

/** LSP's relative encoding → absolute positions, with names rather than indices. */
export function decodeServerTokens(result: SemanticTokensResult | null, legend: SemanticTokensLegend): ServerToken[] {
	const data = result?.data;
	if (!data || data.length === 0) return [];
	const tokens: ServerToken[] = [];
	let line = 0;
	let character = 0;
	for (let i = 0; i + 4 < data.length; i += 5) {
		const deltaLine = data[i];
		const deltaStart = data[i + 1];
		line += deltaLine;
		character = deltaLine === 0 ? character + deltaStart : deltaStart;
		const type = legend.tokenTypes[data[i + 3]];
		if (type === undefined) continue;
		const bits = data[i + 4];
		const modifiers: string[] = [];
		for (let b = 0; b < legend.tokenModifiers.length; b++) {
			if (bits & (1 << b)) modifiers.push(legend.tokenModifiers[b]);
		}
		tokens.push({ line, character, length: data[i + 2], type, modifiers });
	}
	return tokens;
}

/**
 * What the grammar made of each line, one scope string per character.
 *
 * `monaco.editor.tokenize` is Monaco's public tokenizer entry point; under
 * `@shikijs/monaco` it answers with shiki's own tokens, reverse-mapped to a
 * scope name. Measured cost of the whole-file pass: 53 ms at 120 lines, 185 ms
 * at 1902 lines - paid once per ANSWERED request, so on open and on save, never
 * while typing.
 */
export type GrammarLines = readonly { readonly offset: number; readonly scope: string }[][];

export function roleAt(grammar: GrammarLines, line: number, character: number): SyntaxRole {
	const tokens = grammar[line];
	if (!tokens || tokens.length === 0) return "--code-plain";
	// Small lines, linear scan: a binary search here would be optimising the
	// wrong end - the request it belongs to took a second and a half.
	let scope = "";
	for (const token of tokens) {
		if (token.offset > character) break;
		scope = token.scope;
	}
	return grammarRole(scope);
}

/** Absolute, painted tokens → Monaco's relative uint32 encoding. */
export function encodeMonacoTokens(
	painted: readonly { line: number; character: number; length: number; scope: SemanticScope }[],
): Uint32Array {
	const data = new Uint32Array(painted.length * 5);
	let line = 0;
	let character = 0;
	for (let i = 0; i < painted.length; i++) {
		const token = painted[i];
		const deltaLine = token.line - line;
		data[i * 5] = deltaLine;
		data[i * 5 + 1] = deltaLine === 0 ? token.character - character : token.character;
		data[i * 5 + 2] = token.length;
		data[i * 5 + 3] = SCOPE_INDEX.get(token.scope) ?? 0;
		data[i * 5 + 4] = 0;
		line = token.line;
		character = token.character;
	}
	return data;
}

/**
 * The whole pipeline, as one pure function so a test can pin the OUTPUT rather
 * than the fact that a request was sent.
 */
export function paintSemanticTokens(input: {
	result: SemanticTokensResult | null;
	legend: SemanticTokensLegend;
	grammar: GrammarLines;
	lineText: (line: number) => string;
}): { data: Uint32Array; painted: number; dropped: number } {
	const painted: { line: number; character: number; length: number; scope: SemanticScope }[] = [];
	let dropped = 0;
	for (const token of decodeServerTokens(input.result, input.legend)) {
		const text = input.lineText(token.line).slice(token.character, token.character + token.length);
		const scope = scopeForToken(token, text, roleAt(input.grammar, token.line, token.character));
		if (!scope) {
			dropped++;
			continue;
		}
		painted.push({ line: token.line, character: token.character, length: token.length, scope });
	}
	return { data: encodeMonacoTokens(painted), painted: painted.length, dropped };
}
