import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { DropdownMenu } from "radix-ui";
import { Play, Shield, Terminal } from "lucide-react";
import type { components } from "../../api/schema";
import { useSessionPRComments, type PRCommentGroup } from "../hooks/useSessionPRComments";
import { useInboxActions } from "../hooks/useInboxActions";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { approvalLabel, approvalProgress, prRef, providerFromPRURL, type ApprovalProgress } from "../lib/pr-display";
import { useSessionScmSummary } from "../hooks/useSessionScmSummary";
import { sortedPRs, type PullRequestFacts, type WorkspaceSession } from "../types/workspace";
import { ApprovalMeter } from "./ApprovalMeter";
import {
	ACCENT,
	MONO,
	PALETTE as P,
	VIEWER as V,
	accentMix,
	avatarBg,
	baseName,
	batchPrompt,
	initialsFor,
	originOf,
	providerBadge,
	relativeTime,
	splitBodyRuns,
	splitNoteRuns,
	tint,
} from "../lib/comment-inbox";
import { DiffRows } from "./DiffRows";
import { CommentActions, MenuItemBody, Toast, menuBox, menuItemStyle, outlineBtn, pill, solidBtn } from "./inbox-ui";

type Group = PRCommentGroup;
type Thread = Group["threads"][number];
type Comment = Thread["comments"][number];
type PRReviewState = components["schemas"]["PRReviewState"];
type ReviewsResponse = components["schemas"]["ListReviewsResponse"];
type ProjectConfig = components["schemas"]["ProjectConfig"];
type OpenReviewerTerminal = (target: { handleId: string; harness: string }) => void;

/**
 * Everything the full-file diff viewer (center pane) needs to render a comment's
 * file at its line and act on the thread. Emitted by a thread's "Expand full
 * file" affordance and consumed by SessionView → FileDiffView.
 */
export type FileDiffTarget = {
	prUrl: string;
	htmlUrl: string;
	prNumber: number;
	provider: string;
	thread: Thread;
};

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/** One merged PR/MR row: its facts + AO-reviewer state + comment threads, all keyed by PR number. */
type PRBlockData = {
	number: number;
	prUrl: string;
	htmlUrl: string;
	provider: "github" | "gitlab";
	title: string;
	facts?: PullRequestFacts;
	review?: PRReviewState;
	group?: Group;
	unresolved: Thread[];
};

type Tone = "neutral" | "running" | "success" | "danger";

/**
 * Reviews tab — the merged review + comment surface. Each PR/MR the session owns
 * is one block: the AO reviewer's verdict and CI at the top, its unresolved
 * review-comment threads nested underneath (resolve / reply / send-to-worker /
 * open-full-file), plus a select-mode batch bar, the auto-send override, and the
 * reviewer run/terminal controls. Pixel-matched to the Comments-inbox dark
 * palette (themed tokens, app accent), mirroring the sibling Tests tab.
 * Wired to the reviews-trigger, comment-resolve/reply/dispatch, send, and
 * auto-nudge endpoints; drives the per-PR reviewer state from /reviews.
 */
export function ReviewsView({
	session,
	onOpenReviewerTerminal,
	onOpenFile,
}: {
	session: WorkspaceSession;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	/** Open a comment's file as a full-file diff in the center pane. */
	onOpenFile?: (target: FileDiffTarget) => void;
}) {
	const sessionId = session.id;
	const prs = sortedPRs(session);
	const hasPr = prs.length > 0;
	const queryClient = useQueryClient();

	// --- reviews (AO reviewer state per PR) ----------------------------------
	const [reviewNotice, setReviewNotice] = useState<string | null>(null);
	const reviewsQuery = useQuery({
		queryKey: ["session-reviews", sessionId],
		enabled: hasPr,
		refetchInterval: (query) => {
			const data = query.state.data as ReviewsResponse | undefined;
			return (data?.reviews ?? []).some((review) => review.status === "running") ? 2500 : false;
		},
		queryFn: async () => {
			if (usePreviewData) return mockReviewsResponse(session);
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/reviews", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load reviews"));
			return data ?? ({ reviewerHandleId: "", reviews: [] } satisfies ReviewsResponse);
		},
	});
	const projectConfigQuery = useQuery({
		queryKey: ["project-config", session.workspaceId],
		enabled: hasPr,
		queryFn: async () => {
			if (usePreviewData) return mockProjectConfig();
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: session.workspaceId } },
			});
			if (error) return undefined;
			return projectConfig(data?.project);
		},
	});
	const triggerReview = useMutation({
		mutationFn: async () => {
			const { data, error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/reviews/trigger", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to start review"));
			return { data, reused: response?.status === 200 };
		},
		onMutate: () => setReviewNotice(null),
		onSuccess: ({ data, reused }) => {
			void queryClient.invalidateQueries({ queryKey: ["session-reviews", sessionId] });
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			const started = data?.reviews?.find((review) => review.status === "running" && review.latestRun);
			if (reused || !started?.latestRun) {
				setReviewNotice("No needed reviews were started.");
				return;
			}
			if (data?.reviewerHandleId) {
				const harness = started.latestRun.harness || "reviewer";
				onOpenReviewerTerminal?.({ handleId: data.reviewerHandleId, harness });
			}
		},
	});
	const reviewStates = reviewsQuery.data?.reviews ?? [];
	const reviewerHandleId = reviewsQuery.data?.reviewerHandleId ?? "";
	const latestRun = reviewStates.find((review) => review.latestRun)?.latestRun;
	const harness = latestRun?.harness || projectConfigQuery.data?.reviewers?.[0]?.harness || "claude-code";

	// --- comments (unresolved threads per PR) --------------------------------
	const commentsQuery = useSessionPRComments(sessionId);
	const groups = usePreviewData ? mockPRComments(session) : (commentsQuery.data ?? []);

	// --- auto-send override (per-session) + global default -------------------
	const sessionQuery = useQuery({
		queryKey: ["session", sessionId, "autoNudge"],
		enabled: !usePreviewData,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data.session;
		},
	});
	const settingsQuery = useQuery({
		queryKey: ["settings", "autoNudge"],
		enabled: !usePreviewData,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/auto-nudge", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
	});
	const override = sessionQuery.data?.autoNudgeComments ?? null;
	const globalDefault = settingsQuery.data?.enabled ?? false;
	const autoOn = override !== null ? override : globalDefault;
	const setOverride = useMutation({
		mutationFn: async (next: boolean) => {
			const { error } = await apiClient.PUT("/api/v1/sessions/{sessionId}/auto-nudge", {
				params: { path: { sessionId } },
				body: { override: next },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["session", sessionId, "autoNudge"] }),
	});

	// --- auto-resolve gate (per-session; nil = OFF, no global default) --------
	const autoResolveOn = sessionQuery.data?.autoResolveOnReply ?? false;
	const setAutoResolve = useMutation({
		mutationFn: async (next: boolean) => {
			const { error } = await apiClient.PUT("/api/v1/sessions/{sessionId}/auto-resolve", {
				params: { path: { sessionId } },
				body: { override: next },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["session", sessionId, "autoNudge"] }),
	});

	// --- select / batch ------------------------------------------------------
	const [selectMode, setSelectMode] = useState(false);
	const [selected, setSelected] = useState<Set<string>>(new Set());
	const toggleSelect = (id: string) =>
		setSelected((s) => {
			const n = new Set(s);
			n.has(id) ? n.delete(id) : n.add(id);
			return n;
		});
	const exitSelect = () => {
		setSelectMode((v) => !v);
		setSelected(new Set());
	};

	// --- mutations + toast + per-comment handlers (shared with FileDiffView) --
	const { resolve, dispatch, send, busy, toast, showToast, doResolve, doReply, doSendQuick, doSendPrompt } =
		useInboxActions(sessionId);

	// Approval-progress facts live on the SCM summary (the /pr read model), keyed
	// by PR number so each block can show its human-approval pill.
	const scmSummaries = useSessionScmSummary(sessionId).data ?? [];
	const approvalByNumber = useMemo(() => {
		const m = new Map<number, ApprovalProgress | null>();
		for (const s of scmSummaries) m.set(s.number, approvalProgress(s.review));
		return m;
	}, [scmSummaries]);

	// --- merge PRs (facts) + reviews + comment groups into per-PR blocks ------
	const blocks = useMemo(() => mergeBlocks(prs, reviewStates, groups), [prs, reviewStates, groups]);
	const totalUnresolved = blocks.reduce((n, b) => n + b.unresolved.length, 0);
	const commentPrCount = blocks.filter((b) => b.unresolved.length > 0).length;
	const resolvedItems = groups.flatMap((g) =>
		g.threads.filter((t) => t.resolved).map((t) => ({ group: g, thread: t })),
	);

	// threadId → {prUrl, thread} for batch actions (unresolved only)
	const byId = useMemo(() => {
		const m = new Map<string, { prUrl: string; thread: Thread }>();
		for (const b of blocks) for (const t of b.unresolved) m.set(t.threadId, { prUrl: b.prUrl, thread: t });
		return m;
	}, [blocks]);
	const selectedIds = [...selected].filter((id) => byId.has(id));

	const batchResolve = () => {
		selectedIds.forEach((id) => {
			const it = byId.get(id);
			if (it) resolve.mutate({ prUrl: it.prUrl, threadId: it.thread.threadId });
		});
		showToast(`${selectedIds.length} comment${selectedIds.length > 1 ? "s" : ""} resolved`);
		setSelected(new Set());
	};
	const batchSeparate = () => {
		selectedIds.forEach((id) => {
			const it = byId.get(id);
			if (it) dispatch.mutate({ prUrl: it.prUrl, threadId: it.thread.threadId });
		});
		showToast(`Fanned out ${selectedIds.length} worker tasks`);
	};
	const batchOneTask = () => {
		const items = selectedIds
			.map((id) => byId.get(id)?.thread)
			.filter((t): t is Thread => Boolean(t))
			.map((t) => ({ path: t.path, line: t.line, body: t.comments[0]?.body ?? "" }));
		const message = items.length ? batchPrompt(items) : "";
		if (message) send.mutate(message);
		showToast(`${selectedIds.length} comments → 1 worker task`);
	};

	// --- render --------------------------------------------------------------
	const loading = hasPr && (reviewsQuery.isLoading || commentsQuery.isLoading);
	const err = reviewsQuery.error
		? apiErrorMessage(reviewsQuery.error, "Unable to load reviews")
		: commentsQuery.error
			? apiErrorMessage(commentsQuery.error, "Unable to load comments")
			: null;

	return (
		<div
			role="tabpanel"
			style={{
				position: "relative",
				display: "flex",
				flexDirection: "column",
				height: "100%",
				minHeight: 0,
				background: P.rail,
				color: P.text,
			}}
		>
			<ReviewsHeader
				totalUnresolved={totalUnresolved}
				commentPrCount={commentPrCount}
				prCount={prs.length}
				selectMode={selectMode}
				canSelect={totalUnresolved > 0}
				onToggleSelect={exitSelect}
			/>

			{hasPr && (
				<ReviewerStrip
					harness={harness}
					aggregate={sessionReviewVerdict(reviewStates)}
					error={triggerReview.error ? apiErrorMessage(triggerReview.error, "Review request failed") : null}
					notice={reviewNotice}
					runLabel={reviewSessionRunAction(reviewStates, triggerReview.isPending)}
					runDisabled={
						triggerReview.isPending ||
						reviewStates.length === 0 ||
						reviewStates.some((r) => r.status === "running") ||
						reviewStates.every((r) => r.status === "ineligible")
					}
					terminalEnabled={Boolean(reviewerHandleId && onOpenReviewerTerminal)}
					onTrigger={() => triggerReview.mutate()}
					onOpenTerminal={() => reviewerHandleId && onOpenReviewerTerminal?.({ handleId: reviewerHandleId, harness })}
				/>
			)}

			{hasPr && (
				<ReviewToggleRow
					icon="⚡"
					label="Auto-send unresolved comments to worker"
					on={autoOn}
					busy={settingsQuery.isLoading || sessionQuery.isLoading || setOverride.isPending}
					onToggle={(next) => {
						setOverride.mutate(next);
						showToast(next ? "Auto-send on · new comments dispatch automatically" : "Auto-send off");
					}}
				/>
			)}

			{hasPr && (
				<ReviewToggleRow
					icon="✓"
					label="Auto-resolve threads when we reply"
					on={autoResolveOn}
					busy={sessionQuery.isLoading || setAutoResolve.isPending}
					onToggle={(next) => {
						setAutoResolve.mutate(next);
						showToast(next ? "Auto-resolve on · threads resolve when we reply" : "Auto-resolve off");
					}}
				/>
			)}

			<div style={{ flex: 1, overflowY: "auto", padding: "12px 12px 24px" }}>
				{loading && <p style={{ padding: 16, fontSize: 12.5, color: P.muted2 }}>Loading reviews…</p>}
				{!loading && err && <p style={{ padding: 16, fontSize: 12.5, color: P.red }}>{err}</p>}

				{!loading && !err && !hasPr && <NoPrEmptyState />}

				{!loading &&
					!err &&
					hasPr &&
					blocks.map((block) => (
						<PRBlock
							key={block.number}
							block={block}
							approval={approvalByNumber.get(block.number) ?? null}
							sessionId={sessionId}
							selectMode={selectMode}
							selected={selected}
							onToggleSelect={toggleSelect}
							busy={busy}
							onResolve={doResolve}
							onReply={doReply}
							onSendQuick={doSendQuick}
							onSendPrompt={doSendPrompt}
							onOpenFile={onOpenFile}
						/>
					))}

				{!loading && !err && resolvedItems.length > 0 && <ResolvedSection items={resolvedItems} />}
			</div>

			{selectMode && selectedIds.length > 0 && (
				<BatchBar
					count={selectedIds.length}
					onResolve={batchResolve}
					onOneTask={batchOneTask}
					onSeparate={batchSeparate}
				/>
			)}

			{toast && <Toast text={toast} />}
		</div>
	);
}

// ---------------------------------------------------------------------------
// Merge

/** Zip the session's PRs (facts + order) with reviewer state + comment groups, keyed by PR number. */
function mergeBlocks(prs: PullRequestFacts[], reviews: PRReviewState[], groups: Group[]): PRBlockData[] {
	const reviewByNum = new Map(reviews.map((r) => [r.prNumber, r]));
	const groupByNum = new Map(groups.map((g) => [g.number, g]));
	const seen = new Set<number>();
	const blocks: PRBlockData[] = [];

	const build = (number: number, facts: PullRequestFacts | undefined): PRBlockData => {
		const review = reviewByNum.get(number);
		const group = groupByNum.get(number);
		const prUrl = group?.prUrl || facts?.url || review?.prUrl || "";
		const provider = group ? (group.provider === "gitlab" ? "gitlab" : "github") : providerFromPRURL(prUrl);
		return {
			number,
			prUrl,
			htmlUrl: group?.htmlUrl || prUrl,
			provider,
			title: review?.title?.trim() || "",
			facts,
			review,
			group,
			unresolved: (group?.threads ?? []).filter((t) => !t.resolved),
		};
	};

	for (const pr of prs) {
		seen.add(pr.number);
		blocks.push(build(pr.number, pr));
	}
	// Defensive: a comment group with no matching session PR (also drives the
	// preview's GitLab example) still gets its own block so no thread is dropped.
	for (const g of groups) {
		if (seen.has(g.number)) continue;
		seen.add(g.number);
		blocks.push(build(g.number, undefined));
	}
	return blocks;
}

// ---------------------------------------------------------------------------
// Header + reviewer controls

function ReviewsHeader({
	totalUnresolved,
	commentPrCount,
	prCount,
	selectMode,
	canSelect,
	onToggleSelect,
}: {
	totalUnresolved: number;
	commentPrCount: number;
	prCount: number;
	selectMode: boolean;
	canSelect: boolean;
	onToggleSelect: () => void;
}) {
	const subtitle =
		totalUnresolved > 0
			? `${totalUnresolved} unresolved across ${commentPrCount} PR${commentPrCount === 1 ? "" : "s"}`
			: prCount > 0
				? `${prCount} pull request${prCount === 1 ? "" : "s"} · no unresolved comments`
				: "No pull requests yet";
	return (
		<div style={{ flex: "none", padding: "16px 16px 12px", borderBottom: `1px solid ${P.divider}` }}>
			<div style={{ display: "flex", alignItems: "baseline", gap: 9 }}>
				<span style={{ fontSize: 16, fontWeight: 700, color: P.textStrong }}>Reviews</span>
				{totalUnresolved > 0 && <span style={pill(12, P.secondary)}>{totalUnresolved}</span>}
				<span
					style={{
						fontSize: 11.5,
						color: P.muted2,
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
					}}
				>
					{subtitle}
				</span>
				<div style={{ flex: 1 }} />
				{canSelect && (
					<button
						type="button"
						onClick={onToggleSelect}
						style={{
							fontSize: 11.5,
							fontWeight: 600,
							padding: "4px 11px",
							borderRadius: 7,
							cursor: "pointer",
							border: `1px solid ${selectMode ? accentMix(45) : P.borderPill}`,
							color: selectMode ? ACCENT : P.secondary,
							background: selectMode ? accentMix(12) : "transparent",
						}}
					>
						{selectMode ? "Done" : "Select"}
					</button>
				)}
			</div>
		</div>
	);
}

const TONE_COLOR: Record<Tone, string> = {
	neutral: P.secondary,
	running: P.amber,
	success: P.green,
	danger: P.red,
};

function ReviewerStrip({
	harness,
	aggregate,
	error,
	notice,
	runLabel,
	runDisabled,
	terminalEnabled,
	onTrigger,
	onOpenTerminal,
}: {
	harness: string;
	aggregate: { label: string; tone: Tone };
	error: string | null;
	notice: string | null;
	runLabel: string;
	runDisabled: boolean;
	terminalEnabled: boolean;
	onTrigger: () => void;
	onOpenTerminal: () => void;
}) {
	const aggColor = TONE_COLOR[aggregate.tone];
	return (
		<div
			style={{
				flex: "none",
				padding: "12px 16px",
				borderBottom: `1px solid ${P.divider}`,
				display: "flex",
				flexDirection: "column",
				gap: 10,
			}}
		>
			{error && (
				<p
					style={{
						margin: 0,
						fontSize: 11.5,
						lineHeight: 1.45,
						color: P.red,
						background: tint(P.red, 8),
						border: `1px solid ${tint(P.red, 28)}`,
						borderRadius: 7,
						padding: "7px 10px",
					}}
				>
					{error}
				</p>
			)}
			{notice && (
				<p
					style={{
						margin: 0,
						fontSize: 11.5,
						lineHeight: 1.45,
						color: P.green,
						background: tint(P.green, 8),
						border: `1px solid ${tint(P.green, 28)}`,
						borderRadius: 7,
						padding: "7px 10px",
					}}
				>
					{notice}
				</p>
			)}
			<div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap", rowGap: 10 }}>
				<div style={{ display: "inline-flex", alignItems: "center", gap: 7, minWidth: 0, flex: "1 1 auto" }}>
					<Shield aria-hidden="true" style={{ width: 15, height: 15, flex: "none", color: P.muted }} />
					<span
						style={{
							fontFamily: MONO,
							fontSize: 12.5,
							fontWeight: 700,
							color: P.text,
							overflow: "hidden",
							textOverflow: "ellipsis",
							whiteSpace: "nowrap",
						}}
					>
						{harness}
					</span>
					<span style={{ fontSize: 11.5, color: P.muted2 }}>reviewer</span>
					<span
						style={{
							fontSize: 11,
							fontWeight: 600,
							color: aggColor,
							whiteSpace: "nowrap",
							overflow: "hidden",
							textOverflow: "ellipsis",
							minWidth: 0,
						}}
					>
						· {aggregate.label}
					</span>
				</div>
				<div style={{ display: "inline-flex", alignItems: "center", gap: 10, marginLeft: "auto", flex: "none" }}>
					<button
						type="button"
						disabled={runDisabled}
						onClick={onTrigger}
						style={{
							display: "inline-flex",
							alignItems: "center",
							gap: 6,
							fontSize: 12,
							fontWeight: 600,
							padding: "6px 11px",
							borderRadius: 7,
							cursor: runDisabled ? "not-allowed" : "pointer",
							color: P.greenBright,
							border: `1px solid ${tint(P.green, 40)}`,
							background: tint(P.green, 10),
							opacity: runDisabled ? 0.5 : 1,
							whiteSpace: "nowrap",
						}}
					>
						<Play aria-hidden="true" style={{ width: 13, height: 13 }} />
						{runLabel}
					</button>
					<button
						type="button"
						disabled={!terminalEnabled}
						onClick={onOpenTerminal}
						style={{
							display: "inline-flex",
							alignItems: "center",
							gap: 6,
							fontSize: 12,
							fontWeight: 600,
							padding: "6px 11px",
							borderRadius: 7,
							cursor: terminalEnabled ? "pointer" : "not-allowed",
							color: P.secondary,
							border: `1px solid ${P.borderPill}`,
							background: "transparent",
							opacity: terminalEnabled ? 1 : 0.5,
							whiteSpace: "nowrap",
						}}
					>
						<Terminal aria-hidden="true" style={{ width: 13, height: 13 }} />
						Open terminal
					</button>
				</div>
			</div>
		</div>
	);
}

// ReviewToggleRow is the shared control-strip switch used by both the auto-send
// and auto-resolve rows. icon/label parametrize it so the two rows stay pixel
// identical while carrying their own copy and state.
function ReviewToggleRow({
	icon,
	label,
	on,
	busy,
	onToggle,
}: {
	icon: string;
	label: string;
	on: boolean;
	busy: boolean;
	onToggle: (next: boolean) => void;
}) {
	return (
		<div
			style={{
				flex: "none",
				display: "flex",
				alignItems: "center",
				gap: 14,
				padding: "13px 16px",
				borderBottom: `1px solid ${P.divider}`,
				background: on ? accentMix(6) : "transparent",
			}}
		>
			<div style={{ flex: 1, minWidth: 0 }}>
				<div style={{ display: "flex", alignItems: "center", gap: 7 }}>
					<span style={{ flex: "none", width: 16, textAlign: "center", fontSize: 12, color: on ? ACCENT : P.muted2 }}>
						{icon}
					</span>
					<span style={{ fontSize: 13, fontWeight: 600, color: P.text }}>{label}</span>
				</div>
			</div>
			<button
				type="button"
				role="switch"
				aria-checked={on}
				aria-label={label}
				disabled={busy}
				onClick={() => onToggle(!on)}
				style={{
					flex: "none",
					position: "relative",
					width: 40,
					height: 23,
					borderRadius: 999,
					cursor: busy ? "default" : "pointer",
					padding: 0,
					transition: "background .16s ease",
					background: on ? ACCENT : P.controlTrack,
					border: `1px solid ${on ? "transparent" : P.controlBorder}`,
					opacity: busy ? 0.6 : 1,
				}}
			>
				<span
					style={{
						position: "absolute",
						top: 2,
						left: 2,
						width: 17,
						height: 17,
						borderRadius: "50%",
						background: "#fff",
						boxShadow: "0 1px 3px rgba(0,0,0,.4)",
						transition: "transform .16s ease",
						transform: `translateX(${on ? "17px" : "0"})`,
					}}
				/>
			</button>
		</div>
	);
}

// ---------------------------------------------------------------------------
// Per-PR block: review status + CI header, threads nested underneath

function StatusPill({ color, label, dot = false }: { color: string; label: string; dot?: boolean }) {
	return (
		<span
			style={{
				display: "inline-flex",
				alignItems: "center",
				gap: 5,
				fontSize: 10.5,
				fontWeight: 600,
				color,
				background: `color-mix(in srgb, ${color} 13%, transparent)`,
				border: `1px solid color-mix(in srgb, ${color} 30%, transparent)`,
				padding: "2px 8px",
				borderRadius: 999,
				whiteSpace: "nowrap",
				lineHeight: 1.4,
			}}
		>
			{dot && <span style={{ width: 6, height: 6, borderRadius: "50%", background: color, flex: "none" }} />}
			{label}
		</span>
	);
}

// ApprovalPill is the human-approval-progress chip, deliberately distinct from
// the AO-reviewer verdict pill: green once the threshold is met, neutral while
// short. Embeds the pip meter (when the threshold is known) beside the fraction.
function ApprovalPill({ progress }: { progress: ApprovalProgress }) {
	const color = progress.met ? P.green : P.secondary;
	return (
		<span
			style={{
				display: "inline-flex",
				alignItems: "center",
				gap: 5,
				fontSize: 10.5,
				fontWeight: 600,
				color,
				background: `color-mix(in srgb, ${color} 13%, transparent)`,
				border: `1px solid color-mix(in srgb, ${color} 30%, transparent)`,
				padding: "2px 8px",
				borderRadius: 999,
				whiteSpace: "nowrap",
				lineHeight: 1.4,
			}}
		>
			<ApprovalMeter progress={progress} />
			{approvalLabel(progress, { remaining: true })}
		</span>
	);
}

function ciPill(ci?: string): { color: string; label: string } | null {
	switch (ci) {
		case "passing":
			return { color: P.green, label: "CI passed" };
		case "failing":
			return { color: P.red, label: "CI failed" };
		case "pending":
			return { color: P.amber, label: "CI running" };
		default:
			return null;
	}
}

function PRBlock({
	block,
	approval,
	sessionId,
	selectMode,
	selected,
	onToggleSelect,
	busy,
	onResolve,
	onReply,
	onSendQuick,
	onSendPrompt,
	onOpenFile,
}: {
	block: PRBlockData;
	approval: ApprovalProgress | null;
	sessionId: string;
	selectMode: boolean;
	selected: Set<string>;
	onToggleSelect: (id: string) => void;
	busy: boolean;
	onResolve: (prUrl: string, threadId: string) => void;
	onReply: (prUrl: string, threadId: string, body: string, author: string) => void;
	onSendQuick: (prUrl: string, threadId: string, path: string) => void;
	onSendPrompt: (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => void;
	onOpenFile?: (target: FileDiffTarget) => void;
}) {
	const kind = block.provider === "gitlab" ? "MR" : "PR";
	const ci = ciPill(block.facts?.ci);
	const rv = block.review ? reviewVerdict(block.review) : null;
	const conflict = block.facts?.mergeability === "conflicting";
	const threads = block.unresolved;

	return (
		<div style={{ marginBottom: 18 }}>
			{/* PR/MR identity + title, with review status + CI on their own line below (sticky within the scroll) */}
			<div
				style={{
					display: "flex",
					flexDirection: "column",
					gap: 8,
					padding: "8px 4px 10px",
					position: "sticky",
					top: 0,
					background: P.rail,
					zIndex: 2,
				}}
			>
				<div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
					<span
						style={{
							flex: "none",
							fontFamily: MONO,
							fontSize: 9,
							fontWeight: 600,
							color: V.pathFg,
							background: P.pillBg,
							border: `1px solid ${P.borderMenu}`,
							padding: "2px 5px",
							borderRadius: 4,
						}}
					>
						{providerBadge(block.provider)}
					</span>
					<span style={{ flex: "none", fontSize: 13.5, fontWeight: 700, color: P.textStrong, whiteSpace: "nowrap" }}>
						{`${kind} ${prRef(block.provider, block.number)}`}
					</span>
					{block.title && (
						<span
							style={{
								flex: "1 1 auto",
								minWidth: 0,
								fontSize: 12,
								color: P.secondary2,
								lineHeight: 1.42,
								wordBreak: "break-word",
							}}
						>
							{block.title}
						</span>
					)}
				</div>
				{(ci || rv || approval || conflict || threads.length > 0) && (
					<div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
						{ci && <StatusPill color={ci.color} label={ci.label} dot />}
						{rv && <StatusPill color={TONE_COLOR[rv.tone]} label={rv.label} />}
						{approval && <ApprovalPill progress={approval} />}
						{conflict && <StatusPill color={P.red} label="Conflict" />}
						{threads.length > 0 && <span style={pill(11, P.secondary, "1px 7px")}>{threads.length}</span>}
					</div>
				)}
			</div>

			{threads.length === 0 ? (
				<div style={{ fontSize: 11.5, color: P.muted2, padding: "2px 6px 4px", fontStyle: "italic" }}>
					No unresolved comments.
				</div>
			) : (
				threads.map((thread) => (
					<ThreadCard
						key={thread.threadId}
						sessionId={sessionId}
						prUrl={block.prUrl}
						thread={thread}
						selectMode={selectMode}
						selected={selected.has(thread.threadId)}
						onToggleSelect={() => onToggleSelect(thread.threadId)}
						busy={busy}
						onResolve={onResolve}
						onReply={onReply}
						onSendQuick={onSendQuick}
						onSendPrompt={onSendPrompt}
						onOpenFile={
							onOpenFile &&
							(() =>
								onOpenFile({
									prUrl: block.prUrl,
									htmlUrl: block.htmlUrl,
									prNumber: block.number,
									provider: block.provider,
									thread,
								}))
						}
					/>
				))
			)}
		</div>
	);
}

// Per-comment avatar rail geometry. Each comment in a thread carries its own
// avatar so "who said what" reads at a glance; a continuous thread line links
// consecutive avatars into one legible conversation.
const AVATAR = 26;
const RAIL_GAP = 10; // gap between the avatar rail and the comment body
const COMMENT_GAP = 16; // vertical breathing room between comments

/**
 * One comment within a thread: an avatar rail (deterministic hue = identity) on
 * the left, name · time header and body on the right. Consecutive avatars are
 * joined by a faint vertical thread line so a multi-comment thread reads as a
 * conversation rather than one undifferentiated block. A system note (e.g.
 * GitLab's "changed this line in version N of the diff") shows a small hollow
 * dot node instead of an avatar and stays author-less and de-emphasized.
 */
function ThreadComment({
	comment,
	prUrl,
	first,
	last,
}: {
	comment: Comment;
	prUrl: string;
	first: boolean;
	last: boolean;
}) {
	const sep = first ? undefined : { marginTop: COMMENT_GAP };
	// Continuous thread line from this avatar's bottom down to the next avatar's
	// top (the next row's marginTop is exactly COMMENT_GAP, so bottom bridges it).
	const connector = last ? null : (
		<div
			aria-hidden
			style={{
				position: "absolute",
				left: AVATAR / 2 - 0.5,
				top: AVATAR,
				bottom: -COMMENT_GAP,
				width: 1,
				background: P.connector,
			}}
		/>
	);

	if (comment.system) {
		return (
			<div style={{ display: "flex", gap: RAIL_GAP, ...sep }}>
				<div style={{ flex: "none", width: AVATAR, display: "flex", justifyContent: "center", position: "relative" }}>
					<div
						aria-hidden
						style={{
							width: 7,
							height: 7,
							marginTop: 5,
							borderRadius: "50%",
							background: P.borderMenu,
							border: `1px solid ${P.controlBorder}`,
						}}
					/>
					{connector}
				</div>
				<div style={{ flex: 1, minWidth: 0 }}>
					<SystemNoteLine body={comment.body} prUrl={prUrl} first />
				</div>
			</div>
		);
	}

	const author = comment.author || "unknown";
	return (
		<div style={{ display: "flex", gap: RAIL_GAP, ...sep }}>
			<div style={{ flex: "none", width: AVATAR, position: "relative" }}>
				<div
					style={{
						width: AVATAR,
						height: AVATAR,
						borderRadius: "50%",
						display: "flex",
						alignItems: "center",
						justifyContent: "center",
						fontSize: 10,
						fontWeight: 700,
						color: "#fff",
						background: avatarBg(author),
					}}
				>
					{initialsFor(author)}
				</div>
				{connector}
			</div>
			<div style={{ flex: 1, minWidth: 0 }}>
				<div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
					<span style={{ fontSize: 13, fontWeight: 600, color: P.text }}>{author}</span>
					<span style={{ fontSize: 11, color: P.muted2 }}>{relativeTime(comment.createdAt, Date.now())}</span>
				</div>
				<div style={{ fontSize: 13, lineHeight: 1.55, color: P.body, wordBreak: "break-word" }}>
					{splitBodyRuns(comment.body).map((run, j) =>
						run.code ? (
							<code
								key={j}
								style={{
									fontFamily: MONO,
									fontSize: 11.5,
									color: P.code,
									background: P.pillBg,
									border: `1px solid ${P.borderPill}`,
									borderRadius: 4,
									padding: "0 4px",
								}}
							>
								{run.text}
							</code>
						) : (
							<span key={j}>{run.text}</span>
						),
					)}
				</div>
			</div>
		</div>
	);
}

function ThreadCard({
	sessionId,
	prUrl,
	thread,
	selectMode,
	selected,
	onToggleSelect,
	busy,
	onResolve,
	onReply,
	onSendQuick,
	onSendPrompt,
	onOpenFile,
}: {
	sessionId: string;
	prUrl: string;
	thread: Thread;
	selectMode: boolean;
	selected: boolean;
	onToggleSelect: () => void;
	busy: boolean;
	onResolve: (prUrl: string, threadId: string) => void;
	onReply: (prUrl: string, threadId: string, body: string, author: string) => void;
	onSendQuick: (prUrl: string, threadId: string, path: string) => void;
	onSendPrompt: (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => void;
	onOpenFile?: () => void;
}) {
	const [collapsed, setCollapsed] = useState(false);
	const first = thread.comments[0];
	const author = first?.author ?? "unknown";

	return (
		<div
			style={{
				border: `1px solid ${P.borderCard}`,
				borderRadius: 10,
				overflow: "hidden",
				marginBottom: 10,
				background: P.cardBg,
			}}
		>
			<div
				onClick={() => setCollapsed((c) => !c)}
				style={{
					display: "flex",
					alignItems: "center",
					gap: 8,
					padding: "9px 12px",
					cursor: "pointer",
					background: P.fileHeader,
					borderBottom: collapsed ? "none" : `1px solid ${P.borderCard}`,
				}}
			>
				<span style={{ color: P.muted, fontSize: 10, width: 10, flex: "none" }}>{collapsed ? "▸" : "▾"}</span>
				<span
					title={thread.path}
					style={{
						flex: 1,
						minWidth: 0,
						fontFamily: MONO,
						fontSize: 11.5,
						color: V.chromeFg,
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
						direction: "rtl",
						textAlign: "left",
					}}
				>
					{thread.path}
				</span>
				<span style={{ fontFamily: MONO, fontSize: 11, color: ACCENT, flex: "none" }}>:{thread.line}</span>
			</div>

			{!collapsed && (
				<div style={{ position: "relative", padding: "13px 14px" }}>
					<div
						style={{
							position: "absolute",
							inset: 0,
							pointerEvents: "none",
							border: `1.5px solid ${selected ? ACCENT : "transparent"}`,
							background: selected ? accentMix(7) : "transparent",
						}}
					/>
					<div style={{ display: "flex", gap: 10, position: "relative" }}>
						{selectMode && (
							<button
								type="button"
								role="checkbox"
								aria-checked={selected}
								aria-label={`Select comment on ${thread.path}:${thread.line}`}
								onClick={onToggleSelect}
								style={{
									flex: "none",
									width: 18,
									height: 18,
									marginTop: 3,
									borderRadius: 5,
									border: `1.5px solid ${selected ? ACCENT : P.controlBorder}`,
									background: selected ? ACCENT : "transparent",
									display: "flex",
									alignItems: "center",
									justifyContent: "center",
									cursor: "pointer",
									padding: 0,
									color: "var(--accent-fg)",
									fontSize: 11,
									fontWeight: 800,
									lineHeight: 1,
								}}
							>
								{selected ? "✓" : ""}
							</button>
						)}
						<div style={{ flex: 1, minWidth: 0 }}>
							{thread.comments.map((c, i) => (
								<ThreadComment
									key={c.id}
									comment={c}
									prUrl={prUrl}
									first={i === 0}
									last={i === thread.comments.length - 1}
								/>
							))}

							{/* Diff + actions stay aligned under the comment bodies (past the avatar rail). */}
							<div style={{ paddingLeft: AVATAR + RAIL_GAP }}>
								{thread.path && thread.line > 0 && (
									<InboxDiff
										sessionId={sessionId}
										prUrl={prUrl}
										path={thread.path}
										line={thread.line}
										onOpenFile={onOpenFile}
									/>
								)}

								<CommentActions
									prUrl={prUrl}
									threadId={thread.threadId}
									path={thread.path}
									line={thread.line}
									author={author}
									seedBody={first?.body ?? ""}
									busy={busy}
									onResolve={onResolve}
									onReply={onReply}
									onSendQuick={onSendQuick}
									onSendPrompt={onSendPrompt}
								/>
							</div>
						</div>
					</div>
				</div>
			)}
		</div>
	);
}

/**
 * A GitLab (or other provider) system note — e.g. "changed this line in version 6
 * of the diff" that GitLab appends when a thread goes outdated. Rendered as a
 * small, de-emphasized activity line (no avatar/author block, unlike a real
 * comment), with its embedded markdown link shown as a clean hyperlink rather
 * than the raw URL. Purely presentational: it never affects thread counts.
 */
function SystemNoteLine({ body, prUrl, first }: { body: string; prUrl: string; first: boolean }) {
	const runs = splitNoteRuns(body, originOf(prUrl));
	return (
		<div style={{ marginTop: first ? 0 : 8, fontSize: 11.5, lineHeight: 1.5, color: P.muted, wordBreak: "break-word" }}>
			{runs.map((run, j) =>
				run.href ? (
					<a
						key={j}
						href={run.href}
						target="_blank"
						rel="noopener noreferrer"
						style={{ color: P.secondary, textDecoration: "underline", textUnderlineOffset: 2 }}
					>
						{run.text}
					</a>
				) : (
					<span key={j}>{run.text}</span>
				),
			)}
		</div>
	);
}

type DiffCtx = components["schemas"]["DiffContextResponse"];

// Inline diff for one comment: a collapsible, syntax-highlighted hunk plus an
// "Expand full file" affordance that opens the whole file in the center pane.
function InboxDiff({
	sessionId,
	prUrl,
	path,
	line,
	onOpenFile,
}: {
	sessionId: string;
	prUrl: string;
	path: string;
	line: number;
	onOpenFile?: () => void;
}) {
	const [open, setOpen] = useState(false);
	const q = useQuery({
		queryKey: ["diff-context", sessionId, prUrl, path, line, "hunk"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/diff-context", {
				params: { path: { sessionId }, query: { prUrl, path, line, mode: "hunk" } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load code"));
			return data as DiffCtx;
		},
	});
	const ctx = q.data;
	if (!ctx || !ctx.available || ctx.lines.length === 0) return null;
	const n = ctx.lines.length;
	return (
		<div style={{ marginTop: 10 }}>
			<div style={{ display: "flex", alignItems: "center", gap: 16 }}>
				<button
					type="button"
					onClick={() => setOpen((o) => !o)}
					style={{
						fontSize: 11.5,
						color: P.secondary2,
						background: "transparent",
						border: "none",
						cursor: "pointer",
						padding: 0,
						display: "inline-flex",
						alignItems: "center",
						gap: 5,
					}}
				>
					{open ? "▾ Hide" : "▸ Show"} diff · {n} lines
				</button>
				{onOpenFile && (
					<button
						type="button"
						onClick={onOpenFile}
						style={{
							fontSize: 11.5,
							fontWeight: 600,
							color: ACCENT,
							background: "transparent",
							border: "none",
							cursor: "pointer",
							padding: 0,
							display: "inline-flex",
							alignItems: "center",
							gap: 5,
						}}
					>
						Expand full file ↗
					</button>
				)}
			</div>
			{open && (
				<div style={{ marginTop: 8, border: `1px solid ${P.borderCard}`, borderRadius: 8, overflow: "hidden" }}>
					<DiffRows lines={ctx.lines} size="narrow" />
				</div>
			)}
		</div>
	);
}

function BatchBar({
	count,
	onResolve,
	onOneTask,
	onSeparate,
}: {
	count: number;
	onResolve: () => void;
	onOneTask: () => void;
	onSeparate: () => void;
}) {
	return (
		<div
			style={{
				flex: "none",
				padding: "11px 14px",
				borderTop: `1px solid ${P.borderBatch}`,
				background: P.batchBg,
				display: "flex",
				alignItems: "center",
				gap: 10,
			}}
		>
			<span style={{ fontSize: 12.5, fontWeight: 600, color: P.textStrong }}>{count} selected</span>
			<div style={{ flex: 1 }} />
			<button type="button" onClick={onResolve} style={outlineBtn(P.green, tint(P.green, 35), "6px 11px")}>
				✓ Resolve
			</button>
			<div style={{ display: "inline-flex" }}>
				<button
					type="button"
					onClick={onOneTask}
					style={{ ...solidBtn, borderRadius: "7px 0 0 7px", padding: "7px 12px" }}
				>
					⚡ Send batch
				</button>
				<DropdownMenu.Root modal={false}>
					<DropdownMenu.Trigger asChild>
						<button
							type="button"
							aria-label="Batch send options"
							style={{
								...solidBtn,
								width: 28,
								padding: 0,
								borderRadius: "0 7px 7px 0",
								borderLeft: "1px solid rgba(255,255,255,.25)",
								fontSize: 9,
							}}
						>
							▼
						</button>
					</DropdownMenu.Trigger>
					<DropdownMenu.Portal>
						<DropdownMenu.Content side="top" align="end" sideOffset={6} style={menuBox(236)}>
							<DropdownMenu.Item onSelect={onOneTask} style={menuItemStyle}>
								<MenuItemBody title="One task, all comments" desc="Bundle selected into a single worker" />
							</DropdownMenu.Item>
							<DropdownMenu.Item onSelect={onSeparate} style={menuItemStyle}>
								<MenuItemBody title="Separate task each" desc="Fan out one worker per comment" />
							</DropdownMenu.Item>
						</DropdownMenu.Content>
					</DropdownMenu.Portal>
				</DropdownMenu.Root>
			</div>
		</div>
	);
}

function ResolvedSection({ items }: { items: { group: Group; thread: Thread }[] }) {
	const [open, setOpen] = useState(false);
	return (
		<div
			style={{
				marginTop: 8,
				border: `1px solid ${P.borderCard}`,
				borderRadius: 10,
				overflow: "hidden",
				background: P.resolvedBg,
			}}
		>
			<div
				onClick={() => setOpen((o) => !o)}
				style={{ display: "flex", alignItems: "center", gap: 9, padding: "10px 12px", cursor: "pointer" }}
			>
				<span style={{ color: P.muted, fontSize: 10, width: 10 }}>{open ? "▾" : "▸"}</span>
				<span style={{ color: P.green, fontSize: 12 }}>✓</span>
				<span style={{ fontSize: 12.5, fontWeight: 600, color: P.secondary }}>Resolved</span>
				<span style={pill(11, P.muted, "1px 7px")}>{items.length}</span>
			</div>
			{open && (
				<div style={{ borderTop: `1px solid ${P.borderRail}` }}>
					{items.map(({ group, thread }) => {
						const c = thread.comments[0];
						return (
							<div
								key={thread.threadId}
								style={{ display: "flex", gap: 9, padding: "10px 12px", borderBottom: `1px solid ${P.divider}` }}
							>
								<div
									style={{
										flex: "none",
										width: 24,
										height: 24,
										borderRadius: "50%",
										display: "flex",
										alignItems: "center",
										justifyContent: "center",
										fontSize: 9.5,
										fontWeight: 700,
										color: "#fff",
										background: avatarBg(c?.author ?? "?", 0.42, 0.06),
									}}
								>
									{initialsFor(c?.author ?? "?")}
								</div>
								<div style={{ flex: 1, minWidth: 0 }}>
									<div style={{ display: "flex", alignItems: "center", gap: 7 }}>
										<span style={{ fontSize: 12, fontWeight: 600, color: P.secondary2 }}>{c?.author ?? "unknown"}</span>
										<span
											style={{
												fontFamily: MONO,
												fontSize: 10.5,
												color: P.muted2,
												overflow: "hidden",
												textOverflow: "ellipsis",
												whiteSpace: "nowrap",
											}}
										>
											PR #{group.number} · {baseName(thread.path)}:{thread.line}
										</span>
									</div>
									<div
										style={{
											fontSize: 12,
											lineHeight: 1.5,
											color: P.muted,
											marginTop: 3,
											display: "-webkit-box",
											WebkitLineClamp: 2,
											WebkitBoxOrient: "vertical",
											overflow: "hidden",
										}}
									>
										{(c?.body ?? "").replace(/`/g, "")}
									</div>
								</div>
							</div>
						);
					})}
				</div>
			)}
		</div>
	);
}

function NoPrEmptyState() {
	return (
		<div
			style={{
				display: "flex",
				flexDirection: "column",
				alignItems: "center",
				justifyContent: "center",
				padding: "80px 20px",
				textAlign: "center",
				color: P.muted2,
			}}
		>
			<div style={{ fontSize: 30, marginBottom: 14, opacity: 0.5 }}>◌</div>
			<div style={{ fontSize: 14, fontWeight: 600, color: P.secondary, marginBottom: 4 }}>
				No pull request opened yet.
			</div>
			<div style={{ fontSize: 12.5, lineHeight: 1.5, maxWidth: 240 }}>
				Reviews and review comments appear here once this session opens a PR or MR.
			</div>
		</div>
	);
}

// ---------------------------------------------------------------------------
// Reviewer verdict helpers (moved from SessionInspector)

function sessionReviewVerdict(reviewStates: PRReviewState[]): { label: string; tone: Tone } {
	if (reviewStates.some((r) => r.status === "running")) return { label: "Reviewing…", tone: "running" };
	if (reviewStates.some((r) => r.latestRun?.status === "failed")) return { label: "Failed", tone: "danger" };
	if (reviewStates.some((r) => r.status === "changes_requested")) return { label: "Changes requested", tone: "danger" };
	const eligible = reviewStates.filter((r) => r.status !== "ineligible");
	if (eligible.length > 0 && eligible.every((r) => r.status === "up_to_date"))
		return { label: "Approved", tone: "success" };
	return { label: "Not run", tone: "neutral" };
}

function reviewVerdict(reviewState: PRReviewState): { label: string; tone: Tone } {
	if (reviewState.latestRun?.status === "failed") return { label: "Failed", tone: "danger" };
	switch (reviewState.status) {
		case "running":
			return { label: "Reviewing…", tone: "running" };
		case "up_to_date":
			return { label: "Approved", tone: "success" };
		case "changes_requested":
			return { label: "Changes requested", tone: "danger" };
		case "needs_review":
		case "ineligible":
			return { label: "Not run", tone: "neutral" };
	}
	return { label: "Not run", tone: "neutral" };
}

function reviewSessionRunAction(reviewStates: PRReviewState[], isTriggering: boolean): string {
	if (isTriggering || reviewStates.some((r) => r.status === "running")) return "Reviewing…";
	if (reviewStates.some((r) => r.status === "changes_requested" || r.latestRun)) return "Re-run review";
	return "Run review";
}

// ---------------------------------------------------------------------------
// Preview mocks (VITE_NO_ELECTRON=1: no daemon)

function projectConfig(project: components["schemas"]["ProjectOrDegraded"] | undefined): ProjectConfig | undefined {
	if (!project || !("config" in project)) return undefined;
	return project.config;
}

function mockProjectConfig(): ProjectConfig {
	return { worker: { agent: "codex" }, orchestrator: { agent: "codex" }, reviewers: [{ harness: "codex" }] };
}

// Preview only: a spread of reviewer verdicts (approved / changes-requested /
// not-run) so the demo shows the combined block's review pill in each state.
function mockReviewsResponse(session: WorkspaceSession): ReviewsResponse {
	const prs = sortedPRs(session);
	return {
		reviewerHandleId: `${session.id}-reviewer`,
		reviews: prs.map((pr, index) => {
			const targetSha = `demo${pr.number}${index}`;
			const reviewedAt = new Date(Date.now() - (index + 1) * 11 * 60 * 1000).toISOString();
			const verdict =
				pr.state === "draft"
					? "ineligible"
					: index === 0
						? "approved"
						: index === 1
							? "changes_requested"
							: "needs_review";
			const latestRun =
				verdict === "approved" || verdict === "changes_requested"
					? {
							batchId: `demo-batch-${session.id}`,
							body: verdict === "approved" ? "Demo review approved." : "Demo review found polish feedback.",
							createdAt: reviewedAt,
							githubReviewId: `${pr.number}01`,
							harness: "codex",
							id: `demo-review-run-${pr.number}`,
							prUrl: pr.url,
							reviewId: `demo-review-${pr.number}`,
							sessionId: session.id,
							status: "delivered",
							targetSha,
							verdict: verdict === "approved" ? "approved" : "changes_requested",
						}
					: undefined;
			return {
				latestRun,
				prNumber: pr.number,
				prUrl: pr.url,
				status:
					verdict === "approved"
						? "up_to_date"
						: verdict === "changes_requested"
							? "changes_requested"
							: verdict === "ineligible"
								? "ineligible"
								: "needs_review",
				targetSha,
				title: mockReviewTitle(pr.number),
			};
		}),
	};
}

function mockReviewTitle(prNumber: number): string {
	switch (prNumber) {
		case 319:
			return "Browser preview rail renders inside AO";
		case 320:
			return "Reviews tab nests comment threads per PR";
		case 321:
			return "Draft child PR waits for parent review";
		default:
			return `Demo pull request ${prNumber}`;
	}
}

// Preview only: comment threads for the demo session's PRs, exercising a GitHub
// example (inline code + a resolved thread), a GitLab example (GL badge + an
// outdated-diff system note), and a PR with no comments.
function mockPRComments(session: WorkspaceSession): Group[] {
	const prs = sortedPRs(session);
	if (prs.length === 0) return [];
	const c = (id: string, author: string, body: string, resolved = false, system = false, minsAgo = 42) => ({
		id,
		author,
		body,
		url: "",
		resolved,
		isBot: false,
		system,
		createdAt: new Date(Date.now() - minsAgo * 60 * 1000).toISOString(),
	});
	const groups: Group[] = [];
	const gh = prs[0];
	groups.push({
		prUrl: gh.url,
		htmlUrl: gh.url,
		provider: "github",
		number: gh.number,
		headSha: "abc123",
		threads: [
			{
				threadId: `${gh.number}-t1`,
				path: "frontend/src/renderer/components/BrowserPanel.tsx",
				line: 88,
				resolved: false,
				isBot: false,
				comments: [
					c(
						`${gh.number}-c1`,
						"priya",
						"This effect re-runs on every render — wrap the handler in `useCallback` so the panel doesn't tear down mid-preview.",
						false,
						false,
						42,
					),
					c(
						`${gh.number}-c1b`,
						"fluke.s",
						"Good catch. Wrapped it and pinned the deps. Pushed in `a3f9c2`.",
						false,
						false,
						12,
					),
					c(`${gh.number}-c1c`, "priya", "Perfect, thanks.", false, false, 3),
				],
			},
			{
				threadId: `${gh.number}-t2`,
				path: "frontend/src/renderer/hooks/useBrowserView.ts",
				line: 33,
				resolved: false,
				isBot: false,
				comments: [
					c(`${gh.number}-c2`, "marco", "Prefer a stable dependency here; the array identity changes each poll."),
				],
			},
			{
				threadId: `${gh.number}-t3`,
				path: "docs/assets/readme/browser-preview.png",
				line: 1,
				resolved: true,
				isBot: false,
				comments: [c(`${gh.number}-c3`, "priya", "Asset looks good now.", true)],
			},
		],
	});
	const gl = prs[1];
	if (gl) {
		const glUrl = `https://gitlab.com/example-org/agent-orchestrator/-/merge_requests/${gl.number}`;
		groups.push({
			prUrl: glUrl,
			htmlUrl: glUrl,
			provider: "gitlab",
			number: gl.number,
			headSha: "def456",
			threads: [
				{
					threadId: `${gl.number}-t1`,
					path: "backend/internal/httpd/controllers/reviews.go",
					line: 141,
					resolved: false,
					isBot: false,
					comments: [
						c(`${gl.number}-c1`, "fluke.s", "Return the wrapped error here so the request id is preserved."),
						c(
							`${gl.number}-c2`,
							"fluke.s",
							`changed this line in [version 4 of the diff](/example-org/agent-orchestrator/-/merge_requests/${gl.number}/diffs?diff_id=177522)`,
							false,
							true,
						),
					],
				},
			],
		});
	}
	// prs[2] (the draft) intentionally has no comment group → "No unresolved comments."
	return groups;
}
