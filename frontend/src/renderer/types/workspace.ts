export type SessionStatus =
	| "todo"
	| "working"
	| "pr_open"
	| "draft"
	| "ci_failed"
	| "review_pending"
	| "changes_requested"
	| "approved"
	| "mergeable"
	| "merged"
	| "needs_input"
	| "no_signal"
	| "idle"
	| "terminated"
	| "unknown";

const sessionStatuses = new Set<SessionStatus>([
	"todo",
	"working",
	"pr_open",
	"draft",
	"ci_failed",
	"review_pending",
	"changes_requested",
	"approved",
	"mergeable",
	"merged",
	"needs_input",
	"no_signal",
	"idle",
	"terminated",
]);

export function toSessionStatus(status?: string, isTerminated = false): SessionStatus {
	if (status && sessionStatuses.has(status as SessionStatus)) return status as SessionStatus;
	return isTerminated ? "terminated" : "unknown";
}

export type SessionActivityState = "active" | "idle" | "waiting_input" | "exited" | "unknown";

const sessionActivityStates = new Set<SessionActivityState>(["active", "idle", "waiting_input", "exited"]);

export type SessionActivity = {
	state: SessionActivityState;
	lastActivityAt: string;
};

export function toSessionActivity(
	activity?: { state?: string; lastActivityAt?: string } | null,
): SessionActivity | undefined {
	if (!activity) return undefined;
	const state = sessionActivityStates.has(activity.state as SessionActivityState)
		? (activity.state as SessionActivityState)
		: "unknown";
	return {
		state,
		lastActivityAt: activity.lastActivityAt ?? "",
	};
}

export type StatusReason =
	| "working"
	| "waiting_input"
	| "active_stale"
	| "idle_aged"
	| "idle"
	| "no_signal"
	| "pr_pipeline"
	| "terminated"
	| "merged"
	| "unknown";

const statusReasons = new Set<StatusReason>([
	"working",
	"waiting_input",
	"active_stale",
	"idle_aged",
	"idle",
	"no_signal",
	"pr_pipeline",
	"terminated",
	"merged",
]);

/** Normalizes the daemon's reason code; undefined when absent (e.g. mock data). */
export function toStatusReason(reason?: string): StatusReason | undefined {
	if (!reason) return undefined;
	return statusReasons.has(reason as StatusReason) ? (reason as StatusReason) : "unknown";
}

/** Plain-language explanation of WHY a session shows its current status. */
export const statusReasonLabel: Record<StatusReason, string> = {
	working: "Agent active",
	waiting_input: "Agent requested input",
	active_stale: "No activity for a while — assumed waiting (a turn's Stop hook may have been lost)",
	idle_aged: "Turn ended and went quiet — assumed waiting",
	idle: "Recently active",
	no_signal: "No hook has reported since launch",
	pr_pipeline: "Status from the pull request pipeline",
	terminated: "Session ended",
	merged: "Work merged",
	unknown: "",
};

// Only timeout-based readings count down, and only ever to these targets.
const transitionTargetLabel: Partial<Record<SessionStatus, string>> = {
	needs_input: "Needs input",
	no_signal: "No signal",
};

/** Compact human duration for a countdown, e.g. "45s", "4m", "2h", "3d". */
export function formatCountdown(ms: number): string {
	// floor, not round: a countdown must never name a unit the clock hasn't
	// reached (59s should read "59s", not "1m").
	const s = Math.floor(ms / 1000);
	if (s < 60) return `${s}s`;
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m`;
	const h = Math.floor(m / 60);
	if (h < 24) return `${h}h`;
	return `${Math.floor(h / 24)}d`;
}

/**
 * Countdown caption to the next status flip (e.g. "→ Needs input in 4m"), or ""
 * when there is no pending timed transition or it is already due. `now` (ms since
 * epoch) is passed in so the function stays pure and testable.
 */
export function formatNextTransition(
	session: Pick<WorkspaceSession, "nextTransitionAt" | "nextTransitionTo">,
	now: number,
): string {
	if (!session.nextTransitionAt || !session.nextTransitionTo) return "";
	const target = transitionTargetLabel[session.nextTransitionTo];
	if (!target) return "";
	const ms = Date.parse(session.nextTransitionAt) - now;
	if (Number.isNaN(ms) || ms <= 0) return "";
	return `→ ${target} in ${formatCountdown(ms)}`;
}

/** Escalating urgency of an approaching idle-suspend, most urgent last. */
export type IdleCountdownLevel = "soon" | "urgent" | "imminent";

export type IdleCountdown = {
	/** Milliseconds until the idle sweep suspends the session (> 0). */
	remainingMs: number;
	/** Escalation bucket driving the chip's prominence/colour. */
	level: IdleCountdownLevel;
	/** Compact label, e.g. "58m", "9m", "45s". */
	label: string;
};

/** Show the idle-suspend countdown only within this window of the deadline. */
export const IDLE_COUNTDOWN_THRESHOLD_MS = 24 * 60 * 60 * 1000; // 1d
const IDLE_URGENT_MS = 6 * 60 * 60 * 1000; // 6h
const IDLE_IMMINENT_MS = 60 * 60 * 1000; // 1h

/**
 * The idle-suspend countdown to render for a session, or null when nothing
 * should show. Deliberately surfaced only NEAR expiry (≤ {@link
 * IDLE_COUNTDOWN_THRESHOLD_MS}) so a session far from suspension is not noise, and
 * it ESCALATES as the deadline nears: "soon" (≤1d) → "urgent" (≤6h) → "imminent"
 * (≤1h). A suspended session shows a "paused — click to resume" affordance instead
 * of a countdown, so this returns null for it. `now` (ms since epoch) is injected
 * so the function stays pure and testable.
 */
export function idleCountdown(
	session: Pick<WorkspaceSession, "idleCloseAt" | "isSuspended">,
	now: number,
): IdleCountdown | null {
	if (session.isSuspended || !session.idleCloseAt) return null;
	const deadline = Date.parse(session.idleCloseAt);
	if (Number.isNaN(deadline)) return null;
	const remainingMs = deadline - now;
	if (remainingMs <= 0 || remainingMs > IDLE_COUNTDOWN_THRESHOLD_MS) return null;
	const level: IdleCountdownLevel =
		remainingMs <= IDLE_IMMINENT_MS ? "imminent" : remainingMs <= IDLE_URGENT_MS ? "urgent" : "soon";
	return { remainingMs, level, label: formatCountdown(remainingMs) };
}

export type AgentProvider =
	| "codex"
	| "claude-code"
	| "opencode"
	| "aider"
	| "grok"
	| "droid"
	| "amp"
	| "agy"
	| "crush"
	| "cursor"
	| "qwen"
	| "copilot"
	| "goose"
	| "auggie"
	| "continue"
	| "devin"
	| "cline"
	| "kimi"
	| "kiro"
	| "kilocode"
	| "vibe"
	| "pi"
	| "autohand";

/** A file in a worker's worktree diff (drives the Git review rail). */
export type ChangedFile = {
	path: string;
	additions: number;
	deletions: number;
	staged?: boolean;
};

export type SessionKind = "worker" | "orchestrator";

/** Lifecycle state of a single pull request, mirrors the daemon's enum. */
export type PRState = "open" | "draft" | "merged" | "closed";

/**
 * One attributed pull request, mirroring the daemon's SessionPRFacts wire shape.
 * A session can own many (e.g. a stack), so {@link WorkspaceSession.prs} is a
 * list. The wire carries no source/target branch or parent pointer, so the UI
 * renders a flat list of PRs, not a stack tree.
 */
export type PullRequestFacts = {
	url: string;
	number: number;
	state: PRState;
	ci: string;
	review: string;
	mergeability: string;
	reviewComments: boolean;
	updatedAt: string;
};

export type WorkspaceSession = {
	id: string;
	terminalHandleId?: string;
	workspaceId: string;
	workspaceName: string;
	title: string;
	/** Raw issue/task identifier from the daemon. Intake ids are provider-prefixed. */
	issueId?: string;
	provider: AgentProvider;
	kind?: SessionKind;
	branch: string;
	/**
	 * The session's working directory on disk — the git worktree for a worker,
	 * the project root for an orchestrator. Drives the terminal's "Open in…" menu.
	 * Absent when the daemon did not report it (fall back to the project root).
	 */
	workspacePath?: string;
	status: SessionStatus;
	/** Machine reason for the current {@link status}, derived by the daemon. */
	statusReason?: StatusReason;
	/** ISO timestamp when the current timeout-based status will flip, if pending. */
	nextTransitionAt?: string;
	/** What {@link status} becomes at {@link nextTransitionAt} (needs_input / no_signal). */
	nextTransitionTo?: SessionStatus;
	/** ISO timestamp from the daemon — used for relative time in the inspector. */
	createdAt?: string;
	/** ISO timestamp from the daemon. */
	updatedAt: string;
	/** Raw agent lifecycle activity from the daemon. */
	activity?: SessionActivity;
	/**
	 * Live preview target set by the daemon (via `ao preview`) and streamed over
	 * CDC. When non-empty, the browser panel opens and navigates here.
	 */
	previewUrl?: string;
	/**
	 * Monotonic counter the daemon bumps on every `ao preview` call (even when
	 * previewUrl is unchanged), so the browser panel can re-navigate / refresh on
	 * a repeated preview of the same target.
	 */
	previewRevision?: number;
	/** The session's git diff against its base, when known. */
	changedFiles?: ChangedFile[];
	/** Pre-filled commit subject for the Git rail, when known. */
	commitMessage?: string;
	/**
	 * The session's attributed pull requests. One session can own many (a stack
	 * or independent PRs); empty when none are open yet. Status aggregation is
	 * done server-side, so {@link status} already reflects all of these.
	 */
	prs: PullRequestFacts[];
	/**
	 * Display status as derived by the daemon at read time. Optional override; when
	 * absent it is derived from {@link SessionStatus} via {@link workerDisplayStatus}.
	 */
	displayStatus?: WorkerDisplayStatus;
	/** True for a prepared-but-not-started TODO (status === "todo"). */
	isTodo?: boolean;
	/**
	 * The durable "this session has been terminated" fact from the daemon
	 * (`is_terminated`), independent of the DERIVED {@link status}. A Done session
	 * whose PR merged reads status `"merged"` yet is still terminated, so this — not
	 * the display status — is the correct gate for the "Restore session" affordance
	 * (it mirrors the backend `Restore` precondition and excludes suspended/keep-warm
	 * sessions, which are resumed by wake-on-open instead).
	 */
	isTerminated?: boolean;
	/**
	 * True when the idle sweep tore this session's tmux down to free resources
	 * while KEEPING it on the board in its current lane (worktree kept on disk).
	 * Orthogonal to {@link status} — the card stays in its real lane and this only
	 * drives a "paused — click to resume" affordance. Opening it resumes in place.
	 */
	isSuspended?: boolean;
	/**
	 * True when this worker is expected to open MORE PRs, so a PR merge SUSPENDS it
	 * in place (card stays on the board, resumable) instead of terminating it to
	 * Done (feature/merge-suspend-in-place). Opt-in per session via
	 * `ao spawn --keep-warm` or the board card toggle; default false.
	 */
	keepWarmOnMerge?: boolean;
	/**
	 * ISO timestamp when the idle sweep will suspend this live session if no
	 * further activity arrives (idle reference + the daemon's idle TTL). Absent
	 * when the sweep is disabled or the session is not a live suspend candidate
	 * (terminated / todo / already suspended). Drives the board/sidebar countdown.
	 */
	idleCloseAt?: string;
	/** The branch the worktree started from, and the branch this session's PR
	 * merges into. Both are recorded on every session at spawn; `prTarget` is
	 * additionally editable. `prTarget` is the STORED value — read
	 * `targetBranch` below to display a session's target, since that also
	 * accounts for an open PR and for sessions predating the stored value. */
	baseBranch?: string;
	prTarget?: string;
	/** The resolved branch this session's work merges into, and where that answer
	 * came from. Derived by the daemon on read: an open PR's real target wins,
	 * then the stored `prTarget`, then the session's base, then the project
	 * default. `targetBranch` is absent when nothing is known — render "not set",
	 * never a guessed "main". `targetSource` lets the UI distinguish a value the
	 * human set (`pr` / `session_pr_target`) from one merely inherited
	 * (`session_base` / `project`). */
	targetBranch?: string;
	targetSource?: "pr" | "session_pr_target" | "session_base" | "project";
	autoNameBranch?: boolean;
	createdBy?: string;
	prompt?: string;
	/**
	 * Per-session token telemetry the daemon summed from the harness transcript
	 * (claude-code only). Absent for agents without a readable transcript or a
	 * session not yet parsed — the card then shows no token chip.
	 */
	tokenUsage?: TokenUsage;
};

/**
 * Per-session token totals surfaced on the board. The four raw buckets + turn count
 * are measured facts; {@link TokenUsage.rawTotal}, {@link TokenUsage.costWeighted}
 * and {@link TokenUsage.runaway} are derived by the daemon (the cost multipliers and
 * runaway threshold live server-side so the UI never re-implements them).
 */
export type TokenUsage = {
	input: number;
	cacheCreation: number;
	cacheRead: number;
	output: number;
	turns: number;
	rawTotal: number;
	costWeighted: number;
	/** True when rawTotal crossed the daemon's runaway threshold (a stuck/looping session). */
	runaway: boolean;
	/** ISO timestamp of the last transcript parse. */
	updatedAt: string;
};

// Tracker providers whose ids sessions are stamped with, in "<provider>:<native>"
// form: intake stamps github:/gitlab:, and a manually linked Jira issue stamps
// jira:<KEY>. Adding a provider (Linear, …) later is just another prefix here —
// no caller of canonicalTrackerIssueId needs to change.
const TRACKER_PROVIDER_PREFIXES = ["github:", "gitlab:", "jira:"] as const;

/**
 * The provider-prefixed issue id if `issueId` came from tracker intake, or
 * undefined for manually created sessions (whose issueId, if any, is a plain
 * task title with no provider prefix).
 */
export function canonicalTrackerIssueId(issueId?: string): string | undefined {
	if (!issueId) return undefined;
	return TRACKER_PROVIDER_PREFIXES.some((prefix) => issueId.startsWith(prefix)) ? issueId : undefined;
}

const JIRA_ISSUE_PREFIX = "jira:";

/**
 * The bare Jira issue key (`DEMO-101`) for a Jira-bound session, or undefined when
 * the session is not linked to a Jira issue. Drives the board/sidebar Jira badge;
 * the key is known from the binding alone (no fetch), so the badge can render it
 * immediately and enrich type/status once the Jira context loads.
 */
export function jiraKeyFromIssueId(issueId?: string): string | undefined {
	if (!issueId || !issueId.startsWith(JIRA_ISSUE_PREFIX)) return undefined;
	const key = issueId.slice(JIRA_ISSUE_PREFIX.length);
	return key || undefined;
}

export type ProjectKind = "single_repo" | "workspace";

export type WorkspaceRepoSummary = {
	name: string;
	relativePath: string;
	repo: string;
};

/** Glanceable worker status. Maps 1:1 to the accent colors in DESIGN.md. */
export type WorkerDisplayStatus =
	"working" | "needs_you" | "mergeable" | "ci_failed" | "no_signal" | "done" | "unknown";

export function workerDisplayStatus(session: WorkspaceSession): WorkerDisplayStatus {
	if (session.displayStatus) return session.displayStatus;
	switch (session.status) {
		case "needs_input":
		case "changes_requested":
		case "review_pending":
			return "needs_you";
		case "ci_failed":
			return "ci_failed";
		case "no_signal":
			return "no_signal";
		case "approved":
		case "mergeable":
			return "mergeable";
		case "merged":
		case "terminated":
			return "done";
		case "unknown":
			return "unknown";
		default:
			return "working";
	}
}

// Open PRs (actionable) sort above merged/closed; ties break by number.
const prStateRank: Record<PRState, number> = { open: 0, draft: 1, merged: 2, closed: 3 };

/** A session's PRs ordered actionable-first (open, draft, merged, closed). */
export function sortedPRs(session: WorkspaceSession): PullRequestFacts[] {
	return [...session.prs].sort((a, b) => prStateRank[a.state] - prStateRank[b.state] || a.number - b.number);
}

/** PRs still in flight (open or draft). */
export function openPRs(session: WorkspaceSession): PullRequestFacts[] {
	return session.prs.filter((pr) => pr.state === "open" || pr.state === "draft");
}

export function mergedPRCount(session: WorkspaceSession): number {
	return session.prs.filter((pr) => pr.state === "merged").length;
}

/**
 * A worker SUSPENDED after its PR merged (feature/merge-suspend-in-place): its
 * card stays on the board (the daemon surfaces it as needs_input, not merged) with
 * a "Merged · Continue / Close" affordance instead of vanishing to Done. This is
 * exactly the backend completion bar — suspended AND every PR is terminal AND at
 * least one merged — so it never collides with an idle-suspended session (which
 * has no merged-completion state, hence shows the plain "Paused" chip).
 */
export function isMergeSuspended(session: WorkspaceSession): boolean {
	if (!session.isSuspended || session.prs.length === 0) return false;
	return (
		session.prs.every((pr) => pr.state === "merged" || pr.state === "closed") &&
		session.prs.some((pr) => pr.state === "merged")
	);
}

/** The merged PR to name in the merge-suspend chip: the highest (most recent) number. */
export function mergedSuspendPRNumber(session: WorkspaceSession): number | undefined {
	const merged = session.prs.filter((pr) => pr.state === "merged").map((pr) => pr.number);
	return merged.length ? Math.max(...merged) : undefined;
}

/** The highest-priority PR for compact one-line surfaces (board card, sidebar). */
export function primaryPR(session: WorkspaceSession): PullRequestFacts | undefined {
	return sortedPRs(session)[0];
}

export function isOrchestratorSession(session: WorkspaceSession): boolean {
	return session.kind === "orchestrator" || session.id.endsWith("-orchestrator");
}

/**
 * The project's LIVE orchestrator, if any. Terminated orchestrator rows stay in
 * the session list (the daemon returns all sessions, ordered by spawn number),
 * so an earlier dead orchestrator must not shadow a live one — its zellij
 * session is deleted and attaching to it dead-ends in an instant
 * "[process exited]". No live orchestrator → undefined, so the topbar offers
 * Spawn instead of navigating to a dead session.
 */
export function findProjectOrchestrator(
	workspaces: WorkspaceSummary[],
	projectId: string,
): WorkspaceSession | undefined {
	const workspace = workspaces.find((w) => w.id === projectId);
	return newestActiveOrchestrator(workspace?.sessions ?? []);
}

/**
 * Whether the sidebar should highlight `workspace`'s project row as the active
 * project, given the route's active project/session ids. True when the project's
 * board is open (project route, no session) OR when the open session is that
 * project's orchestrator — the orchestrator has no worker row of its own, so the
 * project row carries the same active highlight the board uses. A worker session
 * leaves the project row inactive (its own child row highlights instead).
 */
export function projectRowActive(
	workspace: Pick<WorkspaceSummary, "id" | "sessions">,
	activeProjectId: string | undefined,
	activeSessionId: string | undefined,
): boolean {
	if (activeProjectId !== workspace.id) return false;
	if (!activeSessionId) return true;
	return workspace.sessions.some((session) => session.id === activeSessionId && isOrchestratorSession(session));
}

export function newestActiveOrchestrator(sessions: WorkspaceSession[]): WorkspaceSession | undefined {
	const active = sessions.filter((session) => isOrchestratorSession(session) && sessionIsActive(session));
	return active.reduce<WorkspaceSession | undefined>(
		(newest, session) => (!newest || sessionNewer(session, newest) ? session : newest),
		undefined,
	);
}

function sessionNewer(a: WorkspaceSession, b: WorkspaceSession): boolean {
	const aCreated = timestamp(a.createdAt);
	const bCreated = timestamp(b.createdAt);
	if (aCreated !== bCreated) return aCreated > bCreated;
	const aUpdated = timestamp(a.updatedAt);
	const bUpdated = timestamp(b.updatedAt);
	if (aUpdated !== bUpdated) return aUpdated > bUpdated;
	return a.id > b.id;
}

function timestamp(value?: string): number {
	if (!value) return 0;
	const parsed = Date.parse(value);
	return Number.isNaN(parsed) ? 0 : parsed;
}

export function workerSessions(sessions: WorkspaceSession[]): WorkspaceSession[] {
	return sessions.filter((s) => !isOrchestratorSession(s));
}

export function sessionIsActive(session: WorkspaceSession): boolean {
	return session.status !== "merged" && session.status !== "terminated";
}

export function sessionNeedsAttention(session: WorkspaceSession): boolean {
	return (
		session.status === "needs_input" ||
		session.status === "no_signal" ||
		session.status === "changes_requested" ||
		session.status === "review_pending" ||
		session.status === "ci_failed"
	);
}

export const workerStatusLabel: Record<WorkerDisplayStatus, string> = {
	working: "working",
	needs_you: "needs you",
	mergeable: "mergeable",
	ci_failed: "ci failed",
	no_signal: "no signal",
	done: "done",
	unknown: "unknown",
};

/** Whether a status should breathe (alive/working). */
export function workerStatusPulses(status: WorkerDisplayStatus): boolean {
	return status === "working" || status === "needs_you";
}

/**
 * Kanban attention zone, ordered by human-action urgency — ported from
 * agent-orchestrator's getAttentionLevel (packages/web/src/lib/types.ts),
 * collapsed to its default "simple" set and rebound to reverbcode's
 * {@link SessionStatus}. The board groups sessions into these columns so the
 * highest-ROrI work (a one-click merge) sits leftmost.
 */
export type AttentionZone = "todo" | "merge" | "action" | "pending" | "working" | "done";

/** Columns left→right, most-urgent first. "done" is the archive column. */
export const attentionZoneOrder: AttentionZone[] = ["todo", "merge", "action", "pending", "working", "done"];

export const attentionZoneLabel: Record<AttentionZone, string> = {
	todo: "Todo",
	merge: "Ready to merge",
	action: "Needs you",
	pending: "Pending",
	working: "Working",
	done: "Done",
};

export function attentionZone(session: WorkspaceSession): AttentionZone {
	switch (session.status) {
		// Prepared but not started — the board's far-left TODO lane. No
		// branch/worktree/tmux exists until it is started.
		case "todo":
			return "todo";
		// Terminal — archive.
		case "merged":
		case "terminated":
			return "done";
		// One click to clear — highest ROI, checked first.
		case "approved":
		case "mergeable":
			return "merge";
		// Agent waiting on a human (respond) or a problem to investigate (review);
		// agent-orchestrator collapses these into one "action" zone by default.
		case "needs_input":
		case "no_signal":
		case "ci_failed":
		case "changes_requested":
			return "action";
		// Waiting on an external reviewer / CI — nothing to do right now.
		case "review_pending":
		case "pr_open":
		case "draft":
		case "unknown":
			return "pending";
		// Agents doing their thing — don't interrupt.
		case "working":
		case "idle":
		default:
			return "working";
	}
}

export type WorkspaceSummary = {
	id: string;
	name: string;
	kind?: ProjectKind;
	path: string;
	workspaceRepos?: WorkspaceRepoSummary[];
	type?: "main" | "worktree";
	orchestratorAgent?: AgentProvider;
	accentColor?: string;
	diff?: {
		additions: number;
		deletions: number;
	};
	sessions: WorkspaceSession[];
};

export function orchestratorNeedsRestart(workspace: WorkspaceSummary, orchestrator?: WorkspaceSession): boolean {
	if (!orchestrator || !workspace.orchestratorAgent) return false;
	return orchestrator.provider !== workspace.orchestratorAgent;
}

export type OrchestratorHealth =
	| { state: "ok" }
	| { state: "restarting"; message: string }
	| { state: "restart_needed"; message: string }
	| { state: "missing"; message: string }
	| { state: "duplicates"; message: string };

export function orchestratorHealth(workspace: WorkspaceSummary, restarting = false): OrchestratorHealth {
	if (restarting) {
		return { state: "restarting", message: "Restarting orchestrator. New tasks wait until the replacement is ready." };
	}
	const active = workspace.sessions.filter((session) => isOrchestratorSession(session) && sessionIsActive(session));
	if (active.length > 1) {
		return {
			state: "duplicates",
			message:
				"Multiple orchestrators are active. The newest one is used; stale ones will be cleaned up on daemon reconcile.",
		};
	}
	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	if (!orchestrator) {
		return { state: "missing", message: "No orchestrator is running for this project." };
	}
	if (orchestratorNeedsRestart(workspace, orchestrator)) {
		return {
			state: "restart_needed",
			message: `Configured orchestrator agent is ${workspace.orchestratorAgent}; running agent is ${orchestrator.provider}.`,
		};
	}
	return { state: "ok" };
}

export function toAgentProvider(provider?: string): AgentProvider {
	switch (provider) {
		case "claude-code":
		case "opencode":
		case "aider":
		case "grok":
		case "droid":
		case "amp":
		case "agy":
		case "crush":
		case "cursor":
		case "qwen":
		case "copilot":
		case "goose":
		case "auggie":
		case "continue":
		case "devin":
		case "cline":
		case "kimi":
		case "kiro":
		case "kilocode":
		case "vibe":
		case "pi":
		case "autohand":
			return provider;
		default:
			return "codex";
	}
}
