import * as Dialog from "@radix-ui/react-dialog";
import { Braces, FileCode, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useWorkspaceFiles } from "../hooks/useWorkspaceFiles";
import { apiErrorMessage } from "../lib/api-client";
import { useLanguageServer } from "../lib/lsp/use-language-server";
import { type FileMatch, rankFiles } from "../lib/open-quickly";
import { parseWorkspaceSymbols, rankSymbols, type SymbolHit, type SymbolMatch } from "../lib/open-quickly-symbols";
import type { WorkspaceFileOpen } from "../lib/open-workspace-file";
import { useOverlayDismissFocus } from "../lib/overlay-focus";

/** How many rows the palette will rank into. Xcode shows a comparable window. */
const MAX_RESULTS = 50;

/** Symbols sit above the files, so they take fewer rows before the fold. */
const MAX_SYMBOL_RESULTS = 20;

/**
 * Long enough that typing does not fire a request per keystroke, short enough
 * that a pause reads as instant. Not a correctness device: staleness is handled
 * by tagging the answer with its query, below.
 */
const SYMBOL_DEBOUNCE_MS = 90;

/** The one language this slice serves. Slice 5 and 7 add to it. */
const SYMBOL_LANGUAGE = "go";

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
 * ⌘⇧O — Open Quickly, over the session workspace's FILES and Go SYMBOLS.
 *
 * Files need no language server and are per the editor spike the half that gets
 * used most, so they are always there; symbols JOIN them rather than replacing
 * them, and are absent without a language server rather than blocking the rest.
 *
 * **Results can never lag the query.** The file index is fetched once when the
 * palette opens and ranked in a `useMemo` keyed on the current query, so what is
 * on screen is always the answer to what is in the box — there is no request in
 * flight that a fast typist could outrun. Symbols cannot be synchronous, so they
 * get the same guarantee a different way: each answer is TAGGED with the query
 * that produced it and shown only while that tag still matches the box, which
 * discards a slow answer instead of displaying it late. The spike hit exactly
 * this bug on the symbol side, where results arrived wrong-then-right.
 *
 * The component owns its own shortcut and its own open state, so mounting it is
 * one line. Opening anything goes through the single `onOpenFile` seam
 * (`WorkspaceFileOpen`) — a symbol carries the `column` that seam has always had
 * a field for, and a file does not.
 */
export function OpenQuicklyPalette({
	sessionId,
	enabled = true,
	onOpenFile,
	workspaceRoot,
}: {
	sessionId: string;
	/** False for a session with no worktree to index (an orchestrator). */
	enabled?: boolean;
	onOpenFile: (file: WorkspaceFileOpen) => void;
	/** The session's worktree root, absolute. Without it there are no symbols. */
	workspaceRoot?: string;
}) {
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [activeIndex, setActiveIndex] = useState(0);
	const listRef = useRef<HTMLDivElement | null>(null);
	const inputRef = useRef<HTMLInputElement | null>(null);
	const dismissFocus = useOverlayDismissFocus();

	useEffect(() => {
		if (!enabled) return;
		const handleKeyDown = (event: KeyboardEvent) => {
			if (!isOpenQuicklyShortcut(event)) return;
			event.preventDefault();
			setOpen((prev) => !prev);
			// The query survives (see onOpenAutoFocus) but the SELECTION does not:
			// reopening always lands on the best match, never on wherever the
			// arrow keys were left last time.
			setActiveIndex(0);
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

	// The Go server for THIS workspace, attached only while the palette is OPEN.
	// Pressing ⌘⇧O is therefore what starts gopls - not opening a session, and
	// not launching the app - and closing the palette begins its idle countdown.
	const server = useLanguageServer(open && workspaceRoot ? workspaceRoot : undefined, SYMBOL_LANGUAGE);
	const trimmedQuery = query.trim();

	/**
	 * 🗝 Slice 2's guarantee, extended to an ASYNCHRONOUS source.
	 *
	 * The file half is a `useMemo` over the current query, so it cannot lag.
	 * Symbols cannot be, so each answer is TAGGED with the query that produced it
	 * and rendered only while that tag still equals the box. A slow answer is
	 * DISCARDED rather than shown late - stronger than a debounce, and the exact
	 * bug the spike hit on the symbol side, where results arrived wrong-then-right.
	 */
	const [symbolAnswer, setSymbolAnswer] = useState<{ query: string; hits: SymbolHit[] } | null>(null);

	useEffect(() => {
		const client = server.client;
		// Gate on READINESS, not on latency. workspace/symbol answers WRONG before
		// the index settles, so asking early would put garbage on screen in exactly
		// the seconds people use this most.
		if (!open || !client || server.state !== "ready" || trimmedQuery === "") {
			setSymbolAnswer(null);
			return;
		}
		let cancelled = false;
		const timer = setTimeout(() => {
			void client
				.request("workspace/symbol", { query: trimmedQuery })
				.then((result) => {
					if (cancelled) return;
					const hits = parseWorkspaceSymbols(result);
					if (hits.length === 0) {
						// Up, answering, and returning nothing: logged so it is
						// distinguishable in the console from a server that is broken.
						console.warn(`[lsp] workspace/symbol "${trimmedQuery}" → 0 symbols (server ready)`);
					}
					setSymbolAnswer({ query: trimmedQuery, hits });
				})
				.catch((err: unknown) => {
					if (cancelled) return;
					console.warn(`[lsp] workspace/symbol "${trimmedQuery}" failed`, err);
					setSymbolAnswer({ query: trimmedQuery, hits: [] });
				});
		}, SYMBOL_DEBOUNCE_MS);
		return () => {
			cancelled = true;
			clearTimeout(timer);
		};
	}, [open, server.client, server.state, trimmedQuery]);

	const symbols = useMemo(
		() =>
			symbolAnswer && symbolAnswer.query === trimmedQuery && workspaceRoot
				? rankSymbols(symbolAnswer.hits, trimmedQuery, workspaceRoot, MAX_SYMBOL_RESULTS)
				: [],
		[symbolAnswer, trimmedQuery, workspaceRoot],
	);

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

	const chooseSymbol = useCallback(
		(symbol: SymbolMatch) => {
			setOpen(false);
			// The same seam a terminal reference and a file row use, carrying the
			// COLUMN a symbol has and a file does not - the field slice 2 left here.
			onOpenFile({
				path: symbol.path,
				line: symbol.line,
				column: symbol.column,
				inWorkspace: !symbol.path.startsWith("/"),
			});
		},
		[onOpenFile],
	);

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
		<Dialog.Root open={open} onOpenChange={setOpen}>
			<Dialog.Portal>
				<Dialog.Overlay className="open-quickly__overlay" />
				<Dialog.Content
					{...dismissFocus}
					aria-describedby={undefined}
					className="open-quickly"
					// Reopening keeps the last query and SELECTS it, the way Xcode's
					// Open Quickly does: ⌘⇧O then Enter re-runs the last search, and
					// typing replaces it, so carrying it over costs a user who does not
					// want it nothing. Radix's own focus-return puts the caret at the
					// END of the value, which is the one behaviour that is wrong here —
					// it silently APPENDS the next thing typed to the old query. Taking
					// focus ourselves is what makes the carried-over value a feature
					// rather than that bug.
					onOpenAutoFocus={(event) => {
						event.preventDefault();
						const input = inputRef.current;
						if (!input) return;
						input.focus();
						input.select();
					}}
				>
					<Dialog.Title className="sr-only">Open Quickly</Dialog.Title>
					<div className="open-quickly__search">
						<Search aria-hidden="true" className="open-quickly__search-icon" />
						<input
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
							ref={inputRef}
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
						{trimmedQuery !== "" && workspaceRoot && server.state !== "unavailable" ? (
						<div className="open-quickly__section" data-testid="open-quickly-symbols">
							<div className="open-quickly__section-label">Symbols</div>
							{server.state === "failed" ? (
								<p className="open-quickly__note open-quickly__note--error">
									The Go language server isn&rsquo;t running, so symbols aren&rsquo;t searchable.
									{server.detail ? ` (${server.detail})` : ""}
								</p>
							) : server.state !== "ready" ? (
								/* Gate on READINESS, not latency: an empty list here would be a
								   lie told in exactly the seconds people use this most. */
								<p className="open-quickly__note">Indexing this workspace&rsquo;s Go packages&hellip;</p>
							) : symbolAnswer?.query !== trimmedQuery ? (
								<p className="open-quickly__note">Searching symbols&hellip;</p>
							) : symbols.length === 0 ? (
								<p className="open-quickly__note">No Go symbols match &ldquo;{trimmedQuery}&rdquo;.</p>
							) : (
								<div aria-label="Matching symbols" className="open-quickly__list" role="listbox">
									{symbols.map((symbol) => (
										<SymbolRow
											key={`${symbol.uri}:${symbol.line}:${symbol.column}:${symbol.name}`}
											match={symbol}
											onPick={() => chooseSymbol(symbol)}
										/>
									))}
								</div>
							)}
						</div>
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

/**
 * One symbol. Deliberately shaped like `ResultRow`: same commit-on-click,
 * never-take-focus behaviour, so the caret cannot be stolen from the input
 * mid-typing, and the same underline treatment over the matched characters.
 */
function SymbolRow({ match, onPick }: { match: SymbolMatch; onPick: () => void }) {
	return (
		<button
			className="open-quickly__row open-quickly__row--symbol"
			onMouseDown={(e) => e.preventDefault()}
			onClick={onPick}
			role="option"
			aria-selected={false}
			title={`${match.path}:${match.line}`}
			type="button"
		>
			<Braces aria-hidden="true" className="open-quickly__row-icon" />
			<span className="open-quickly__name">
				{highlightRuns(match.name, match.positions, 0).map((run, i) =>
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
				{match.containerName ? `${match.containerName} · ` : ""}
				{match.path}:{match.line}
			</span>
		</button>
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
