import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, Check, EyeOff, Loader2, RefreshCw, Settings2, X } from "lucide-react";
import type { WikiTaskRow, WikiTasks, WikiTasksSettings } from "../hooks/useWiki";
import { WikiTaskTickError } from "../hooks/useWiki";
import { partitionTasks, type OwnerFilter } from "../lib/wiki-tasks";
import {
	loadCollapsedGroups,
	loadOwnerFilter,
	loadShowHidden,
	saveCollapsedGroups,
	saveOwnerFilter,
	saveShowHidden,
} from "../lib/wiki-tree-state";
import { WikiTasksSettingsForm } from "./WikiTasksSettingsForm";

/**
 * The Tasks tab: the unchecked rows in the configured corners of the vault,
 * grouped by the day they are due, and one click to tick a row off in the note
 * it actually lives in.
 *
 * 🗝 Two properties this component exists to hold, both of which cost the
 * markdown version that came before it:
 *
 *  1. A tick sends the row's text back EXACTLY as it was drawn. The daemon
 *     writes only to a line whose full text still matches, so ticking the
 *     wrong row is impossible by construction and a mismatch is a visible,
 *     explained refusal rather than a silent write.
 *  2. A refresh NEVER discards a tick. Ticked rows are held in `pending` here,
 *     outside the query cache, so a poll or a manual re-read replaces the rows
 *     underneath without touching what the reader just did. A row leaves
 *     `pending` only when the daemon has answered.
 *
 * Ticking only ever TICKS. There is no un-tick here: this tab collects
 * unchecked rows, so a ticked one leaves the list and there is nothing left to
 * un-tick from — and the guarantee above does not extend to a `- [x]` line the
 * tab never showed anyone. The note itself is one click away for that.
 */

/** A tick the daemon has not answered yet, or has refused. */
type Pending =
	{ state: "saving" } | { state: "done"; moved: boolean } | { state: "failed"; title: string; detail: string };

export function WikiTasksPanel({
	tasks,
	settings,
	loading,
	error,
	onRefresh,
	onComplete,
	onSaveSettings,
	savingSettings,
	settingsError,
	onOpenNote,
}: {
	tasks: WikiTasks | undefined;
	settings: WikiTasksSettings | undefined;
	loading: boolean;
	error: Error | null;
	onRefresh: () => void;
	onComplete: (row: WikiTaskRow) => Promise<{ moved: boolean }>;
	onSaveSettings: (next: WikiTasksSettings) => Promise<unknown>;
	savingSettings: boolean;
	settingsError: string | null;
	/** Opening the row's note is the way back out of every refusal. */
	onOpenNote: (path: string) => void;
}) {
	const [ownerFilter, setOwnerFilter] = useState<OwnerFilter>(loadOwnerFilter);
	const [showHidden, setShowHidden] = useState<boolean>(loadShowHidden);
	const [collapsed, setCollapsed] = useState<Record<string, boolean>>(loadCollapsedGroups);
	const [configuring, setConfiguring] = useState(false);

	// 🗝 Ticks live HERE, not in the query cache, and that is the whole point:
	// the cache is replaced wholesale by every refetch, and a tick that lived
	// in it would be thrown away by a poll that happened to land mid-write.
	const [pending, setPending] = useState<Record<string, Pending>>({});
	// A ref beside it so the refetch guard reads the current value without
	// re-subscribing every render.
	const pendingRef = useRef(pending);
	pendingRef.current = pending;

	const rows = useMemo(() => tasks?.tasks ?? [], [tasks]);
	const aliases = useMemo(() => tasks?.ownerAliases ?? [], [tasks]);
	const cutoff = tasks?.cutoff ?? "";

	const view = useMemo(
		() => partitionTasks(rows, { ownerFilter, ownerAliases: aliases, cutoff, showHidden }),
		[rows, ownerFilter, aliases, cutoff, showHidden],
	);

	// A row that has been ticked and confirmed is gone from the list the moment
	// the daemon re-reads the vault. Until then it stays on screen, struck
	// through, so the click has somewhere to land — a row vanishing the instant
	// it is clicked reads as a bug even when it is correct.
	const settleTick = useCallback((id: string) => {
		setPending((current) => {
			if (!current[id]) return current;
			const next = { ...current };
			delete next[id];
			return next;
		});
	}, []);

	const tick = useCallback(
		async (row: WikiTaskRow) => {
			if (pendingRef.current[row.id]) return;
			setPending((current) => ({ ...current, [row.id]: { state: "saving" } }));
			try {
				const result = await onComplete(row);
				setPending((current) => ({ ...current, [row.id]: { state: "done", moved: result.moved } }));
			} catch (caught) {
				const failure =
					caught instanceof WikiTaskTickError
						? caught.failure
						: { title: "This couldn’t be ticked off.", detail: String(caught) };
				setPending((current) => ({
					...current,
					[row.id]: { state: "failed", title: failure.title, detail: failure.detail },
				}));
			}
		},
		[onComplete],
	);

	const toggleGroup = useCallback((key: string) => {
		setCollapsed((current) => {
			const next = { ...current, [key]: !current[key] };
			saveCollapsedGroups(next);
			return next;
		});
	}, []);

	const chooseFilter = useCallback((next: OwnerFilter) => {
		setOwnerFilter(next);
		saveOwnerFilter(next);
	}, []);

	const toggleHidden = useCallback(() => {
		setShowHidden((current) => {
			saveShowHidden(!current);
			return !current;
		});
	}, []);

	// A tick that is still saving must not be raced by a re-read: the refresh
	// button is disabled while one is in flight, which is the one moment where
	// a re-read could arrive between the daemon's write and this list learning
	// about it.
	const saving = Object.values(pending).some((p) => p.state === "saving");

	if (configuring || (tasks && !tasks.configured && !loading)) {
		return (
			<WikiTasksSettingsForm
				settings={settings}
				saving={savingSettings}
				error={settingsError}
				// With nothing configured there is no list to go back to, so the
				// form is the tab rather than a panel over it.
				onCancel={tasks?.configured ? () => setConfiguring(false) : undefined}
				onSave={async (next) => {
					await onSaveSettings(next);
					setConfiguring(false);
				}}
			/>
		);
	}

	return (
		<>
			<div className="wiki-rail__summary">
				<span className="wiki-rail__count">
					{loading && rows.length === 0
						? "Reading the tasks…"
						: `${view.visible} open${view.hiddenByOwner > 0 ? ` · ${view.hiddenByOwner} filtered out` : ""}`}
				</span>
				<div className="wiki-rail__actions">
					<button
						type="button"
						className="wiki-rail__action"
						aria-label="Choose which tasks are read"
						title="Choose which tasks are read"
						onClick={() => setConfiguring(true)}
					>
						<Settings2 aria-hidden="true" />
					</button>
					<button
						type="button"
						className="wiki-rail__action"
						aria-label="Re-read the tasks"
						title={saving ? "Waiting for a tick to be written…" : "Re-read the tasks"}
						disabled={saving}
						onClick={onRefresh}
					>
						<RefreshCw aria-hidden="true" />
					</button>
				</div>
			</div>

			<div className="wiki-tasks__filters" role="group" aria-label="Whose tasks to show">
				{(["all", "mine", "others"] as const).map((option) => (
					<button
						key={option}
						type="button"
						className={`wiki-tasks__filter${ownerFilter === option ? " is-active" : ""}`}
						aria-pressed={ownerFilter === option}
						onClick={() => chooseFilter(option)}
					>
						{option === "all" ? "All" : option === "mine" ? "Mine" : "Others"}
					</button>
				))}
			</div>

			{/*
			 * The cutoff always announces itself, and it announces BOTH of its
			 * outcomes. A backlog that quietly went missing is the failure the
			 * first sentence exists to prevent; the second is the same promise
			 * pointed the other way — rows with no date of their own are kept,
			 * so the reader learns that the cutoff has an edge rather than
			 * wondering why the list never empties.
			 */}
			{cutoff !== "" && (view.hiddenByCutoff > 0 || view.undated > 0) && (
				<div className="wiki-tasks__cutoff">
					<EyeOff aria-hidden="true" className="wiki-tasks__cutoff-icon" />
					<span>
						{view.hiddenByCutoff > 0 && (
							<>
								{view.hiddenByCutoff} row{view.hiddenByCutoff === 1 ? " " : "s "}
								before {cutoff} {view.hiddenByCutoff === 1 ? "is" : "are"} {showHidden ? "shown" : "hidden"}.{" "}
								{view.hiddenByCutoff === 1 ? "It is" : "They are"} still in your notes.{" "}
							</>
						)}
						{view.undated > 0 && (
							<>
								{view.undated} row{view.undated === 1 ? " carries" : "s carry"} no date of{" "}
								{view.undated === 1 ? "its" : "their"} own, so the cutoff leaves{" "}
								{view.undated === 1 ? "it" : "them"} here.
							</>
						)}
					</span>
					{view.hiddenByCutoff > 0 && (
						<button type="button" className="wiki-tasks__cutoff-toggle" onClick={toggleHidden}>
							{showHidden ? "Hide them" : "Show them"}
						</button>
					)}
				</div>
			)}

			<div className="wiki-rail__tree">
				{error && <div className="wiki-rail__empty">{error.message}</div>}
				{!error && !loading && rows.length === 0 && (
					<div className="wiki-rail__empty">
						Nothing unchecked under {(tasks?.folders ?? []).join(", ") || "the configured folders"}.
					</div>
				)}
				{!error && rows.length > 0 && view.visible === 0 && (
					<div className="wiki-rail__empty">Every row is filtered out. {view.hiddenByOwner > 0 && "Try “All”."}</div>
				)}
				{view.groups.map((group) => {
					const shut = collapsed[group.key] === true;
					return (
						<div key={group.key} className="wiki-tasks__group">
							<button
								type="button"
								className="wiki-tasks__group-head"
								aria-expanded={!shut}
								onClick={() => toggleGroup(group.key)}
							>
								<span className={`wiki-tasks__group-label${group.key === "overdue" ? " is-overdue" : ""}`}>
									{group.label}
								</span>
								<span className="wiki-rail__age">{group.rows.length}</span>
							</button>
							{!shut &&
								group.rows.map((row) => (
									<TaskRow
										key={row.id}
										row={row}
										pending={pending[row.id]}
										onTick={() => void tick(row)}
										onDismiss={() => settleTick(row.id)}
										onOpenNote={() => onOpenNote(row.path)}
									/>
								))}
						</div>
					);
				})}
				{tasks?.truncated && (
					<div className="wiki-rail__empty">
						Only the first {rows.length} rows are listed — this folder holds more than a task list.
					</div>
				)}
			</div>
		</>
	);
}

function TaskRow({
	row,
	pending,
	onTick,
	onDismiss,
	onOpenNote,
}: {
	row: WikiTaskRow;
	pending: Pending | undefined;
	onTick: () => void;
	onDismiss: () => void;
	onOpenNote: () => void;
}) {
	// A confirmed tick clears itself after a beat, so the row does not sit
	// struck through until the next poll. A refusal does NOT: it stays until
	// the reader has read it.
	useEffect(() => {
		if (pending?.state !== "done") return;
		const timer = window.setTimeout(onDismiss, 2_400);
		return () => window.clearTimeout(timer);
	}, [pending, onDismiss]);

	const done = pending?.state === "done";
	const saving = pending?.state === "saving";
	const failed = pending?.state === "failed";

	return (
		<div className={`wiki-tasks__row${done ? " is-done" : ""}${failed ? " is-failed" : ""}`}>
			<button
				type="button"
				className="wiki-tasks__box"
				aria-label={done ? `Ticked off: ${row.text}` : `Tick off: ${row.text}`}
				disabled={saving || done}
				onClick={onTick}
			>
				{saving ? (
					<Loader2 aria-hidden="true" className="wiki-tasks__spin" />
				) : done ? (
					<Check aria-hidden="true" />
				) : null}
			</button>
			<div className="wiki-tasks__body">
				{/*
				 * Vault content is untrusted, and this is a plain text node —
				 * no markdown pass, no HTML, nothing evaluated. A row that
				 * contains angle brackets shows angle brackets.
				 */}
				<span className="wiki-tasks__text">{row.text}</span>
				<span className="wiki-tasks__meta">
					{row.owner && <span className="wiki-tasks__owner">@{row.owner}</span>}
					<button type="button" className="wiki-tasks__where" onClick={onOpenNote} title={row.path}>
						{noteLabel(row.path)}
						{row.section ? ` · ${row.section}` : ""}
					</button>
				</span>
				{pending?.state === "done" && pending.moved && (
					<span className="wiki-tasks__note">The row had moved in the note — ticked where it is now.</span>
				)}
				{failed && (
					<span className="wiki-tasks__error">
						<AlertTriangle aria-hidden="true" />
						<span>
							<strong>{pending.title}</strong> {pending.detail}
						</span>
						<button type="button" className="wiki-tasks__dismiss" aria-label="Dismiss" onClick={onDismiss}>
							<X aria-hidden="true" />
						</button>
					</span>
				)}
			</div>
		</div>
	);
}

/**
 * Where a row lives, short enough for the meta line.
 *
 * The note's own folder is included, because a vault that keeps one task note
 * per project names them all the same thing — every row would otherwise read
 * `_tasks` and the column would say nothing. The full path is on the button's
 * title, so this is a label rather than the address.
 */
export function noteLabel(path: string): string {
	const segments = path.split("/");
	const base = (segments.pop() ?? path).replace(/\.md$/i, "");
	const folder = segments.pop();
	return folder ? `${folder}/${base}` : base;
}
