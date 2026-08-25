import * as Dialog from "@radix-ui/react-dialog";
import { FileCode, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useWorkspaceFiles } from "../hooks/useWorkspaceFiles";
import { apiErrorMessage } from "../lib/api-client";
import { type FileMatch, rankFiles } from "../lib/open-quickly";
import type { WorkspaceFileOpen } from "../lib/open-workspace-file";
import { useOverlayDismissFocus } from "../lib/overlay-focus";

/** How many rows the palette will rank into. Xcode shows a comparable window. */
const MAX_RESULTS = 50;

/** ⌘⇧O on macOS, Ctrl+⇧O elsewhere. Nothing else in the app binds O. */
function isOpenQuicklyShortcut(event: KeyboardEvent): boolean {
	if (!event.shiftKey || event.altKey) return false;
	if (!event.metaKey && !event.ctrlKey) return false;
	// `code` survives a non-US layout where Shift+o produces some other `key`;
	// `key` survives a remapped keyboard where the physical key moved.
	return event.code === "KeyO" || event.key.toLowerCase() === "o";
}

/** Split `text` into runs, marking the characters the query matched. */
export function highlightRuns(text: string, positions: readonly number[], offset: number) {
	const hits = new Set<number>();
	for (const p of positions) {
		const local = p - offset;
		if (local >= 0 && local < text.length) hits.add(local);
	}
	const runs: { text: string; hit: boolean }[] = [];
	let start = 0;
	for (let i = 1; i <= text.length; i++) {
		if (i === text.length || hits.has(i) !== hits.has(start)) {
			runs.push({ text: text.slice(start, i), hit: hits.has(start) });
			start = i;
		}
	}
	return runs;
}

function splitPath(path: string): { dir: string; name: string } {
	const cut = path.lastIndexOf("/") + 1;
	return { dir: path.slice(0, cut), name: path.slice(cut) };
}

/**
 * ⌘⇧O — Open Quickly, over the session workspace's FILES.
 *
 * The half of Xcode's Open Quickly that needs no language server, and per the
 * editor spike the half that gets used most. Symbols arrive in a later slice and
 * are meant to join this list rather than replace it.
 *
 * **Results can never lag the query.** The whole index is fetched once when the
 * palette opens and ranked in a `useMemo` keyed on the current query, so what is
 * on screen is always the answer to what is in the box — there is no request in
 * flight that a fast typist could outrun. That is a structural guarantee, not a
 * debounce or a request-generation counter, and it is deliberate: the spike hit
 * exactly this bug on the symbol side, where results arrived wrong-then-right.
 *
 * The component owns its own shortcut and its own open state, so mounting it is
 * one line and it stays out of the file viewer's way — slice 1 is replacing that
 * viewer with Monaco on another branch. Opening a file goes through the single
 * `onOpenFile` seam (`WorkspaceFileOpen`), which is what a later slice repoints
 * at Monaco and at a go-to-definition target.
 */
export function OpenQuicklyPalette({
	sessionId,
	enabled = true,
	onOpenFile,
}: {
	sessionId: string;
	/** False for a session with no worktree to index (an orchestrator). */
	enabled?: boolean;
	onOpenFile: (file: WorkspaceFileOpen) => void;
}) {
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [activeIndex, setActiveIndex] = useState(0);
	const listRef = useRef<HTMLDivElement | null>(null);
	const dismissFocus = useOverlayDismissFocus();

	useEffect(() => {
		if (!enabled) return;
		const handleKeyDown = (event: KeyboardEvent) => {
			if (!isOpenQuicklyShortcut(event)) return;
			event.preventDefault();
			setOpen((prev) => !prev);
		};
		window.addEventListener("keydown", handleKeyDown);
		return () => window.removeEventListener("keydown", handleKeyDown);
	}, [enabled]);

	// A session switch must not leave the previous workspace's palette open over
	// the new one.
	useEffect(() => setOpen(false), [sessionId]);

	// The index is only fetched while the palette is open: a worker session that
	// never presses ⌘⇧O never pays for `git ls-files`.
	const index = useWorkspaceFiles(sessionId, enabled && open);
	const paths = index.data?.paths;

	const results = useMemo(() => rankFiles(paths ?? [], query, MAX_RESULTS), [paths, query]);

	// Clamp rather than reset: re-ranking on each keystroke can shorten the list
	// under a selection that was valid a character ago.
	const active = results.length === 0 ? -1 : Math.min(activeIndex, results.length - 1);

	useEffect(() => {
		if (active < 0) return;
		const row = listRef.current?.querySelector(`[data-index="${active}"]`);
		if (row instanceof HTMLElement && typeof row.scrollIntoView === "function") {
			row.scrollIntoView({ block: "nearest" });
		}
	}, [active]);

	const choose = useCallback(
		(match: FileMatch | undefined) => {
			if (!match) return;
			setOpen(false);
			// Every path here came out of the session's own workspace index, so the
			// containment verdict is known without asking the server for it again.
			onOpenFile({ path: match.path, inWorkspace: true });
		},
		[onOpenFile],
	);

	const handleOpenChange = (next: boolean) => {
		setOpen(next);
		if (next) {
			setQuery("");
			setActiveIndex(0);
		}
	};

	const handleInputKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
		if (event.key === "ArrowDown" || (event.key === "n" && event.ctrlKey)) {
			event.preventDefault();
			if (results.length > 0) setActiveIndex((i) => (Math.min(i, results.length - 1) + 1) % results.length);
			return;
		}
		if (event.key === "ArrowUp" || (event.key === "p" && event.ctrlKey)) {
			event.preventDefault();
			if (results.length > 0)
				setActiveIndex((i) => (Math.min(i, results.length - 1) + results.length - 1) % results.length);
			return;
		}
		if (event.key === "Home") {
			event.preventDefault();
			setActiveIndex(0);
			return;
		}
		if (event.key === "End") {
			event.preventDefault();
			setActiveIndex(Math.max(results.length - 1, 0));
			return;
		}
		if (event.key === "Enter") {
			event.preventDefault();
			choose(results[active]);
		}
	};

	const unavailable = index.data && !index.data.available;

	return (
		<Dialog.Root open={open} onOpenChange={handleOpenChange}>
			<Dialog.Portal>
				<Dialog.Overlay className="open-quickly__overlay" />
				<Dialog.Content {...dismissFocus} className="open-quickly" aria-describedby={undefined}>
					<Dialog.Title className="sr-only">Open Quickly</Dialog.Title>
					<div className="open-quickly__search">
						<Search aria-hidden="true" className="open-quickly__search-icon" />
						{/* eslint-disable-next-line jsx-a11y/no-autofocus -- the palette exists to be typed into */}
						<input
							autoFocus
							aria-activedescendant={active >= 0 ? `open-quickly-row-${active}` : undefined}
							aria-autocomplete="list"
							aria-controls="open-quickly-results"
							aria-expanded
							aria-label="Open Quickly: search files by name"
							className="open-quickly__input"
							onChange={(e) => {
								setQuery(e.target.value);
								setActiveIndex(0);
							}}
							onKeyDown={handleInputKeyDown}
							placeholder="Open Quickly — type a file name"
							role="combobox"
							spellCheck={false}
							type="text"
							value={query}
						/>
					</div>

					<div className="open-quickly__body">
						{index.isLoading ? <p className="open-quickly__note">Indexing this workspace…</p> : null}
						{index.error ? (
							<p className="open-quickly__note open-quickly__note--error">
								{apiErrorMessage(index.error, "Unable to index this workspace")}
							</p>
						) : null}
						{unavailable ? (
							<p className="open-quickly__note">
								This session&rsquo;s worktree is no longer on disk, so there are no files to search.
							</p>
						) : null}
						{!index.isLoading && !index.error && !unavailable && query.trim() === "" ? (
							<p className="open-quickly__note">Type to search this workspace&rsquo;s files.</p>
						) : null}
						{!index.isLoading && !index.error && !unavailable && query.trim() !== "" && results.length === 0 ? (
							<p className="open-quickly__note">No files match &ldquo;{query.trim()}&rdquo;.</p>
						) : null}
						{results.length > 0 ? (
							<div
								aria-label="Matching files"
								className="open-quickly__list"
								id="open-quickly-results"
								ref={listRef}
								role="listbox"
							>
								{results.map((match, i) => (
									<ResultRow
										key={match.path}
										active={i === active}
										index={i}
										match={match}
										onHover={() => setActiveIndex(i)}
										onPick={() => choose(match)}
									/>
								))}
							</div>
						) : null}
					</div>

					<div className="open-quickly__footer">
						<span className="open-quickly__hint">
							<kbd>↑</kbd>
							<kbd>↓</kbd> to move · <kbd>↵</kbd> to open · <kbd>esc</kbd> to close
						</span>
						{index.data?.truncated ? (
							<span className="open-quickly__truncated" title="Only the first files in this workspace are indexed">
								indexed the first {index.data.paths.length.toLocaleString()} files
							</span>
						) : null}
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}

function ResultRow({
	active,
	index,
	match,
	onHover,
	onPick,
}: {
	active: boolean;
	index: number;
	match: FileMatch;
	onHover: () => void;
	onPick: () => void;
}) {
	const { dir, name } = splitPath(match.path);
	return (
		<button
			aria-selected={active}
			className={`open-quickly__row${active ? " is-active" : ""}`}
			data-index={index}
			id={`open-quickly-row-${index}`}
			// The mouse must not steal the caret from the input mid-typing, so the
			// row commits on click and never takes focus.
			onMouseDown={(e) => e.preventDefault()}
			onMouseMove={onHover}
			onClick={onPick}
			role="option"
			title={match.path}
			type="button"
		>
			<FileCode aria-hidden="true" className="open-quickly__row-icon" />
			{/* The NAME never gives up room before the directory does: a path long
			    enough to overflow is nearly always long in its directory part, and
			    truncating the file name would eat the one thing that identifies it. */}
			<span className="open-quickly__name">
				{highlightRuns(name, match.positions, match.path.length - name.length).map((run, i) =>
					run.hit ? (
						// eslint-disable-next-line react/no-array-index-key -- runs are positional
						<mark className="open-quickly__hit" key={i}>
							{run.text}
						</mark>
					) : (
						// eslint-disable-next-line react/no-array-index-key -- runs are positional
						<span key={i}>{run.text}</span>
					),
				)}
			</span>
			<span className="open-quickly__dir">
				{dir === ""
					? null
					: highlightRuns(dir, match.positions, 0).map((run, i) =>
							run.hit ? (
								// eslint-disable-next-line react/no-array-index-key -- runs are positional
								<mark className="open-quickly__hit" key={i}>
									{run.text}
								</mark>
							) : (
								// eslint-disable-next-line react/no-array-index-key -- runs are positional
								<span key={i}>{run.text}</span>
							),
						)}
			</span>
		</button>
	);
}
