/**
 * What the indent engine has to know about a language to re-indent it without a
 * parser: how its strings and comments are spelled (so a `{` inside either is
 * not a bracket), and the few places its house style departs from "one level
 * per open bracket".
 *
 * 🗝 A language is in this table only when bracket structure IS its
 * indentation. Python and YAML are deliberately absent: their indentation is
 * the program, and re-indenting one is changing what it means. Ruby, Lua and
 * shell close blocks with words (`end`, `fi`), which brackets cannot see. For
 * all of those, Re-Indent says it cannot help rather than guessing.
 */

/** Strings that may run past the end of their line, and how each one closes. */
export type MultilineString =
	/** Go's backtick raw string: no escapes, no interpolation. */
	| "go-raw"
	/** JavaScript's template literal, with `${}` holes that are code again. */
	| "js-template"
	/** `"""` (Kotlin, Scala, Java text blocks, GraphQL block strings). */
	| "triple-quote"
	/** Swift's `"""` and every `#`-delimited raw form, single- and multi-line. */
	| "swift"
	/** Rust: every `"` string may span lines, plus `r#"…"#` raw strings. */
	| "rust"
	/** C++ `R"delim(…)delim"`. */
	| "cpp-raw"
	/** C# `@"…"`, where `""` is the only escape. */
	| "verbatim";

export type IndentProfile = {
	lineComments: readonly string[];
	blockComment: { open: string; close: string; nests: boolean } | null;
	/** Delimiters of strings that end at their line's end at the latest. */
	quotes: readonly string[];
	/**
	 * What `'` means when it is not one of `quotes`: a character literal that
	 * closes on the same line (C, Go, Java), Rust's character-or-lifetime, or an
	 * ordinary character (Swift, where `'` is not a delimiter at all).
	 */
	singleQuote: "char" | "rust" | "plain";
	multiline: readonly MultilineString[];
	/** Swift's `\(…)` holes, which may hold strings of their own. */
	swiftInterpolation: boolean;
	/** JavaScript's `/re/` literals, whose `[{]` is not a bracket. */
	regexLiterals: boolean;
	/**
	 * Where `case` sits in a switch. `flat` is gofmt's and Xcode's (and
	 * clang-format's): the labels at the switch's own level, bodies one in.
	 * `indented` is Prettier's and Java's: labels one in, bodies two.
	 */
	switchCase: "flat" | "indented";
	switchKeywords: readonly string[];
	/**
	 * Xcode and clang-format line an argument up under the first one when the
	 * call's opening bracket is followed by code on its own line. gofmt,
	 * Prettier and rustfmt never do.
	 */
	alignToOpenBracket: boolean;
	/** C's `#include` / `#define` live at column 0 whatever surrounds them. */
	preprocessorAtColumnZero: boolean;
	/**
	 * `a ?` continues the statement in a language with a ternary. In Swift and
	 * Kotlin a trailing `?` is an optional TYPE, which ends one.
	 */
	trailingQuestionContinues: boolean;
	/** `=>` at a line's end continues it (JavaScript arrows, C# lambdas). */
	arrowContinues: boolean;
	/**
	 * Statements whose comma-separated lists run over lines and are indented as
	 * continuations: Swift's `guard a,\n    b else`, Go's `case 1,\n\t2:`. A
	 * trailing comma anywhere else separates elements and continues nothing.
	 */
	commaContinues: readonly string[];
	/** Xcode lines a comma-continued list up under its first item; gofmt indents it one level. */
	alignCommaContinuations: boolean;
	/** `if (x)\n    foo();` - a body without braces is one level in. */
	unbracedBodies: boolean;
	/** gofmt puts a `label:` one level out from the code it labels. */
	outdentLabels: boolean;
	/** `key:` at a line's end continues it (object literals) - except a `case x:` label. */
	colonContinues: boolean;
	/** Prettier's ternary layout - see the engine's `ternaryIndent`. */
	prettierTernaries: boolean;
	/** Prettier keeps a grouped condition's operands level: `if (\n\ta ||\n\tb\n)`. */
	alignBracketedOperands: boolean;
	/** JSX elements are structure too: children one level in, `</Tag>` back out. */
	jsx: boolean;
	/** TypeScript type arguments left open at a line's end: `Pick<\n\tT,\n>`. */
	angleGenerics: boolean;
	/** Words that leave a statement unfinished at a line's end: TypeScript's `as`. */
	continuationWords: readonly string[];
	/** Words that continue the line above when they open a line: `extends`, `implements`. */
	leadingContinuationWords: readonly string[];
	/**
	 * Whether a `.member` link right after its head's closing bracket lines up
	 * with it (Xcode's SwiftUI `}` / `.padding()`) or steps in (Prettier).
	 */
	chainAlignsAfterHead: boolean;
	/** Leave `//` lines at column 0 where they are - Xcode's ⌘/ puts commented-out code there. */
	keepColumnZeroComments: boolean;
};

const C_COMMENTS = {
	lineComments: ["//"],
	blockComment: { open: "/*", close: "*/", nests: false },
} as const;

const NESTING_COMMENTS = {
	lineComments: ["//"],
	blockComment: { open: "/*", close: "*/", nests: true },
} as const;

/** The defaults every profile starts from: a C-like language with no extras. */
const BASE: IndentProfile = {
	...C_COMMENTS,
	quotes: ['"'],
	singleQuote: "char",
	multiline: [],
	swiftInterpolation: false,
	regexLiterals: false,
	switchCase: "indented",
	switchKeywords: ["switch"],
	alignToOpenBracket: false,
	preprocessorAtColumnZero: false,
	trailingQuestionContinues: true,
	arrowContinues: false,
	commaContinues: [],
	alignCommaContinuations: false,
	unbracedBodies: false,
	outdentLabels: false,
	colonContinues: false,
	prettierTernaries: false,
	alignBracketedOperands: false,
	jsx: false,
	angleGenerics: false,
	continuationWords: [],
	leadingContinuationWords: [],
	chainAlignsAfterHead: false,
	keepColumnZeroComments: false,
};

const GO: IndentProfile = {
	...BASE,
	multiline: ["go-raw"],
	switchCase: "flat",
	switchKeywords: ["switch", "select"],
	trailingQuestionContinues: false,
	// Go's multi-value forms: `return a,\n\tb`, `if x, y := f(),\n\t\tg(); …`.
	// Composite literals separate their elements with commas inside `{}` and
	// never start with one of these words.
	commaContinues: ["case", "var", "const", "return", "if", "for", "switch"],
	outdentLabels: true,
};

const SWIFT: IndentProfile = {
	...BASE,
	...NESTING_COMMENTS,
	quotes: [],
	singleQuote: "plain",
	multiline: ["swift"],
	swiftInterpolation: true,
	switchCase: "flat",
	alignToOpenBracket: true,
	trailingQuestionContinues: false,
	commaContinues: ["guard", "if", "while", "case", "let", "var"],
	alignCommaContinuations: true,
	// An argument label with its value on the next line: `colors:\n    [`.
	colonContinues: true,
	chainAlignsAfterHead: true,
	keepColumnZeroComments: true,
};

const JAVASCRIPT: IndentProfile = {
	...BASE,
	quotes: ['"', "'"],
	singleQuote: "plain",
	multiline: ["js-template"],
	regexLiterals: true,
	arrowContinues: true,
	unbracedBodies: true,
	colonContinues: true,
	prettierTernaries: true,
	alignBracketedOperands: true,
};

const C_FAMILY: IndentProfile = {
	...BASE,
	switchCase: "flat",
	alignToOpenBracket: true,
	preprocessorAtColumnZero: true,
	unbracedBodies: true,
};

const PROFILES: Record<string, IndentProfile> = {
	go: GO,
	swift: SWIFT,
	typescript: {
		...JAVASCRIPT,
		angleGenerics: true,
		continuationWords: ["as", "satisfies", "extends"],
		leadingContinuationWords: ["extends", "implements"],
	},
	javascript: JAVASCRIPT,
	c: C_FAMILY,
	cpp: { ...C_FAMILY, multiline: ["cpp-raw"] },
	"objective-c": C_FAMILY,
	java: { ...BASE, multiline: ["triple-quote"], unbracedBodies: true },
	csharp: { ...BASE, multiline: ["verbatim"], arrowContinues: true, unbracedBodies: true },
	kotlin: { ...BASE, ...NESTING_COMMENTS, multiline: ["triple-quote"], trailingQuestionContinues: false },
	scala: { ...BASE, ...NESTING_COMMENTS, multiline: ["triple-quote"] },
	rust: {
		...BASE,
		...NESTING_COMMENTS,
		quotes: [],
		singleQuote: "rust",
		multiline: ["rust"],
		trailingQuestionContinues: false,
	},
	json: { ...BASE, singleQuote: "plain" },
	css: { ...BASE, lineComments: [], quotes: ['"', "'"], singleQuote: "plain" },
	scss: { ...BASE, quotes: ['"', "'"], singleQuote: "plain" },
	less: { ...BASE, quotes: ['"', "'"], singleQuote: "plain" },
	proto: { ...BASE, quotes: ['"', "'"], singleQuote: "plain" },
	graphql: { ...BASE, lineComments: ["#"], blockComment: null, singleQuote: "plain", multiline: ["triple-quote"] },
};

/**
 * JSX only where JSX can be. Monaco calls `.ts` and `.tsx` both `typescript`,
 * and in a `.ts` file `<Type>value` is a type assertion, not a tag.
 */
const JSX_FILE = /\.(tsx|jsx|js|mjs|cjs)$/i;

/**
 * The profile for a Monaco language id, or null when brackets are not its
 * indentation. `path` decides whether a TypeScript/JavaScript file may hold JSX.
 */
export function indentProfileFor(languageId: string, path = ""): IndentProfile | null {
	const profile = PROFILES[languageId];
	if (!profile) return null;
	if ((languageId === "typescript" || languageId === "javascript") && JSX_FILE.test(path))
		return { ...profile, jsx: true };
	return profile;
}
