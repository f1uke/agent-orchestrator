import {
	Fragment,
	type KeyboardEvent,
	type MouseEvent,
	type ReactNode,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
} from "react";
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
import { type EditableBlock, indexNote, type NoteIndex, type TaskMarker, taskMarker } from "../lib/note/edit";
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

/**
 * Editing a note IN PLACE, on the page that renders it.
 *
 * 🗝 Nothing here serialises the DOM back to markdown. A click opens ONE block
 * in a text box holding that block's own source text, and committing splices
 * that block's byte range back into the file — see `lib/note/edit.ts` for the
 * mapping and for the constructs it refuses to map. A block with no mapping
 * simply does not become editable, and reads exactly as it did before.
 *
 * A block whose source and rendered text are the same — a plain paragraph, a
 * plain task item, a plain heading, which is most of a vault — opens with the
 * words already on screen and no syntax appears at all. A block that really
 * does contain markup shows that markup while it is open, and only while it is
 * open, which is what Obsidian's own editor does.
 */
export type NoteEditing = {
	/** The note's whole bytes. Every block range is an offset into these. */
	content: string;
	/** Where the rendered `source` starts inside `content`. */
	sourceOffset: number;
	/** The `start` of the block currently open, if any. */
	openAt: number | null;
	onOpen: (block: EditableBlock) => void;
	onCommit: (block: EditableBlock, text: string) => void;
	onCancel: () => void;
	onToggleTask: (marker: TaskMarker) => void;
	/** True while a write is in flight: nothing new opens until it lands. */
	busy: boolean;
};

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
	editing,
}: {
	source: string;
	theme: Theme;
	navigation?: NoteNavigation;
	editing?: NoteEditing;
}) {
	const { tokens } = useMemo(() => parseNote(source), [source]);
	// The index is built from the SAME token objects the render walks, because it
	// is keyed by their identity. Re-parsing separately would key it by objects
	// nothing on screen refers to.
	const index = useMemo(
		() => (editing ? indexNote(source, tokens, editing.sourceOffset) : undefined),
		[editing, source, tokens],
	);
	return <Blocks tokens={tokens} theme={theme} navigation={navigation} edit={bind(editing, index)} />;
}

/** The editing props and the index travel together or not at all. */
type Edit = { editing: NoteEditing; index: NoteIndex };

function bind(editing: NoteEditing | undefined, index: NoteIndex | undefined): Edit | undefined {
	return editing && index ? { editing, index } : undefined;
}

function Blocks({
	tokens,
	theme,
	navigation,
	edit,
}: {
	tokens: Token[];
	theme: Theme;
	navigation?: NoteNavigation;
	edit?: Edit;
}) {
	return (
		<>
			{tokens.map((token, index) => (
				// Tokens have no stable identity, and a note re-renders wholesale when
				// its content changes, so the index IS the identity here.
				<Block key={index} token={token} first={index === 0} theme={theme} navigation={navigation} edit={edit} />
			))}
		</>
	);
}

function Block({
	token,
	first,
	theme,
	navigation,
	edit,
}: {
	token: Token;
	first: boolean;
	theme: Theme;
	navigation?: NoteNavigation;
	edit?: Edit;
}) {
	const block = edit?.index.editable.get(token);
	if (edit && block && edit.editing.openAt === block.start) {
		return <BlockEditor block={block} editing={edit.editing} kind={token.type === "heading" ? "heading" : "prose"} />;
	}
	const open = block && edit ? openHandlers(block, edit.editing) : undefined;

	switch (token.type) {
		case "space":
			return null;

		case "heading": {
			const heading = token as Tokens.Heading;
			return (
				<Heading level={heading.depth} first={first} open={open}>
					<Inline tokens={heading.tokens} navigation={navigation} />
				</Heading>
			);
		}

		case "paragraph":
			return (
				<p
					className={`note-prose__p${first ? " note-prose__p--first" : ""}${open ? " note-prose__editable" : ""}`}
					{...open}
				>
					<Inline tokens={(token as Tokens.Paragraph).tokens} navigation={navigation} />
				</p>
			);

		case "text": {
			// A loose list item's content arrives as a bare text token carrying its
			// own inline tokens.
			const text = token as Tokens.Text;
			const body = text.tokens ? <Inline tokens={text.tokens} navigation={navigation} /> : plainSegments(text.text);
			if (!open) return <>{body}</>;
			return (
				<span className="note-prose__editable" {...open}>
					{body}
				</span>
			);
		}

		case "list":
			return <List list={token as Tokens.List} theme={theme} navigation={navigation} edit={edit} />;

		case "code":
			return <CodeBlock code={token as Tokens.Code} theme={theme} />;

		case "blockquote": {
			// Quoted content is deliberately not editable: `> ` is not indentation,
			// so its bytes cannot be mapped (see lib/note/edit.ts).
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

function Heading({
	level,
	first,
	children,
	open,
}: {
	level: number;
	first: boolean;
	children: ReactNode;
	open?: OpenHandlers;
}) {
	const className = `note-prose__h note-prose__h${Math.min(level, 6)}${first ? " note-prose__h--first" : ""}${
		open ? " note-prose__editable" : ""
	}`;
	switch (level) {
		case 1:
			return (
				<h1 className={className} {...open}>
					{children}
				</h1>
			);
		case 2:
			return (
				<h2 className={className} {...open}>
					{children}
				</h2>
			);
		case 3:
			return (
				<h3 className={className} {...open}>
					{children}
				</h3>
			);
		case 4:
			return (
				<h4 className={className} {...open}>
					{children}
				</h4>
			);
		case 5:
			return (
				<h5 className={className} {...open}>
					{children}
				</h5>
			);
		default:
			return (
				<h6 className={className} {...open}>
					{children}
				</h6>
			);
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
 *
 * The marker never reaches this file: `parse.ts` drops `marked`'s synthetic
 * `checkbox` token at the lexer, so `item.task`/`item.checked` are the only
 * record of it. See the note on `lex` there for why that has to happen once, in
 * the parser, rather than in each of this file's two render switches.
 */
function List({
	list,
	theme,
	navigation,
	edit,
}: {
	list: Tokens.List;
	theme: Theme;
	navigation?: NoteNavigation;
	edit?: Edit;
}) {
	const items = list.items.map((item, index) => (
		<li className={`note-prose__li${item.task ? " note-prose__li--task" : ""}`} key={index}>
			{item.task ? (
				<span className="note-prose__task">
					<Checkbox item={item} edit={edit} />
					<span className={`note-prose__task-text${item.checked ? " note-prose__task-text--done" : ""}`}>
						<Blocks tokens={item.tokens} theme={theme} navigation={navigation} edit={edit} />
					</span>
				</span>
			) : (
				<Blocks tokens={item.tokens} theme={theme} navigation={navigation} edit={edit} />
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

/**
 * One task item's box.
 *
 * Clicking it rewrites ONE character of the note — the space or the `x` between
 * the brackets — and nothing else. It stays an inert square when the note is
 * read-only or when this particular item's marker could not be located, which
 * is the same rule every other edit here follows: no mapping, no writing.
 */
function Checkbox({ item, edit }: { item: Tokens.ListItem; edit?: Edit }) {
	const face = (
		<span className={`note-prose__checkbox${item.checked ? " note-prose__checkbox--on" : ""}`} aria-hidden="true">
			{item.checked && <Check className="note-prose__checkbox-tick" />}
		</span>
	);
	const span = edit?.index.spans.get(item);
	const marker = edit && span ? taskMarker(edit.editing.content, span) : null;
	if (!edit || !marker) return face;
	return (
		<button
			type="button"
			className="note-prose__checkbox-hit"
			role="checkbox"
			aria-checked={marker.checked}
			aria-label={marker.checked ? "Mark as not done" : "Mark as done"}
			disabled={edit.editing.busy}
			onClick={() => edit.editing.onToggleTask(marker)}
		>
			{face}
		</button>
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

		case "wikilink": {
			const link = token as WikilinkToken;
			// The brackets are SYNTAX, not text: Obsidian draws `[[a-note]]` as
			// "a-note" and `[[note|shown]]` as "shown", and a rendered view that
			// keeps them makes every link read like a typo. The pill is what says
			// this is a link.
			const text = link.label;
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

/** The props that turn a rendered block into one the reader can click into. */
type OpenHandlers = {
	onClick: (event: MouseEvent) => void;
	onKeyDown: (event: KeyboardEvent) => void;
	tabIndex: number;
	role: "button";
	title: string;
};

/**
 * Opening a block for editing.
 *
 * A click that landed on a link, a wikilink pill or a tag is left alone: those
 * navigate, and swallowing them to open an editor would make every link in the
 * vault unreachable.
 */
function openHandlers(block: EditableBlock, editing: NoteEditing): OpenHandlers {
	const open = () => {
		if (!editing.busy) editing.onOpen(block);
	};
	return {
		onClick: (event) => {
			if ((event.target as HTMLElement | null)?.closest("a, button")) return;
			open();
		},
		onKeyDown: (event) => {
			if (event.key !== "Enter" || event.target !== event.currentTarget) return;
			event.preventDefault();
			open();
		},
		tabIndex: 0,
		role: "button",
		title: "Click to edit",
	};
}

/**
 * The open block, as a text box holding its own source text.
 *
 * It grows with its content rather than scrolling, so the page does not jump
 * when a block is opened, and it commits on blur — the way a note-taking app
 * behaves, rather than making the reader hunt for a save button. Escape leaves
 * the note exactly as it was.
 */
function BlockEditor({
	block,
	editing,
	kind,
}: {
	block: EditableBlock;
	editing: NoteEditing;
	kind: "heading" | "prose";
}) {
	const [text, setText] = useState(block.text);
	const area = useRef<HTMLTextAreaElement | null>(null);
	// Set once a commit or a cancel has been decided, so the blur that follows
	// does not commit a second time (or commit text the reader just discarded).
	const settled = useRef(false);

	useLayoutEffect(() => {
		const node = area.current;
		if (!node) return;
		node.focus();
		node.setSelectionRange(node.value.length, node.value.length);
	}, []);

	useLayoutEffect(() => {
		const node = area.current;
		if (!node) return;
		node.style.height = "auto";
		node.style.height = `${node.scrollHeight}px`;
	}, [text]);

	const commit = () => {
		if (settled.current) return;
		settled.current = true;
		if (text === block.text) editing.onCancel();
		else editing.onCommit(block, text);
	};
	const cancel = () => {
		if (settled.current) return;
		settled.current = true;
		editing.onCancel();
	};

	return (
		<textarea
			ref={area}
			className={`note-prose__editor note-prose__editor--${kind}`}
			aria-label="Edit this block"
			value={text}
			spellCheck={false}
			rows={1}
			onChange={(event) => setText(event.target.value)}
			onBlur={commit}
			onKeyDown={(event) => {
				if (event.key === "Escape") {
					event.preventDefault();
					cancel();
					return;
				}
				if (event.key !== "Enter") return;
				// A single-line block cannot hold a newline, so Enter commits. A
				// paragraph can, so there Enter types one and the modifier commits.
				if (!block.multiline || event.metaKey || event.ctrlKey) {
					event.preventDefault();
					commit();
				}
			}}
		/>
	);
}
