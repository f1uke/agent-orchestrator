import { CaseSensitive, FolderOpen, Regex, Search, SlidersHorizontal, WholeWord } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { type SearchOptions, useWorkspaceSearch } from "../hooks/useWorkspaceSearch";
import { searchSummary } from "../lib/editor/search-results";
import { cn } from "../lib/utils";
import { EmptyState } from "./FilesEmptyState";
import { type SearchHit, SearchResultsList } from "./SearchResultsList";
import { SimpleTooltip } from "./ui/tooltip";

/**
 * Search mode: every line in the worktree whose CONTENT matches — ⌘⇧F.
 *
 * It is a MODE of the Files rail rather than an overlay or a fourth panel,
 * because the rail is already the app's files surface, narrowed three ways:
 * Browse is every file, Changes is the ones that differ from the target branch,
 * and this is the ones that contain something. All three answer "which file do I
 * open next", all three open through the same seam, and all three inherit the
 * rail's width, scroller and virtualisation. A separate overlay would have been
 * a second files surface whose results open into the first.
 *
 * It is deliberately NOT the rail's per-file search box, which filters PATHS in
 * Browse and Changes. Those two questions look alike and are not: one narrows a
 * list you can see, the other reads 62 MB off disk.
 */
export function SearchPanel({
	sessionId,
	active,
	focusNonce,
	seed,
	onOpenHit,
	onExit,
	selected,
}: {
	sessionId: string;
	/** False while another mode is on screen; nothing is searched then. */
	active: boolean;
	/**
	 * Bumped every time ⌘⇧F is pressed, so pressing it again while the panel is
	 * already up re-focuses and SELECTS the box rather than doing nothing —
	 * the trap #247 hit with ⌘⇧O, where a controlled dialog never gets
	 * `onOpenChange(true)` and any "reset on open" written there is dead code.
	 */
	focusNonce?: number;
	/** Text ⌘⇧F arrived with — the editor's selection, when there was one. */
	seed?: string;
	onOpenHit: (hit: SearchHit) => void;
	/** Escape leaves search and returns the rail to the mode it came from. */
	onExit: () => void;
	selected?: { path: string; line?: number } | null;
}) {
	const [query, setQuery] = useState("");
	const [matchCase, setMatchCase] = useState(false);
	const [wholeWord, setWholeWord] = useState(false);
	const [regex, setRegex] = useState(false);
	const [showFilters, setShowFilters] = useState(false);
	const [include, setInclude] = useState("");
	const [exclude, setExclude] = useState("");
	const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set());
	const inputRef = useRef<HTMLInputElement | null>(null);

	const options: SearchOptions = useMemo(
		() => ({ matchCase, wholeWord, regex, include, exclude }),
		[matchCase, wholeWord, regex, include, exclude],
	);
	const search = useWorkspaceSearch(sessionId, query, options, active);

	// ⌘⇧F: take the box, seed it from the editor's selection when there is one,
	// and SELECT what is in it so the next keystroke replaces rather than
	// appends. Keeping the previous query is on purpose — Xcode re-runs the last
	// search — which is exactly why it has to arrive selected.
	//
	// 🗝 Selecting takes TWO passes, and doing it in one is a silent bug: seeding
	// calls `setQuery`, so at the moment this effect runs the input still holds
	// the OLD value and `select()` would select that — leaving the caret at the
	// end of the new one, which is #247's "the next thing typed was appended"
	// trap arriving by a different route. So the select is re-run in a layout
	// effect after the value has actually landed.
	const selectPending = useRef(false);
	useEffect(() => {
		if (!active) return;
		if (seed && seed !== "") setQuery(seed);
		selectPending.current = true;
		selectBox(inputRef.current);
	}, [active, focusNonce, seed]);
	useLayoutEffect(() => {
		if (!selectPending.current) return;
		selectPending.current = false;
		selectBox(inputRef.current);
	});

	// A new result set is a new list; folds from the previous query would hide
	// files the reader never closed. Keyed on the query that was ANSWERED, not
	// the one being typed, so the folds survive until the list actually changes.
	const answered = search.answered;
	useEffect(() => {
		setCollapsed(new Set());
	}, [answered]);

	const toggleFile = useCallback((path: string) => {
		setCollapsed((prev) => {
			const next = new Set(prev);
			if (!next.delete(path)) next.add(path);
			return next;
		});
	}, []);

	// The search route answers availability itself, the way changes/files do — so
	// this is read off the response rather than from a second query the panel
	// would otherwise have to run just to learn the worktree is gone.
	if (search.data && !search.data.available) {
		return (
			<EmptyState
				icon={<FolderOpen aria-hidden="true" className="h-6 w-6" />}
				title="Worktree no longer on disk"
				detail="This session's worktree was cleaned up, so there are no files left to search."
			/>
		);
	}

	const data = search.data;
	const typing = query.trim() !== "";

	return (
		// Escape is caught here rather than on the input, so it also works from the
		// glob fields and the option toggles — anywhere inside search mode.
		<div className="files-search" onKeyDown={(e) => e.key === "Escape" && onExit()}>
			<div className="files-panel__toolbar">
				<span className="files-panel__search files-search__field">
					<Search aria-hidden="true" className="files-panel__search-icon" />
					<input
						ref={inputRef}
						type="text"
						role="searchbox"
						aria-label="Search in project"
						placeholder="Search in project"
						className="files-panel__search-input files-search__input"
						spellCheck={false}
						autoComplete="off"
						value={query}
						onChange={(e) => setQuery(e.target.value)}
					/>
					<span className="files-search__toggles">
						<OptionToggle label="Match case" active={matchCase} onClick={() => setMatchCase((v) => !v)}>
							<CaseSensitive aria-hidden="true" className="h-3.5 w-3.5" />
						</OptionToggle>
						<OptionToggle label="Whole word" active={wholeWord} onClick={() => setWholeWord((v) => !v)}>
							<WholeWord aria-hidden="true" className="h-3.5 w-3.5" />
						</OptionToggle>
						<OptionToggle label="Regular expression" active={regex} onClick={() => setRegex((v) => !v)}>
							<Regex aria-hidden="true" className="h-3.5 w-3.5" />
						</OptionToggle>
					</span>
				</span>
				<OptionToggle
					label="Files to include and exclude"
					active={showFilters || include !== "" || exclude !== ""}
					onClick={() => setShowFilters((v) => !v)}
				>
					<SlidersHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
				</OptionToggle>
			</div>

			{/* Folded away by default so the rail's default state stays two lines
			    tall. Discoverable as a control rather than as syntax — the point of
			    shipping the options at all. */}
			{showFilters ? (
				<div className="files-search__filters">
					<GlobField
						label="Files to include"
						placeholder="e.g. *.swift, App/Features"
						value={include}
						onChange={setInclude}
					/>
					<GlobField
						label="Files to exclude"
						placeholder="e.g. Pods, *.generated.swift"
						value={exclude}
						onChange={setExclude}
					/>
				</div>
			) : null}

			<div className="files-panel__summary files-search__summary" role="status" aria-live="polite">
				{!typing ? (
					<span className="files-search__hint">Type to search every file in this worktree.</span>
				) : search.error ? (
					<span className="files-search__error">{search.error}</span>
				) : data?.invalidRegex ? (
					// A pattern the reader has not finished typing. Said quietly, in
					// the summary line, because it is a state of the box between two
					// working keystrokes — not a failure of the request.
					<span className="files-search__error">Invalid pattern: {data.invalidRegex}</span>
				) : data ? (
					<span className="files-search__count-line">{searchSummary(data)}</span>
				) : (
					<span className="files-search__hint">Searching…</span>
				)}
				{search.searching ? <span aria-hidden="true" className="files-search__pulse" /> : null}
			</div>

			{typing && data && !data.invalidRegex && data.files.length > 0 ? (
				<SearchResultsList
					files={data.files}
					collapsed={collapsed}
					onToggleFile={toggleFile}
					onOpen={onOpenHit}
					selected={selected}
					label="Search results"
				/>
			) : null}
		</div>
	);
}

/** Focus the search box and select what is in it. */
function selectBox(input: HTMLInputElement | null): void {
	if (!input) return;
	input.focus();
	input.select();
}

function OptionToggle({
	label,
	active,
	onClick,
	children,
}: {
	label: string;
	active: boolean;
	onClick: () => void;
	children: React.ReactNode;
}) {
	return (
		<SimpleTooltip label={label}>
			<button
				type="button"
				aria-label={label}
				aria-pressed={active}
				className={cn("files-search__toggle", active && "is-active")}
				onClick={onClick}
			>
				{children}
			</button>
		</SimpleTooltip>
	);
}

function GlobField({
	label,
	placeholder,
	value,
	onChange,
}: {
	label: string;
	placeholder: string;
	value: string;
	onChange: (value: string) => void;
}) {
	return (
		<label className="files-search__glob">
			<span className="files-search__glob-label">{label}</span>
			<input
				type="text"
				className="files-panel__search-input files-search__glob-input"
				placeholder={placeholder}
				spellCheck={false}
				autoComplete="off"
				value={value}
				onChange={(e) => onChange(e.target.value)}
			/>
		</label>
	);
}
