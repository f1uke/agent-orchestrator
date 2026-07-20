import { useEffect, useState, type ReactNode } from "react";
import { ArrowUpRight, GitPullRequest } from "lucide-react";
import { formatTimeCompact } from "../lib/format-time";
import { useSessionScmSummary, type SessionPRSummary } from "../hooks/useSessionScmSummary";
import { useSessionSmokeChecks } from "../hooks/useSessionSmokeChecks";
import { progressFor } from "../lib/smoke-test";
import { deriveReadiness } from "../lib/readiness";
import { ReadinessStrip } from "./ReadinessStrip";
import {
	isArchivedPRState,
	prBrowserUrl,
	prNoun,
	prTitleLabel,
	providerFromPRURL,
	sessionPRDisplaySummaries,
} from "../lib/pr-display";
import type { SessionActivityState, WorkspaceSession } from "../types/workspace";
import { canonicalTrackerIssueId, formatNextTransition, sortedPRs, statusReasonLabel } from "../types/workspace";
import { BrowserPanelView } from "./BrowserPanel";
import type { BrowserViewModel } from "../hooks/useBrowserView";
import { ReviewsView, type FileDiffTarget } from "./ReviewsView";
import { FilesPanel, type ChangedFileTarget } from "./FilesPanel";
import { SmokeTestView } from "./SmokeTestView";
import { JiraIssueSection } from "./JiraIssueSection";
import { ProviderBadge } from "./ProviderBadge";
import { Badge } from "./ui/badge";
import { cn } from "../lib/utils";
import { PRSummaryMeta, PRSummaryParts } from "./PRSummaryDisplay";

type OpenReviewerTerminal = (target: { handleId: string; harness: string }) => void;

export type InspectorView = "summary" | "reviews" | "files" | "tests" | "browser";

const VIEWS: { id: InspectorView; label: string; icon: ReactNode }[] = [
	{
		id: "summary",
		label: "Summary",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<line x1="8" y1="7" x2="20" y2="7" />
				<line x1="8" y1="12" x2="20" y2="12" />
				<line x1="8" y1="17" x2="16" y2="17" />
				<circle cx="4" cy="7" r="1" />
				<circle cx="4" cy="12" r="1" />
				<circle cx="4" cy="17" r="1" />
			</svg>
		),
	},
	{
		id: "reviews",
		label: "Reviews",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z" />
			</svg>
		),
	},
	{
		id: "files",
		label: "Files",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
				<path d="M14 3v5h5" />
				<path d="M9 13h6M9 17h4" />
			</svg>
		),
	},
	{
		id: "tests",
		label: "Tests",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<path d="M20 6 9 17l-5-5" />
			</svg>
		),
	},
	{
		id: "browser",
		label: "Browser",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<circle cx="12" cy="12" r="9" />
				<line x1="3" y1="12" x2="21" y2="12" />
				<path d="M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18" />
			</svg>
		),
	},
];

const prStateTone: Record<SessionPRSummary["state"], string> = {
	open: "border-success/40 bg-success/10 text-success",
	draft: "border-border bg-raised text-muted-foreground",
	merged: "border-accent/40 bg-accent-weak text-accent",
	closed: "border-error/40 bg-error/10 text-error",
};

/**
 * Tabbed inspector rail beside the terminal (Summary · Reviews · Browser).
 */
export function SessionInspector({
	session,
	onOpenReviewerTerminal,
	browserPoppedOut = false,
	isInspectorVisible = true,
	onToggleBrowserPopOut,
	browserView,
	view: viewProp,
	onViewChange,
	onOpenFile,
	onOpenChangedFile,
	selectedChangedPath,
}: {
	session?: WorkspaceSession;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	browserPoppedOut?: boolean;
	isInspectorVisible?: boolean;
	onToggleBrowserPopOut?: (next: boolean) => void;
	browserView?: BrowserViewModel;
	/** Controlled active tab. Omit to let the inspector own its own selection. */
	view?: InspectorView;
	onViewChange?: (view: InspectorView) => void;
	/** Open a review comment's file as a full-file diff in the center pane. */
	onOpenFile?: (target: FileDiffTarget) => void;
	/** Open a Changes-mode row in the center pane. */
	onOpenChangedFile?: (target: ChangedFileTarget) => void;
	/** Path of the Changes row currently open in the center pane. */
	selectedChangedPath?: string;
}) {
	const [internalView, setInternalView] = useState<InspectorView>("summary");
	const view = viewProp ?? internalView;
	const setView = (next: InspectorView) => {
		setInternalView(next);
		onViewChange?.(next);
	};
	// An orchestrator's workspace is the project checkout, not a per-task worktree,
	// and it has no branch of its own to diff — the same reason the readiness strip
	// is hidden for it.
	const showFiles = session?.kind !== "orchestrator";
	const views = showFiles ? VIEWS : VIEWS.filter((v) => v.id !== "files");

	if (!session) {
		return (
			<aside className="session-inspector" aria-label="Session inspector">
				<div className="session-inspector__body">
					<p className="inspector-empty">Loading session…</p>
				</div>
			</aside>
		);
	}

	return (
		<aside className="session-inspector" aria-label="Session inspector">
			<div className="session-inspector__tabs" role="tablist">
				{views.map((entry) => (
					<button
						key={entry.id}
						type="button"
						role="tab"
						aria-selected={view === entry.id}
						className={cn("session-inspector__tab", view === entry.id && "is-active")}
						onClick={() => setView(entry.id)}
					>
						<span className="session-inspector__tab-icon">{entry.icon}</span>
						<span className="session-inspector__tab-label">{entry.label}</span>
					</button>
				))}
			</div>

			<div
				className={cn(
					"session-inspector__body",
					// The Browser tab renders its own bordered panel edge-to-edge, so
					// drop the body padding for it (except when popped out, where the
					// body only holds the "return to panel" empty state).
					view === "browser" && !browserPoppedOut && "session-inspector__body--browser",
					// The merged Reviews tab owns its full-height layout (fixed header +
					// reviewer strip + auto-send, scrolling per-PR list, pinned batch
					// bar), so it renders flush.
					view === "reviews" && "session-inspector__body--reviews",
					// The Tests tab (smoke checklist) owns the same full-height layout.
					view === "tests" && "session-inspector__body--tests",
					// Files owns its own scroll (segmented control + summary pinned,
					// list scrolling beneath), so it renders flush too.
					view === "files" && "session-inspector__body--files",
				)}
			>
				{view === "summary" ? <SummaryView session={session} /> : null}
				{view === "reviews" ? (
					<ReviewsView onOpenReviewerTerminal={onOpenReviewerTerminal} onOpenFile={onOpenFile} session={session} />
				) : null}
				{view === "files" && showFiles ? (
					<FilesPanel sessionId={session.id} onOpenFile={onOpenChangedFile} selectedPath={selectedChangedPath} />
				) : null}
				{view === "tests" ? (
					<SmokeTestView sessionId={session.id} worker={session.title} issueId={session.issueId} />
				) : null}
				{view === "browser" ? (
					<BrowserView
						browserPoppedOut={browserPoppedOut}
						browserView={browserView}
						isActive={isInspectorVisible && !browserPoppedOut}
						onTogglePopOut={onToggleBrowserPopOut}
						session={session}
					/>
				) : null}
			</div>
		</aside>
	);
}

function Section({
	action,
	children,
	className,
	title,
}: {
	action?: ReactNode;
	children: ReactNode;
	className?: string;
	title: string;
}) {
	return (
		<section className={cn("inspector-section", className)}>
			<div className="inspector-section__head">
				<span>{title}</span>
				{action ?? null}
			</div>
			{children}
		</section>
	);
}

function SummaryView({ session }: { session: WorkspaceSession }) {
	const query = useSessionScmSummary(session.id);
	const prSummaries = sessionPRDisplaySummaries(session, query.data);
	// Readiness strip: the "how far along, ready to merge?" verdict + gate row,
	// derived purely from the PR summaries + the smoke rollup + session activity.
	// Skipped for prepared TODOs and orchestrator sessions (no merge pipeline).
	const smokeQuery = useSessionSmokeChecks(session.id, session.title);
	const readiness = deriveReadiness(session, prSummaries, progressFor(smokeQuery.data?.checks ?? []));
	const showReadiness = session.kind !== "orchestrator" && !session.isTodo;
	// Pin the still-actionable PRs/MRs (open, draft) to the top — they're what
	// needs attention — and sink merged/closed ones into a de-emphasized "archive"
	// below. prSummaries is already sorted actionable-first, so the partition keeps
	// each side's order.
	const activePRs = prSummaries.filter((pr) => !isArchivedPRState(pr.state));
	const archivedPRs = prSummaries.filter((pr) => isArchivedPRState(pr.state));
	// Singular title follows the one PR's provider ("Pull request" / "Merge
	// request"); a mixed list keeps the generic plural.
	const singularNoun = prSummaries.length === 1 ? prNoun(prSummaries[0].provider) : "pull request";
	const prSectionTitle =
		prSummaries.length > 1
			? `Pull requests (${prSummaries.length})`
			: `${singularNoun[0].toUpperCase()}${singularNoun.slice(1)}`;
	const branchLabel = session.branch || `session/${session.id}`;
	const issueId = canonicalTrackerIssueId(session.issueId);
	const jiraLinked = issueId?.startsWith("jira:") ?? false;

	return (
		<div role="tabpanel">
			{showReadiness ? <ReadinessStrip readiness={readiness} /> : null}

			<JiraIssueSection sessionId={session.id} linked={jiraLinked} />

			<Section title={prSectionTitle}>
				{prSummaries.length === 0 ? (
					<p className="inspector-empty">No pull request opened yet.</p>
				) : (
					<div className="flex flex-col gap-2">
						{activePRs.map((pr) => (
							<PRSummaryCard key={pr.number} pr={pr} />
						))}
						{archivedPRs.length > 0 ? (
							<>
								{activePRs.length > 0 ? (
									<div className="mt-1 text-[10px] font-medium uppercase tracking-wide text-passive">
										Archived · {archivedPRs.length}
									</div>
								) : null}
								{archivedPRs.map((pr) => (
									<PRSummaryCard key={pr.number} pr={pr} archived />
								))}
							</>
						) : null}
					</div>
				)}
			</Section>

			<Section title="Activity">
				<ActivityTimeline session={session} />
			</Section>

			<Section className="inspector-section--separated" title="Overview">
				<dl className="inspector-kv">
					<Row k="Agent" v={session.provider} mono />
					{issueId && <Row k="Issue" v={issueId} mono />}
					<Row k="Branch" v={branchLabel} mono />
					<TargetRow session={session} />
					<Row k="Started" v={formatTimeCompact(session.createdAt ?? session.updatedAt)} mono />
					<Row k="Session" v={session.id} mono />
				</dl>
			</Section>
		</div>
	);
}

function PRSummaryCard({ pr, archived = false }: { pr: SessionPRSummary; archived?: boolean }) {
	return (
		<div
			className={cn(
				"rounded-[7px] border border-border bg-surface px-3 py-2.5",
				// Archived (merged/closed): de-emphasized so the eye lands on the
				// actionable PRs above, without hiding the record.
				archived && "opacity-65",
			)}
		>
			<div className="flex items-center gap-2">
				<GitPullRequest className="h-3.5 w-3.5 shrink-0 text-passive" aria-hidden="true" />
				<span className="text-[12.5px] font-medium text-foreground">{prTitleLabel(pr.provider, pr.number)}</span>
				<Badge variant="outline" className={cn("h-5 px-1.5 text-[10px] font-medium", prStateTone[pr.state])}>
					{pr.state}
				</Badge>
				<ProviderBadge provider={pr.provider} />
				<a
					href={prBrowserUrl(pr)}
					target="_blank"
					rel="noopener noreferrer"
					className="ml-auto inline-flex items-center gap-0.5 text-[11px] font-medium text-accent hover:underline"
				>
					<span>Open</span>
					<ArrowUpRight aria-hidden="true" className="h-3 w-3" strokeWidth={2} />
				</a>
			</div>
			{pr.title ? <div className="mt-2 text-[12px] font-medium leading-snug text-foreground">{pr.title}</div> : null}
			<PRSummaryMeta className="mt-1.5" pr={pr} />
			<PRSummaryParts className="mt-2" pr={pr} variant="stacked" />
		</div>
	);
}

type TimelineTone = "now" | "good" | "warn" | "neutral";

function ActivityTimeline({ session }: { session: WorkspaceSession }) {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!session.nextTransitionAt) return;
		const id = setInterval(() => setNow(Date.now()), 1000);
		return () => clearInterval(id);
	}, [session.nextTransitionAt]);
	const why = session.statusReason ? statusReasonLabel[session.statusReason] : "";
	const countdown = formatNextTransition(session, now);
	const activityCaption = [why, countdown].filter(Boolean).join(" · ");
	const events: { tone: TimelineTone; node: ReactNode; ts: string | null }[] = [];

	events.push({
		tone: "neutral",
		node: <>Created worktree &amp; branch</>,
		ts: formatTimeCompact(session.createdAt ?? session.updatedAt),
	});

	const prs = sortedPRs(session);
	for (const pr of prs.filter((pr) => pr.state === "draft")) {
		events.push({
			tone: "neutral",
			node: (
				<>
					Draft <b>{prTitleLabel(providerFromPRURL(pr.url), pr.number)}</b>
				</>
			),
			ts: null,
		});
	}

	for (const pr of prs.filter((pr) => pr.state !== "draft")) {
		events.push({
			tone: "neutral",
			node: (
				<>
					Opened <b>{prTitleLabel(providerFromPRURL(pr.url), pr.number)}</b>
				</>
			),
			ts: null,
		});
	}

	events.push({
		tone: "now",
		node: (
			<span className="inline-flex flex-col gap-1">
				<span className="inline-flex flex-wrap items-center gap-1.5">
					<span className="inspector-timeline__badge">
						<InspectorActivityPill state={session.activity?.state ?? "unknown"} />
					</span>
					{session.status === "no_signal" ? (
						<span className="inspector-timeline__badge">
							<TimelinePill {...ACTIVITY_WARNING_PILL.no_signal} />
						</span>
					) : null}
					{scmTimelineStates(session).map((state) => (
						<span key={state} className="inspector-timeline__badge">
							<InspectorScmPill state={state} />
						</span>
					))}
				</span>
				{activityCaption ? (
					<span className="text-[11px] leading-snug text-[var(--fg-muted)]">{activityCaption}</span>
				) : null}
			</span>
		),
		ts: session.activity?.lastActivityAt ? formatTimeCompact(session.activity.lastActivityAt) : null,
	});

	for (const pr of prs.filter((pr) => pr.state === "merged")) {
		events.push({
			tone: "good",
			node: (
				<>
					Merged <b>{prTitleLabel(providerFromPRURL(pr.url), pr.number)}</b>
				</>
			),
			ts: null,
		});
	}

	if (session.status === "merged") {
		events.push({
			tone: "good",
			node: <>Done</>,
			ts: formatTimeCompact(session.updatedAt),
		});
	}

	return (
		<div className="inspector-timeline">
			{events.map((event, index) => (
				<div
					key={index}
					className={cn(
						"inspector-timeline__ev",
						event.tone === "now" && "inspector-timeline__ev--now",
						event.tone === "good" && "inspector-timeline__ev--good",
						event.tone === "warn" && "inspector-timeline__ev--warn",
					)}
				>
					<span className="inspector-timeline__node" aria-hidden="true" />
					<div className="inspector-timeline__et">{event.node}</div>
					{event.ts ? <div className="inspector-timeline__ets">{event.ts}</div> : null}
				</div>
			))}
		</div>
	);
}

const ACTIVITY_PILL: Record<SessionActivityState, { label: string; tone: string; breathe: boolean }> = {
	active: { label: "Working", tone: "var(--orange)", breathe: true },
	idle: { label: "Idle", tone: "var(--fg-muted)", breathe: false },
	waiting_input: { label: "Input Needed", tone: "var(--amber)", breathe: false },
	exited: { label: "Exited", tone: "var(--fg-muted)", breathe: false },
	unknown: { label: "Activity Unavailable", tone: "var(--fg-muted)", breathe: false },
};

const ACTIVITY_WARNING_PILL: Record<"no_signal", { label: string; tone: string; breathe: boolean }> = {
	no_signal: { label: "No Signal", tone: "var(--fg-muted)", breathe: false },
};

type ScmTimelineState = "ci_failed" | "changes_requested" | "conflict";

const SCM_PILL: Record<ScmTimelineState, { label: string; tone: string; breathe: boolean }> = {
	ci_failed: { label: "CI Failed", tone: "var(--red)", breathe: false },
	changes_requested: { label: "Changes Requested", tone: "var(--amber)", breathe: false },
	conflict: { label: "Conflict", tone: "var(--red)", breathe: false },
};

function InspectorActivityPill({ state }: { state: SessionActivityState }) {
	return <TimelinePill {...ACTIVITY_PILL[state]} />;
}

function InspectorScmPill({ state }: { state: ScmTimelineState }) {
	return <TimelinePill {...SCM_PILL[state]} />;
}

function TimelinePill({ label, tone, breathe }: { label: string; tone: string; breathe: boolean }) {
	return (
		<span
			className="inline-flex shrink-0 items-center gap-[7px] whitespace-nowrap rounded-[7px] px-[11px] py-[5px] text-[11.5px] font-semibold"
			style={{
				color: tone,
				background: `color-mix(in srgb, ${tone} 7%, transparent)`,
				boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${tone} 25%, transparent)`,
			}}
		>
			<span
				className={cn("h-1.5 w-1.5 rounded-full", breathe && "animate-status-pulse")}
				style={{ background: tone }}
			/>
			{label}
		</span>
	);
}

function scmTimelineStates(session: WorkspaceSession): ScmTimelineState[] {
	const states: ScmTimelineState[] = [];
	const seen = new Set<ScmTimelineState>();
	const add = (state: ScmTimelineState) => {
		if (seen.has(state)) return;
		seen.add(state);
		states.push(state);
	};

	if (session.status === "ci_failed") add("ci_failed");
	if (session.status === "changes_requested") add("changes_requested");
	for (const pr of session.prs) {
		if (pr.ci === "failing") add("ci_failed");
		if (pr.review === "changes_requested") add("changes_requested");
		if (pr.mergeability === "conflicting") add("conflict");
	}

	return states;
}

function BrowserView({
	session,
	isActive,
	browserPoppedOut,
	onTogglePopOut,
	browserView,
}: {
	session: WorkspaceSession;
	isActive: boolean;
	browserPoppedOut: boolean;
	onTogglePopOut?: (next: boolean) => void;
	browserView?: BrowserViewModel;
}) {
	// While maximized, the browser is a full-window overlay that covers the rail,
	// so the inspector's Browser tab has nothing to show (and must not mount a
	// second BrowserPanelView — it would fight the overlay over the shared native
	// view slot). Exit is via the overlay's own minimize button.
	if (browserPoppedOut) {
		return null;
	}

	if (!browserView) {
		return null;
	}

	return (
		<BrowserPanelView
			active={isActive}
			browserView={browserView}
			onTogglePopOut={(next) => onTogglePopOut?.(next)}
			poppedOut={false}
			session={session}
		/>
	);
}

// How a resolved target branch was arrived at, in the human's words. A target
// the human recorded needs no caveat, so it maps to no note at all; anything
// merely inherited says so, because a value nobody chose must never read like
// one somebody did.
const TARGET_SOURCE_NOTE: Record<NonNullable<WorkspaceSession["targetSource"]>, string> = {
	pr: "from pull request",
	session_pr_target: "",
	session_base: "inherited from start branch",
	project: "project default",
};

// Where this session's work is headed. Sits directly under Branch so the pair
// reads as "from → into". Renders "Not set" rather than assuming a default: an
// unknown target shown as `main` is exactly the confident-but-wrong answer this
// row exists to replace.
function TargetRow({ session }: { session: WorkspaceSession }) {
	const note = session.targetBranch ? TARGET_SOURCE_NOTE[session.targetSource ?? "project"] : "";
	return (
		<div className="inspector-kv__row">
			<dt className="inspector-kv__k">Target</dt>
			<dd className="inspector-kv__v" data-testid="overview-target">
				{session.targetBranch ? (
					<>
						<span className="inspector-kv__v--mono">{session.targetBranch}</span>
						{note ? <span className="ml-1.5 text-passive">· {note}</span> : null}
					</>
				) : (
					<span className="text-passive">Not set</span>
				)}
			</dd>
		</div>
	);
}

function Row({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
	return (
		<div className="inspector-kv__row">
			<dt className="inspector-kv__k">{k}</dt>
			<dd className={cn("inspector-kv__v", mono && "inspector-kv__v--mono")}>{v}</dd>
		</div>
	);
}
