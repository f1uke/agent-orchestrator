import { type CSSProperties, useCallback, useEffect, useMemo, useState } from "react";
import { ChevronLeft, ChevronRight, Link2, X } from "lucide-react";
import { NoteMarkdown, type NoteEditing } from "./NoteMarkdown";
import { type Drift, FileDriftBanner } from "./FileDriftBanner";
import { parseNote, splitTags } from "../lib/note/parse";
import { type EditableBlock, spliceBlock, type TaskMarker, toggleTask } from "../lib/note/edit";
import { useSaveWikiNote, type WikiNote } from "../hooks/useWiki";
import type { Theme } from "../stores/ui-store";

/**
 * A note, open in the CENTRE of the Wiki page in place of the terminal — the
 * same takeover `SessionView.renderFocusedCenter` does with `workspaceFile`,
 * and for the same reason: a reader wants the note, not the note squeezed
 * beside a terminal.
 *
 * Closing returns to the terminal, which never stopped running.
 *
 * The chrome mirrors `WorkspaceFileView`: back/forward history chevrons, and a
 * path that ellipsises its DIRECTORY and never its filename.
 *
 * 🗝 The note is EDITABLE in place, and every write goes through the same two
 * steps: splice one mapped byte range into the content that was read (never a
 * re-rendering of what was drawn — see `lib/note/edit.ts`), then PUT it
 * preconditioned on the hash it was read with. The vault's own agent writes
 * these files, so a refused save is the normal case and gets the same
 * `FileDriftBanner` the workspace editor uses.
 */

export function WikiNoteView({
	note,
	loading,
	error,
	theme,
	back,
	forward,
	onClose,
	onReload,
	onOpenNote,
	onOpenTag,
}: {
	note: WikiNote | undefined;
	loading: boolean;
	error?: string;
	theme: Theme;
	back: { to: string; go: () => void } | null;
	forward: { to: string; go: () => void } | null;
	onClose: () => void;
	/** Re-read the note from disk, discarding what the reader has not saved. */
	onReload: () => void;
	onOpenNote: (target: string) => void;
	onOpenTag: (tag: string) => void;
}) {
	const parsed = useMemo(() => (note ? parseNote(note.content) : null), [note]);
	const save = useSaveWikiNote();

	// The block currently open, addressed by the byte it starts at. A byte offset
	// rather than a token: the note re-parses on every content change, and a
	// token reference would point into the previous parse.
	const [openAt, setOpenAt] = useState<number | null>(null);
	const [drift, setDrift] = useState<Drift | null>(null);
	const closeEditor = useCallback(() => setOpenAt(null), []);

	// Moving to another note closes whatever was open in this one.
	const path = note?.path;
	useEffect(() => {
		setOpenAt(null);
		setDrift(null);
		save.reset();
		// `save` is a stable mutation object; re-running this on its own state
		// changes would close the editor mid-save.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [path]);

	/**
	 * One write. The caller hands back the note's bytes with exactly one range
	 * replaced, and this is the only place that talks to the daemon.
	 */
	const write = useCallback(
		(next: string) => {
			if (!note) return;
			setOpenAt(null);
			save.mutate(
				{ path: note.path, content: next, baseHash: note.contentHash },
				{
					onSuccess: () => setDrift(null),
					onError: (error) => {
						const current = error.failure.kind === "conflict" ? error.failure.current : undefined;
						// A conflict is the one failure the reader RESOLVES. The block's
						// byte range no longer means anything against the file that is
						// there now, so the banner offers no re-save: reload and edit
						// what the agent actually wrote.
						setDrift(
							error.failure.kind === "conflict"
								? { hash: current?.hash, size: current?.size, modifiedAt: current?.modifiedAt, reviewing: false }
								: null,
						);
					},
				},
			);
		},
		[note, save],
	);

	const editing: NoteEditing | undefined = note
		? {
				content: note.content,
				// Both strips ahead of the parse are prefix-only, so where the rendered
				// text starts in the file is just the length difference.
				sourceOffset: note.content.length - stripLeadingTitle(note.content, titleOf(note, parsed)).length,
				openAt,
				onOpen: (block: EditableBlock) => setOpenAt(block.start),
				onCommit: (block: EditableBlock, text: string) => write(spliceBlock(note.content, block, text)),
				onCancel: closeEditor,
				onToggleTask: (marker: TaskMarker) => write(toggleTask(note.content, marker)),
				busy: save.isPending,
			}
		: undefined;

	// Tags shown under the title are the frontmatter's, plus any the note opens
	// with on its own first line — the two places a vault actually puts them.
	const headerTags = useMemo(() => {
		if (!parsed || !note) return [];
		const inline = splitTags(firstLine(note.content))
			.filter((segment) => segment.kind === "tag")
			.map((segment) => (segment.kind === "tag" ? segment.tag : ""));
		return [...new Set([...parsed.frontmatterTags, ...inline])];
	}, [parsed, note]);

	const title = titleOf(note, parsed);
	const refused = save.error && save.error.failure.kind !== "conflict" ? save.error.failure : null;

	return (
		<div className="wiki-note">
			<div className="wiki-note__bar">
				<NavButton direction="back" target={back} />
				<NavButton direction="forward" target={forward} />
				<span className="wiki-note__bar-divider" />
				<PathLabel path={note?.path ?? ""} />
				<span className="wiki-note__bar-spacer" />
				<button type="button" className="wiki-note__bar-button" aria-label="Close note" onClick={onClose}>
					<X aria-hidden="true" />
				</button>
			</div>

			{drift && (
				<FileDriftBanner
					drift={drift}
					onDiscardMine={() => {
						setDrift(null);
						save.reset();
						onReload();
					}}
					onDismiss={() => setDrift(null)}
				/>
			)}
			{refused && (
				<div className="wiki-note__refused" role="alert">
					<strong>{refused.title}</strong> {refused.detail}
				</div>
			)}

			<div className="wiki-note__scroll">
				<div className="wiki-note__measure">
					{error && <div className="wiki-note__message">{error}</div>}
					{!error && loading && !note && <div className="wiki-note__message">Opening…</div>}
					{!error && note && parsed && (
						<>
							<h1 className="wiki-note__title">{title}</h1>
							<div className="wiki-note__meta">
								{headerTags.map((tag) => (
									<button
										type="button"
										key={tag}
										className="note-prose__tag note-prose__tag--active"
										onClick={() => onOpenTag(tag)}
									>
										#{tag}
									</button>
								))}
								<span className="wiki-note__meta-text">
									{[editedLabel(note.modifiedAt), `${parsed.wordCount} words`].filter(Boolean).join(" · ")}
								</span>
							</div>

							<div className="note-prose wiki-note__body">
								<NoteMarkdown
									source={stripLeadingTitle(note.content, title)}
									theme={theme}
									navigation={{ onOpenWikilink: onOpenNote, onOpenTag }}
									editing={editing}
								/>
							</div>

							{note.backlinks.length > 0 && (
								<>
									<hr className="note-prose__rule" />
									<div className="wiki-note__backlinks">
										<Link2 aria-hidden="true" className="wiki-note__backlinks-icon" />
										<span className="wiki-note__backlinks-label">Linked from</span>
										{note.backlinks.map((path) => (
											<button
												type="button"
												key={path}
												className="wiki-note__backlink"
												onClick={() => onOpenNote(path)}
												title={path}
											>
												{baseName(path).replace(/\.md$/i, "")}
											</button>
										))}
									</div>
								</>
							)}
							<div className="wiki-note__tail" />
						</>
					)}
				</div>
			</div>
		</div>
	);
}

/**
 * One of the two history chevrons. Disabled rather than hidden when there is
 * nowhere to go — a control that appears and disappears as you navigate makes
 * the header twitch and slides the other one under your cursor. (Same rule, and
 * the same reason, as WorkspaceFileView's.)
 */
function NavButton({
	direction,
	target,
}: {
	direction: "back" | "forward";
	target: { to: string; go: () => void } | null;
}) {
	const label = direction === "back" ? "Back" : "Forward";
	const Icon = direction === "back" ? ChevronLeft : ChevronRight;
	return (
		<button
			type="button"
			className="wiki-note__bar-button"
			aria-label={label}
			disabled={!target}
			title={target ? `${label} to ${target.to}` : `No note to go ${direction} to`}
			onClick={() => target?.go()}
		>
			<Icon aria-hidden="true" />
		</button>
	);
}

/**
 * A path that truncates its DIRECTORY, never its filename. A vault nests
 * deeply, and a plain tail ellipsis would eat the one part that says which note
 * this is.
 */
function PathLabel({ path }: { path: string }) {
	const slash = path.lastIndexOf("/");
	const dir = slash >= 0 ? path.slice(0, slash + 1) : "";
	const base = slash >= 0 ? path.slice(slash + 1) : path;
	const shrink: CSSProperties = { minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" };
	return (
		<span className="wiki-note__path" title={path}>
			{dir !== "" && (
				<span className="wiki-note__path-dir" style={{ flex: "1 1 auto", ...shrink }}>
					{dir}
				</span>
			)}
			<span className="wiki-note__path-base" style={{ flex: "0 1 auto", ...shrink }}>
				{base}
			</span>
		</span>
	);
}

/**
 * The note without its YAML frontmatter and without the blank lines that
 * follow it. Both callers below match at the very start of the body, so a
 * leading newline left behind by the strip would silently defeat them — which
 * is exactly how the title came out printed twice.
 */
export function noteBody(content: string): string {
	return content.replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/, "").replace(/^(\r?\n)+/, "");
}

function firstLine(content: string): string {
	return noteBody(content).split(/\r?\n/, 1)[0] ?? "";
}

/** The note's own opening `# Heading`, when it has one. */
export function headingTitle(content: string): string {
	return /^#[ \t]+(.+?)[ \t]*$/m.exec(firstLine(content))?.[1]?.trim() ?? "";
}

/**
 * Drops the note's leading `# Heading` when the page is already showing it as
 * the title, so the reader does not get the same words twice.
 */
export function stripLeadingTitle(content: string, title: string): string {
	const body = noteBody(content);
	const match = /^#[ \t]+(.+?)[ \t]*(\r?\n|$)/.exec(body);
	return match && match[1].trim() === title ? body.slice(match[0].length).replace(/^(\r?\n)+/, "") : body;
}

/**
 * The note's title: its frontmatter's, else its own opening heading, else its
 * filename. Read in one place because the body strip depends on it — the
 * heading is only dropped from the body when the page is already showing it.
 */
function titleOf(note: WikiNote | undefined, parsed: { frontmatterTitle: string } | null): string {
	return parsed?.frontmatterTitle || headingTitle(note?.content ?? "") || baseName(note?.path ?? "");
}

function baseName(path: string): string {
	return path.slice(path.lastIndexOf("/") + 1);
}

function editedLabel(iso: string | undefined, now = Date.now()): string {
	if (!iso) return "";
	const at = Date.parse(iso);
	if (Number.isNaN(at)) return "";
	const days = Math.floor((now - at) / 86_400_000);
	if (days <= 0) return "Edited today";
	if (days === 1) return "Edited yesterday";
	if (days < 30) return `Edited ${days} days ago`;
	const months = Math.floor(days / 30);
	return months < 12 ? `Edited ${months} months ago` : `Edited ${Math.floor(days / 365)} years ago`;
}
