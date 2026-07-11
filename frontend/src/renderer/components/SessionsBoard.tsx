import { type KeyboardEvent, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import * as Dialog from "@radix-ui/react-dialog";
import { AlertTriangle, ChevronDown, CircleCheck, MoreHorizontal, Play, RotateCw, Trash2 } from "lucide-react";
import { useOverlayDismissFocus } from "../lib/overlay-focus";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { captureRendererEvent } from "../lib/telemetry";
import { DashboardSubhead } from "./DashboardSubhead";
import {
	type AttentionZone,
	type WorkspaceSession,
	attentionZone,
	canonicalTrackerIssueId,
	jiraKeyFromIssueId,
	orchestratorHealth,
	primaryPR,
	workerSessions,
} from "../types/workspace";
import { JiraKeyBadge } from "./JiraKeyBadge";
import { useSessionScmSummary, type SessionPRSummary } from "../hooks/useSessionScmSummary";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { TodoDetailDialog } from "./TodoDetailDialog";
import { IdleStatusChip } from "./IdleStatusChip";
import { useAgentsQuery } from "../hooks/useAgentsQuery";
import { Button } from "./ui/button";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { prBrowserUrl, prKindLabel, prRef, providerFromPRURL, sessionPRDisplaySummaries } from "../lib/pr-display";
import { type DoneDisposition, doneDisposition, formatMovedAgo, sortDoneRecentFirst } from "../lib/done-chip";
import { LANE_ORDER, LANES, type LaneConfig } from "../lib/lane-indicator";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";

type SessionsBoardProps = {
	/** When set, the board shows only this project's sessions. */
	projectId?: string;
};

// The four kanban lanes, left→right by flow (work → review → merge). Each lane
// owns one hue in the 4-color semantic system (see lib/lane-indicator +
// design handoff Board.dc.html); "done" is archived in the Done bar, not a lane.
const COLUMNS: LaneConfig[] = LANE_ORDER.map((key) => LANES[key]);

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
	const isProjectRestarting = projectId ? restartingProjectIds.has(projectId) : false;
	const health = workspace ? orchestratorHealth(workspace, isProjectRestarting) : { state: "ok" as const };

	const byZone = new Map<AttentionZone, WorkspaceSession[]>();
	for (const session of sessions) {
		const zone = attentionZone(session);
		(byZone.get(zone) ?? byZone.set(zone, []).get(zone)!).push(session);
	}
	// Most-recently-moved first, so the session just archived sits at the front.
	const done = sortDoneRecentFirst(byZone.get("done") ?? []);
	// Collapsed by default, like agent-orchestrator's done-bar: finished and
	// killed sessions cost one quiet line under the board until expanded.
	const [doneExpanded, setDoneExpanded] = useState(false);

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
					<div className="grid h-full grid-cols-5 gap-2">
						{COLUMNS.map((col) => (
							<ZoneColumn
								key={col.key}
								col={col}
								sessions={byZone.get(col.key) ?? []}
								onOpen={col.key === "todo" ? openTodo : openSession}
								onStarted={handleTodoStarted}
							/>
						))}
					</div>
				)}
			</div>

			{done.length > 0 && (
				<div className="shrink-0 border-t border-border px-[18px]">
					{/* agent-orchestrator's done-bar (Dashboard.tsx + globals.css):
					    a full-width chevron + label + count toggle row. min-h matches
					    the sidebar footer (7px pad ×2 + 37px Settings button) so this
					    border-t aligns with the sidebar's footer border. The button is
					    37px (not the 35.5px its text-[13px] implies) because the
					    unlayered `button { font: inherit }` in styles.css outranks
					    Tailwind's layered text utilities, leaving it at 14px/21px. */}
					<div className="flex min-h-[51px] w-full items-center gap-2 py-2">
						<button
							aria-expanded={doneExpanded}
							className="group flex flex-1 items-center gap-2 text-muted-foreground transition-colors hover:text-foreground"
							onClick={() => setDoneExpanded((v) => !v)}
							type="button"
						>
							<svg
								aria-hidden="true"
								className={cn("h-3 w-3 shrink-0 transition-transform duration-150", doneExpanded && "rotate-90")}
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
								viewBox="0 0 24 24"
							>
								<path d="m9 18 6-6-6-6" />
							</svg>
							<span className="font-mono text-[10.5px] font-medium uppercase tracking-[0.05em]">Done / Terminated</span>
							<span className="ml-auto shrink-0 font-mono text-[10px] text-passive">{done.length}</span>
						</button>
						{done.length > 0 && <ClearAllButton sessions={done} />}
					</div>
					{doneExpanded && (
						<div className="flex flex-wrap gap-2 pb-2.5 pt-1">
							{done.map((s) => (
								<DoneChip key={s.id} session={s} onOpen={() => openSession(s)} />
							))}
						</div>
					)}
				</div>
			)}
			<TodoDetailDialog
				session={todoDetail}
				onOpenChange={(open) => !open && setTodoDetail(null)}
				onStarted={handleTodoStarted}
			/>
		</div>
	);
}

// Done (merged) reads success-green; terminated (killed) reads passive-gray, so
// the two archive states are tellable apart at a glance. Dot + lowercase label
// mirrors the board card's status idiom.
const DONE_DISPOSITION: Record<DoneDisposition, { label: string; className: string }> = {
	done: { label: "done", className: "text-success" },
	terminated: { label: "terminated", className: "text-passive" },
};

// A finished/terminated session's chip in the done-bar. Deleting is
// permanent (unlike kill, which just stops a running worker), so it mirrors
// TopbarKillButton's inline arm-confirm rather than firing on a single click.
// Default force=false preserves an uncommitted worktree; a dirty-worktree
// refusal surfaces the daemon's error and offers "Delete anyway" (force=true)
// instead of silently discarding work.
function DoneChip({ session, onOpen }: { session: WorkspaceSession; onOpen: () => void }) {
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
	return (
		<div className="flex items-center gap-1 rounded-[7px] border border-border bg-surface pl-2.5 pr-1 py-1.5 transition-colors hover:border-border-strong">
			<div className="flex min-w-0 flex-col gap-0.5">
				<button className="text-left text-[12px] text-muted-foreground" onClick={onOpen} type="button">
					{session.title}
				</button>
				{/* Second line: how it finished (done vs terminated) + when it moved here. */}
				<span className="flex items-center gap-1 text-[10px] leading-none">
					<span className={cn("inline-flex items-center gap-1 font-medium", disposition.className)}>
						<span className="h-[5px] w-[5px] rounded-full bg-current" aria-hidden="true" />
						{disposition.label}
					</span>
					<span className="text-passive" aria-hidden="true">
						·
					</span>
					<span className="font-mono text-passive">{formatMovedAgo(session.updatedAt)}</span>
				</span>
			</div>
			{confirming ? (
				<>
					<button
						aria-label="Confirm delete"
						className="text-[11px] text-error"
						disabled={del.isPending}
						onClick={() => del.mutate(false)}
						type="button"
					>
						Confirm
					</button>
					<button
						aria-label="Cancel delete"
						className="text-[11px] text-passive"
						onClick={() => {
							setConfirming(false);
							setError(null);
						}}
						type="button"
					>
						Cancel
					</button>
				</>
			) : (
				<>
					<button
						aria-label="Reopen session"
						title="Reopen session"
						className="rounded p-1 text-passive hover:text-foreground"
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
						className="rounded p-1 text-passive hover:text-error"
						onClick={() => {
							setError(null);
							setConfirming(true);
						}}
						type="button"
					>
						<Trash2 className="h-3 w-3" aria-hidden="true" />
					</button>
				</>
			)}
			{error && (
				<span className="flex items-center gap-1 text-[10px] text-error">
					{error}
					{/* A dirty-worktree refusal (SESSION_WORKSPACE_DIRTY) is the expected
					    reason; offer a force delete that discards uncommitted changes. */}
					<button
						aria-label="Delete anyway"
						className="underline hover:text-error"
						disabled={del.isPending}
						onClick={() => del.mutate(true)}
						type="button"
					>
						Delete anyway
					</button>
				</span>
			)}
			{reopenError && (
				<span className="text-[10px] text-error" role="alert">
					Couldn’t reopen: {reopenError}
				</span>
			)}
		</div>
	);
}

// "Clear all" empties the whole done bucket. There is no bulk-delete endpoint,
// so this fires one DELETE per session (force=false, same as a single chip) and
// reports how many failed rather than partially retrying. Confirmed via a
// Radix dialog (mirrors RestoreUnavailableDialog) since it is destructive and
// scoped to N sessions rather than one.
function ClearAllButton({ sessions }: { sessions: WorkspaceSession[] }) {
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
		// drop out of the done-bar instead of lingering as stale rows until the
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
				aria-label="Clear all"
				className="shrink-0 font-mono text-[10px] text-passive hover:text-error"
				onClick={(e) => {
					e.stopPropagation();
					setError(null);
					setOpen(true);
				}}
				type="button"
			>
				Clear all
			</button>
			<Dialog.Root open={open} onOpenChange={setOpen}>
				<Dialog.Portal>
					<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
					<Dialog.Content
						{...dismissFocus}
						className="fixed left-1/2 top-1/2 z-50 w-[420px] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-5 shadow-lg"
					>
						<Dialog.Title className="text-sm font-medium text-foreground">Clear all finished sessions</Dialog.Title>
						<Dialog.Description className="mt-2 text-[13px] text-muted-foreground">
							Permanently remove {sessions.length} finished session(s) from AO. Their git branches are kept.
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
	sessions,
	onOpen,
	onStarted,
}: {
	col: LaneConfig;
	sessions: WorkspaceSession[];
	onOpen: (s: WorkspaceSession) => void;
	onStarted: (sessionId: string) => void;
}) {
	const isTodo = col.key === "todo";
	const { hueVar, dotVar } = col;
	return (
		<section
			className="flex min-w-0 flex-col overflow-hidden rounded-[12px]"
			style={{
				// The lane's hue lands in five places (top border, top-down tint,
				// header dot+label, count badge, and each card's left accent) so it is
				// unmistakable. The tint fades out by ~240px so long lanes go dark.
				border: "1px solid var(--kanban-col-border)",
				borderTop: `3px solid ${hueVar}`,
				background: `linear-gradient(180deg, color-mix(in srgb, ${hueVar} 11%, transparent) 0%, transparent 240px), var(--kanban-column-bg)`,
			}}
		>
			<div className="flex shrink-0 items-center gap-[9px] px-[15px] pb-[11px] pt-[13px]">
				<span
					className="h-[9px] w-[9px] shrink-0 rounded-full"
					style={{ background: dotVar, boxShadow: `0 0 10px color-mix(in srgb, ${dotVar} 70%, transparent)` }}
				/>
				<span className="text-[11.5px] font-bold uppercase tracking-[0.09em]" style={{ color: dotVar }}>
					{col.label}
				</span>
				<span
					className="ml-auto min-w-[22px] rounded-full px-[9px] py-px text-center font-mono text-[11px] font-bold leading-[1.5]"
					style={{
						color: dotVar,
						background: `color-mix(in srgb, ${hueVar} 16%, transparent)`,
						border: `1px solid color-mix(in srgb, ${hueVar} 34%, transparent)`,
					}}
				>
					{sessions.length}
				</span>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto px-[11px] pb-3">
				{sessions.length === 0 ? (
					<EmptyLane col={col} />
				) : (
					<div className="flex flex-col gap-2.5">
						{sessions.map((session) =>
							isTodo ? (
								<TodoCard
									key={session.id}
									session={session}
									col={col}
									onOpen={() => onOpen(session)}
									onStarted={onStarted}
								/>
							) : (
								<SessionCard key={session.id} session={session} col={col} onOpen={() => onOpen(session)} />
							),
						)}
					</div>
				)}
			</div>
		</section>
	);
}

// The quiet placeholder shown in a lane with no cards. It deliberately carries
// NO lane hue and no filled surface — a faint neutral dashed hairline plus
// passive, low-contrast text, faded further with opacity — so an empty lane
// reads as "nothing here" filler and recedes, letting a single real card (solid
// surface, bright lane-coloured dot, full-strength title) clearly dominate.
function EmptyLane({ col }: { col: LaneConfig }) {
	const { Icon } = col;
	return (
		<div className="mt-2 flex flex-col items-center justify-center gap-2 rounded-[10px] border border-dashed border-border px-4 py-[34px] text-center text-[12px] text-passive opacity-60">
			<Icon
				className="h-[15px] w-[15px]"
				style={col.filled ? { fill: "currentColor" } : undefined}
				aria-hidden="true"
			/>
			<span>{col.emptyText}</span>
		</div>
	);
}

// A board card's overflow menu. Its single action, "Move to Done", terminates the
// session via POST /sessions/{id}/kill — the same terminate + reclaim-worktree path
// as the topbar Kill (ShellTopbar's TopbarKillButton), reached from the board so a
// no-PR session (e.g. an investigation) can be finished without opening it. Kill keeps
// the git branch and preserves an uncommitted worktree, so the session stays restorable
// via the Done bar's Reopen (the sole reversal affordance — this menu has no reopen).
// Terminating is destructive, so the item arms a one-step confirm inside the menu
// before firing.
function SessionCardMenu({ session }: { session: WorkspaceSession }) {
	const queryClient = useQueryClient();
	const [open, setOpen] = useState(false);
	const [confirming, setConfirming] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const kill = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.session_kill_requested", { project_id: session.workspaceId });
			const { error: apiError } = await apiClient.POST("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: session.id } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
		},
		onSuccess: () => {
			void captureRendererEvent("ao.renderer.session_kill_succeeded", { project_id: session.workspaceId });
			// The session flips to terminated on the next refresh, so its card leaves this
			// column for the Done bar and this menu unmounts with it.
			setOpen(false);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
		onError: (e) => {
			void captureRendererEvent("ao.renderer.session_kill_failed", { project_id: session.workspaceId });
			setError(e instanceof Error ? e.message : "Move to Done failed");
		},
	});

	return (
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
						"rounded p-0.5 text-passive opacity-0 transition-opacity hover:text-foreground",
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
							Stops the agent and reclaims its worktree. The branch and any open PR stay. Reopen from the Done bar to
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
				)}
			</DropdownMenuContent>
		</DropdownMenu>
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
				borderLeft: `3px solid ${col.hueVar}`,
			}}
		>
			<div onClick={onOpen} onKeyDown={handleKeyDown} role="button" tabIndex={0} className="cursor-pointer">
				<div className="flex items-center gap-2 px-[13px] pb-[9px] pt-3">
					<span className="inline-flex items-center gap-1.5 text-[12px] font-semibold" style={{ color: col.dotVar }}>
						{/* Ring (not filled) dot: queued, not live. */}
						<span className="size-2 shrink-0 rounded-full border-[1.5px]" style={{ borderColor: col.dotVar }} />
						Queued
					</span>
					<span className="ml-auto font-mono text-[10.5px] tracking-[0.04em] text-passive">
						{agentLabel(session.provider)}
					</span>
				</div>
				<div className="line-clamp-2 overflow-hidden px-[13px] pb-2 text-[13px] font-medium leading-[1.42] tracking-[-0.01em] text-foreground">
					{session.title}
				</div>
				{showBranch && <div className="px-[13px] pb-2.5 font-mono text-[10.5px] text-passive">{branchLabel}</div>}
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

function SessionCard({ session, col, onOpen }: { session: WorkspaceSession; col: LaneConfig; onOpen: () => void }) {
	const badge = sessionBadge(session);
	const issueId = canonicalTrackerIssueId(session.issueId);
	// A Jira-linked session gets the richer display-only Jira badge (KEY · type ·
	// status) below the branch instead of the raw provider-prefixed intake chip.
	const jiraKey = jiraKeyFromIssueId(session.issueId);
	const branch = session.branch || "";
	const showBranch = branch !== "" && !sameLabel(branch, session.title) && !sameLabel(branch, session.id);
	const prSummaries = sessionPRDisplaySummaries(session, useSessionScmSummary(session.id).data);
	const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (event.currentTarget !== event.target) return;
		if (event.key !== "Enter" && event.key !== " ") return;
		event.preventDefault();
		onOpen();
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
				// The lane hue repeats on the card's left edge so a card is tied to its
				// lane even once scrolled away from the column header.
				borderLeft: `3px solid ${col.hueVar}`,
			}}
		>
			<div onClick={onOpen} onKeyDown={handleKeyDown} role="button" tabIndex={0}>
				<div className="flex items-center gap-2 px-[13px] pb-[9px] pt-3">
					{/* Status dot + label take the lane's colour so every card in a lane
					    reads as one status group; the dot pulses only for genuinely-live
					    WORKING sessions (per DESIGN.md motion). */}
					<span className="inline-flex items-center gap-1.5 text-[12px] font-semibold" style={{ color: col.dotVar }}>
						<span
							className={cn("h-2 w-2 shrink-0 rounded-full", col.key === "working" && "animate-status-pulse")}
							style={{
								background: col.dotVar,
								boxShadow: `0 0 8px color-mix(in srgb, ${col.dotVar} 65%, transparent)`,
							}}
						/>
						{badge.label}
					</span>
					{issueId && !jiraKey && (
						<span
							className="inline-flex max-w-[13rem] items-center truncate rounded-[4px] bg-[color-mix(in_srgb,var(--accent)_12%,transparent)] px-1.5 py-0.5 font-mono text-[10px] text-accent"
							title={`Intake issue: ${issueId}`}
						>
							{issueId}
						</span>
					)}
					<div className="ml-auto flex shrink-0 items-center gap-1.5">
						<IdleStatusChip session={session} />
						<span className="font-mono text-[10.5px] tracking-[0.04em] text-passive">
							{agentLabel(session.provider)}
						</span>
						<SessionCardMenu session={session} />
					</div>
				</div>
				<div
					className={cn(
						"px-[13px] text-[13px] font-medium leading-[1.42] tracking-[-0.01em] text-foreground",
						showBranch ? "pb-2" : "pb-3",
						"line-clamp-2 overflow-hidden",
					)}
				>
					{session.title}
				</div>
				{showBranch && <div className="px-[13px] pb-2.5 font-mono text-[10.5px] text-passive">{branch}</div>}
				{jiraKey && (
					<div className="px-[13px] pb-2.5">
						<JiraKeyBadge sessionId={session.id} issueKey={jiraKey} variant="card" />
					</div>
				)}
			</div>
			<div
				className="px-[13px] py-2 font-mono text-[10.5px] text-passive"
				style={{
					borderTop: "1px solid var(--kanban-card-divider)",
					background: `color-mix(in srgb, ${col.hueVar} 3%, transparent)`,
				}}
				onClick={(event) => event.stopPropagation()}
			>
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
		</div>
	);
}

type BoardPRLifecycleStatus = { label: "closed" | "open" | "draft" | "merged"; className: string };
type BoardPRGroup = { status: BoardPRLifecycleStatus; prs: SessionPRSummary[] };

function BoardPRGroup({ group }: { group: BoardPRGroup }) {
	// A group is one lifecycle status within one session, so its PRs share a
	// provider in practice; label the kind from the first PR ("PR" / "MR").
	const kind = group.prs.length > 0 ? prKindLabel(group.prs[0].provider) : "PR";
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

function sessionBadge(session: WorkspaceSession): { label: string; className: string } {
	// "PR"/"MR" follows the session's primary change request; other statuses are
	// provider-neutral.
	const kind = prKindLabel(providerFromPRURL(primaryPR(session)?.url));
	switch (session.status) {
		case "needs_input":
			return { label: "Input needed", className: "text-warning" };
		case "no_signal":
			return { label: "No signal", className: "text-passive" };
		case "ci_failed":
			return { label: "CI failed", className: "text-error" };
		case "changes_requested":
			return { label: "Changes requested", className: "text-warning" };
		case "review_pending":
			return { label: "Review pending", className: "text-muted-foreground" };
		case "draft":
			return { label: `Draft ${kind}`, className: "text-muted-foreground" };
		case "pr_open":
			return { label: `${kind} open`, className: "text-muted-foreground" };
		case "approved":
			return { label: "Approved", className: "text-success" };
		case "mergeable":
			return { label: "Ready", className: "text-success" };
		case "merged":
			return { label: "Merged", className: "text-passive" };
		case "terminated":
			return { label: "Terminated", className: "text-passive" };
		default:
			return { label: "Working", className: "text-working" };
	}
}
