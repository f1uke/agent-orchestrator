import { type KeyboardEvent, type ReactNode, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import * as Dialog from "@radix-ui/react-dialog";
import {
	AlertTriangle,
	Check,
	ChevronDown,
	CircleCheck,
	CircleDashed,
	Flame,
	FoldHorizontal,
	type LucideIcon,
	MoreHorizontal,
	Play,
	RotateCw,
	Search,
	Trash2,
	UnfoldHorizontal,
	X,
} from "lucide-react";
import { useOverlayDismissFocus } from "../lib/overlay-focus";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { captureRendererEvent } from "../lib/telemetry";
import { useAddCrewRole } from "../hooks/useAddCrewRole";
import { DashboardSubhead } from "./DashboardSubhead";
import {
	type AttentionZone,
	type WorkspaceSession,
	canonicalTrackerIssueId,
	isMergeSuspended,
	isUndeliveredParked,
	jiraKeyFromIssueId,
	orchestratorHealth,
	workerSessions,
} from "../types/workspace";
import { type Task, type TaskGates, crewChipState, reviewGateState, taskLane, workerTasks } from "../lib/crew";
import { useTaskGates } from "../hooks/useTaskGates";
import { CrewStrip } from "./CrewStrip";
import { JiraKeyBadge } from "./JiraKeyBadge";
import { useSessionScmSummary, type SessionPRSummary } from "../hooks/useSessionScmSummary";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { TodoDetailDialog } from "./TodoDetailDialog";
import { IdleStatusChip } from "./IdleStatusChip";
import { QueuedMessagesChip } from "./QueuedMessagesChip";
import { MergeSuspendChip } from "./MergeSuspendChip";
import { UndeliveredWorkChip } from "./UndeliveredWorkChip";
import { UndeliveredWorkDialog } from "./UndeliveredWorkDialog";
import { killSession, UndeliveredWorkError, type UncommittedFile } from "../lib/kill-session";
import { TokenUsageChip } from "./TokenUsageChip";
import { useAgentsQuery } from "../hooks/useAgentsQuery";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { approvalProgress, prBrowserUrl, prKindLabel, prRef, sessionPRDisplaySummaries } from "../lib/pr-display";
import { ApprovalMeter } from "./ApprovalMeter";
import {
	type DoneDisposition,
	doneDisposition,
	doneAt,
	doneSearchText,
	matchesDoneQuery,
	sortDoneRecentFirst,
} from "../lib/done-lane";
import { type BoardLaneKey, DONE_LANE, LANE_ORDER, LANES, type LaneConfig } from "../lib/lane-indicator";
import { statusGlyph } from "../lib/status-glyph";
import { formatTimeCompact } from "../lib/format-time";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { measuredOrFallbackRect } from "../lib/virtual-rect";

type SessionsBoardProps = {
	/** When set, the board shows only this project's sessions. */
	projectId?: string;
};

// The live kanban lanes, left→right by flow (work → review → merge). Each lane
// owns one hue in the 4-color semantic system (see lib/lane-indicator +
// design handoff Board.dc.html). Done follows them as the board's last lane.
const COLUMNS: LaneConfig[] = LANE_ORDER.map((key) => LANES[key]);

// 11rem is the narrowest lane whose card still reads: its status line keeps the
// agent label beside a wrapping status, and the widest chip fits by wrapping its
// button. It is sized so the board fits the windows people use without scrolling
// sideways: at 1280px the five live lanes open plus Done folded, and at the 960px
// minimum three open lanes with the other three folded. Above the floor the lanes
// share the room, so at 1280px each is ~186px.
const OPEN_LANE_WIDTH = "minmax(11rem, 1fr)";
// Narrow enough that at the 960px minimum, three open lanes fit beside three
// folded ones.
const COLLAPSED_LANE_WIDTH = "2.25rem";

export function SessionsBoard({ projectId }: SessionsBoardProps) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	const all = workspaceQuery.data ?? [];
	const workspaces = projectId ? all.filter((w) => w.id === projectId) : all;
	const workspace = projectId ? workspaces[0] : undefined;
	const sessions = workspaces.flatMap((w) => workerSessions(w.sessions));
	const [todoDetail, setTodoDetail] = useState<WorkspaceSession | null>(null);
	const restartingProjectIds = useUiStore((state) => state.restartingProjectIds);
	const setProjectRestarting = useUiStore((state) => state.setProjectRestarting);
	const setOrchestratorReplacementError = useUiStore((state) => state.setOrchestratorReplacementError);
	const collapsedLanes = useUiStore((state) => state.collapsedBoardLanes);
	const toggleLaneCollapsed = useUiStore((state) => state.toggleBoardLaneCollapsed);
	const isProjectRestarting = projectId ? restartingProjectIds.has(projectId) : false;
	const health = workspace ? orchestratorHealth(workspace, isProjectRestarting) : { state: "ok" as const };

	// The board draws one card per TASK, not per session: a crew's qa is drawn on
	// its dev's card rather than as a second card for work already on screen.
	// A solo task is a task of one, so a board with no crews on it groups into
	// exactly the list it always had.
	const tasks = workerTasks(sessions);
	const gates = useTaskGates(tasks);
	const byZone = new Map<AttentionZone, Task[]>();
	for (const task of tasks) {
		const zone = taskLane(task, gates.get(task.dev.id) ?? { review: "not run" }).zone;
		(byZone.get(zone) ?? byZone.set(zone, []).get(zone)!).push(task);
	}
	// Finished work is one card per session, unsorted here: only the open Done
	// lane orders and searches it, so a folded lane costs a count and nothing more.
	const done = (byZone.get("done") ?? []).map((task) => task.dev);
	const doneFolded = collapsedLanes.has(DONE_LANE.key);

	// A lane opened by hand is brought into view. Below the width that fits every
	// lane the board scrolls sideways, and opening Done - the last lane - at 1280px
	// would otherwise unfold it past the right edge, where it looks like nothing
	// happened. Only on an unfold the person made: a board that loads with a lane
	// already open stays scrolled where it starts.
	const boardRef = useRef<HTMLDivElement | null>(null);
	const expandLane = (lane: BoardLaneKey) => {
		toggleLaneCollapsed(lane);
		// A click's update is committed before the next frame, so by then the lane
		// is drawn open at its full width.
		requestAnimationFrame(() =>
			boardRef.current
				?.querySelector(`[data-lane="${lane}"]`)
				?.scrollIntoView?.({ behavior: "smooth", block: "nearest", inline: "nearest" }),
		);
	};

	const openSession = (session: WorkspaceSession) =>
		void navigate({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: session.workspaceId, sessionId: session.id },
		});

	// A TODO card opens the detail/edit modal instead of navigating — it has no
	// live terminal yet.
	const openTodo = (session: WorkspaceSession) => setTodoDetail(session);
	const handleTodoStarted = (sessionId: string) => {
		const workspaceId = projectId ?? todoDetail?.workspaceId;
		if (!workspaceId) return;
		void navigate({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: workspaceId, sessionId },
		});
	};

	const restartOrchestrator = async () => {
		if (!projectId) return;
		await restartProjectOrchestrator({
			projectId,
			queryClient,
			navigate,
			setProjectRestarting,
			setOrchestratorReplacementError,
		});
	};

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			{/* Actions (New task + bell) live in the shared ShellTopbar so they sit
			    top-right identically on the board and the orchestrator; the subhead
			    keeps just the page title + subtitle. */}
			<DashboardSubhead title="Board" subtitle="Live agent sessions flowing from work → review → merge." />

			<div className="min-h-0 flex-1 overflow-hidden p-[18px]">
				{projectId && health.state !== "ok" ? (
					<div className="mb-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-[12px] text-muted-foreground">
						<AlertTriangle className="size-4 shrink-0 text-warning" aria-hidden="true" />
						<span className="min-w-0 flex-1">{health.message}</span>
						{health.state === "restart_needed" || health.state === "duplicates" ? (
							<button
								className="dashboard-app-header__primary-btn"
								disabled={isProjectRestarting}
								onClick={() => void restartOrchestrator()}
								type="button"
							>
								<RotateCw className="size-3.5" aria-hidden="true" />
								Restart
							</button>
						) : null}
					</div>
				) : null}
				{workspaceQuery.isError ? (
					<p className="py-10 text-center text-[12px] text-passive">Could not load sessions.</p>
				) : (
					// A lane is never narrower than its cards can be read at. Five lanes at
					// the 960px minimum window used to get 130px each, which left a card's
					// header 52px - narrower than the word "requested" - and no layout of
					// that row could hold its status, its agent and its chips. Below the
					// floor the board scrolls sideways (as agent-orchestrator's does), and
					// the person takes the room back by folding lanes they are not watching.
					//
					// Done is the board's last lane - a sanctioned departure from the
					// reference, which keeps finished work in a bar under the board.
					<div
						ref={boardRef}
						className="grid h-full gap-2 overflow-x-auto"
						style={{
							gridTemplateColumns: [...COLUMNS, DONE_LANE]
								.map((col) => (collapsedLanes.has(col.key) ? COLLAPSED_LANE_WIDTH : OPEN_LANE_WIDTH))
								.join(" "),
						}}
					>
						{COLUMNS.map((col) =>
							collapsedLanes.has(col.key) ? (
								<CollapsedZoneColumn
									key={col.key}
									col={col}
									count={byZone.get(col.key)?.length ?? 0}
									onExpand={() => expandLane(col.key)}
								/>
							) : (
								<ZoneColumn
									key={col.key}
									col={col}
									tasks={byZone.get(col.key) ?? []}
									gates={gates}
									onOpen={col.key === "todo" ? openTodo : openSession}
									onStarted={handleTodoStarted}
									onCollapse={() => toggleLaneCollapsed(col.key)}
								/>
							),
						)}
						{doneFolded ? (
							<CollapsedZoneColumn col={DONE_LANE} count={done.length} onExpand={() => expandLane(DONE_LANE.key)} />
						) : (
							<DoneLane sessions={done} onOpen={openSession} onCollapse={() => toggleLaneCollapsed(DONE_LANE.key)} />
						)}
					</div>
				)}
			</div>
			<TodoDetailDialog
				session={todoDetail}
				onOpenChange={(open) => !open && setTodoDetail(null)}
				onStarted={handleTodoStarted}
			/>
		</div>
	);
}

// A done card's first guess at its own height, before it is measured: a
// two-line title, the disposition line and the branch line. Cards are measured
// once drawn, so this only has to be close enough that the scrollbar does not
// jump on the first scroll.
const DONE_CARD_ESTIMATE = 86;
const DONE_CARD_GAP = 10;

// The Done lane, open: every merged or terminated session, most recently ended
// first, behind a search box. It can hold hundreds - a busy machine had 426 - so
// the list is WINDOWED: only the cards in view (and a few either side) are in the
// DOM, and the search filters an index built once per list, never per keystroke.
// Folded, none of this mounts; the board draws the strip from a count alone.
function DoneLane({
	sessions,
	onOpen,
	onCollapse,
}: {
	sessions: WorkspaceSession[];
	onOpen: (s: WorkspaceSession) => void;
	onCollapse: () => void;
}) {
	const [query, setQuery] = useState("");
	const searchable = useMemo(
		() => sortDoneRecentFirst(sessions).map((session) => ({ session, text: doneSearchText(session) })),
		[sessions],
	);
	const searching = query.trim() !== "";
	const shown = useMemo(
		() =>
			(searching ? searchable.filter((entry) => matchesDoneQuery(entry.text, query)) : searchable).map(
				(entry) => entry.session,
			),
		[searchable, searching, query],
	);

	const scrollRef = useRef<HTMLDivElement | null>(null);
	const virtualizer = useVirtualizer({
		count: shown.length,
		getScrollElement: () => scrollRef.current,
		estimateSize: () => DONE_CARD_ESTIMATE,
		getItemKey: (index) => shown[index].id,
		gap: DONE_CARD_GAP,
		overscan: 6,
		observeElementRect: measuredOrFallbackRect,
	});
	// A new query is a new list: start it at its top, not wherever the last one
	// was scrolled to.
	const search = (next: string) => {
		setQuery(next);
		if (scrollRef.current) scrollRef.current.scrollTop = 0;
	};

	return (
		<section data-lane={DONE_LANE.key} className={LANE_SURFACE}>
			<LaneHeader col={DONE_LANE} count={sessions.length} onCollapse={onCollapse} />
			{sessions.length > 0 && (
				<div className="shrink-0 px-[11px] pb-2">
					<div className="relative">
						<Search
							className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-passive"
							aria-hidden="true"
						/>
						<Input
							type="search"
							aria-label="Search finished sessions"
							title="Matches the name, session id, branch, Jira key or PR number"
							placeholder="Search"
							value={query}
							onChange={(event) => search(event.target.value)}
							onKeyDown={(event) => {
								if (event.key === "Escape" && query !== "") {
									event.preventDefault();
									search("");
								}
							}}
							className="h-7 bg-[var(--kanban-card-bg)] pl-7 pr-7 text-[12px] [&::-webkit-search-cancel-button]:appearance-none"
						/>
						{query !== "" && (
							<button
								type="button"
								aria-label="Clear search"
								title="Clear search"
								onClick={() => search("")}
								className="absolute right-1 top-1/2 grid size-5 -translate-y-1/2 cursor-pointer place-items-center rounded text-passive hover:bg-interactive-hover hover:text-foreground"
							>
								<X className="size-3" aria-hidden="true" />
							</button>
						)}
					</div>
					<div className="mt-1.5 flex items-center gap-2 px-0.5 font-mono text-[10px] text-passive">
						<span aria-live="polite" className="min-w-0 flex-1 truncate">
							{searching ? `${shown.length} of ${sessions.length}` : `${sessions.length} finished`}
						</span>
						{shown.length > 0 && <ClearAllButton sessions={shown} filtered={searching} />}
					</div>
				</div>
			)}
			{/* The same body as a live lane - see ZoneColumn - except that it is the
			    virtualiser's scroll element. */}
			<div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto pb-3 pl-[11px] pr-px [scrollbar-gutter:stable]">
				{sessions.length === 0 ? (
					<EmptyLane col={DONE_LANE} />
				) : shown.length === 0 ? (
					<EmptyLane col={DONE_LANE} text={`Nothing finished matches “${query.trim()}”`} />
				) : (
					<div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
						{virtualizer.getVirtualItems().map((item) => (
							<div
								key={item.key}
								data-index={item.index}
								ref={virtualizer.measureElement}
								className="absolute left-0 top-0 w-full"
								style={{ transform: `translateY(${item.start}px)` }}
							>
								<DoneCard session={shown[item.index]} onOpen={() => onOpen(shown[item.index])} />
							</div>
						))}
					</div>
				)}
			</div>
		</section>
	);
}

// Done (merged) reads success-green; terminated (killed) reads passive-gray, so
// the two archive states are tellable apart at a glance. Dot + lowercase label
// mirrors the board card's status idiom.
const DONE_DISPOSITION: Record<DoneDisposition, { label: string; className: string }> = {
	done: { label: "done", className: "text-success" },
	terminated: { label: "terminated", className: "text-passive" },
};

// A finished/terminated session's card in the Done lane. It is deliberately not
// a live card: no status gutter, no agent label, no crew strip, no PR footer and
// nothing that pulses - its title sits in the muted ink and its one status is how
// it ended, and when.
//
// Deleting is permanent (unlike kill, which just stops a running worker), so it
// mirrors TopbarKillButton's inline arm-confirm rather than firing on a single
// click. Default force=false preserves an uncommitted worktree; a dirty-worktree
// refusal surfaces the daemon's error and offers "Delete anyway" (force=true)
// instead of silently discarding work.
function DoneCard({ session, onOpen }: { session: WorkspaceSession; onOpen: () => void }) {
	const queryClient = useQueryClient();
	const [confirming, setConfirming] = useState(false);
	const [error, setError] = useState<string | null>(null);
	// Reopen tracks its own error separately from delete: a reopen failure must not
	// render delete's inline "Delete anyway" affordance (clicking that would force a
	// permanent delete the user never asked for).
	const [reopenError, setReopenError] = useState<string | null>(null);

	// Reopen re-activates a done session behind the scenes so its card leaves the
	// done bucket. A terminated (or terminated-then-merged) session is restored;
	// once it is live again the daemon's SCM observer auto-claims any newer open PR
	// on its worktree branch, which re-derives the status into an active zone — so
	// the UI never has to know the PR number or call claim-pr itself. A merged
	// session that is still live on disk is not terminated, so restore reports
	// SESSION_NOT_RESTORABLE; that is a no-op success here (it is already active and
	// the observer handles the PR), not a failure to surface.
	const reopen = useMutation({
		mutationFn: async () => {
			const { error: apiError } = await apiClient.POST("/api/v1/sessions/{sessionId}/restore", {
				params: { path: { sessionId: session.id } },
			});
			if (apiError) {
				if ((apiError as { code?: string }).code === "SESSION_NOT_RESTORABLE") return;
				throw new Error(apiErrorMessage(apiError, "Unable to reopen session"));
			}
		},
		onSuccess: () => {
			setReopenError(null);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
		onError: (e) => {
			setReopenError(e instanceof Error ? e.message : "Reopen failed");
		},
	});

	const del = useMutation({
		mutationFn: async (force: boolean) => {
			const { error: apiError } = await apiClient.DELETE("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId: session.id }, query: { force } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
		},
		onSuccess: () => {
			setConfirming(false);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
		onError: (e) => {
			setConfirming(false);
			setError(e instanceof Error ? e.message : "Delete failed");
		},
	});

	const disposition = DONE_DISPOSITION[doneDisposition(session)];
	const jiraKey = jiraKeyFromIssueId(session.issueId);
	const branch = session.branch || "";
	const showBranch = branch !== "" && !sameLabel(branch, session.title) && !sameLabel(branch, session.id);
	return (
		<div
			data-done-card={session.id}
			className="rounded-[10px] px-[13px] pb-2.5 pt-[9px]"
			style={{ background: "var(--kanban-card-bg)", border: "1px solid var(--kanban-card-border)" }}
		>
			<div className="flex items-start gap-1">
				<button
					className="line-clamp-2 min-w-0 flex-1 cursor-pointer text-left text-[12.5px] font-medium leading-[1.4] tracking-[-0.01em] text-muted-foreground transition-colors hover:text-foreground"
					onClick={onOpen}
					title={session.title}
					type="button"
				>
					{session.title}
				</button>
				{!confirming && (
					<div className="-mr-1.5 -mt-0.5 flex shrink-0 items-center">
						<button
							aria-label="Reopen session"
							title="Reopen session"
							className="cursor-pointer rounded p-1 text-passive hover:bg-interactive-hover hover:text-foreground"
							disabled={reopen.isPending}
							onClick={() => {
								setReopenError(null);
								reopen.mutate();
							}}
							type="button"
						>
							<RotateCw className="h-3 w-3" aria-hidden="true" />
						</button>
						<button
							aria-label="Delete session"
							title="Delete session"
							className="cursor-pointer rounded p-1 text-passive hover:bg-interactive-hover hover:text-error"
							onClick={() => {
								setError(null);
								setConfirming(true);
							}}
							type="button"
						>
							<Trash2 className="h-3 w-3" aria-hidden="true" />
						</button>
					</div>
				)}
			</div>
			{/* How it finished (done vs terminated) + how long ago. */}
			<div className="mt-1 flex items-center gap-1 text-[10px] leading-none">
				<span className={cn("inline-flex items-center gap-1 font-medium", disposition.className)}>
					<span className="h-[5px] w-[5px] rounded-full bg-current" aria-hidden="true" />
					{disposition.label}
				</span>
				<span className="text-passive" aria-hidden="true">
					·
				</span>
				{/* The dot already says it ended; the time alone keeps the line whole in
				    a narrow lane, and the tooltip gives the exact moment. */}
				<span className="truncate font-mono text-passive" title={`Ended ${new Date(doneAt(session)).toLocaleString()}`}>
					{formatTimeCompact(doneAt(session))}
				</span>
			</div>
			{/* What the search box matches beyond the name, so a hit on a branch or a
			    key shows why it matched. Plain text: a Jira badge would fetch the
			    issue for every card scrolled past. */}
			{(showBranch || jiraKey) && (
				<div className="mt-1.5 flex min-w-0 items-center gap-1.5 font-mono text-[10.5px] text-passive">
					{jiraKey && <span className="shrink-0 text-accent">{jiraKey}</span>}
					{showBranch && <span className="truncate">{branch}</span>}
				</div>
			)}
			{confirming && (
				<div className="mt-2 flex items-center gap-2 text-[11px]">
					<span className="min-w-0 flex-1 text-muted-foreground">Delete for good?</span>
					<button
						aria-label="Confirm delete"
						className="cursor-pointer text-error hover:underline"
						disabled={del.isPending}
						onClick={() => del.mutate(false)}
						type="button"
					>
						Confirm
					</button>
					<button
						aria-label="Cancel delete"
						className="cursor-pointer text-passive hover:text-foreground"
						onClick={() => {
							setConfirming(false);
							setError(null);
						}}
						type="button"
					>
						Cancel
					</button>
				</div>
			)}
			{error && (
				<div className="mt-2 text-[10.5px] leading-snug text-error">
					{error}{" "}
					{/* A dirty-worktree refusal (SESSION_WORKSPACE_DIRTY) is the expected
					    reason; offer a force delete that discards uncommitted changes. */}
					<button
						aria-label="Delete anyway"
						className="cursor-pointer underline hover:text-error"
						disabled={del.isPending}
						onClick={() => del.mutate(true)}
						type="button"
					>
						Delete anyway
					</button>
				</div>
			)}
			{reopenError && (
				<div className="mt-2 text-[10.5px] leading-snug text-error" role="alert">
					Couldn’t reopen: {reopenError}
				</div>
			)}
		</div>
	);
}

// "Clear all" empties the Done lane - or, while its search narrows the lane, only
// the sessions the search shows, since those are the ones on screen when the
// button is pressed. There is no bulk-delete endpoint, so this fires one DELETE
// per session (force=false, same as a single card) and reports how many failed
// rather than partially retrying. Confirmed via a Radix dialog (mirrors
// RestoreUnavailableDialog) since it is destructive and scoped to N sessions
// rather than one; the dialog names N.
function ClearAllButton({ sessions, filtered }: { sessions: WorkspaceSession[]; filtered: boolean }) {
	const queryClient = useQueryClient();
	const [open, setOpen] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const clear = useMutation({
		mutationFn: async () => {
			const results = await Promise.allSettled(
				sessions.map((s) =>
					apiClient.DELETE("/api/v1/sessions/{sessionId}", {
						params: { path: { sessionId: s.id }, query: { force: false } },
					}),
				),
			);
			const failed = results.filter((r) => r.status === "rejected" || (r.value && "error" in r.value && r.value.error));
			if (failed.length > 0) throw new Error(`${failed.length} session(s) could not be deleted (uncommitted changes?)`);
		},
		onSuccess: () => {
			setOpen(false);
		},
		onError: (e) => setError(e instanceof Error ? e.message : "Clear failed"),
		// Refresh the workspace query whether or not every deletion succeeded, so
		// sessions that WERE deleted (a partial-failure run still deletes some)
		// drop out of the Done lane instead of lingering as stale rows until the
		// next unrelated refetch.
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});

	// An outside pointer press that closes the confirm dialog must not yank focus
	// back to the "Clear all" trigger (stray ring); keyboard closes still restore it.
	const dismissFocus = useOverlayDismissFocus();

	return (
		<>
			<button
				className="shrink-0 cursor-pointer font-mono text-[10px] text-passive hover:text-error"
				onClick={() => {
					setError(null);
					setOpen(true);
				}}
				type="button"
			>
				{filtered ? "Clear shown" : "Clear all"}
			</button>
			<Dialog.Root open={open} onOpenChange={setOpen}>
				<Dialog.Portal>
					<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
					<Dialog.Content
						{...dismissFocus}
						className="fixed left-1/2 top-1/2 z-50 w-[420px] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-5 shadow-lg"
					>
						<Dialog.Title className="text-sm font-medium text-foreground">
							{filtered ? "Clear the finished sessions shown" : "Clear all finished sessions"}
						</Dialog.Title>
						<Dialog.Description className="mt-2 text-[13px] text-muted-foreground">
							Permanently remove {sessions.length} finished session(s) from AO
							{filtered ? ", the ones matching the search" : ""}. Their git branches are kept.
						</Dialog.Description>
						{error && <div className="mt-3 text-[12px] text-error">{error}</div>}
						<div className="mt-4 flex justify-end gap-2">
							<Button variant="ghost" onClick={() => setOpen(false)} disabled={clear.isPending}>
								Cancel
							</Button>
							<Button onClick={() => clear.mutate()} disabled={clear.isPending}>
								Delete all
							</Button>
						</div>
					</Dialog.Content>
				</Dialog.Portal>
			</Dialog.Root>
		</>
	);
}

function ZoneColumn({
	col,
	tasks,
	gates,
	onOpen,
	onStarted,
	onCollapse,
}: {
	col: LaneConfig;
	tasks: Task[];
	gates: Map<string, TaskGates>;
	onOpen: (s: WorkspaceSession) => void;
	onStarted: (sessionId: string) => void;
	onCollapse: () => void;
}) {
	const isTodo = col.key === "todo";
	return (
		<section
			// No painted edges: the lane's identity is carried by the header's shape
			// glyph + label, and each card repeats its own status as a glyph in the
			// card gutter. The column itself is a plain neutral surface, so nothing
			// competes with the cards standing on it.
			data-lane={col.key}
			className={LANE_SURFACE}
		>
			<LaneHeader col={col} count={tasks.length} onCollapse={onCollapse} />
			{/* The scrollbar's gutter is reserved in every lane, scrolling or not, and
			    stands in for the right padding: otherwise the one lane long enough to
			    scroll draws its cards 10px narrower than its neighbours. */}
			<div className="min-h-0 flex-1 overflow-y-auto pb-3 pl-[11px] pr-px [scrollbar-gutter:stable]">
				{tasks.length === 0 ? (
					<EmptyLane col={col} />
				) : (
					<div className="flex flex-col gap-2.5">
						{tasks.map((task) =>
							isTodo ? (
								<TodoCard
									key={task.dev.id}
									session={task.dev}
									col={col}
									onOpen={() => onOpen(task.dev)}
									onStarted={onStarted}
								/>
							) : (
								<SessionCard key={task.dev.id} task={task} gates={gates.get(task.dev.id)} col={col} onOpen={onOpen} />
							),
						)}
					</div>
				)}
			</div>
		</section>
	);
}

// A lane's header, and the control that folds it: the WHOLE strip is one button
// - glyph, name, count and the space between - so folding a lane never means
// hunting for a 20px icon. It looks like one: the pointer, a hover wash over the
// strip, and the lane glyph turning into the fold glyph while the strip is
// hovered or focused. Nothing else may live inside it; a lane with its own
// controls (Done's search) puts them under the header, where a click on them can
// never fold the lane by accident.
function LaneHeader({
	col,
	count,
	onCollapse,
}: {
	col: LaneConfig<BoardLaneKey>;
	count: number;
	onCollapse: () => void;
}) {
	return (
		<button
			type="button"
			aria-expanded={true}
			aria-label={`Collapse ${col.label}, ${cardCount(count)}`}
			title={`Collapse ${col.label}`}
			onClick={onCollapse}
			className="group/lane flex w-full shrink-0 cursor-pointer items-center gap-[9px] px-[15px] pb-[11px] pt-[13px] text-left transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover focus-visible:outline-none"
		>
			<SwapGlyph col={col} Swap={FoldHorizontal} />
			<span className="truncate text-[11.5px] font-bold uppercase tracking-[0.09em]" style={{ color: col.dotVar }}>
				{col.label}
			</span>
			<LaneCount className="ml-auto" count={count} />
		</button>
	);
}

// A lane folded out of the way: the same trough, holding only what says which
// lane it is and how much is in it - the glyph, the count, the name read
// sideways. The whole strip is the button that opens it again, and it answers a
// hover the way an open lane's header does.
function CollapsedZoneColumn({
	col,
	count,
	onExpand,
}: {
	col: LaneConfig<BoardLaneKey>;
	count: number;
	onExpand: () => void;
}) {
	return (
		<section data-lane={col.key} data-collapsed="" className={LANE_SURFACE}>
			<button
				type="button"
				aria-expanded={false}
				aria-label={`Expand ${col.label}, ${cardCount(count)}`}
				title={`Expand ${col.label}`}
				onClick={onExpand}
				className="group/lane flex h-full cursor-pointer flex-col items-center gap-2.5 pb-3 pt-[15px] transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover focus-visible:outline-none"
			>
				<SwapGlyph col={col} Swap={UnfoldHorizontal} />
				<LaneCount count={count} compact />
				<span
					className="whitespace-nowrap text-[11.5px] font-bold uppercase tracking-[0.09em] [writing-mode:vertical-rl]"
					style={{ color: col.dotVar }}
				>
					{col.label}
				</span>
			</button>
		</section>
	);
}

function cardCount(count: number): string {
	return `${count} ${count === 1 ? "card" : "cards"}`;
}

const LANE_SURFACE =
	"flex min-w-0 flex-col overflow-hidden rounded-[12px] border border-[var(--kanban-col-border)] bg-[var(--kanban-column-bg)]";

function LaneGlyph({ col, className }: { col: LaneConfig<BoardLaneKey>; className?: string }) {
	const { Icon } = col;
	return (
		<Icon
			data-lane-glyph={col.key}
			className={cn("h-[13px] w-[13px] shrink-0", className)}
			style={{ color: col.dotVar, ...(col.filled ? { fill: "currentColor" } : {}) }}
			aria-hidden="true"
		/>
	);
}

// The lane glyph in a fold control, standing in for the fold (or unfold) glyph
// while the control is hovered or focused: the one cell says both which lane
// this is and what pressing it does, without costing the name any room.
function SwapGlyph({ col, Swap }: { col: LaneConfig<BoardLaneKey>; Swap: LucideIcon }) {
	return (
		<span className="grid shrink-0 place-items-center *:[grid-area:1/1]">
			<LaneGlyph col={col} className="group-hover/lane:invisible group-focus-visible/lane:invisible" />
			<Swap
				className="invisible h-[14px] w-[14px] text-muted-foreground group-hover/lane:visible group-focus-visible/lane:visible"
				aria-hidden="true"
			/>
		</span>
	);
}

// `compact` is the folded strip's: 2.25rem across, which the badge's own padding
// would overrun at three digits - and the Done lane reaches three digits.
function LaneCount({ count, className, compact }: { count: number; className?: string; compact?: boolean }) {
	return (
		<span
			className={cn(
				"min-w-[22px] rounded-full border border-border-strong bg-interactive-hover py-px text-center font-mono font-bold leading-[1.5] text-muted-foreground",
				compact ? "px-[4px] text-[10px]" : "px-[9px] text-[11px]",
				className,
			)}
		>
			{count}
		</span>
	);
}

// The quiet placeholder shown in a lane with no cards. It deliberately carries
// NO lane hue and no filled surface — a faint neutral dashed hairline plus
// passive, low-contrast text, faded further with opacity — so an empty lane
// reads as "nothing here" filler and recedes, letting a single real card (solid
// surface, bright lane-coloured dot, full-strength title) clearly dominate.
function EmptyLane({ col, text }: { col: LaneConfig<BoardLaneKey>; text?: string }) {
	const { Icon } = col;
	return (
		<div className="mt-2 flex flex-col items-center justify-center gap-2 rounded-[10px] border border-dashed border-border px-4 py-[34px] text-center text-[12px] text-passive opacity-60">
			<Icon
				className="h-[15px] w-[15px]"
				style={col.filled ? { fill: "currentColor" } : undefined}
				aria-hidden="true"
			/>
			<span className="break-words [overflow-wrap:anywhere]">{text ?? col.emptyText}</span>
		</div>
	);
}

// A board card's overflow menu. Its single action, "Move to Done", terminates the
// session via POST /sessions/{id}/kill — the same terminate + reclaim-worktree path
// as the topbar Kill (ShellTopbar's TopbarKillButton), reached from the board so a
// no-PR session (e.g. an investigation) can be finished without opening it. Kill keeps
// the git branch and preserves an uncommitted worktree, so the session stays restorable
// via the Done lane's Reopen (the sole reversal affordance — this menu has no reopen).
// Terminating is destructive, so the item arms a one-step confirm inside the menu
// before firing.
//
// The kill can be REFUSED — a worktree still holding work no pull request carries —
// and that refusal is the one thing this menu may not swallow. A menu is far too
// small for a file list, so the refusal hands off to UndeliveredWorkDialog, which
// says what is in the way and offers the deliberate discard.
function SessionCardMenu({ session, onOpenSession }: { session: WorkspaceSession; onOpenSession: () => void }) {
	const queryClient = useQueryClient();
	const [open, setOpen] = useState(false);
	const [confirming, setConfirming] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [refused, setRefused] = useState<UncommittedFile[] | null>(null);

	const kill = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.session_kill_requested", { project_id: session.workspaceId });
			await killSession(session.id);
		},
		onSuccess: () => {
			void captureRendererEvent("ao.renderer.session_kill_succeeded", { project_id: session.workspaceId });
			// The session flips to terminated on the next refresh, so its card leaves this
			// column for the Done lane and this menu unmounts with it.
			setOpen(false);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
		onError: (e) => {
			void captureRendererEvent("ao.renderer.session_kill_failed", { project_id: session.workspaceId });
			if (e instanceof UndeliveredWorkError) {
				setOpen(false);
				setConfirming(false);
				setRefused(e.files);
				return;
			}
			setError(e instanceof Error ? e.message : "Move to Done failed");
		},
	});

	// Toggle keep-warm-on-merge: when on, a PR merge suspends the worker in place
	// (card stays on the board, resumable) instead of archiving it to Done. Keeps
	// the menu open so the checkmark updates in place.
	const keepWarm = useMutation({
		mutationFn: async () => {
			const { error: apiError } = await apiClient.PUT("/api/v1/sessions/{sessionId}/keep-warm", {
				params: { path: { sessionId: session.id } },
				body: { enabled: !session.keepWarmOnMerge },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
		onError: (e) => setError(e instanceof Error ? e.message : "Keep-warm toggle failed"),
	});

	return (
		<>
			{refused && (
				<UndeliveredWorkDialog
					open
					onOpenChange={(next) => !next && setRefused(null)}
					sessionId={session.id}
					sessionTitle={session.title}
					files={refused}
					onOpenSession={onOpenSession}
				/>
			)}
			<DropdownMenu
				open={open}
				onOpenChange={(next) => {
					setOpen(next);
					// Reset the arm-confirm whenever the menu closes so it never reopens mid-confirm.
					if (!next) {
						setConfirming(false);
						setError(null);
					}
				}}
			>
				<DropdownMenuTrigger asChild>
					<button
						aria-label="Session actions"
						className={cn(
							"peer -my-1 rounded p-0.5 text-passive opacity-0 transition-opacity hover:text-foreground",
							"focus-visible:opacity-100 group-hover:opacity-100 data-[state=open]:opacity-100",
						)}
						// Stop the click from reaching the card's open-session handler.
						onClick={(event) => event.stopPropagation()}
						type="button"
					>
						<MoreHorizontal className="h-4 w-4" aria-hidden="true" />
					</button>
				</DropdownMenuTrigger>
				{/* The content is portaled, but React events bubble along the React tree — the
			    menu lives inside the card's open-on-click wrapper — so stop clicks here or
			    choosing an item would also navigate into the session. */}
				<DropdownMenuContent align="end" onClick={(event) => event.stopPropagation()}>
					{confirming ? (
						<>
							<div className="max-w-[15rem] px-2 py-1.5 text-[11px] leading-snug text-muted-foreground">
								Stops the agent and reclaims its worktree. The branch and any open PR stay. Reopen from the Done lane to
								undo.
							</div>
							<DropdownMenuItem
								className="text-error focus:text-error [&_svg]:text-error"
								disabled={kill.isPending}
								onSelect={(event) => {
									// Keep the menu open through the mutation so pending/error state is visible.
									event.preventDefault();
									kill.mutate();
								}}
							>
								<CircleCheck aria-hidden="true" />
								{kill.isPending ? "Moving…" : "Confirm — move to Done"}
							</DropdownMenuItem>
							<DropdownMenuItem
								disabled={kill.isPending}
								onSelect={(event) => {
									event.preventDefault();
									setConfirming(false);
								}}
							>
								Cancel
							</DropdownMenuItem>
							{error ? (
								<div className="max-w-[15rem] px-2 py-1.5 text-[11px] text-error" role="alert">
									{error}
								</div>
							) : null}
						</>
					) : (
						<>
							<DropdownMenuItem
								disabled={keepWarm.isPending}
								onSelect={(event) => {
									// Keep the menu open so the checkmark flips in place.
									event.preventDefault();
									setError(null);
									keepWarm.mutate();
								}}
							>
								<Flame aria-hidden="true" />
								Keep warm after merge
								{session.keepWarmOnMerge ? <Check className="ml-auto h-3.5 w-3.5" aria-hidden="true" /> : null}
							</DropdownMenuItem>
							<DropdownMenuSeparator />
							<DropdownMenuItem
								className="text-error focus:text-error [&_svg]:text-error"
								onSelect={(event) => {
									event.preventDefault();
									setError(null);
									setConfirming(true);
								}}
							>
								<CircleCheck aria-hidden="true" />
								Move to Done
							</DropdownMenuItem>
							{error ? (
								<div className="max-w-[15rem] px-2 py-1.5 text-[11px] text-error" role="alert">
									{error}
								</div>
							) : null}
						</>
					)}
				</DropdownMenuContent>
			</DropdownMenu>
		</>
	);
}

// A TODO-lane card: a prepared, not-yet-started worker. Unlike SessionCard it
// has no PR footer and no navigate-on-click (clicking opens the detail/edit
// modal); its footer is a split "▶ Start ▾" button whose caret picks the agent
// to start with. Start materializes the session in place (POST /start).
function TodoCard({
	session,
	col,
	onOpen,
	onStarted,
}: {
	session: WorkspaceSession;
	col: LaneConfig;
	onOpen: () => void;
	onStarted: (sessionId: string) => void;
}) {
	const queryClient = useQueryClient();
	const [menuOpen, setMenuOpen] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const agentsQuery = useAgentsQuery();
	const branch = session.branch || "";
	const showBranch = session.autoNameBranch || branch === "" ? true : !sameLabel(branch, session.title);
	const branchLabel = branch && !session.autoNameBranch ? branch : "auto-named on start";

	const start = useMutation({
		mutationFn: async (harness?: string) => {
			if (harness && harness !== session.provider) {
				const { error: patchErr } = await apiClient.PATCH("/api/v1/sessions/{sessionId}/spec", {
					params: { path: { sessionId: session.id } },
					body: { harness: harness as never },
				});
				if (patchErr) throw new Error(apiErrorMessage(patchErr, "Could not set agent"));
			}
			const { error: apiError } = await apiClient.POST("/api/v1/sessions/{sessionId}/start", {
				params: { path: { sessionId: session.id } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Could not start task"));
		},
		onSuccess: async () => {
			setMenuOpen(false);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			onStarted(session.id);
		},
		onError: (e) => setError(e instanceof Error ? e.message : "Could not start task"),
	});

	const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (event.currentTarget !== event.target) return;
		if (event.key !== "Enter" && event.key !== " ") return;
		event.preventDefault();
		onOpen();
	};

	const agentOptions = agentsQuery.data?.supported ?? [];

	return (
		<div
			className="group w-full overflow-visible rounded-[10px] text-left transition-colors"
			style={{
				background: "var(--kanban-card-bg)",
				border: "1px solid var(--kanban-card-border)",
			}}
		>
			<div
				onClick={onOpen}
				onKeyDown={handleKeyDown}
				role="button"
				tabIndex={0}
				className="flex cursor-pointer gap-2 px-[13px] pt-3"
			>
				{/* Same status gutter as a live card, so a queued task lines up with its
				    neighbours; the dashed ring reads as "not real work yet". */}
				<span
					data-card-status-glyph="todo"
					className="flex w-[18px] shrink-0 justify-center pt-px"
					style={{ color: col.dotVar }}
				>
					<CircleDashed className="h-[15px] w-[15px]" aria-hidden="true" />
				</span>
				<div className="min-w-0 flex-1">
					{/* The live card's status line, without its chips or menu - see SessionCard. */}
					<div className="flow-root pb-[7px] text-[12px] leading-[1.3] text-pretty">
						<CardAgentLabel provider={session.provider} />
						<span data-status="Queued" className="font-semibold" style={{ color: col.dotVar }}>
							Queued
						</span>
					</div>
					{/* Margin, not padding — see the note on the live card's title. */}
					<div className="mb-2 line-clamp-2 overflow-hidden text-[13px] font-medium leading-[1.42] tracking-[-0.01em] text-foreground">
						{session.title}
					</div>
					{showBranch && <div className="truncate pb-2.5 font-mono text-[10.5px] text-passive">{branchLabel}</div>}
				</div>
			</div>
			<div
				className="flex items-center justify-end px-[13px] py-2"
				style={{ borderTop: "1px solid var(--kanban-card-divider)" }}
				onClick={(event) => event.stopPropagation()}
			>
				<div className="inline-flex">
					<button
						type="button"
						disabled={start.isPending}
						onClick={() => start.mutate(undefined)}
						className="inline-flex items-center gap-1.5 rounded-l-[7px] px-3 py-1 text-[11.5px] font-semibold text-[#12121a] disabled:opacity-60"
						style={{ background: col.dotVar }}
					>
						{start.isPending ? (
							<span className="size-3 animate-spin rounded-full border-[1.5px] border-current border-t-transparent" />
						) : (
							<Play className="size-3" aria-hidden="true" />
						)}
						Start
					</button>
					<DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
						<DropdownMenuTrigger asChild>
							<button
								type="button"
								aria-label="Start with a specific agent"
								disabled={start.isPending}
								className="inline-flex w-6 items-center justify-center rounded-r-[7px] text-[#12121a] disabled:opacity-60"
								style={{ background: col.dotVar, borderLeft: "1px solid rgba(0,0,0,0.22)" }}
							>
								<ChevronDown className="size-3" aria-hidden="true" />
							</button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="w-48">
							<div className="px-2 py-1 font-mono text-[9.5px] font-semibold uppercase tracking-[0.07em] text-passive">
								Start with
							</div>
							{agentOptions.map((a) => (
								<DropdownMenuItem key={a.id} onSelect={() => start.mutate(a.id)}>
									<span className="truncate">{a.label}</span>
								</DropdownMenuItem>
							))}
						</DropdownMenuContent>
					</DropdownMenu>
				</div>
			</div>
			{error && <div className="px-[13px] pb-2 text-[11px] text-destructive">{error}</div>}
		</div>
	);
}

function SessionCard({
	task,
	gates,
	col,
	onOpen,
}: {
	task: Task;
	gates?: TaskGates;
	col: LaneConfig;
	onOpen: (s: WorkspaceSession) => void;
}) {
	const session = task.dev;
	// The gutter glyph is whichever member currently HOLDS THE BALL, and the
	// status text names the role when it is not dev - so `qa · Input needed` reads
	// as a fact about the task rather than as dev having stalled. On a solo task
	// the ball holder is the session itself and nothing changes.
	const lane = taskLane(task, gates ?? { review: "not run" });
	// The gutter keeps exactly ONE glyph, and it belongs to whoever holds the ball.
	// When the lane came from a fact about the TASK rather than about a member -
	// "the checklist is waiting for you", "nobody is working on this" - there is no
	// member to draw and the LANE's own shape stands in.
	const holderGlyph = lane.holder ? statusGlyph(lane.holder) : undefined;
	const Icon = holderGlyph?.Icon ?? col.Icon;
	const filled = holderGlyph?.filled ?? col.filled;
	const statusText = lane.note !== "" ? lane.note : (holderGlyph?.label ?? statusGlyph(session).label);
	// This member is paused while its crewmate works - which is a fact about ONE
	// member, not about the task, and the crew strip is where per-member facts go.
	const pausedWhileCrewmateWorks =
		task.isCrew &&
		session.isSuspended &&
		task.members.some((m) => m.id !== session.id && crewChipState(m) === "working");
	const prSummaries0 = useSessionScmSummary(session.id).data;
	const review = gates?.review ?? reviewGateState(prSummaries0 ?? []);
	// `+ qa` on a solo card. Shared with the session topbar's member switcher,
	// which offers the same affordance from the other end - one mutation, so the
	// two entry points cannot start a member two different ways.
	const addRole = useAddCrewRole(session);
	const addRoleError = addRole.error instanceof Error ? addRole.error.message : null;
	const issueId = canonicalTrackerIssueId(session.issueId);
	// A Jira-linked session gets the richer display-only Jira badge (KEY · type ·
	// status) below the branch instead of the raw provider-prefixed intake chip.
	const jiraKey = jiraKeyFromIssueId(session.issueId);
	const branch = session.branch || "";
	const showBranch = branch !== "" && !sameLabel(branch, session.title) && !sameLabel(branch, session.id);
	const prSummaries = sessionPRDisplaySummaries(session, prSummaries0);
	const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (event.currentTarget !== event.target) return;
		if (event.key !== "Enter" && event.key !== " ") return;
		event.preventDefault();
		onOpen(session);
	};
	return (
		<div
			className={cn(
				"group w-full overflow-hidden rounded-[10px] text-left transition-colors",
				// A suspended card stays in its real lane but reads as dormant.
				session.isSuspended && "opacity-80",
			)}
			style={{
				background: "var(--kanban-card-bg)",
				border: "1px solid var(--kanban-card-border)",
			}}
		>
			<div
				onClick={() => onOpen(session)}
				onKeyDown={handleKeyDown}
				role="button"
				tabIndex={0}
				className="flex gap-2 px-[13px] pt-3"
			>
				{/* Status gutter. The glyph is the silhouette of this card's EXACT
				    status, not its lane — so the four NEEDS YOU statuses stay four
				    distinct marks instead of one coral bar. Every card's glyph sits on
				    the same x, so a column scans as a vertical run of shapes. It is
				    decorative (aria-hidden): the status text beside it is the
				    accessible carrier, and colour is the third, redundant channel. */}
				<span
					data-card-status-glyph={(lane.holder ?? session).status}
					className="flex w-[18px] shrink-0 justify-center pt-px"
					style={{ color: col.dotVar }}
				>
					<Icon
						className={cn("h-[15px] w-[15px]", col.key === "working" && "animate-status-pulse")}
						style={filled ? { fill: "currentColor" } : undefined}
						aria-hidden="true"
					/>
				</span>
				<div className="min-w-0 flex-1">
					{/* Two lines with fixed jobs, never a wrap decided by width. Both earlier
					    layouts broke on a width: one shrinkable line squeezed the status to
					    3px ("C."), and one wrapping line dropped a shrink-0 cluster to a line
					    of its own - where "Claude" sat alone, or, carrying the 169px
					    undelivered chip, ran off the card.
					    Line 1 is the status, with the agent floated into its top-right
					    corner: the status wraps its own words AROUND it, so only its first
					    line gives up room and every later line has the full width. The agent
					    is what yields (capped, then an ellipsis), so it always shares the
					    status's first line and never stands alone.
					    Line 2 holds the chips - the wide items - and exists only when one
					    renders. A chip too wide even for that whole line wraps inside itself
					    (lib/chip-with-action). */}
					<div className="flow-root pb-[7px] text-[12px] leading-[1.3] text-pretty">
						<CardAgentLabel
							provider={session.provider}
							menu={<SessionCardMenu session={session} onOpenSession={() => onOpen(session)} />}
						/>
						<span data-status={statusText} className="font-semibold" style={{ color: col.dotVar }}>
							{statusText}
						</span>
					</div>
					<div data-card-chips className="@container -mt-0.5 flex flex-wrap items-start gap-1.5 pb-[7px] empty:hidden">
						{issueId && !jiraKey && (
							<span
								className="max-w-full truncate rounded-[4px] bg-[color-mix(in_srgb,var(--accent)_12%,transparent)] px-1.5 py-0.5 font-mono text-[10px] text-accent"
								title={`Intake issue: ${issueId}`}
							>
								{issueId}
							</span>
						)}
						<QueuedMessagesChip session={session} />
						{isMergeSuspended(session) ? (
							<MergeSuspendChip session={session} />
						) : isUndeliveredParked(session) ? (
							// Parked holding work nobody has seen. Its own chip, for the same
							// reason the merged one has one: "Paused - open to resume" is true
							// but says nothing about why this card will not move to Done.
							<UndeliveredWorkChip session={session} onOpenSession={() => onOpen(session)} />
						) : (
							// "Paused - open to resume" is a fact about a session, and on a
							// crew card it would be read as a fact about the TASK - which is
							// not paused at all while its other member is running. The crew
							// strip already says which member is asleep, in the place where
							// per-member facts live.
							!pausedWhileCrewmateWorks && <IdleStatusChip session={session} />
						)}
					</div>
					{/* MARGIN, not padding. Bottom padding on a `overflow-hidden` clamped
					    box is inside the clip region, so the clamped third line bleeds
					    into it as a sliver of half-height letters — visible on any title
					    long enough to wrap three times. Margin sits outside the clip. */}
					<div
						className={cn(
							"text-[13px] font-medium leading-[1.42] tracking-[-0.01em] text-foreground",
							showBranch || jiraKey ? "mb-2" : "mb-3",
							"line-clamp-2 overflow-hidden",
						)}
					>
						{session.title}
					</div>
					{showBranch && <div className="truncate pb-2.5 font-mono text-[10.5px] text-passive">{branch}</div>}
					{jiraKey && (
						<div className="pb-2.5">
							<JiraKeyBadge sessionId={session.id} issueKey={jiraKey} variant="card" />
						</div>
					)}
				</div>
			</div>
			<CrewStrip
				task={task}
				review={review}
				onOpenMember={onOpen}
				onOpenReviews={() => onOpen(session)}
				onAddRole={() => addRole.mutate()}
				addRolePending={addRole.isPending}
			/>
			{addRoleError && (
				<div className="px-[13px] pb-1.5 text-[10.5px] text-destructive" onClick={(event) => event.stopPropagation()}>
					{addRoleError}
				</div>
			)}
			<div
				className="flex items-start justify-between gap-2 px-[13px] py-2"
				style={{ borderTop: "1px solid var(--kanban-card-divider)" }}
				onClick={(event) => event.stopPropagation()}
			>
				<div className="min-w-0 flex-1 font-mono text-[10.5px] text-passive">
					{prSummaries.length === 0 ? (
						"no PR yet"
					) : (
						<div className="flex flex-col gap-1">
							{groupPRsByLifecycle(prSummaries).map((group) => (
								<BoardPRGroup group={group} key={group.status.label} />
							))}
						</div>
					)}
				</div>
				<TokenUsageChip usage={session.tokenUsage} />
			</div>
		</div>
	);
}

type BoardPRLifecycleStatus = { label: "closed" | "open" | "draft" | "merged"; className: string };
type BoardPRGroup = { status: BoardPRLifecycleStatus; prs: SessionPRSummary[] };

function BoardPRGroup({ group }: { group: BoardPRGroup }) {
	// A group is one lifecycle status within one session, so its PRs share a
	// provider in practice; label the kind from the first PR ("PR" / "MR").
	const kind = group.prs.length > 0 ? prKindLabel(group.prs[0].provider) : "PR";
	// Approval progress is a per-PR fact; surface it only for a single-PR group
	// with a known threshold, where it fits the compact footer unambiguously.
	const progress = group.prs.length === 1 ? approvalProgress(group.prs[0].review) : null;
	return (
		<span
			aria-label={`${group.prs.map((pr) => prRef(pr.provider, pr.number)).join(", ")} ${group.status.label}`}
			className="inline-flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1"
		>
			<span>{kind}</span>
			{group.prs.map((pr, index) => (
				<span key={pr.number}>
					<a
						className="text-passive underline-offset-2 transition-colors hover:text-foreground hover:underline"
						href={prBrowserUrl(pr)}
						rel="noreferrer"
						target="_blank"
					>
						{prRef(pr.provider, pr.number)}
					</a>
					{index < group.prs.length - 1 ? "," : null}
				</span>
			))}
			<span className={cn("font-medium", group.status.className)}>{group.status.label}</span>
			{progress?.required != null ? (
				<span className="inline-flex items-center gap-1">
					<ApprovalMeter progress={progress} />
					<span className={cn("font-medium", progress.met ? "text-success" : "text-passive")}>
						{progress.approved}/{progress.required}
					</span>
				</span>
			) : null}
		</span>
	);
}

function groupPRsByLifecycle(prs: SessionPRSummary[]): BoardPRGroup[] {
	const groups = new Map<BoardPRLifecycleStatus["label"], BoardPRGroup>();
	for (const pr of prs) {
		const status = prLifecycleStatus(pr);
		const group = groups.get(status.label);
		if (group) {
			group.prs.push(pr);
		} else {
			groups.set(status.label, { status, prs: [pr] });
		}
	}
	return Array.from(groups.values());
}

function prLifecycleStatus(pr: SessionPRSummary): BoardPRLifecycleStatus {
	if (pr.state === "draft") return { label: "draft", className: "text-passive" };
	if (pr.state === "merged") return { label: "merged", className: "text-accent" };
	if (pr.state === "closed") return { label: "closed", className: "text-error" };
	return { label: "open", className: "text-success" };
}

function sameLabel(a: string, b: string): boolean {
	const normalize = (value: string) =>
		value
			.toLowerCase()
			.replace(/^(feat|fix|chore|refactor|session)\//, "")
			.replace(/[^a-z0-9]+/g, "");
	return normalize(a) === normalize(b);
}

// The top-right corner of a card's status line: which agent, and the card's menu.
// It floats, so the status wraps around it rather than being held to a column
// beside it. Of everything on the line it is what yields: capped at 40% of the
// line and truncated past that, which leaves the status's first word room beside
// it at the narrowest lane - so it never ends up on a line of its own.
//
// The menu is a hover affordance, and it takes the agent's place rather than a
// slot of its own: both sit in one grid cell, and the name steps aside while the
// card is hovered, the menu is focused, or it is open. A permanent slot for an
// invisible button cost the status a quarter of a narrow card's line.
function CardAgentLabel({ provider, menu }: { provider: WorkspaceSession["provider"]; menu?: ReactNode }) {
	return (
		// One status line tall, so the name centres on the status's first line.
		<div className="float-right ml-2 grid h-[1.3em] max-w-[40%] grid-cols-[minmax(0,auto)] items-center justify-items-end *:[grid-area:1/1]">
			{menu}
			<span
				className={cn(
					"max-w-full truncate font-mono text-[10.5px] tracking-[0.04em] text-passive",
					menu && "group-hover:invisible peer-focus-visible:invisible peer-data-[state=open]:invisible",
				)}
			>
				{agentLabel(provider)}
			</span>
		</div>
	);
}

function agentLabel(provider: WorkspaceSession["provider"]): string {
	switch (provider) {
		case "claude-code":
			return "Claude";
		case "opencode":
			return "OpenCode";
		default:
			return provider;
	}
}
