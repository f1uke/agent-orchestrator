import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, Check, EyeOff, Loader2, RefreshCw, Settings2, Trash2, X } from "lucide-react";
import type { WikiTaskRow, WikiTasks, WikiTasksSettings } from "../hooks/useWiki";
import { WikiTaskWriteError } from "../hooks/useWiki";
import { mergeHeldRows, partitionTasks, taskKey, type OwnerFilter } from "../lib/wiki-tasks";
import { fromTagAddsSomething, sourceLabel, splitFromTags, splitWikilinks } from "../lib/wiki-task-text";
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
 * grouped by the day they are due, one click to tick a row off in the note it
 * actually lives in, and one confirmed click to delete a row he is never going
 * to do.
 *
 * 🗝 Two properties this component exists to hold, both of which cost the
 * markdown version that came before it:
 *
 *  1. A tick sends the row's text back EXACTLY as it was drawn. The daemon
 *     writes only to a line whose full text still matches, so ticking the
 *     wrong row is impossible by construction and a mismatch is a visible,
 *     explained refusal rather than a silent write.
 *  2. A refresh NEVER discards a tick. Ticked rows are held in `pending` here,
 *     outside the query cache, WITH THEIR OWN COPY OF THE ROW, so a poll or a
 *     manual re-read replaces the rows underneath without touching what the
 *     reader just did - and without needing the re-read to be held off. A row
 *     leaves `pending` only when the daemon has answered.
 *
 *     That second half is the fix for a list that froze: holding the ROW, not
 *     just its state, is what lets the list re-read as often as it likes. The
 *     older shape held only the state, so it needed refetching suspended while
 *     a tick was in flight, and a tick that never settled suspended it forever.
 *
 * Ticking only ever TICKS. There is no un-tick here: this tab collects
 * unchecked rows, so a ticked one leaves the list and there is nothing left to
 * un-tick from — and the guarantee above does not extend to a `- [x]` line the
 * tab never showed anyone. The note itself is one click away for that.
 *
 * 🗝 DELETING is the same write with a consequence nothing here can walk back:
 * the line leaves the note and this app keeps no copy of it. Property (1) above
 * is what stops a delete landing on the wrong row; the confirm step below is
 * what stops it landing at all on a row nobody meant to touch.
 *
 * The confirm, and why it is a confirm rather than an undo. The two ways to put
 * something between a stray click and a destroyed line are a confirm before, or
 * an undo after. An undo would have to REWRITE the line back into a note that
 * may have changed since — and it would have to refuse when it has, which is
 * exactly the case where the reader needs it most: the agent that edits these
 * notes every night is the likeliest thing to change the note out from under an
 * undo. An undo that can fail is not a safety net, it is a second thing to be
 * disappointed by. Worse, an undo only helps a reader who NOTICED: this control
 * sits beside a checkbox that is clicked quickly, twenty-five rows at a time,
 * and the whole danger is a delete nobody saw happen. A confirm cannot be
 * missed, cannot fail, needs no window of time, and names the row it is about
 * to destroy while the reader can still read it.
 */

/**
 * A tick the daemon has not answered yet, or has refused.
 *
 * It carries `row` because the row it belongs to may be gone from the daemon's
 * next answer - a written tick means exactly that - and the reader still has to
 * see their own click land. `mergeHeldRows` puts it back on screen.
 */
type Pending =
	| { state: "saving"; action: RowAction; row: WikiTaskRow }
	| { state: "done"; action: RowAction; moved: boolean; row: WikiTaskRow }
	| { state: "failed"; action: RowAction; title: string; detail: string; row: WikiTaskRow };

/** Which write a `Pending` belongs to. Both go through the same states. */
type RowAction = "tick" | "delete";

export function WikiTasksPanel({
	tasks,
	settings,
	loading,
	error,
	onRefresh,
	onComplete,
	onDelete,
	onSaveSettings,
	savingSettings,
	settingsError,
	onOpenSource,
	onOpenWikilink,
}: {
	tasks: WikiTasks | undefined;
	settings: WikiTasksSettings | undefined;
	loading: boolean;
	error: Error | null;
	onRefresh: () => void;
	onComplete: (row: WikiTaskRow) => Promise<{ moved: boolean }>;
	/**
	 * Remove the row's line from its note. Reached only through the confirm
	 * below, and it does not come back.
	 */
	onDelete: (row: WikiTaskRow) => Promise<{ moved: boolean }>;
	onSaveSettings: (next: WikiTasksSettings) => Promise<unknown>;
	savingSettings: boolean;
	settingsError: string | null;
	/**
	 * Open the row's note AT the row. The line is a hint the note view checks
	 * against `raw` before it scrolls anywhere — see `lib/note/reveal.ts`.
	 */
	onOpenSource: (path: string, line: number, raw: string) => void;
	/** Open the note a `[[wikilink]]` in a row names, as the Notes tab does. */
	onOpenWikilink: (target: string) => void;
}) {
	const [ownerFilter, setOwnerFilter] = useState<OwnerFilter>(loadOwnerFilter);
	const [showHidden, setShowHidden] = useState<boolean>(loadShowHidden);
	const [collapsed, setCollapsed] = useState<Record<string, boolean>>(loadCollapsedGroups);
	const [configuring, setConfiguring] = useState(false);

	// 🗝 Ticks live HERE, not in the query cache, and that is the whole point:
	// the cache is replaced wholesale by every refetch, and a tick that lived
	// in it would be thrown away by a poll that happened to land mid-write.
	//
	// Keyed by the row's IDENTITY (`taskKey`: note + text) rather than by
	// `row.id`, which hashes the line number too. An edit above the row
	// renumbers it and gives it a new id, and a tick must not lose its place on
	// screen because somebody else's edit moved the row a line down.
	const [pending, setPending] = useState<Record<string, Pending>>({});
	/**
	 * The one row whose delete is armed, by `row.id`, or null.
	 *
	 * 🗝 ONE. Arming a second row disarms the first, so there is never a screen
	 * with two live "Delete" buttons on it, and a reader who walked away from a
	 * confirm cannot come back and hit a stale one two rows further down. It is
	 * cleared by Cancel, by Escape, by arming another row, and by the delete
	 * itself — never on a timer: a confirm that disarms itself turns a deliberate
	 * click into nothing happening, which is its own kind of lie.
	 *
	 * 🗝 By `row.id`, NOT by `taskKey`, and this is the one place the two must
	 * differ. `taskKey` is note + text, so two rows reading exactly alike share
	 * it — which is right for `pending`, where the daemon cannot tell them apart
	 * either and both honestly carry the same refusal, and wrong here: arming one
	 * lit up BOTH, putting two live "Delete" buttons on screen, which is exactly
	 * what "only one at a time" exists to prevent. `row.id` hashes the line as
	 * well, so it names one DRAWN row. Its cost is that an edit renumbering the
	 * row disarms the confirm — which is the right way for that to fail: the
	 * reader reads the row again and arms it again.
	 */
	const [confirming, setConfirming] = useState<string | null>(null);
	// A ref beside it so a click reads the current value without the callback
	// re-subscribing every render.
	const pendingRef = useRef(pending);
	pendingRef.current = pending;

	const served = useMemo(() => tasks?.tasks ?? [], [tasks]);
	// The rows the tab is still holding on screen: a tick being written, or one
	// the daemon refused and the reader has not read yet.
	const held = useMemo(() => Object.values(pending).map((p) => p.row), [pending]);
	const rows = useMemo(() => mergeHeldRows(served, held), [served, held]);
	const aliases = useMemo(() => tasks?.ownerAliases ?? [], [tasks]);
	const cutoff = tasks?.cutoff ?? "";
	// Read from the tab's own answer rather than from the settings query, so
	// the rule the list is drawn under and the rows it is drawn from always
	// came back together. It defaults to false, which fails OPEN: a tab still
	// loading shows too much for an instant, never too little.
	const requireCreated = tasks?.requireCreated === true;

	/*
	 * Two readings of the same rule, and they answer two different questions.
	 *
	 * `view` is what is DRAWN, held rows and all, so a tick keeps its place on
	 * screen while it settles. `truth` is what the VAULT says, so the counts and
	 * the hidden-rows notice describe the notes rather than the animation: a row
	 * that has been ticked is not an open task the moment the daemon says it was
	 * written, even though it is still on screen for another beat.
	 *
	 * With nothing pending - the whole time the reader is just reading - the two
	 * are the same object and the second pass never runs.
	 */
	const rule = useMemo(
		() => ({ ownerFilter, ownerAliases: aliases, cutoff, requireCreated, showHidden }),
		[ownerFilter, aliases, cutoff, requireCreated, showHidden],
	);
	const truth = useMemo(() => partitionTasks(served, rule), [served, rule]);
	const view = useMemo(() => (rows === served ? truth : partitionTasks(rows, rule)), [rows, served, truth, rule]);

	// A row that has been ticked and confirmed is gone from the list the moment
	// the daemon re-reads the vault. Until then it stays on screen, struck
	// through, so the click has somewhere to land — a row vanishing the instant
	// it is clicked reads as a bug even when it is correct.
	const settleTick = useCallback((key: string) => {
		setPending((current) => {
			if (!current[key]) return current;
			const next = { ...current };
			delete next[key];
			return next;
		});
	}, []);

	/**
	 * One row, one write. Ticking and deleting differ in what they ask the
	 * daemon and in nothing else here: the same key, the same held row, the same
	 * three states, the same rule about which click is worth sending.
	 */
	const write = useCallback(
		async (row: WikiTaskRow, action: RowAction, send: (row: WikiTaskRow) => Promise<{ moved: boolean }>) => {
			const key = taskKey(row);
			// A write already in flight is not clicked twice, and a row already
			// written has nothing left to do. A REFUSED one, though, is exactly
			// the row the reader is most likely to click again - they read the
			// reason, fixed the note, and want to try. Swallowing that click is
			// what made a failure look like a wedged list.
			const already = pendingRef.current[key];
			if (already && already.state !== "failed") return;
			setPending((current) => ({ ...current, [key]: { state: "saving", action, row } }));
			try {
				const result = await send(row);
				setPending((current) => ({ ...current, [key]: { state: "done", action, moved: result.moved, row } }));
			} catch (caught) {
				const failure =
					caught instanceof WikiTaskWriteError
						? caught.failure
						: {
								title: action === "delete" ? "This couldn’t be deleted." : "This couldn’t be ticked off.",
								detail: String(caught),
							};
				setPending((current) => ({
					...current,
					[key]: { state: "failed", action, title: failure.title, detail: failure.detail, row },
				}));
			}
		},
		[],
	);

	const tick = useCallback((row: WikiTaskRow) => write(row, "tick", onComplete), [write, onComplete]);

	/**
	 * 🗝 Only ever reached from the confirm step. Nothing else in this file
	 * calls it, and nothing else should: the click that opens the confirm and
	 * the click that destroys the line have to be two different clicks on two
	 * different targets.
	 */
	const drop = useCallback(
		(row: WikiTaskRow) => {
			setConfirming(null);
			return write(row, "delete", onDelete);
		},
		[write, onDelete],
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
					{loading && served.length === 0
						? "Reading the tasks…"
						: `${truth.visible} open${truth.hiddenByOwner > 0 ? ` · ${truth.hiddenByOwner} filtered out` : ""}`}
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
					{/*
					 * Always live. It used to be disabled while a tick was in
					 * flight, to stop a re-read racing the write - but the rows
					 * being ticked are now held on screen by the panel itself,
					 * so there is nothing left for a re-read to take away, and
					 * a tick that never came back took the reader's only way of
					 * refreshing the list down with it.
					 */}
					<button
						type="button"
						className="wiki-rail__action"
						aria-label="Re-read the tasks"
						title="Re-read the tasks"
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
			{(cutoff !== "" || requireCreated) && (truth.hiddenByCutoff > 0 || truth.undated > 0) && (
				<div className="wiki-tasks__cutoff">
					<EyeOff aria-hidden="true" className="wiki-tasks__cutoff-icon" />
					<span>
						{truth.hiddenByCutoff > 0 && (
							<>
								{truth.hiddenByCutoff} row{truth.hiddenByCutoff === 1 ? " " : "s "}
								before {cutoff} {truth.hiddenByCutoff === 1 ? "is" : "are"} {showHidden ? "shown" : "hidden"}.{" "}
								{truth.hiddenByCutoff === 1 ? "It is" : "They are"} still in your notes.{" "}
							</>
						)}
						{/*
						 * The same count, said the way it actually behaves. Under
						 * `requireCreated` an untagged row is HIDDEN, and the
						 * sentence has to say that plainly — the reader turned the
						 * rule on, but they still get told what it cost.
						 */}
						{truth.undated > 0 &&
							(truth.undatedHidden ? (
								<>
									{truth.undated} row{truth.undated === 1 ? " carries" : "s carry"} no <code>created:</code> date, so{" "}
									{truth.undated === 1 ? "it is" : "they are"} {showHidden ? "shown" : "hidden"}.{" "}
									{truth.undated === 1 ? "It is" : "They are"} still in your notes.
								</>
							) : (
								<>
									{truth.undated} row{truth.undated === 1 ? " carries" : "s carry"} no date of{" "}
									{truth.undated === 1 ? "its" : "their"} own, so the cutoff leaves{" "}
									{truth.undated === 1 ? "it" : "them"} here.
								</>
							))}
					</span>
					{(truth.hiddenByCutoff > 0 || truth.undatedHidden) && (
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
					<div className="wiki-rail__empty">Every row is filtered out. {truth.hiddenByOwner > 0 && "Try “All”."}</div>
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
										pending={pending[taskKey(row)]}
										confirming={confirming === row.id}
										onTick={() => void tick(row)}
										onArmDelete={() => setConfirming(row.id)}
										onCancelDelete={() => setConfirming(null)}
										onConfirmDelete={() => void drop(row)}
										onDismiss={() => settleTick(taskKey(row))}
										onOpenSource={() => onOpenSource(row.path, row.line, row.raw)}
										onOpenWikilink={onOpenWikilink}
									/>
								))}
						</div>
					);
				})}
				{tasks?.truncated && (
					<div className="wiki-rail__empty">
						Only the first {served.length} rows are listed — this folder holds more than a task list.
					</div>
				)}
			</div>
		</>
	);
}

function TaskRow({
	row,
	pending,
	confirming,
	onTick,
	onArmDelete,
	onCancelDelete,
	onConfirmDelete,
	onDismiss,
	onOpenSource,
	onOpenWikilink,
}: {
	row: WikiTaskRow;
	pending: Pending | undefined;
	/** Whether this row's delete is armed — see `confirming` in the panel. */
	confirming: boolean;
	onTick: () => void;
	onArmDelete: () => void;
	onCancelDelete: () => void;
	onConfirmDelete: () => void;
	onDismiss: () => void;
	onOpenSource: () => void;
	onOpenWikilink: (target: string) => void;
}) {
	// A settled write clears itself after a beat, so the row does not sit
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
	const dropping = pending?.action === "delete";
	// Nothing to arm on a row that is already being written, and nothing to arm
	// on a row that has just left. A refused one can be armed again.
	const armable = !saving && !done;

	/*
	 * The TASK is the row, so `(from: …)` is lifted out of the sentence before
	 * anything is drawn — it is provenance somebody appended, not part of what
	 * is to be done. It comes back as a chip on the meta line only when it
	 * still says something the row does not already say: a tag reading
	 * `My active items` directly above a source line reading `· My active
	 * items` is noise, and so is one whose only content is the date that put
	 * the row under its day heading.
	 *
	 * `row.raw` — the byte-exact key a tick is written by — is untouched by any
	 * of this. Only where the reader sees the words changes.
	 */
	const { text, tags } = splitFromTags(row.text);
	const section = row.section ?? "";
	const from = tags.filter((tag) => fromTagAddsSomething(tag, section, row.subsection ?? ""));
	const where = sourceLabel(row.path) + (section ? ` · ${section}` : "");

	return (
		<div
			className={`wiki-tasks__row${done ? " is-done" : ""}${failed ? " is-failed" : ""}${
				dropping ? " is-dropping" : ""
			}${confirming ? " is-confirming" : ""}`}
			// Escape gets out of an armed delete from anywhere in the row,
			// including from the Delete button itself. A confirm the keyboard
			// cannot back out of is a trap.
			onKeyDown={(event) => {
				if (event.key === "Escape" && confirming) {
					event.stopPropagation();
					onCancelDelete();
				}
			}}
		>
			<button
				type="button"
				className="wiki-tasks__box"
				aria-label={done ? `Ticked off: ${row.text}` : `Tick off: ${row.text}`}
				disabled={saving || done}
				onClick={onTick}
			>
				{saving && !dropping ? (
					<Loader2 aria-hidden="true" className="wiki-tasks__spin" />
				) : done && !dropping ? (
					<Check aria-hidden="true" />
				) : null}
			</button>
			<div className="wiki-tasks__body">
				{/*
				 * Vault content is untrusted, and this is still not markup:
				 * `splitWikilinks` hands back TOKENS and each one becomes an
				 * element here, so a row containing angle brackets shows angle
				 * brackets. Only `[[…]]` becomes a link, and it borrows the Notes
				 * tab's own class rather than growing a second treatment for the
				 * same thing.
				 */}
				<span className="wiki-tasks__text">
					{splitWikilinks(text).map((part, index) =>
						part.kind === "text" ? (
							<span key={index}>{part.value}</span>
						) : (
							<button
								key={index}
								type="button"
								className="note-prose__wikilink note-prose__wikilink--active"
								title={part.anchor ? `${part.target} › ${part.anchor}` : part.target}
								onClick={() => onOpenWikilink(part.target)}
							>
								{part.label}
							</button>
						),
					)}
				</span>
				<span className="wiki-tasks__meta">
					{row.owner && <span className="wiki-tasks__owner">@{row.owner}</span>}
					{/*
					 * The address, and the quietest thing in the row. It goes to the
					 * row's own LINE, not merely the file — a reader who clicks it is
					 * asking "what does this sit next to", and a note scrolled to the
					 * top does not answer that.
					 */}
					<button type="button" className="wiki-tasks__where" onClick={onOpenSource} title={`${row.path}:${row.line}`}>
						{where}
					</button>
					{from.map((tag) => (
						<span key={tag} className="wiki-tasks__from" title={`from: ${tag}`}>
							<span className="wiki-tasks__from-label">from</span> {tag}
						</span>
					))}
				</span>
				{pending?.state === "done" &&
					(pending.action === "delete" ? (
						<span className="wiki-tasks__note">
							{pending.moved ? "The row had moved in the note — deleted where it was." : "Deleted from the note."}
						</span>
					) : (
						pending.moved && (
							<span className="wiki-tasks__note">The row had moved in the note — ticked where it is now.</span>
						)
					))}
				{/*
				 * The confirm. It says which note the line is coming out of and
				 * that nothing here can put it back, because those are the two
				 * facts a reader needs and cannot see from the row alone. The
				 * row's own text is still the loudest thing above it: this is
				 * 10.5px, the same quiet strip a refusal uses.
				 */}
				{confirming && (
					<span className="wiki-tasks__confirm" role="group" aria-label={`Confirm deleting: ${row.text}`}>
						<AlertTriangle aria-hidden="true" />
						<span>
							Delete this line from <strong>{sourceLabel(row.path)}</strong>? It cannot be undone here.
						</span>
						{/*
						 * Its accessible name is the word on it. The control that
						 * armed this says "Delete this row from the note: …", so
						 * the two are never the same target by name either.
						 */}
						<button type="button" className="wiki-tasks__confirm-go" onClick={onConfirmDelete}>
							Delete
						</button>
						<button type="button" className="wiki-tasks__confirm-stop" onClick={onCancelDelete}>
							Cancel
						</button>
					</span>
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
			{/*
			 * The one control here that DESTROYS something, so it is the one
			 * kept out of the way: at the far end of the row from the checkbox,
			 * invisible and unclickable until the row is hovered or something
			 * in it has focus. A resting list shows twenty-five sentences and
			 * twenty-five checkboxes, and no rank of delete buttons at all.
			 *
			 * It ARMS the confirm. It never deletes.
			 */}
			{armable && (
				<button
					type="button"
					className="wiki-tasks__drop"
					aria-label={`Delete this row from the note: ${row.text}`}
					title="Delete this row from the note"
					aria-expanded={confirming}
					onClick={confirming ? onCancelDelete : onArmDelete}
				>
					<Trash2 aria-hidden="true" />
				</button>
			)}
		</div>
	);
}
