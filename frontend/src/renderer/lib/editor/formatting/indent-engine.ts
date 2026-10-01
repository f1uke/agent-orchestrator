import type { IndentProfile } from "./indent-profiles";

/**
 * Xcode's Re-Indent, for any language whose indentation is its bracket
 * structure: given a document and a range of its lines, the leading whitespace
 * each of those lines should have. Indentation ONLY - nothing past a line's
 * first non-blank character is ever read back out of this module, let alone
 * rewritten.
 *
 * One engine serves every way indentation happens in the editor - Return, a
 * closing bracket, a paste, ⌃I, and the format-document fallback for languages
 * with no formatter - so they can never disagree about where a line belongs.
 *
 * ## The rules
 *
 * - A line inside an open bracket sits one level in from that bracket's ANCHOR
 *   line. A line that starts with a closing bracket sits AT its opener's anchor.
 * - The anchor is usually the opener's own line. It is the statement's first line
 *   when the opener is a block brace on a condition that ran over lines
 *   (`if a &&\n\tb {` - gofmt puts the body one level in from `if`, not from
 *   `b`), and it is the outermost earlier line closed on the opener's line
 *   (`}) { done in` hangs off the call that started two lines up).
 * - A statement that runs over lines - a trailing operator, or a line opening
 *   with `.member`, `&&`, `||`, `??` - indents its continuation lines one level,
 *   once, however many there are.
 * - Lines inside a string are data and are never touched: re-indenting a Swift
 *   `"""` body changes the string. Lines inside a block comment move with the
 *   comment's first line, keeping their own alignment.
 *
 * Several lines' worth of house style ride on the profile: where `case` sits,
 * whether arguments line up under the first one, C's preprocessor at column 0.
 */

export type IndentUnit = {
	/** Spaces when true, a tab per level when false. */
	insertSpaces: boolean;
	/** Columns per level when indenting with spaces. */
	indentSize: number;
};

export type IndentChange = {
	/** 0-based. */
	line: number;
	/** The leading whitespace the line should have. */
	indent: string;
	/** The leading whitespace it has now. */
	previous: string;
};

export type ReindentRequest = {
	lines: readonly string[];
	profile: IndentProfile;
	unit: IndentUnit;
	/** First line to re-indent, 0-based, inclusive. */
	from: number;
	/** Last line to re-indent, 0-based, inclusive. */
	to: number;
	/**
	 * Blank lines that should be indented - the caret's line after Return, or
	 * the one ⌃I was pressed on. Every other whitespace-only line is left alone.
	 */
	indentBlank?: ReadonlySet<number>;
};

/** Every line in `[from, to]` whose leading whitespace should change. */
export function computeReindent(request: ReindentRequest): IndentChange[] {
	return new Reindenter(request).run();
}

/** The whole document re-indented - the format fallback for a language with no formatter. */
export function reindentDocument(text: string, profile: IndentProfile, unit: IndentUnit): string {
	const lines = text.split("\n");
	const changes = computeReindent({ lines, profile, unit, from: 0, to: lines.length - 1 });
	if (changes.length === 0) return text;
	const out = [...lines];
	for (const change of changes) out[change.line] = change.indent + out[change.line].slice(change.previous.length);
	return out.join("\n");
}

/** The leading whitespace of a line (the `\r` of a CRLF line is never part of it). */
export function leadingWhitespace(line: string): string {
	let i = 0;
	while (i < line.length && (line[i] === " " || line[i] === "\t")) i++;
	return line.slice(0, i);
}

// ---------------------------------------------------------------------------
// Lexing

type Frame =
	/** A string that ends at its line's end at the latest. */
	| { kind: "string"; close: string; escapes: boolean; hashes: number }
	/** A string that may span lines. */
	| { kind: "multiline"; close: string; escapes: boolean; jsHoles: boolean; doubledQuote: boolean }
	| { kind: "comment"; depth: number; line: number }
	/** Code inside a string's interpolation hole; its brackets belong to the hole. */
	| { kind: "hole"; open: string; close: string; depth: number };

type Opener = {
	/**
	 * `(` `[` `{`; in JSX `<` while reading a tag's attributes and `<>` while
	 * reading its children; `⟨` for TypeScript type arguments left open at a
	 * line's end (`Pick<`).
	 */
	ch: string;
	line: number;
	col: number;
	/** The line whose indentation this bracket's contents hang off. */
	anchor: number;
	/** The column (in the opener's line) to line arguments up under, if any. */
	align: number | null;
	isSwitch: boolean;
	sawCase: boolean;
	/** Added to the anchor's indentation for this bracket's closer. */
	closeAt: string;
	/** Added to the anchor's indentation for this bracket's contents. */
	contentAt: string;
	/** A `(` that groups (`if (`, `return (`, `= (`) rather than calls. */
	grouping: boolean;
};

/** What the rules need to remember about a line once it has been read. */
/**
 * How a line continues the statement above it: a `.member` chain, a list
 * after `guard`/`case` (Xcode lines those up under the first item), or any
 * other unfinished expression.
 */
type Continuation = "none" | "dot" | "comma" | "other";

type LineFacts = {
	/** The line with strings as `0` and comments as spaces, trimmed. */
	code: string;
	hasCode: boolean;
	endsInCode: boolean;
	/** The first line of the statement this line belongs to. */
	runStart: number;
};

/** A statement whose header may run over lines before its block: `if a &&\n\tb {`. */
const CONTROL_HEAD =
	/^(?:}\s*)?(?:else\s+)?(?:if|for|while|switch|select|guard|catch)\b|^(?:export\s+)?(?:default\s+)?(?:abstract\s+)?(?:interface|class)\b/;

/** Words after which a `(` groups an expression rather than calling something. */
const GROUPING_KEYWORDS = new Set([
	"if",
	"while",
	"for",
	"switch",
	"return",
	"catch",
	"await",
	"typeof",
	"in",
	"of",
	"case",
	"else",
	"throw",
	"yield",
	"void",
	"new",
]);

const CLOSERS = ")]}";
/** A line that is one arm of a ternary: `? a` or `: b` (not `?.x`, `??`, `::`). */
const TERNARY_BRANCH = /^(\?(?![?.])|:(?!:))/;

/** `<T,>(…) =>` and `<T extends U>(…) =>` in a .tsx file: type parameters, not a tag. */
const GENERIC_ARROW = /^<[A-Za-z_$][\w$]*\s*(,|extends\b)/;

/** What may follow `<` for it to open a JSX tag: a name, or `>` for a fragment. */
const JSX_TAG_START = /[A-Za-z>]/;

function isJsx(opener: Opener | undefined): boolean {
	return opener?.ch === "<" || opener?.ch === "<>";
}
const OPENERS = "([{";
const MATCHING: Record<string, string> = { ")": "(", "]": "[", "}": "{" };
const IDENT = /[A-Za-z0-9_$]/;

/** Ordered longest-first so `&&` is never read as `&`. */
const TRAILING_CONTINUATIONS = [
	"&&",
	"||",
	"??",
	"->",
	"+=",
	"-=",
	"*=",
	"/=",
	"%=",
	"|=",
	"&=",
	"^=",
	":=",
	"==",
	"!=",
	"<=",
	">=",
	"=",
	"+",
	"-",
	"*",
	"/",
	"%",
	"|",
	"&",
	"^",
	".",
];

/** Tokens before which a JavaScript `/` starts a regex rather than dividing. */
const REGEX_PRECEDERS = "(,=:[!&|?{};+-*%<>~^";
const REGEX_KEYWORDS = new Set([
	"return",
	"typeof",
	"case",
	"do",
	"else",
	"in",
	"of",
	"new",
	"delete",
	"void",
	"throw",
	"yield",
	"await",
]);

class Reindenter {
	private readonly lines: readonly string[];
	private readonly profile: IndentProfile;
	private readonly unit: string;
	private readonly from: number;
	private readonly to: number;
	private readonly indentBlank: ReadonlySet<number>;

	private readonly frames: Frame[] = [];
	private readonly stack: Opener[] = [];
	/** Each line's indentation as the rules see it: the new one inside the range, the real one before it. */
	private readonly effective: string[] = [];
	private readonly original: string[] = [];
	private readonly facts: LineFacts[] = [];
	/** Whether each line continues the statement before it, and how. */
	private readonly continuation: Continuation[] = [];
	/** For a line that opens with a closing bracket, its opener's anchor line. */
	private readonly closesFrom: number[] = [];
	/** The first line of the statement each line belongs to, decided at the line's start. */
	private readonly runStart: number[] = [];
	/** Whether each line is a link in a `.member` chain. */
	private readonly chainLink: boolean[] = [];
	/** The innermost open bracket at each line's start. */
	private readonly topAtStart: (Opener | undefined)[] = [];
	/** Whether each line is the first inside a bracket opened at the end of the line before. */
	private readonly firstInBracket: boolean[] = [];
	/** Whether each line is a Prettier ternary branch (`? a` / `: b`). */
	private readonly ternaryBranch: boolean[] = [];
	/** Open ternaries: the `?` lines a later `:` line lines up with, innermost last. */
	private readonly ternaries: { line: number; top: Opener | undefined }[] = [];
	private lastCodeLine = -1;

	constructor(request: ReindentRequest) {
		this.lines = request.lines;
		this.profile = request.profile;
		this.unit = request.unit.insertSpaces ? " ".repeat(Math.max(1, request.unit.indentSize)) : "\t";
		this.from = Math.max(0, request.from);
		this.to = Math.min(request.to, request.lines.length - 1);
		this.indentBlank = request.indentBlank ?? new Set();
	}

	run(): IndentChange[] {
		const changes: IndentChange[] = [];
		for (let i = 0; i <= this.to; i++) {
			const line = this.lines[i];
			const original = leadingWhitespace(line);
			this.original[i] = original;
			const top = this.frames[this.frames.length - 1];
			const startsIn = top === undefined ? "code" : top.kind === "comment" ? "comment" : "string";
			const inRange = i >= this.from;
			const desired = this.decide(i, line, original, startsIn, inRange);
			// After deciding, so the label itself is not counted as a case body.
			const switchTop = this.stack[this.stack.length - 1];
			if (switchTop?.isSwitch && startsIn === "code" && this.isCaseLabel(line.slice(original.length))) {
				switchTop.sawCase = true;
			}
			if (startsIn === "code") this.noteTernary(i, line.slice(original.length));
			this.effective[i] = inRange && desired !== null ? desired : original;
			if (inRange && desired !== null && desired !== original)
				changes.push({ line: i, indent: desired, previous: original });
			this.scan(i, line, startsIn);
		}
		return changes;
	}

	// -------------------------------------------------------------------------
	// Deciding a line's indentation

	private decide(i: number, line: string, original: string, startsIn: string, inRange: boolean): string | null {
		const trimmed = line.slice(original.length).replace(/\r$/, "");
		if (startsIn !== "code") {
			// Inside a string or comment that began above: part of that statement.
			this.continuation[i] = "none";
			this.runStart[i] = this.lastCodeLine >= 0 ? this.facts[this.lastCodeLine].runStart : i;
			return inRange && startsIn === "comment" ? this.commentLine(original) : null;
		}
		const indentThisBlank = trimmed === "" && inRange && this.indentBlank.has(i);
		// A comment sits where the code it introduces does: before a union
		// member, a `case`, a continuation line. (Not before a closing bracket -
		// a comment closing out a block belongs to the block.)
		const shape = this.isCommentOnly(trimmed) ? (this.nextCodeAfter(i) ?? trimmed) : trimmed;
		this.continuation[i] = shape === "" && !indentThisBlank ? "none" : this.continuationKind(shape);
		const top = this.stack[this.stack.length - 1];
		const p = this.lastCodeLine;
		this.topAtStart[i] = top;
		this.ternaryBranch[i] = this.profile.prettierTernaries && TERNARY_BRANCH.test(shape);
		this.firstInBracket[i] = top !== undefined && top.line === p;
		if (top && this.opensWithCloser(shape, top)) this.closesFrom[i] = top.anchor;
		this.runStart[i] = this.runStartOf(i);
		// `(await page` / `.locator(…)`: the first link of a chain whose head
		// opened the bracket is indented by the bracket, but it is still a link.
		this.chainLink[i] =
			this.continuation[i] === "dot" || (this.firstInBracket[i] && /^\??\.(?!\.)/.test(shape) && p >= 0);
		if (!inRange) return null;
		// A whitespace-only line is left as it is: Xcode keeps a block's
		// indentation on its blank lines, gofmt strips it, and re-indenting is
		// not the place to impose either. The caret's own blank line is the
		// exception - it is about to be typed on.
		if (trimmed === "") return indentThisBlank ? this.structural(i, trimmed) : null;
		if (this.profile.preprocessorAtColumnZero && trimmed.startsWith("#")) return "";
		// Xcode's ⌘/ comments a line out at column 0. Moving those lines is
		// churn in exactly the code nobody is reading.
		if (
			this.profile.keepColumnZeroComments &&
			original === "" &&
			this.profile.lineComments.some((m) => trimmed.startsWith(m))
		) {
			return null;
		}
		return this.structural(i, shape);
	}

	/**
	 * Whether a line opens by closing the innermost bracket: `}` `)` `]`, or in
	 * JSX `</Tag>` for an element and `/>` / `>` for a tag whose attributes ran
	 * over lines.
	 */
	private opensWithCloser(trimmed: string, top: Opener | undefined): boolean {
		if (trimmed === "") return false;
		if (top?.ch === "<>") return trimmed.startsWith("</");
		if (top?.ch === "<") return trimmed.startsWith("/>") || trimmed.startsWith(">");
		if (top?.ch === "⟨" && trimmed.startsWith(">")) return true;
		return CLOSERS.includes(trimmed[0]);
	}

	private isCommentOnly(trimmed: string): boolean {
		if (trimmed === "") return false;
		const bc = this.profile.blockComment;
		return this.profile.lineComments.some((m) => trimmed.startsWith(m)) || (bc !== null && trimmed.startsWith(bc.open));
	}

	/**
	 * The first code line after a run of comment lines starting at `i`, trimmed -
	 * or null when there is none, it closes a bracket or opens a case, or a block
	 * comment in the run spans lines (and so might change what the lines after it
	 * are).
	 */
	private nextCodeAfter(i: number): string | null {
		const bc = this.profile.blockComment;
		for (let j = i; j < this.lines.length; j++) {
			const trimmed = this.lines[j].trim();
			if (trimmed === "") continue;
			if (this.isCommentOnly(trimmed)) {
				if (bc && trimmed.startsWith(bc.open) && !trimmed.includes(bc.close)) return null;
				continue;
			}
			// A comment before `}` or `case` closes out what is above it - often
			// it IS an empty case's whole body - so it stays with that.
			return CLOSERS.includes(trimmed[0]) || trimmed.startsWith("</") || this.isCaseLabel(trimmed) ? null : trimmed;
		}
		return null;
	}

	/** A line inside a block comment keeps its offset from the comment's first line. */
	private commentLine(original: string): string | null {
		const frame = this.frames.find((f) => f.kind === "comment");
		if (frame?.kind !== "comment") return null;
		const before = this.original[frame.line];
		const after = this.effective[frame.line];
		if (before === after || !original.startsWith(before)) return null;
		return after + original.slice(before.length);
	}

	private structural(i: number, trimmed: string): string {
		const top = this.stack[this.stack.length - 1];
		if (this.opensWithCloser(trimmed, top)) return top ? this.effective[top.anchor] + top.closeAt : "";
		// JSX is markup: attributes and children one level in, and none of the
		// statement rules below mean anything there.
		if (isJsx(top)) return this.bracketIndent(top);
		const ternary = this.ternaryIndent(trimmed, top);
		if (ternary !== null) return ternary;
		if (top?.isSwitch && this.isCaseLabel(trimmed)) {
			const anchor = this.effective[top.anchor];
			return this.profile.switchCase === "flat" ? anchor : anchor + this.unit;
		}
		// Prettier indents whatever continues a ternary branch one level further
		// than it would anywhere else (`? text` / `.split(",")` two in).
		const branchExtra = (line: number) => (this.ternaryBranch[line] ? this.unit : "");
		// A union's members line up with each other, however it began.
		const prevCode = this.lastCodeLine >= 0 ? this.facts[this.lastCodeLine].code : "";
		if (
			this.profile.angleGenerics &&
			/^[|&]\s/.test(trimmed) &&
			prevCode[0] === trimmed[0] &&
			/^[|&]\s/.test(prevCode)
		) {
			return this.effective[this.lastCodeLine];
		}
		if (this.continuation[i] === "dot") {
			// A chain's links line up with each other, and a link after a closing
			// bracket lines up with it - SwiftUI's `}` / `.padding()`, Prettier's
			// `})` / `.catch()`. Only the first link after its head steps in.
			const prev = this.lastCodeLine;
			if (this.chainLink[prev]) return this.effective[prev];
			const closed = this.closesFrom[prev];
			// After the head's OWN closer, Xcode still lines up (`VStack {` / `}` /
			// `.padding()`) where Prettier steps in (`[a, b]` / `.filter()`) - and
			// Xcode too once the closer's line carries a call (`).subscribe(…)`).
			const closersOnly = /^[)\]}]+[;,]?$/.test(this.facts[prev].code);
			if (closed !== undefined && (this.chainLink[closed] || (this.profile.chainAlignsAfterHead && closersOnly))) {
				return this.effective[prev];
			}
			return this.effective[prev] + branchExtra(prev) + this.unit;
		}
		if (this.continuation[i] === "comma" && this.profile.alignCommaContinuations) {
			// `guard let a = x,` / `      let b = y` - under the first condition.
			const start = this.runStart[i];
			const word = /^([A-Za-z_]+)\s/.exec(this.facts[start]?.code ?? "")?.[1];
			if (word) return this.effective[start] + " ".repeat(word.length + 1);
		}
		if (this.continuation[i] !== "none") {
			const start = this.runStart[i];
			// Prettier lines a grouped condition's operands up with each other:
			// `if (\n\ta ||\n\tb\n) {`. A call's arguments still step in.
			if (
				this.profile.alignBracketedOperands &&
				top?.grouping &&
				this.firstInBracket[start] &&
				this.topAtStart[start] === top
			) {
				return this.effective[start];
			}
			return this.effective[start] + branchExtra(start) + this.unit;
		}
		// A label stands alone on its line; `Name: value,` is a composite literal's key.
		if (this.profile.outdentLabels && /^(?!default\b)[A-Za-z_]\w*:\s*(\/\/.*)?$/.test(trimmed)) {
			const indent = this.bracketIndent(top);
			return indent.endsWith(this.unit) ? indent.slice(0, -this.unit.length) : indent;
		}
		const body = this.unbracedBodyIndent(trimmed);
		if (body !== null) return body;
		return this.bracketIndent(top);
	}

	/** One level in from the innermost open bracket, or lined up under its first argument. */
	private bracketIndent(top: Opener | undefined): string {
		if (!top) return "";
		if (top.align !== null) return this.effective[top.line] + " ".repeat(top.align - this.original[top.line].length);
		let indent = this.effective[top.anchor] + top.contentAt;
		if (top.isSwitch && top.sawCase && this.profile.switchCase === "indented") indent += this.unit;
		return indent;
	}

	/**
	 * Prettier's ternaries: each `?` branch one level in from the line before it
	 * (so a nested ternary steps in again), and each `:` level with its `?`.
	 */
	private ternaryIndent(trimmed: string, top: Opener | undefined): string | null {
		if (!this.profile.prettierTernaries || this.lastCodeLine < 0) return null;
		// `? // why` with the branch itself on the line below.
		const prev = this.lastCodeLine;
		if (this.ternaryBranch[prev] && /^[?:]$/.test(this.facts[prev].code)) return this.effective[prev] + this.unit;
		if (/^\?(?![?.])/.test(trimmed)) return this.effective[this.lastCodeLine] + this.unit;
		// (A `:` line is also a branch; its own indentation is its `?`'s.)
		if (/^:(?!:)/.test(trimmed)) {
			const at = this.openTernary(top);
			if (at >= 0) return this.effective[this.ternaries[at].line];
		}
		return null;
	}

	/** The innermost `?` still waiting for its `:` in this bracket, or -1. */
	private openTernary(top: Opener | undefined): number {
		for (let at = this.ternaries.length - 1; at >= 0; at--) if (this.ternaries[at].top === top) return at;
		return -1;
	}

	/** Track which `?` a `:` belongs to, once the line's own indentation is settled. */
	private noteTernary(i: number, trimmed: string): void {
		if (!this.profile.prettierTernaries) return;
		const top = this.stack[this.stack.length - 1];
		if (/^\?(?![?.])/.test(trimmed)) {
			this.ternaries.push({ line: i, top });
			return;
		}
		if (/^:(?!:)/.test(trimmed)) {
			const at = this.openTernary(top);
			if (at >= 0) this.ternaries.splice(at, 1);
		}
	}

	private isCaseLabel(trimmed: string): boolean {
		return /^(case\b|default\s*:|@unknown\s+default\b)/.test(trimmed);
	}

	/** Whether a line opening with `trimmed` continues the statement on the code line before it. */
	private continuationKind(trimmed: string): Continuation {
		const p = this.lastCodeLine;
		if (p < 0) return "none";
		const prev = this.facts[p];
		if (!prev.endsInCode) return "none";
		// The previous line opened a bracket this line is inside: the bracket
		// indents it, and counting it as a continuation too would indent it twice.
		const top = this.stack[this.stack.length - 1];
		if (top && top.line === p) return "none";
		if (isJsx(top) || this.opensWithCloser(trimmed, top)) return "none";
		if (top?.isSwitch && this.isCaseLabel(trimmed)) return "none";
		if (this.endsWithContinuation(prev.code)) return "other";
		const operandEnd = /[\w$0)\]}?!]$/.test(prev.code);
		if (operandEnd && trimmed !== "") {
			if (/^\??\.(?!\.)/.test(trimmed)) return "dot";
			if (/^(&&|\|\||\?\?)/.test(trimmed)) return "other";
			if (/^[-+*/%|&^=](\s|$)/.test(trimmed)) return "other";
			if (this.profile.trailingQuestionContinues && /^[?:](\s|$)/.test(trimmed)) return "other";
		}
		if (trimmed !== "") {
			const lead = /^([A-Za-z_]+)\b/.exec(trimmed)?.[1];
			if (lead && this.profile.leadingContinuationWords.includes(lead)) return "other";
		}
		if (prev.code.endsWith(",") && this.profile.commaContinues.length > 0) {
			const start = this.facts[prev.runStart]?.code ?? "";
			const word = /^(?:}\s*)?(?:else\s+)?([A-Za-z_]+)/.exec(start)?.[1];
			if (word && this.profile.commaContinues.includes(word)) return "comma";
		}
		return "none";
	}

	private endsWithContinuation(code: string): boolean {
		if (code.endsWith("++") || code.endsWith("--") || code.endsWith("...")) return false;
		if (this.profile.arrowContinues && code.endsWith("=>")) return true;
		if (code.endsWith("=>")) return false;
		const word = /(?:^|[^\w$])([A-Za-z_]+)$/.exec(code)?.[1];
		if (word && this.profile.continuationWords.includes(word)) return true;
		// `key:` with its value on the next line - but `case x:` is a label.
		if (this.profile.colonContinues && code.endsWith(":") && !code.endsWith("::") && !this.isCaseLabel(code))
			return true;
		if (this.profile.trailingQuestionContinues && code.endsWith("?") && !code.endsWith("??")) return true;
		for (const token of TRAILING_CONTINUATIONS) if (code.endsWith(token)) return true;
		return false;
	}

	/** The first line of the statement line `i` belongs to. Read the stack as it is at the line's START. */
	private runStartOf(i: number): number {
		// `})` belongs to the statement that opened what it closes, so a
		// `.catch(…)` after it continues THAT statement, not the closer's line.
		const closed = this.closesFrom[i];
		if (closed !== undefined) return this.facts[closed]?.runStart ?? closed;
		const p = this.lastCodeLine;
		if (p < 0) return i;
		if (this.continuation[i] !== "none") return this.facts[p].runStart;
		// The first line inside a bracket whose opener line ended mid-expression
		// (`write(w, a +` / `\tb +` / `\tc)`): the bracket indents it, but the
		// expression started on the opener's line, so that is where the lines
		// continuing it hang from - all at one level, as gofmt has them.
		const top = this.stack[this.stack.length - 1];
		if (top && top.line === p && this.endsWithContinuation(this.facts[p].code)) return p;
		return i;
	}

	/** `if (x)\n    foo();` - the one line after a brace-less head sits one level in. */
	private unbracedBodyIndent(trimmed: string): string | null {
		if (!this.profile.unbracedBodies || trimmed.startsWith("{")) return null;
		const p = this.lastCodeLine;
		if (p < 0) return null;
		const prev = this.facts[p];
		const top = this.stack[this.stack.length - 1];
		if (!prev.endsInCode || (top && top.line === p)) return null;
		const head =
			/^(?:}\s*)?(?:else\s+)?(?:if|for|while|foreach|using|lock)\s*\(.*\)$/.test(prev.code) ||
			/^(?:}\s*)?(?:else|do)$/.test(prev.code);
		return head ? this.effective[p] + this.unit : null;
	}

	// -------------------------------------------------------------------------
	// Reading a line

	private scan(i: number, line: string, startsIn: string): void {
		const code = new Array<string>(line.length).fill(" ");
		const pushedHere: Opener[] = [];
		/** The outermost opener from an EARLIER line closed on this one so far. */
		let closedEarlier: Opener | null = null;
		let prevSignificant = "";
		let prevWord = "";
		const p = this.profile;
		const preprocessor = p.preprocessorAtColumnZero && startsIn === "code" && line.trimStart().startsWith("#");
		const style = startsIn === "code" ? this.bracketStyle(line.trimStart()) : this.plainStyle();
		let k = preprocessor ? line.length : 0;
		const n = line.length;

		while (k < n) {
			const ch = line[k];
			const frame = this.frames[this.frames.length - 1];

			if (frame?.kind === "comment") {
				const bc = p.blockComment;
				if (!bc) {
					this.frames.pop();
					continue;
				}
				if (bc.nests && line.startsWith(bc.open, k)) {
					frame.depth++;
					k += bc.open.length;
					continue;
				}
				if (line.startsWith(bc.close, k)) {
					frame.depth--;
					k += bc.close.length;
					if (frame.depth === 0) this.frames.pop();
					continue;
				}
				k++;
				continue;
			}

			if (frame?.kind === "string" || frame?.kind === "multiline") {
				code[k] = "0";
				// In a Swift raw string a backslash is only an escape when the
				// string's own `#`s follow it: `#"C:\"#` closes after the `\`.
				const escapes =
					frame.escapes && ch === "\\" && (frame.kind !== "string" || hashesFollow(line, k + 1, frame.hashes));
				if (escapes) {
					if (frame.kind === "string" && p.swiftInterpolation && this.isSwiftHole(line, k + 1, frame.hashes)) {
						const width = 1 + frame.hashes + 1;
						for (let j = k; j < k + width && j < n; j++) code[j] = "0";
						this.frames.push({ kind: "hole", open: "(", close: ")", depth: 1 });
						k += width;
						prevSignificant = "(";
						prevWord = "";
						continue;
					}
					if (k + 1 < n) code[k + 1] = "0";
					k += 2;
					continue;
				}
				if (frame.kind === "multiline" && frame.jsHoles && ch === "$" && line[k + 1] === "{") {
					code[k + 1] = "0";
					this.frames.push({ kind: "hole", open: "{", close: "}", depth: 1 });
					k += 2;
					prevSignificant = "{";
					prevWord = "";
					continue;
				}
				if (line.startsWith(frame.close, k)) {
					if (frame.kind === "multiline" && frame.doubledQuote && line[k + 1] === '"') {
						code[k + 1] = "0";
						k += 2;
						continue;
					}
					for (let j = k; j < k + frame.close.length; j++) code[j] = "0";
					k += frame.close.length;
					this.frames.pop();
					prevSignificant = "0";
					prevWord = "";
					continue;
				}
				k++;
				continue;
			}

			const hole = frame?.kind === "hole" ? frame : null;

			if (ch === " " || ch === "\t" || ch === "\r") {
				if (hole) code[k] = "0";
				k++;
				continue;
			}

			const jsx = !hole && p.jsx ? this.jsxTop() : null;
			if (jsx?.ch === "<") {
				// Inside an opening tag: names, `=`, quoted values, `{…}` values.
				code[k] = "0";
				if (ch === "/" && line[k + 1] === ">") {
					code[k + 1] = "0";
					closedEarlier = this.closeJsx(jsx, i) ?? closedEarlier;
					k += 2;
					prevSignificant = ">";
					prevWord = "";
					continue;
				}
				if (ch === ">") {
					jsx.ch = "<>";
					k++;
					continue;
				}
				if (ch === '"' || ch === "'") {
					const end = line.indexOf(ch, k + 1);
					for (let j = k; j <= (end < 0 ? n - 1 : end); j++) code[j] = "0";
					k = end < 0 ? n : end + 1;
					continue;
				}
				if (ch !== "{") {
					k++;
					continue;
				}
			} else if (jsx?.ch === "<>") {
				// Children: text (where `'` and `//` are just characters), `{…}`
				// expressions, nested elements, and the closing tag.
				code[k] = "0";
				if (ch === "<" && line[k + 1] === "/") {
					const end = line.indexOf(">", k);
					for (let j = k; j <= (end < 0 ? n - 1 : end); j++) code[j] = "0";
					closedEarlier = this.closeJsx(jsx, i) ?? closedEarlier;
					k = end < 0 ? n : end + 1;
					prevSignificant = ">";
					prevWord = "";
					continue;
				}
				if (ch !== "{" && !(ch === "<" && JSX_TAG_START.test(line[k + 1] ?? ""))) {
					k++;
					continue;
				}
			}

			if (
				p.jsx &&
				!hole &&
				ch === "<" &&
				JSX_TAG_START.test(line[k + 1] ?? "") &&
				!GENERIC_ARROW.test(line.slice(k)) &&
				(jsx?.ch === "<>" || this.regexMayStart(prevSignificant, prevWord))
			) {
				const fragment = line[k + 1] === ">";
				code[k] = "0";
				pushedHere.push(this.pushOpener(fragment ? "<>" : "<", i, k, closedEarlier, i, style, false));
				k += fragment ? 2 : 1;
				prevSignificant = "<";
				prevWord = "";
				continue;
			}

			if (!hole && p.lineComments.some((marker) => line.startsWith(marker, k))) break;

			if (p.blockComment && line.startsWith(p.blockComment.open, k)) {
				this.frames.push({ kind: "comment", depth: 1, line: i });
				k += p.blockComment.open.length;
				continue;
			}

			const opened = this.openString(line, k);
			if (opened) {
				for (let j = k; j < k + opened.width && j < n; j++) code[j] = "0";
				this.frames.push(opened.frame);
				k += opened.width;
				continue;
			}

			if (ch === "'" && !p.quotes.includes("'")) {
				const end = this.charLiteralEnd(line, k);
				if (end > k) {
					for (let j = k; j <= end; j++) code[j] = "0";
					k = end + 1;
					prevSignificant = "0";
					prevWord = "";
					continue;
				}
			}

			if (p.regexLiterals && ch === "/" && this.regexMayStart(prevSignificant, prevWord)) {
				const end = regexEnd(line, k);
				if (end > k) {
					for (let j = k; j <= end; j++) code[j] = "0";
					k = end + 1;
					prevSignificant = "0";
					prevWord = "";
					continue;
				}
			}

			if (hole) {
				code[k] = "0";
				if (ch === hole.open) hole.depth++;
				else if (ch === hole.close && --hole.depth === 0) this.frames.pop();
				// Tracked here too: `${(a / b) * 100}%` must read that `/` as a
				// division, or the "regex" swallows the hole's own closing brace.
				if (IDENT.test(ch)) {
					let j = k;
					while (j < n && IDENT.test(line[j])) code[j++] = "0";
					prevWord = line.slice(k, j);
					prevSignificant = line[j - 1];
					k = j;
					continue;
				}
				prevSignificant = ch;
				prevWord = "";
				k++;
				continue;
			}

			code[k] = ch;
			if (OPENERS.includes(ch)) {
				const parent = this.stack[this.stack.length - 1];
				// A JSX `{…}` holds one expression, like `if (…)` does.
				const grouping =
					(ch === "(" && (prevWord ? GROUPING_KEYWORDS.has(prevWord) : !/[\w$)\]>]/.test(prevSignificant))) ||
					(ch === "{" && isJsx(parent));
				pushedHere.push(this.pushOpener(ch, i, k, closedEarlier, this.anchorFor(i, ch), style, grouping));
			} else if (
				ch === "<" &&
				p.angleGenerics &&
				/[\w$]/.test(prevSignificant) &&
				/^\s*(\/\/.*)?$/.test(line.slice(k + 1))
			) {
				// `Pick<` with its arguments on the lines below.
				pushedHere.push(this.pushOpener("⟨", i, k, closedEarlier, i, style, false));
			} else if (ch === ">" && this.stack[this.stack.length - 1]?.ch === "⟨" && line[k - 1] !== "=") {
				const generic = this.stack.pop();
				if (generic && generic.line < i) closedEarlier = generic;
			} else if (CLOSERS.includes(ch)) {
				// Pop down to the bracket this one matches, so an opener left
				// unclosed by a typo is dropped here instead of shifting every line
				// below it. A closer that matches nothing open pops nothing.
				const want = MATCHING[ch];
				let at = this.stack.length - 1;
				while (at >= 0 && this.stack[at].ch !== want) at--;
				if (at >= 0) {
					const popped = this.stack.splice(at);
					if (popped[0].line < i) closedEarlier = popped[0];
				}
			}
			if (IDENT.test(ch)) {
				let j = k;
				while (j < n && IDENT.test(line[j])) {
					code[j] = line[j];
					j++;
				}
				prevWord = line.slice(k, j);
				prevSignificant = line[j - 1];
				k = j;
				continue;
			}
			prevSignificant = ch;
			prevWord = "";
			k++;
		}

		// A string that cannot span lines ends with its line, and so does any
		// interpolation hole opened inside it.
		const firstSingle = this.frames.findIndex((f) => f.kind === "string");
		if (firstSingle >= 0) this.frames.length = firstSingle;

		const codeText = code.join("");
		const trimmedCode = codeText.trim();
		const hasCode = !preprocessor && trimmedCode !== "";

		// Brackets still open at the end of the line they were opened on: does an
		// argument follow them here (Xcode lines the next ones up under it), and is
		// the brace a switch's?
		for (const opener of pushedHere) {
			if (!this.stack.includes(opener)) continue;
			if (p.alignToOpenBracket && (opener.ch === "(" || opener.ch === "[")) {
				const rest = codeText.slice(opener.col + 1);
				const offset = rest.search(/\S/);
				if (offset >= 0) opener.align = opener.col + 1 + offset;
			}
			if (opener.ch === "{") {
				// `switch (\n\ta,\n\tb\n) {` - the switch is on the line the brace hangs off.
				opener.isSwitch =
					this.opensSwitch(codeText.slice(0, opener.col)) ||
					(opener.anchor !== i && this.opensSwitch(this.facts[opener.anchor]?.code ?? ""));
			}
		}

		this.facts[i] = { code: trimmedCode, hasCode, endsInCode: this.frames.length === 0, runStart: this.runStart[i] };
		if (hasCode) this.lastCodeLine = i;
	}

	private pushOpener(
		ch: string,
		i: number,
		col: number,
		closedEarlier: Opener | null,
		anchor: number,
		style: { closeAt: string; contentAt: string },
		grouping: boolean,
	): Opener {
		const opener: Opener = {
			ch,
			line: i,
			col,
			anchor: closedEarlier ? closedEarlier.anchor : anchor,
			align: null,
			isSwitch: false,
			sawCase: false,
			closeAt: closedEarlier ? closedEarlier.closeAt : style.closeAt,
			contentAt: closedEarlier ? closedEarlier.contentAt : style.contentAt,
			grouping,
		};
		this.stack.push(opener);
		return opener;
	}

	private plainStyle(): { closeAt: string; contentAt: string } {
		return { closeAt: "", contentAt: this.unit };
	}

	/**
	 * Where a bracket opened on this line puts its contents and its closer.
	 * Prettier gives one opened on a ternary branch or a union member an extra
	 * level - `: {` / contents two in / `}` one in - and lines a union member's
	 * closer up under its `| ` with spaces when it indents with tabs.
	 */
	private bracketStyle(trimmed: string): { closeAt: string; contentAt: string } {
		const p = this.profile;
		if (p.prettierTernaries && TERNARY_BRANCH.test(trimmed))
			return { closeAt: this.unit, contentAt: this.unit + this.unit };
		if (p.angleGenerics && /^[|&]\s/.test(trimmed)) {
			return { closeAt: this.unit === "\t" ? "  " : this.unit, contentAt: this.unit + this.unit };
		}
		return this.plainStyle();
	}

	private jsxTop(): Opener | null {
		const top = this.stack[this.stack.length - 1];
		return top !== undefined && isJsx(top) ? top : null;
	}

	/** Close a JSX tag or element, and anything a typo left open inside it. */
	private closeJsx(opener: Opener, i: number): Opener | null {
		const at = this.stack.lastIndexOf(opener);
		if (at < 0) return null;
		this.stack.splice(at);
		return opener.line < i ? opener : null;
	}

	/**
	 * The line an opener's contents should hang off - see the module comment.
	 *
	 * Only a control statement's OWN block brace reaches back to the statement's
	 * first line. A brace nested in an expression on the continuation line
	 * (`return a ||\n\tf(func() {`) hangs off that line, as gofmt has it.
	 */
	private anchorFor(i: number, ch: string): number {
		if (ch !== "{" || (this.continuation[i] !== "other" && this.continuation[i] !== "comma")) return i;
		if (this.stack.some((opener) => opener.line === i)) return i;
		const start = this.runStart[i];
		return CONTROL_HEAD.test(this.facts[start]?.code ?? "") ? start : i;
	}

	private opensSwitch(before: string): boolean {
		const words = this.profile.switchKeywords;
		const test = (text: string) => words.some((w) => new RegExp(`(^|[^\\w$.])${w}\\b`).test(text));
		if (test(before)) return true;
		// An Allman brace on a line of its own belongs to the line above it.
		if (before.trim() === "" && this.lastCodeLine >= 0) return test(this.facts[this.lastCodeLine].code);
		return false;
	}

	private isSwiftHole(line: string, at: number, hashes: number): boolean {
		return hashesFollow(line, at, hashes) && line[at + hashes] === "(";
	}

	/** A string opening at `k`, as the frame that will read it and its delimiter's width. */
	private openString(line: string, k: number): { frame: Frame; width: number } | null {
		const p = this.profile;
		const ch = line[k];
		const prevIsIdent = k > 0 && IDENT.test(line[k - 1]);

		if (p.multiline.includes("swift") && (ch === "#" || ch === '"')) {
			let hashes = 0;
			while (line[k + hashes] === "#") hashes++;
			if (line[k + hashes] !== '"') return null;
			const pad = "#".repeat(hashes);
			if (line.startsWith('"""', k + hashes)) {
				return {
					frame: { kind: "multiline", close: `"""${pad}`, escapes: hashes === 0, jsHoles: false, doubledQuote: false },
					width: hashes + 3,
				};
			}
			return { frame: { kind: "string", close: `"${pad}`, escapes: true, hashes }, width: hashes + 1 };
		}
		if (ch === "`" && p.multiline.includes("go-raw")) {
			return {
				frame: { kind: "multiline", close: "`", escapes: false, jsHoles: false, doubledQuote: false },
				width: 1,
			};
		}
		if (ch === "`" && p.multiline.includes("js-template")) {
			return { frame: { kind: "multiline", close: "`", escapes: true, jsHoles: true, doubledQuote: false }, width: 1 };
		}
		if (ch === '"' && p.multiline.includes("triple-quote") && line.startsWith('"""', k)) {
			return {
				frame: { kind: "multiline", close: '"""', escapes: false, jsHoles: false, doubledQuote: false },
				width: 3,
			};
		}
		if (p.multiline.includes("rust") && !prevIsIdent) {
			const raw = /^b?r(#*)"/.exec(line.slice(k, k + 40));
			if (raw) {
				return {
					frame: { kind: "multiline", close: `"${raw[1]}`, escapes: false, jsHoles: false, doubledQuote: false },
					width: raw[0].length,
				};
			}
			const plain = /^b?"/.exec(line.slice(k, k + 2));
			if (plain) {
				return {
					frame: { kind: "multiline", close: '"', escapes: true, jsHoles: false, doubledQuote: false },
					width: plain[0].length,
				};
			}
		}
		if (ch === "R" && line[k + 1] === '"' && p.multiline.includes("cpp-raw") && !prevIsIdent) {
			const open = line.indexOf("(", k + 2);
			if (open > 0 && open - (k + 2) <= 16) {
				const delimiter = line.slice(k + 2, open);
				return {
					frame: {
						kind: "multiline",
						close: `)${delimiter}"`,
						escapes: false,
						jsHoles: false,
						doubledQuote: false,
					},
					width: open - k + 1,
				};
			}
		}
		if (ch === "@" && line[k + 1] === '"' && p.multiline.includes("verbatim")) {
			return { frame: { kind: "multiline", close: '"', escapes: false, jsHoles: false, doubledQuote: true }, width: 2 };
		}
		if (p.quotes.includes(ch)) return { frame: { kind: "string", close: ch, escapes: true, hashes: 0 }, width: 1 };
		return null;
	}

	/** The index of the `'` closing a character literal opening at `k`, or -1 when it is not one. */
	private charLiteralEnd(line: string, k: number): number {
		const mode = this.profile.singleQuote;
		if (mode === "plain") return -1;
		if (mode === "rust") {
			// `'a'` and `'\n'` are characters; `'a` with no closing quote right
			// after is a lifetime, and has to stay out of the way of the brackets.
			if (line[k + 1] === "\\") return closingQuote(line, k + 2);
			return line[k + 2] === "'" ? k + 2 : -1;
		}
		return closingQuote(line, k + 1);
	}

	private regexMayStart(prevSignificant: string, prevWord: string): boolean {
		if (prevSignificant === "") return true;
		if (prevWord) return REGEX_KEYWORDS.has(prevWord);
		return REGEX_PRECEDERS.includes(prevSignificant) || prevSignificant === "}";
	}
}

function hashesFollow(line: string, at: number, hashes: number): boolean {
	for (let h = 0; h < hashes; h++) if (line[at + h] !== "#") return false;
	return true;
}

function closingQuote(line: string, from: number): number {
	for (let j = from; j < line.length; j++) {
		if (line[j] === "\\") {
			j++;
			continue;
		}
		if (line[j] === "'") return j;
	}
	return -1;
}

/** The index of the `/` closing a regex literal at `k`, or -1 when the line has none. */
function regexEnd(line: string, k: number): number {
	const next = line[k + 1];
	if (next === "/" || next === "*" || next === undefined) return -1;
	let inClass = false;
	for (let j = k + 1; j < line.length; j++) {
		const c = line[j];
		if (c === "\\") {
			j++;
			continue;
		}
		if (c === "[") inClass = true;
		else if (c === "]") inClass = false;
		else if (c === "/" && !inClass) {
			let end = j;
			while (end + 1 < line.length && /[a-z]/i.test(line[end + 1])) end++;
			return end;
		}
	}
	return -1;
}
