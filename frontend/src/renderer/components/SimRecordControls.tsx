import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Circle, Film, Pencil, Square, Trash2 } from "lucide-react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import {
	simRecordingQueryKey,
	useSimFlowActions,
	useSimFlows,
	useSimRecording,
	type SimFlow,
	type SimRecordingState,
} from "../hooks/useSimRecording";
import { cn } from "../lib/utils";
import { CopyButton } from "./CopyButton";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { SimpleTooltip } from "./ui/tooltip";

/**
 * Recording, from the tab where the person who benefits from it is already
 * standing.
 *
 * Everything here already worked from a terminal. What did not work was doing
 * it while dragging on the screen with your own hand - which is exactly who
 * recording is for. So this is a control surface over the same daemon routes
 * `ao sim record start/status/stop` drive; there is no second recording
 * mechanism, and a recording started in a terminal is the one shown here.
 *
 * ## The two invariants this file exists to hold
 *
 * 🗝 **The counter must be able to embarrass us.** The worst bug in the slice
 * that built the recorder was that it was never wired into the daemon at all:
 * start succeeded, status sat at 0 forever, and stop wrote a header with no
 * steps under it - found out later, from an empty file, by somebody who had
 * already replayed the whole path by hand. A count on screen that visibly fails
 * to move turns that into a one-second discovery. It is a requirement, not
 * decoration, and it is why the number is polled from the daemon rather than
 * incremented locally when this panel happens to send a gesture: a locally
 * counted number would keep counting a recorder that had stopped listening.
 *
 * ⚠ **Nothing here may change the size of the device screen.** The screen was
 * measured at 84.6% of the pane, and "the screen is too small" is the complaint
 * that produced that number. So the list is a popover - a portal, a different
 * DOM subtree entirely - and cannot push the screen no matter how many
 * recordings it holds. And every number that changes while somebody is dragging
 * sits in a fixed-width slot, because this row wraps at the rail's narrowest and
 * a control that grows by a character is a control that can add a line to the
 * row and take that line out of the screen.
 */

/**
 * What is true before the daemon has answered: nothing is being recorded. It is
 * deliberately the same shape as a real answer, so no code path has to ask
 * whether it is looking at a loading state.
 */
const NOTHING_RECORDED: SimRecordingState = { found: false, open: false, holder: "", name: "", stepCount: 0 };

/** Above this, the count is shown as "99+" rather than allowed to widen. */
const MAX_SHOWN_COUNT = 99;

function shortCount(n: number): string {
	return n > MAX_SHOWN_COUNT ? `${MAX_SHOWN_COUNT}+` : String(n);
}

export type StopSummary = {
	fileName: string;
	path: string;
	steps: number;
	review: number;
};

export function SimRecordControls({
	sessionId,
	udid,
	watching,
	heldByThisSession,
	deviceChosen,
	onProblem,
	onStopped,
}: {
	sessionId: string;
	udid: string | null;
	watching: boolean;
	heldByThisSession: boolean;
	deviceChosen: boolean;
	onProblem: (message: string) => void;
	onStopped: (summary: StopSummary) => void;
}) {
	const queryClient = useQueryClient();
	const recording = useSimRecording(sessionId, udid, watching);
	const state: SimRecordingState = recording.data ?? NOTHING_RECORDED;
	const flows = useSimFlows(sessionId, watching);

	const refreshRecording = () => {
		void queryClient.invalidateQueries({ queryKey: simRecordingQueryKey(udid) });
	};

	const start = useMutation({
		mutationFn: async () => {
			if (!udid) throw new Error("No simulator is selected");
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/sim-recordings/{udid}", {
				params: { path: { sessionId, udid } },
				body: {},
			});
			if (error) throw error;
		},
		onSuccess: refreshRecording,
		onError: (error) => onProblem(apiErrorMessage(error, "Could not start recording")),
		onSettled: refreshRecording,
	});

	const stop = useMutation({
		mutationFn: async (): Promise<StopSummary | null> => {
			if (!udid) throw new Error("No simulator is selected");
			const { data, error } = await apiClient.DELETE("/api/v1/sessions/{sessionId}/sim-recordings/{udid}", {
				params: { path: { sessionId, udid }, query: {} },
			});
			if (error) throw error;
			const flow = data?.flow;
			if (!flow) return null;
			return { fileName: flow.fileName, path: flow.path, steps: flow.steps, review: flow.review };
		},
		onSuccess: (summary) => {
			void flows.refetch();
			if (summary) onStopped(summary);
		},
		onError: (error) => onProblem(apiErrorMessage(error, "Could not stop recording")),
		onSettled: refreshRecording,
	});

	// Who owns the recording decides what this control may do. A recording is
	// per device, so one this session did not start is still shown - AO would
	// rather say "somebody else is recording this" than pretend nothing is.
	const heldByOtherSession = state.open && state.holder !== "" && state.holder !== sessionId;
	const busy = start.isPending || stop.isPending;

	let disabledReason = "";
	if (!deviceChosen) disabledReason = "Choose a simulator first.";
	else if (heldByOtherSession) disabledReason = `@${state.holder} is recording this device.`;
	else if (!heldByThisSession) {
		disabledReason =
			"Recording needs this session to hold the device. Claim it first - the same lease `ao sim tap` takes.";
	}

	const recordingOpenHere = state.open && !heldByOtherSession;

	return (
		<div
			className="flex items-center gap-0.5 rounded-full border border-border bg-raised p-1"
			data-testid="sim-record-group"
		>
			<RecordButton
				busy={busy}
				disabledReason={disabledReason}
				onToggle={() => (recordingOpenHere ? stop.mutate() : start.mutate())}
				recording={recordingOpenHere}
				stepCount={state.stepCount}
			/>
			<RecordingsPopover
				count={flows.data?.length ?? 0}
				flows={flows.data ?? []}
				loading={flows.isPending && watching}
				onProblem={onProblem}
				sessionId={sessionId}
			/>
		</div>
	);
}

/**
 * The record toggle, and the live count.
 *
 * Recording is a MODE, and a mode nobody can see is a trap. Three separate
 * channels say it is on, because colour alone is not one a colour-blind reader
 * or a screen reader has: the glyph becomes a stop square, the label becomes a
 * number that is visibly climbing, and the ground fills.
 *
 * ⚠ There is deliberately no pulse. The app's own `status-pulse` dips to
 * opacity 0.35, which measures 2.07:1 on dark and 1.65:1 on light at its
 * faintest - unreadable at the dim end of every cycle - and a thing blinking
 * once every 1.8s in a panel somebody is trying to work in pulls the eye off
 * the screen they are watching. A number that changes when they touch the
 * device is a stronger signal than a blink, and it is honest: it moves because
 * something was captured. Nothing here animates, so there is nothing for
 * prefers-reduced-motion to suppress.
 */
function RecordButton({
	busy,
	disabledReason,
	onToggle,
	recording,
	stepCount,
}: {
	busy: boolean;
	disabledReason: string;
	onToggle: () => void;
	recording: boolean;
	stepCount: number;
}) {
	const disabled = disabledReason !== "" || busy;
	// The reason lives in the accessible NAME, not only in the tooltip. A
	// disabled button takes no focus and fires no pointer events, so a tooltip
	// on it is unreachable by keyboard and easy to miss with a mouse - which
	// makes "it does nothing and I cannot find out why" the actual experience.
	// The name still says what the control does first.
	const label = disabledReason
		? `Start recording - unavailable: ${disabledReason}`
		: recording
			? `Stop recording - ${stepCount} step${stepCount === 1 ? "" : "s"} captured`
			: "Start recording";
	const tooltip =
		disabledReason ||
		(recording
			? "Stop recording and write the Maestro flow. Everything you drag on the screen is being captured."
			: "Record what you do on this device as a Maestro flow, the same recording `ao sim record start` opens.");

	return (
		<SimpleTooltip label={<span className="block max-w-[240px]">{tooltip}</span>}>
			{/* The span keeps the tooltip working while the button is disabled:
			    a disabled button fires no pointer events, and the reason it is
			    disabled is exactly what somebody needs to read. */}
			<span className="flex">
				<button
					aria-label={label}
					aria-pressed={recording}
					className={cn(
						"flex h-7 items-center gap-1.5 rounded-full px-2 text-[11px] font-medium transition-colors",
						"disabled:opacity-40",
						recording
							? // Measured, not eyeballed. A 15% wash read at 1.37:1 against
								// the pane on dark - present if you already knew to look for
								// it, which is not what "obvious at a glance" means. The ring
								// is inset so turning it on cannot move anything, and it is
								// the channel that survives at a glance; the label and glyph
								// keep their own 5.20:1 / 7.10:1 against this ground.
								"bg-error/25 text-error ring-1 ring-error/50 ring-inset"
							: "text-muted-foreground hover:bg-overlay hover:text-foreground disabled:hover:bg-transparent",
					)}
					data-recording={recording ? "true" : "false"}
					data-testid="sim-record-toggle"
					disabled={disabled}
					onClick={onToggle}
					type="button"
				>
					{recording ? (
						<Square aria-hidden className="size-3 fill-current" />
					) : (
						<Circle aria-hidden className="size-3 fill-current" />
					)}
					{/* ⚠ Fixed width and tabular figures. This is the number that
					    changes while a finger is on the screen, and 9 -> 10 -> 100
					    must not nudge its neighbours - at the rail's narrowest this
					    row wraps, and one nudged character can cost the device a
					    whole line of height. */}
					<span
						className="w-[3.25ch] text-center font-mono tabular-nums"
						data-testid="sim-record-count"
						// The count is already in the button's accessible name, so
						// announcing it a second time on every captured step would
						// make a reader unusable while somebody drags.
						aria-hidden
					>
						{recording ? shortCount(stepCount) : "rec"}
					</span>
				</button>
			</span>
		</SimpleTooltip>
	);
}

/**
 * What has been recorded, and what a human does with it next.
 *
 * ⚠ A POPOVER, on purpose. The requirement is that the device screen's
 * dimensions are identical with zero recordings, with one, and with fifty - and
 * the only way to promise that rather than tune for it is for the list to live
 * outside the pane's layout entirely. Radix portals this to the body, so no
 * amount of content in it can reach the screen. Growth inside it is bounded by
 * its own max height and its own scrollbar.
 *
 * The count sits on the trigger, always visible, because "how many attempts do
 * I have at this path" is the question a person recording the same flow for the
 * fourth time is actually asking.
 */
function RecordingsPopover({
	count,
	flows,
	loading,
	onProblem,
	sessionId,
}: {
	count: number;
	flows: SimFlow[];
	loading: boolean;
	onProblem: (message: string) => void;
	sessionId: string;
}) {
	const actions = useSimFlowActions(sessionId, onProblem);
	return (
		<Popover>
			<SimpleTooltip label="Recordings this session has made">
				<PopoverTrigger asChild>
					<button
						aria-label={`Recordings - ${count} in this session`}
						className="flex h-7 items-center gap-1.5 rounded-full px-2 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-overlay hover:text-foreground"
						data-testid="sim-recordings-trigger"
						type="button"
					>
						<Film aria-hidden className="size-3.5" />
						{/* Fixed width for the same reason the step count has one. */}
						<span className="w-[2.5ch] text-center font-mono tabular-nums" data-testid="sim-recordings-count">
							{loading ? "·" : shortCount(count)}
						</span>
					</button>
				</PopoverTrigger>
			</SimpleTooltip>
			<PopoverContent
				align="center"
				// Narrower than the 280px rail floor it has to survive, and never
				// wider than the window at the 960px minimum.
				className="w-[min(300px,calc(100vw-1.5rem))] p-0"
				data-testid="sim-recordings-popover"
				side="top"
			>
				<div className="border-b border-border px-3 py-2 text-[11px] font-medium text-muted-foreground">
					{count === 0 ? "No recordings yet" : `${count} recording${count === 1 ? "" : "s"} in this session`}
				</div>
				{/* Bounded, with its own scroll. Fifty recordings are fifty rows
				    inside this box and nothing outside it moves. */}
				<div className="max-h-[220px] overflow-y-auto" data-testid="sim-recordings-list">
					{flows.length === 0 ? (
						<p className="px-3 py-3 text-[11px] leading-snug text-muted-foreground">
							Press record, drive the device by hand, then stop. The flow lands in this session&rsquo;s own directory,
							outside every repository.
						</p>
					) : (
						<ul>
							{flows.map((flow) => (
								<FlowRow
									busy={actions.rename.isPending || actions.remove.isPending}
									flow={flow}
									key={flow.fileName}
									onDelete={() => actions.remove.mutate(flow.fileName)}
									onRename={(name) => actions.rename.mutate({ fileName: flow.fileName, name })}
								/>
							))}
						</ul>
					)}
				</div>
			</PopoverContent>
		</Popover>
	);
}

/**
 * One recording: what it is called, when, how big, and how much of it a human
 * has to check.
 *
 * The path and the bare name are separate copy buttons on purpose. They are
 * different jobs: the path is what a worker can act on (`ao sim flow run
 * <path>`), the name is what a person writes in a sentence about it. One
 * control that copied "the identifier" would be right for neither.
 *
 * Nothing here shows what the recording CONTAINS. A list exists to find a file.
 */
function FlowRow({
	busy,
	flow,
	onDelete,
	onRename,
}: {
	busy: boolean;
	flow: SimFlow;
	onDelete: () => void;
	onRename: (name: string) => void;
}) {
	const [mode, setMode] = useState<"idle" | "renaming" | "confirming">("idle");
	const title = flow.name || "(unnamed)";

	if (mode === "confirming") {
		return (
			<li className="border-b border-border px-3 py-2 last:border-b-0">
				{/* Named, because deleting the wrong one costs replaying a whole
				    path by hand. */}
				<p className="text-[11px] leading-snug text-foreground">
					Delete <span className="font-mono break-all">{flow.fileName}</span>? You would have to record it again.
				</p>
				<div className="mt-2 flex justify-end gap-2">
					<button
						className="rounded-md px-2 py-1 text-[11px] text-muted-foreground hover:text-foreground"
						disabled={busy}
						onClick={() => setMode("idle")}
						type="button"
					>
						Cancel
					</button>
					<button
						className="rounded-md bg-error/15 px-2 py-1 text-[11px] font-medium text-error hover:bg-error/25 disabled:opacity-40"
						disabled={busy}
						onClick={() => {
							setMode("idle");
							onDelete();
						}}
						type="button"
					>
						Delete
					</button>
				</div>
			</li>
		);
	}

	return (
		<li className="group/flow border-b border-border px-3 py-2 last:border-b-0">
			{mode === "renaming" ? (
				<RenameField
					initial={flow.name}
					onCancel={() => setMode("idle")}
					onCommit={(name) => {
						setMode("idle");
						onRename(name);
					}}
				/>
			) : (
				<div className="flex items-center gap-1.5">
					<span className="min-w-0 flex-1 truncate text-[12px] text-foreground" title={flow.fileName}>
						{title}
					</span>
					<CopyButton
						className="opacity-0 group-focus-within/flow:opacity-100 group-hover/flow:opacity-100 focus-visible:opacity-100"
						value={flow.name || flow.fileName}
						what="recording name"
					/>
					<CopyButton
						className="opacity-0 group-focus-within/flow:opacity-100 group-hover/flow:opacity-100 focus-visible:opacity-100"
						value={flow.path}
						what="recording path"
					/>
					<RowAction icon={Pencil} label={`Rename ${title}`} onClick={() => setMode("renaming")} />
					<RowAction icon={Trash2} label={`Delete ${title}`} onClick={() => setMode("confirming")} />
				</div>
			)}
			<p className="mt-0.5 flex items-center gap-1.5 text-[10.5px] text-muted-foreground">
				<time dateTime={flow.recordedAt}>{formatRecordedAt(flow.recordedAt)}</time>
				<span aria-hidden>·</span>
				{/* A flow recorded before flows stated their own counts is
				    unmeasured, and says so. Showing "0 steps" for a flow with
				    twelve of them would be a number somebody acts on. */}
				{flow.countsKnown ? (
					<>
						<span>
							{flow.steps} step{flow.steps === 1 ? "" : "s"}
						</span>
						{flow.review > 0 ? (
							<>
								<span aria-hidden>·</span>
								<span className="text-warning">{flow.review} to review</span>
							</>
						) : null}
					</>
				) : (
					<span title="Recorded before flows recorded their own counts.">steps unknown</span>
				)}
			</p>
		</li>
	);
}

function RowAction({ icon: Icon, label, onClick }: { icon: typeof Pencil; label: string; onClick: () => void }) {
	return (
		<button
			aria-label={label}
			className={cn(
				"relative grid size-[13px] shrink-0 place-items-center rounded-[3px]",
				"before:absolute before:-inset-1 before:content-['']",
				"text-muted-foreground transition-[opacity,color] hover:text-foreground",
				"opacity-0 group-focus-within/flow:opacity-100 group-hover/flow:opacity-100",
				"focus-visible:opacity-100 focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-none",
			)}
			onClick={onClick}
			title={label}
			type="button"
		>
			<Icon aria-hidden className="size-3" />
		</button>
	);
}

/**
 * Naming happens here rather than while recording, so the loop stays press
 * record, drive, press stop, again - with no text field in the middle asking a
 * person to compose a name for something they have not decided is worth
 * keeping. The timestamp stays on the file either way, so nothing collides and
 * no name is ever required.
 */
function RenameField({
	initial,
	onCancel,
	onCommit,
}: {
	initial: string;
	onCancel: () => void;
	onCommit: (name: string) => void;
}) {
	const [value, setValue] = useState(initial);
	const ref = useRef<HTMLInputElement | null>(null);
	useEffect(() => ref.current?.select(), []);
	return (
		<input
			aria-label="Recording name"
			className="w-full rounded-md border border-border bg-background px-2 py-1 text-[12px] text-foreground focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-none"
			data-testid="sim-flow-rename"
			onBlur={() => onCommit(value)}
			onChange={(event) => setValue(event.target.value)}
			onKeyDown={(event) => {
				if (event.key === "Enter") {
					event.preventDefault();
					onCommit(value);
				}
				if (event.key === "Escape") {
					event.preventDefault();
					onCancel();
				}
			}}
			placeholder="name this recording"
			ref={ref}
			value={value}
		/>
	);
}

function formatRecordedAt(iso: string): string {
	const at = new Date(iso);
	if (Number.isNaN(at.getTime())) return iso;
	return at.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

/** The line shown over the screen after a recording stops. */
export function useStopSummary(): [StopSummary | null, (summary: StopSummary | null) => void] {
	const [summary, setSummary] = useState<StopSummary | null>(null);
	return [summary, setSummary];
}

export function StopSummaryNote({ summary, onDismiss }: { summary: StopSummary; onDismiss: () => void }) {
	const review = useMemo(
		() =>
			summary.review > 0
				? `${summary.review} marked "# REVIEW:" - check those before trusting it`
				: "nothing marked for review",
		[summary.review],
	);
	return (
		<div
			className="absolute inset-x-2 bottom-2 rounded-md border border-border bg-raised/95 px-3 py-2 text-[11px] leading-snug text-foreground shadow-lg"
			data-testid="sim-stop-summary"
			role="status"
		>
			<p>
				Recorded {summary.steps} step{summary.steps === 1 ? "" : "s"} · {review}
			</p>
			<p className="mt-1 flex items-center gap-1.5">
				<span className="min-w-0 flex-1 truncate font-mono text-muted-foreground" title={summary.path}>
					{summary.fileName}
				</span>
				<CopyButton value={summary.path} what="recording path" />
				<button
					aria-label="Dismiss"
					className="rounded-md px-1.5 text-[11px] text-muted-foreground hover:text-foreground"
					onClick={onDismiss}
					type="button"
				>
					Close
				</button>
			</p>
		</div>
	);
}
