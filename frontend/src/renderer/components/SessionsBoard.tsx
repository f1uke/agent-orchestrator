import { type KeyboardEvent, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import * as Dialog from "@radix-ui/react-dialog";
import { AlertTriangle, CircleCheck, MoreHorizontal, Plus, RotateCw, Trash2 } from "lucide-react";
import { useOverlayDismissFocus } from "../lib/overlay-focus";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { captureRendererEvent } from "../lib/telemetry";
import { DashboardSubhead } from "./DashboardSubhead";
import {
	type AttentionZone,
	type WorkspaceSession,
	attentionZone,
	canonicalTrackerIssueId,
	newestActiveOrchestrator,
	orchestratorHealth,
	primaryPR,
	workerSessions,
} from "../types/workspace";
import { useSessionScmSummary, type SessionPRSummary } from "../hooks/useSessionScmSummary";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { OrchestratorIcon } from "./icons";
import { NewTaskDialog } from "./NewTaskDialog";
import { Button } from "./ui/button";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { prBrowserUrl, prKindLabel, prRef, providerFromPRURL, sessionPRDisplaySummaries } from "../lib/pr-display";
import { type DoneDisposition, doneDisposition, formatMovedAgo, sortDoneRecentFirst } from "../lib/done-chip";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";

type SessionsBoardProps = {
	/** When set, the board shows only this project's sessions. */
	projectId?: string;
};

// The four kanban columns, left→right by flow (work → review → merge), ported
// verbatim from agent-orchestrator (SIMPLE_KANBAN_LEVELS + AttentionZone +
// mc-board.css). "done" is archived, not a column.
type Column = {
	level: AttentionZone;
	label: string;
	glow: string;
	dot: string;
	dotGlow: boolean;
	titleClass: string;
};
const COLUMNS: Column[] = [
	{
		level: "working",
		label: "Working",
		glow: "color-mix(in srgb, var(--orange) 7%, transparent)",
		dot: "var(--orange)",
		dotGlow: true,
		titleClass: "text-working",
	},
	{
		level: "action",
		label: "Needs you",
		glow: "color-mix(in srgb, var(--amber) 6%, transparent)",
		dot: "var(--amber)",
		dotGlow: true,
		titleClass: "text-warning",
	},
	{
		level: "pending",
		label: "In review",
		glow: "var(--kanban-pending-glow)",
		dot: "var(--fg-muted)",
		dotGlow: false,
		titleClass: "text-muted-foreground",
	},
	{
		level: "merge",
		label: "Ready to merge",
		glow: "color-mix(in srgb, var(--green) 7%, transparent)",
		dot: "var(--green)",
		dotGlow: true,
		titleClass: "text-success",
	},
];

export function SessionsBoard({ projectId }: SessionsBoardProps) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	const all = workspaceQuery.data ?? [];
	const workspaces = projectId ? all.filter((w) => w.id === projectId) : all;
	const workspace = projectId ? workspaces[0] : undefined;
	const sessions = workspaces.flatMap((w) => workerSessions(w.sessions));
	const orchestrator = projectId ? newestActiveOrchestrator(workspaces[0]?.sessions ?? []) : undefined;
	const [isNewTaskOpen, setIsNewTaskOpen] = useState(false);
	const [isSpawning, setIsSpawning] = useState(false);
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

	const openOrchestrator = async () => {
		if (!projectId || isProjectRestarting) return;
		if (orchestrator) {
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId: orchestrator.id },
			});
			return;
		}
		setIsSpawning(true);
		try {
			const sessionId = await spawnOrchestrator(projectId, "board");
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId },
			});
		} finally {
			setIsSpawning(false);
		}
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

	const handleTaskCreated = async (sessionId: string) => {
		if (!projectId) return;
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		void navigate({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId, sessionId },
		});
	};

	const actions = projectId ? (
		<>
			<button
				aria-label="New task"
				className="dashboard-app-header__accent-btn"
				disabled={isProjectRestarting}
				onClick={() => setIsNewTaskOpen(true)}
				type="button"
			>
				<Plus className="h-3.5 w-3.5" aria-hidden="true" />
				New task
			</button>
			<button
				aria-label={orchestrator ? "Orchestrator" : "Spawn Orchestrator"}
				className="dashboard-app-header__primary-btn"
				disabled={isSpawning || isProjectRestarting}
				onClick={() => void openOrchestrator()}
				type="button"
			>
				<OrchestratorIcon className="h-3.5 w-3.5" aria-hidden="true" />
				{isProjectRestarting
					? "Restarting..."
					: isSpawning
						? "Spawning..."
						: orchestrator
							? "Orchestrator"
							: "Spawn Orchestrator"}
			</button>
		</>
	) : undefined;

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<DashboardSubhead
				title="Board"
				subtitle="Live agent sessions flowing from work → review → merge."
				actions={actions}
			/>

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
					<div className="grid h-full grid-cols-4 gap-2">
						{COLUMNS.map((col) => (
							<ZoneColumn key={col.level} col={col} sessions={byZone.get(col.level) ?? []} onOpen={openSession} />
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
			<NewTaskDialog
				open={isNewTaskOpen}
				projectId={projectId}
				onCreated={(sessionId) => void handleTaskCreated(sessionId)}
				onOpenChange={setIsNewTaskOpen}
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
}: {
	col: Column;
	sessions: WorkspaceSession[];
	onOpen: (s: WorkspaceSession) => void;
}) {
	return (
		<section
			className="flex min-w-0 flex-col overflow-hidden rounded-[13px]"
			style={{ background: `linear-gradient(180deg, ${col.glow}, transparent 130px), var(--kanban-column-bg)` }}
		>
			<div className="flex shrink-0 items-center gap-[9px] px-[15px] pb-[11px] pt-[14px]">
				<span
					className="h-[7px] w-[7px] rounded-full"
					style={{
						background: col.dot,
						boxShadow: col.dotGlow ? `0 0 7px color-mix(in srgb, ${col.dot} 60%, transparent)` : undefined,
					}}
				/>
				<span className={cn("text-[11px] font-semibold uppercase tracking-[0.08em]", col.titleClass)}>{col.label}</span>
				<span className="ml-auto font-mono text-[11px] leading-none text-passive">{sessions.length}</span>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto px-[11px] pb-3">
				<div className="flex flex-col gap-2.5">
					{sessions.map((session) => (
						<SessionCard key={session.id} session={session} onOpen={() => onOpen(session)} />
					))}
				</div>
			</div>
		</section>
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

function SessionCard({ session, onOpen }: { session: WorkspaceSession; onOpen: () => void }) {
	const badge = sessionBadge(session);
	const issueId = canonicalTrackerIssueId(session.issueId);
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
		<div className="group w-full rounded-[7px] border border-border bg-surface text-left transition-colors hover:border-border-strong">
			<div onClick={onOpen} onKeyDown={handleKeyDown} role="button" tabIndex={0}>
				<div className="flex items-center gap-2 px-[13px] pb-[9px] pt-3">
					<span className={cn("inline-flex items-center gap-1.5 text-[11px] font-medium", badge.className)}>
						<span className={cn("h-[7px] w-[7px] rounded-full bg-current")} />
						{badge.label}
					</span>
					{issueId && (
						<span
							className="inline-flex max-w-[13rem] items-center truncate rounded-[4px] bg-[color-mix(in_srgb,var(--accent)_12%,transparent)] px-1.5 py-0.5 font-mono text-[10px] text-accent"
							title={`Intake issue: ${issueId}`}
						>
							{issueId}
						</span>
					)}
					<div className="ml-auto flex shrink-0 items-center gap-1.5">
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
			</div>
			<div
				className="border-t border-border px-[13px] py-2 font-mono text-[10.5px] text-passive"
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
