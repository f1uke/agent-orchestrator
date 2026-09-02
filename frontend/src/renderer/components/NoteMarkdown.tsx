import { Fragment, type ReactNode, useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";
import {
	type Callout,
	type CalloutKind,
	parseNote,
	readCallout,
	safeHref,
	splitTags,
	type Token,
	type Tokens,
	type WikilinkToken,
} from "../lib/note/parse";
import { type CodeToken, highlightCode } from "../lib/note/highlight";
import type { Theme } from "../stores/ui-store";

/**
 * A vault note, rendered.
 *
 * 🗝 Every node below is a React element built from a token. There is no
 * `dangerouslySetInnerHTML` anywhere in this file, and there must never be one:
 * that is the whole reason untrusted vault markdown can be drawn at all (see
 * the note atop `lib/note/parse.ts`). Raw HTML in a note arrives as an `html`
 * token and is drawn as literal text.
 *
 * The measure is capped at 660px by the caller, per the design: a note is
 * long-form reading and a full-width line on a wide window is unreadable.
 */

/** Where a click on a `[[wikilink]]` or a `#tag` goes. */
export type NoteNavigation = {
	/** Open the note a wikilink names. Absent → wikilinks render as plain pills. */
	onOpenWikilink?: (target: string) => void;
	/** Search the vault for a tag. Absent → tags render as inert pills. */
	onOpenTag?: (tag: string) => void;
};

export function NoteMarkdown({
	source,
	theme,
	navigation,
}: {
	source: string;
	theme: Theme;
	navigation?: NoteNavigation;
}) {
	const { tokens } = parseNote(source);
	return <Blocks tokens={tokens} theme={theme} navigation={navigation} />;
}

function Blocks({ tokens, theme, navigation }: { tokens: Token[]; theme: Theme; navigation?: NoteNavigation }) {
	return (
		<>
			{tokens.map((token, index) => (
				// Tokens have no stable identity, and a note re-renders wholesale when
				// its content changes, so the index IS the identity here.
				<Block key={index} token={token} first={index === 0} theme={theme} navigation={navigation} />
			))}
		</>
	);
}

function Block({
	token,
	first,
	theme,
	navigation,
}: {
	token: Token;
	first: boolean;
	theme: Theme;
	navigation?: NoteNavigation;
}) {
	switch (token.type) {
		case "space":
			return null;

		case "heading": {
			const heading = token as Tokens.Heading;
			return (
				<Heading level={heading.depth} first={first}>
					<Inline tokens={heading.tokens} navigation={navigation} />
				</Heading>
			);
		}

		case "paragraph":
			return (
				<p className={`note-prose__p${first ? " note-prose__p--first" : ""}`}>
					<Inline tokens={(token as Tokens.Paragraph).tokens} navigation={navigation} />
				</p>
			);

		case "text": {
			// A loose list item's content arrives as a bare text token carrying its
			// own inline tokens.
			const text = token as Tokens.Text;
			return text.tokens ? <Inline tokens={text.tokens} navigation={navigation} /> : <>{plainSegments(text.text)}</>;
		}

		case "list":
			return <List list={token as Tokens.List} theme={theme} navigation={navigation} />;

		case "code":
			return <CodeBlock code={token as Tokens.Code} theme={theme} />;

		case "blockquote": {
			const quote = token as Tokens.Blockquote;
			const callout = readCallout(quote);
			return callout ? (
				<CalloutBlock callout={callout} theme={theme} navigation={navigation} />
			) : (
				<blockquote className="note-prose__quote">
					<Blocks tokens={quote.tokens} theme={theme} navigation={navigation} />
				</blockquote>
			);
		}

		case "hr":
			return <hr className="note-prose__rule" />;

		case "table":
			return <Table table={token as Tokens.Table} navigation={navigation} />;

		case "html":
			// Never parsed, never injected: a note's raw HTML is shown as the text it
			// literally is, which is also the honest thing to show a reader.
			return <pre className="note-prose__raw-html">{(token as Tokens.HTML).raw.replace(/\n$/, "")}</pre>;

		default: {
			const raw = (token as { raw?: string }).raw ?? "";
			return raw.trim() === "" ? null : <p className="note-prose__p">{plainSegments(raw)}</p>;
		}
	}
}

function Heading({ level, first, children }: { level: number; first: boolean; children: ReactNode }) {
	const className = `note-prose__h note-prose__h${Math.min(level, 6)}${first ? " note-prose__h--first" : ""}`;
	switch (level) {
		case 1:
			return <h1 className={className}>{children}</h1>;
		case 2:
			return <h2 className={className}>{children}</h2>;
		case 3:
			return <h3 className={className}>{children}</h3>;
		case 4:
			return <h4 className={className}>{children}</h4>;
		case 5:
			return <h5 className={className}>{children}</h5>;
		default:
			return <h6 className={className}>{children}</h6>;
	}
}

/**
 * A list.
 *
 * Task items are handled PER ITEM rather than per list. A blank line between
 * bullets makes CommonMark one loose list, so a vault that writes its plain
 * bullets and its checkboxes as separate-looking blocks still arrives here as a
 * single mixed list — and an all-or-nothing checklist mode would then print
 * every `- [x]` as the literal text "[x]". A task item gets its checkbox and
 * loses its marker; everything beside it keeps its bullet.
 */
function List({ list, theme, navigation }: { list: Tokens.List; theme: Theme; navigation?: NoteNavigation }) {
	const items = list.items.map((item, index) => (
		<li className={`note-prose__li${item.task ? " note-prose__li--task" : ""}`} key={index}>
			{item.task ? (
				<span className="note-prose__task">
					<span className={`note-prose__checkbox${item.checked ? " note-prose__checkbox--on" : ""}`} aria-hidden="true">
						{item.checked && <Check className="note-prose__checkbox-tick" />}
					</span>
					<span className={`note-prose__task-text${item.checked ? " note-prose__task-text--done" : ""}`}>
						<Blocks tokens={item.tokens} theme={theme} navigation={navigation} />
					</span>
				</span>
			) : (
				<Blocks tokens={item.tokens} theme={theme} navigation={navigation} />
			)}
		</li>
	));
	return list.ordered ? (
		<ol className="note-prose__list" start={typeof list.start === "number" ? list.start : undefined}>
			{items}
		</ol>
	) : (
		<ul className="note-prose__list">{items}</ul>
	);
}

const CALLOUT_LABELS: Record<CalloutKind, string> = {
	note: "Note",
	tip: "Tip",
	warning: "Warning",
	danger: "Danger",
	quote: "Quote",
};

function CalloutBlock({ callout, theme, navigation }: { callout: Callout; theme: Theme; navigation?: NoteNavigation }) {
	const label = CALLOUT_LABELS[callout.kind];
	// The kind is the tiny uppercase tag; a custom title sits beside it in
	// ordinary case. Uppercasing the author's own sentence would shout it.
	const heading = callout.title.toLowerCase() === callout.kind ? "" : callout.title;
	return (
		<div className={`note-prose__callout note-prose__callout--${callout.kind}`}>
			<div className="note-prose__callout-head">
				<span className="note-prose__callout-kind">{label}</span>
				{heading !== "" && <span className="note-prose__callout-title">{heading}</span>}
			</div>
			<div className="note-prose__callout-body">
				<Blocks tokens={callout.tokens} theme={theme} navigation={navigation} />
			</div>
		</div>
	);
}

function Table({ table, navigation }: { table: Tokens.Table; navigation?: NoteNavigation }) {
	return (
		<div className="note-prose__table-scroll">
			<table className="note-prose__table">
				<thead>
					<tr>
						{table.header.map((cell, index) => (
							<th key={index} style={{ textAlign: table.align[index] ?? undefined }}>
								<Inline tokens={cell.tokens} navigation={navigation} />
							</th>
						))}
					</tr>
				</thead>
				<tbody>
					{table.rows.map((row, rowIndex) => (
						<tr key={rowIndex}>
							{row.map((cell, cellIndex) => (
								<td key={cellIndex} style={{ textAlign: table.align[cellIndex] ?? undefined }}>
									<Inline tokens={cell.tokens} navigation={navigation} />
								</td>
							))}
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

/**
 * A fenced code block: a header naming the language with a copy button, and the
 * code beneath it, shiki-highlighted.
 *
 * Highlighting is async (the grammar is a lazy chunk), so the code is drawn
 * plain first and re-drawn coloured when the grammar lands. That ordering is
 * deliberate: a note must never wait on a grammar download to be readable.
 */
function CodeBlock({ code, theme }: { code: Tokens.Code; theme: Theme }) {
	const text = code.text.replace(/\n$/, "");
	const language = (code.lang ?? "").trim();
	const [lines, setLines] = useState<CodeToken[][] | null>(null);
	const [copied, setCopied] = useState(false);

	useEffect(() => {
		let live = true;
		setLines(null);
		void highlightCode(text, language, theme).then((result) => {
			if (live) setLines(result);
		});
		return () => {
			live = false;
		};
	}, [text, language, theme]);

	useEffect(() => {
		if (!copied) return;
		const timer = setTimeout(() => setCopied(false), 1400);
		return () => clearTimeout(timer);
	}, [copied]);

	return (
		<div className="note-prose__code">
			<div className="note-prose__code-head">
				<span className="note-prose__code-lang">{language || "text"}</span>
				<button
					type="button"
					className="note-prose__code-copy"
					aria-label={copied ? "Copied" : "Copy code"}
					onClick={() => {
						void navigator.clipboard?.writeText(text).then(() => setCopied(true));
					}}
				>
					{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
				</button>
			</div>
			<pre className="note-prose__code-body">
				<code>
					{lines
						? lines.map((line, lineIndex) => (
								<Fragment key={lineIndex}>
									{line.map((token, tokenIndex) => (
										<span key={tokenIndex} style={{ color: token.color }}>
											{token.text}
										</span>
									))}
									{lineIndex < lines.length - 1 && "\n"}
								</Fragment>
							))
						: text}
				</code>
			</pre>
		</div>
	);
}

function Inline({ tokens, navigation }: { tokens?: Token[]; navigation?: NoteNavigation }) {
	if (!tokens) return null;
	return (
		<>
			{tokens.map((token, index) => (
				<InlineToken key={index} token={token} navigation={navigation} />
			))}
		</>
	);
}

function InlineToken({ token, navigation }: { token: Token; navigation?: NoteNavigation }) {
	switch (token.type) {
		case "text":
			return <>{plainSegments((token as Tokens.Text).text, navigation)}</>;

		case "escape":
			return <>{(token as Tokens.Escape).text}</>;

		case "strong":
			return (
				<strong className="note-prose__strong">
					<Inline tokens={(token as Tokens.Strong).tokens} navigation={navigation} />
				</strong>
			);

		case "em":
			return (
				<em>
					<Inline tokens={(token as Tokens.Em).tokens} navigation={navigation} />
				</em>
			);

		case "del":
			return (
				<del>
					<Inline tokens={(token as Tokens.Del).tokens} navigation={navigation} />
				</del>
			);

		case "codespan":
			return <code className="note-prose__code-inline">{(token as Tokens.Codespan).text}</code>;

		case "br":
			return <br />;

		// A LOOSE task item's `[x]` arrives as an inline token inside the item's
		// paragraph (a tight item's is stripped from its text instead). The item
		// already draws its own checkbox, so this must render nothing — left to
		// the default branch it printed the literal "[x] ".
		case "checkbox":
			return null;

		case "wikilink": {
			const link = token as WikilinkToken;
			// An unaliased link keeps its brackets: that is what the note says, and
			// it is how the vault's own editor draws it. An alias replaces them.
			const text = link.aliased ? link.label : `[[${link.label}]]`;
			if (!navigation?.onOpenWikilink) return <span className="note-prose__wikilink">{text}</span>;
			return (
				<button
					type="button"
					className="note-prose__wikilink note-prose__wikilink--active"
					onClick={() => navigation.onOpenWikilink?.(link.target)}
					title={link.anchor ? `${link.target} › ${link.anchor}` : link.target}
				>
					{text}
				</button>
			);
		}

		case "link": {
			const link = token as Tokens.Link;
			const href = safeHref(link.href);
			// A link the app will not open is drawn as its own text, not silently
			// dropped: the reader still sees what the note said.
			if (!href)
				return (
					<span className="note-prose__link note-prose__link--inert" title={link.href}>
						<Inline tokens={link.tokens} navigation={navigation} />
					</span>
				);
			return (
				<a className="note-prose__link" href={href} target="_blank" rel="noreferrer noopener">
					<Inline tokens={link.tokens} navigation={navigation} />
				</a>
			);
		}

		case "image": {
			// Not rendered as an <img>: a note's image path is relative to the vault,
			// which the renderer cannot fetch, and a remote src would make opening a
			// note phone home. The alt text is what the reader gets.
			const image = token as Tokens.Image;
			return <span className="note-prose__image">{image.text || image.href}</span>;
		}

		case "html":
			return <>{(token as Tokens.HTML).raw}</>;

		default: {
			const withTokens = token as { tokens?: Token[]; raw?: string; text?: string };
			if (withTokens.tokens) return <Inline tokens={withTokens.tokens} navigation={navigation} />;
			return <>{withTokens.text ?? withTokens.raw ?? ""}</>;
		}
	}
}

/** Plain text, with its `#tags` lifted out into pills. */
function plainSegments(text: string, navigation?: NoteNavigation): ReactNode[] {
	return splitTags(text).map((segment, index) => {
		if (segment.kind === "text") {
			return <Fragment key={index}>{segment.text}</Fragment>;
		}
		if (!navigation?.onOpenTag) {
			return (
				<span className="note-prose__tag" key={index}>
					#{segment.tag}
				</span>
			);
		}
		return (
			<button
				type="button"
				className="note-prose__tag note-prose__tag--active"
				key={index}
				onClick={() => navigation.onOpenTag?.(segment.tag)}
			>
				#{segment.tag}
			</button>
		);
	});
}
