import { type CSSProperties, type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { CircleSlash, Contrast, ExternalLink, Eye, FolderOpen, OctagonX } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage, getApiBaseUrl } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { sessionSmokeQueryKey, useSessionSmokeChecks, type SmokeChecksResponse } from "../hooks/useSessionSmokeChecks";
import { useSessionScmSummary } from "../hooks/useSessionScmSummary";
import { useSessionCrewRuns } from "../hooks/useSessionCrewRuns";
import {
	ACCENT,
	MONO,
	PALETTE as P,
	accentMix,
	activeChecks,
	agentMeta,
	agentState,
	authorLabel,
	checkTag,
	checklistState,
	evidenceForRun,
	headShaFor,
	isAgentStale,
	latestRun,
	progressFor,
	progressSegments,
	relativeTime,
	retiredChecks,
	RUN_LABEL,
	runState,
	runTag,
	runsNewestFirst,
	shortSha,
	standDownActor,
	unknownRunEvidence,
	verdictMeta,
	type AgentState,
	type HeadRef,
	type SmokeCheck,
	type SmokeEvidence,
	type SmokeProgress,
	type SmokeRun,
	type ChecklistState,
	type SmokeStandDown,
} from "../lib/smoke-test";
import { CrewRunStrip } from "./CrewRunStrip";
import { Toast } from "./inbox-ui";
import { MediaLightbox, MediaThumb } from "./MediaLightbox";
import { JiraLinkDialog } from "./JiraLinkDialog";
import { jiraKeyFromIssueId } from "../types/workspace";

type PostToJiraResponse = components["schemas"]["PostSmokeToJiraResponse"];

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

const ACCEPT = "image/png,image/jpeg,image/gif,image/webp,video/mp4,video/webm,video/quicktime";

// Evidence thumbnail box (kept ~16:11 so screenshots read at a glance before the
// full-size lightbox). Both the media element and its positioning wrapper use these.
const THUMB_W = 150;
const THUMB_H = 106;

const DECIDED_CAPTION: Record<string, string> = {
	pass: "Passed — behaves as expected",
	fail: "Failed — needs another look",
	skip: "Skipped — doesn't apply",
};

/** Daemon URL for one evidence blob (bytes flow through the CORS-gated fetch). */
function evidenceUrl(sessionId: string, checkId: string, evidenceId: string): string {
	return `${getApiBaseUrl()}/api/v1/sessions/${encodeURIComponent(sessionId)}/smoke-checks/${encodeURIComponent(checkId)}/evidence/${encodeURIComponent(evidenceId)}`;
}

/**
 * Tests tab — the "Smoke test" checklist: a worker authored 3–6 manual cases the
 * user plays live in the app, attaching evidence (drop/paste an image or short
 * clip), noting what they saw, and marking Pass / Fail / Skip. A report-back bar
 * composes the results and delivers them to the worker. Pixel-matched to the
 * Tests.dc.html design, mirroring the sibling Comments tab's inline-style
 * approach — the palette resolves to themed tokens so the tab follows light
 * mode. Always visible with an empty state, even when the session has no
 * checklist.
 */
export function SmokeTestView({
	sessionId,
	worker,
	issueId,
}: {
	sessionId: string;
	worker?: string;
	issueId?: string;
}) {
	const queryClient = useQueryClient();
	const [toast, setToast] = useState<string | null>(null);
	const [linkOpen, setLinkOpen] = useState(false);
	const jiraKey = jiraKeyFromIssueId(issueId);
	const jiraLinked = Boolean(jiraKey);
	const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const showToast = useCallback((text: string) => {
		setToast(text);
		if (toastTimer.current) clearTimeout(toastTimer.current);
		toastTimer.current = setTimeout(() => setToast(null), 2600);
	}, []);
	useEffect(() => () => void (toastTimer.current && clearTimeout(toastTimer.current)), []);

	const query = useSessionSmokeChecks(sessionId, worker);

	const invalidate = useCallback(() => {
		void queryClient.invalidateQueries({ queryKey: sessionSmokeQueryKey(sessionId) });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	}, [queryClient, sessionId]);

	const setVerdict = useMutation({
		mutationFn: async (vars: { checkId: string; verdict: "pass" | "fail" | "skip"; note: string }) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/verdict", {
				params: { path: { sessionId, checkId: vars.checkId } },
				body: { verdict: vars.verdict, note: vars.note },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to save verdict"));
		},
		onSuccess: () => invalidate(),
	});

	const resetCheck = useMutation({
		mutationFn: async (checkId: string) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/reset", {
				params: { path: { sessionId, checkId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to reset case"));
		},
		onSuccess: () => invalidate(),
	});

	const report = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/smoke-checks/report", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to report results"));
			return data;
		},
		onSuccess: (data) => {
			invalidate();
			const label = query.data?.worker || worker || "worker";
			if (data?.target === "persisted") {
				showToast("Results saved — will reach the worker when it's live");
			} else {
				showToast(`Reported results → ${data?.target === "orchestrator" ? "orchestrator" : label}`);
			}
		},
	});

	const postJira = useMutation({
		mutationFn: async (): Promise<PostToJiraResponse> => {
			if (usePreviewData) {
				return {
					key: jiraKey ?? "DEMO-101",
					commentUrl: "",
					attachmentsUploaded: 0,
					rowsPosted: progress.checked,
					embeddedMedia: false,
					evidenceLinked: 0,
				};
			}
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/smoke-checks/jira", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to post results to Jira"));
			return data!;
		},
		onSuccess: (data) => {
			invalidate();
			const rows = data.rowsPosted;
			// Jira ingests an upload asynchronously, so evidence can end up as a
			// download link rather than an inline preview. The comment is still
			// correct, but the difference is invisible from here — say it, otherwise
			// a degraded post reads exactly like a clean one and the only way to find
			// out is to open the issue.
			const linked = data.evidenceLinked ?? 0;
			const degraded =
				linked > 0 ? ` — ${linked} evidence file${linked === 1 ? "" : "s"} posted as links, not previews` : "";
			showToast(`Posted ${rows} result${rows === 1 ? "" : "s"} to ${data.key}${degraded}`);
			if (data.commentUrl) window.open(data.commentUrl, "_blank", "noopener,noreferrer");
		},
		onError: (err) => showToast(apiErrorMessage(err, "Couldn't post to Jira")),
	});

	// The button guides an unlinked session to the link flow first (locked
	// decision #2); a linked session posts the run rows as a Jira table comment.
	const onPostJira = () => {
		if (!jiraLinked) {
			setLinkOpen(true);
			return;
		}
		postJira.mutate();
	};

	const uploadEvidence = useCallback(
		async (checkId: string, file: File) => {
			const form = new FormData();
			form.append("file", file);
			const res = await fetch(
				`${getApiBaseUrl()}/api/v1/sessions/${encodeURIComponent(sessionId)}/smoke-checks/${encodeURIComponent(checkId)}/evidence`,
				{ method: "POST", body: form },
			);
			if (!res.ok) {
				showToast("Couldn't attach that file");
				return;
			}
			invalidate();
			showToast("Evidence attached");
		},
		[sessionId, invalidate, showToast],
	);

	// Reveal-in-Finder / Open for a stored evidence item. The on-disk blob is
	// extensionless, so the daemon materializes a correctly-named, correctly-typed
	// copy and returns its path; the desktop shell then reveals or opens THAT.
	const revealEvidence = useCallback(
		async (checkId: string, evidenceId: string, mode: "reveal" | "open") => {
			if (usePreviewData) return;
			const { data, error } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/evidence/{evidenceId}/export",
				{ params: { path: { sessionId, checkId, evidenceId } } },
			);
			if (error || !data?.path) {
				showToast(apiErrorMessage(error, "Couldn't open that file"));
				return;
			}
			try {
				if (mode === "open") await aoBridge.shell.openPath(data.path);
				else await aoBridge.shell.showItemInFolder(data.path);
			} catch {
				showToast(mode === "open" ? "Couldn't open that file" : "Couldn't reveal that file");
			}
		},
		[sessionId, showToast],
	);

	// Optimistically drop the thumbnail, then reconcile with the server's
	// authoritative case (the DELETE returns the updated check). On failure the
	// prior cache is restored and a toast explains. No blocking confirm (dialog
	// policy) — the small hover-revealed × plus instant feedback is the guard.
	const deleteEvidence = useMutation({
		mutationFn: async (vars: { checkId: string; evidenceId: string }) => {
			if (usePreviewData) return;
			const { error } = await apiClient.DELETE(
				"/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/evidence/{evidenceId}",
				{ params: { path: { sessionId, checkId: vars.checkId, evidenceId: vars.evidenceId } } },
			);
			if (error) throw new Error(apiErrorMessage(error, "Unable to remove evidence"));
		},
		onMutate: async (vars) => {
			await queryClient.cancelQueries({ queryKey: sessionSmokeQueryKey(sessionId) });
			const prev = queryClient.getQueryData<SmokeChecksResponse>(sessionSmokeQueryKey(sessionId));
			queryClient.setQueryData<SmokeChecksResponse>(sessionSmokeQueryKey(sessionId), (old) =>
				old
					? {
							...old,
							checks: old.checks.map((c) =>
								c.id === vars.checkId ? { ...c, evidence: c.evidence.filter((e) => e.id !== vars.evidenceId) } : c,
							),
						}
					: old,
			);
			return { prev };
		},
		onError: (err, _vars, ctx) => {
			if (ctx?.prev) queryClient.setQueryData(sessionSmokeQueryKey(sessionId), ctx.prev);
			showToast(apiErrorMessage(err, "Couldn't remove that evidence"));
		},
		onSettled: () => invalidate(),
	});

	// Head commits, for the staleness rule below. Same query key as the Summary
	// tab's readiness strip, so this is a cache read rather than a second request.
	const scmQuery = useSessionScmSummary(sessionId);
	const heads: HeadRef[] = (scmQuery.data ?? []).map((pr) => ({ number: pr.number, headSha: pr.headSha }));

	const data = query.data;
	// Retired cases are part of the record but not part of what the user is asked
	// to play, so they stay out of the play list (and out of progressFor's
	// counts). They are not hidden either - they surface, frozen and with their
	// reason, in the "retired from this checklist" disclosure at the foot of the
	// list, because "3 retired, now covered by tests" is the auditable form of a
	// checklist shrinking and three cases silently vanishing is not.
	const crewRuns = useSessionCrewRuns(sessionId);
	const checks = activeChecks(data?.checks ?? []);
	const retired = retiredChecks(data?.checks ?? []);
	const progress = progressFor(checks);
	// An empty list and a stood-down one are DIFFERENT answers, so they are one
	// decision made here rather than two conditions repeated at each use site.
	const standDown = data?.standDown ?? null;
	const state = checklistState(data?.checks ?? [], standDown);
	const workerLabel = data?.worker || worker || "worker";

	const decide = (check: SmokeCheck, verdict: "pass" | "fail" | "skip", note: string) => {
		setVerdict.mutate({ checkId: check.id, verdict, note });
		showToast(
			verdict === "pass" ? "Marked Pass" : verdict === "fail" ? "Marked Fail · worker will be notified" : "Marked Skip",
		);
	};

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
			<Header worker={workerLabel} progress={progress} stoodDown={state === "stood-down"} />

			<div style={{ flex: 1, overflowY: "auto", padding: "12px 12px 24px" }}>
				{/* The machine's bracketed runs sit ABOVE the checklist: a run thrown
				    away because the tree moved under it is a fact about whether
				    anything below can be believed, so it must be read first. Renders
				    nothing at all when the session has never bracketed a run. */}
				<CrewRunStrip runs={crewRuns.data?.runs ?? []} />
				{query.isLoading && <p style={{ padding: 16, fontSize: 12.5, color: P.muted2 }}>Loading smoke checks…</p>}
				{query.error && (
					<p style={{ padding: 16, fontSize: 12.5, color: P.danger }}>
						{apiErrorMessage(query.error, "Unable to load smoke checks")}
					</p>
				)}
				{!query.isLoading && !query.error && state !== "cases" && (
					<EmptyState state={state} retired={retired.length} standDown={standDown} />
				)}
				{!query.isLoading &&
					!query.error &&
					checks.map((check) => (
						<CaseCard
							key={check.id}
							sessionId={sessionId}
							check={check}
							heads={heads}
							busy={setVerdict.isPending || resetCheck.isPending}
							onDecide={(verdict, note) => decide(check, verdict, note)}
							onChange={() => resetCheck.mutate(check.id)}
							onUpload={(file) => uploadEvidence(check.id, file)}
							onDeleteEvidence={(evidenceId) => deleteEvidence.mutate({ checkId: check.id, evidenceId })}
							onRevealEvidence={(evidenceId, mode) => revealEvidence(check.id, evidenceId, mode)}
						/>
					))}
				{!query.isLoading && !query.error && retired.length > 0 && (
					<RetiredSection
						sessionId={sessionId}
						checks={retired}
						onRevealEvidence={(checkId, evidenceId, mode) => revealEvidence(checkId, evidenceId, mode)}
					/>
				)}
			</div>

			{progress.checked > 0 && (
				<ReportBar
					progress={progress}
					busy={report.isPending}
					jiraBusy={postJira.isPending}
					jiraLinked={jiraLinked}
					onReport={() => report.mutate()}
					onPostJira={onPostJira}
				/>
			)}

			<JiraLinkDialog sessionId={sessionId} open={linkOpen} onOpenChange={setLinkOpen} />

			{toast && <Toast text={toast} />}
		</div>
	);
}

// ---------------------------------------------------------------------------

function Header({ worker, progress, stoodDown }: { worker: string; progress: SmokeProgress; stoodDown: boolean }) {
	const segments = progressSegments(progress);
	return (
		<div style={{ flex: "none", padding: "16px 16px 13px", borderBottom: `1px solid ${P.divider}` }}>
			<div style={{ display: "flex", alignItems: "baseline", gap: 9 }}>
				<span style={{ fontSize: 16, fontWeight: 700, color: P.textStrong }}>Smoke test</span>
				<span
					style={{
						fontSize: 12,
						fontWeight: 600,
						color: P.secondary,
						background: P.pillBg,
						border: `1px solid ${P.borderPill}`,
						borderRadius: 999,
						padding: "1px 8px",
					}}
				>
					{progress.total}
				</span>
			</div>

			<div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 9 }}>
				<span
					aria-hidden="true"
					style={{
						flex: "none",
						width: 18,
						height: 18,
						borderRadius: "50%",
						display: "flex",
						alignItems: "center",
						justifyContent: "center",
						fontSize: 10,
						color: "var(--accent-fg)",
						// Keeps the light end dark enough for the white glyph (3.8:1 at
						// 85%, vs 3.0:1 if it tracked the handoff's lighter stop).
						background: `linear-gradient(135deg, ${ACCENT}, ${accentMix(85, "#ffffff")})`,
					}}
				>
					◆
				</span>
				{/* "run these live" is an instruction, and it must not sit above a
				    panel saying there is nothing to run - which is exactly the kind of
				    contradiction that makes a screen unreadable. */}
				<span style={{ fontSize: 12, color: P.secondary2, lineHeight: 1.4 }}>
					Checklist from <b style={{ color: P.body, fontWeight: 600 }}>{worker}</b>
					{stoodDown ? " · nothing to run" : " · run these live & attach evidence"}
				</span>
			</div>

			{/* A track and a "0 of 0 verified" are chrome for progress that cannot
			    exist. On an empty checklist they are noise; above a stand-down they
			    read as a contradiction of the panel below. */}
			{progress.total > 0 && (
				<>
					<div
						style={{
							marginTop: 12,
							display: "flex",
							height: 8,
							borderRadius: 999,
							overflow: "hidden",
							background: P.trackBg,
						}}
					>
						{segments.map((seg, i) =>
							seg.count > 0 ? (
								<div key={i} style={{ width: `${(seg.count / progress.total) * 100}%`, background: seg.color }} />
							) : null,
						)}
					</div>

					<div
						style={{ marginTop: 9, display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap", fontSize: 11.5 }}
					>
						<span style={{ color: P.body }}>
							<b style={{ color: P.textStrong, fontWeight: 700 }}>{progress.checked}</b> of {progress.total} verified
						</span>
						{progress.fail > 0 && <CountChip color={P.segFail} text={`${progress.fail} failed`} />}
						{progress.skip > 0 && <CountChip color={P.muted2} text={`${progress.skip} skipped`} />}
						{progress.pending > 0 && <CountChip color={P.segSkip} text={`${progress.pending} to check`} />}
					</div>
				</>
			)}

			<QaBanner progress={progress} />
		</div>
	);
}

/**
 * What the machine did, said once at the top - and deliberately kept OUT of the
 * progress bar and the counts row above it. Those two say how far a PERSON has
 * got; folding a machine result into either is the exact mistake this tab must
 * not make, because "2 of 4 verified" with nobody having touched the app is a
 * lie that reads as progress.
 *
 * So the machine gets a sentence instead of a number, and the sentence always
 * says what the result is not.
 */
function QaBanner({ progress }: { progress: SmokeProgress }) {
	const lines: { icon: typeof Contrast; color: string; text: ReactNode }[] = [];
	if (progress.agentFail > 0) {
		lines.push({
			icon: OctagonX,
			color: P.segFail,
			text: (
				<>
					<b style={{ fontWeight: 600 }}>
						qa hit a failure on {progress.agentFail} case{progress.agentFail === 1 ? "" : "s"}
					</b>{" "}
					you haven&apos;t played. Read what it saw, then judge it yourself.
				</>
			),
		});
	}
	if (progress.agentPass > 0) {
		lines.push({
			icon: Contrast,
			color: P.qaFg,
			text: (
				<>
					<b style={{ fontWeight: 600 }}>
						qa ran {progress.agentPass} of the {progress.pending} still open
					</b>{" "}
					and the steps passed. That is not a check off your list; nobody has seen it behave yet.
				</>
			),
		});
	}
	if (progress.agentCaptured > 0) {
		lines.push({
			icon: Eye,
			color: P.qaFg,
			text: (
				<>
					<b style={{ fontWeight: 600 }}>
						qa captured the screen on {progress.agentCaptured} case{progress.agentCaptured === 1 ? "" : "s"}
					</b>{" "}
					without settling {progress.agentCaptured === 1 ? "it" : "them"}, so you can call{" "}
					{progress.agentCaptured === 1 ? "that one" : "those"} from the evidence without driving the app yourself.
				</>
			),
		});
	}
	if (lines.length === 0) return null;
	return (
		<div
			style={{
				marginTop: 10,
				border: `1px solid ${P.qaBorder}`,
				background: P.qaBg,
				borderRadius: 8,
				padding: "8px 10px",
				display: "flex",
				flexDirection: "column",
				gap: 6,
			}}
		>
			{lines.map(({ icon: Icon, color, text }, i) => (
				<div key={i} style={{ display: "flex", alignItems: "flex-start", gap: 7 }}>
					<Icon size={12} strokeWidth={2.2} color={color} aria-hidden="true" style={{ flex: "none", marginTop: 1 }} />
					<span style={{ fontSize: 11.5, lineHeight: 1.45, color: P.qaFg }}>{text}</span>
				</div>
			))}
		</div>
	);
}

function CountChip({ color, text }: { color: string; text: string }) {
	return (
		<span style={{ display: "inline-flex", alignItems: "center", gap: 5, color: P.secondary2 }}>
			<span aria-hidden="true" style={{ width: 8, height: 8, borderRadius: 2, background: color }} />
			<span>{text}</span>
		</span>
	);
}

function CaseCard({
	sessionId,
	check,
	heads,
	busy,
	onDecide,
	onChange,
	onUpload,
	onDeleteEvidence,
	onRevealEvidence,
}: {
	sessionId: string;
	check: SmokeCheck;
	heads: HeadRef[];
	busy: boolean;
	onDecide: (verdict: "pass" | "fail" | "skip", note: string) => void;
	onChange: () => void;
	onUpload: (file: File) => void;
	onDeleteEvidence: (evidenceId: string) => void;
	onRevealEvidence: (evidenceId: string, mode: "reveal" | "open") => void;
}) {
	const [open, setOpen] = useState(check.verdict === "pending");
	const [note, setNote] = useState(check.note ?? "");
	const meta = verdictMeta(check.verdict);
	const decided = check.verdict !== "pending";
	const hasEvidence = check.evidence.length > 0;
	// The machine's lane, read alongside the human's - never in place of it. The
	// leading glyph box below stays the HUMAN's verdict, so the one mark that
	// reads as "this case is done" can only ever be earned by a person.
	const qa = agentState(check);
	const qaStale = isAgentStale(check, heads);
	const qaShots = (check.agentEvidence ?? []).length;
	const author = authorLabel(check);
	const authoredWhen = relativeTime(check.authoredAt, Date.now());

	return (
		<div
			style={{
				border: `1px solid ${open ? P.borderCardOpen : P.borderCard}`,
				borderRadius: 11,
				overflow: "hidden",
				marginBottom: 10,
				background: open ? P.cardBgOpen : P.cardBg,
			}}
		>
			<div
				onClick={() => setOpen((o) => !o)}
				style={{ display: "flex", alignItems: "flex-start", gap: 10, padding: "11px 12px", cursor: "pointer" }}
			>
				<span
					aria-hidden="true"
					style={{
						flex: "none",
						width: 24,
						height: 24,
						borderRadius: 7,
						display: "flex",
						alignItems: "center",
						justifyContent: "center",
						fontSize: 12,
						fontWeight: 700,
						color: meta.color,
						background: meta.pillBg,
						border: `1px solid ${meta.pillBorder}`,
					}}
				>
					{meta.icon}
				</span>
				<div style={{ flex: 1, minWidth: 0 }}>
					<div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", rowGap: 5 }}>
						<span style={{ fontFamily: MONO, fontSize: 10, fontWeight: 700, letterSpacing: ".05em", color: P.muted }}>
							{checkTag(check.seq)}
						</span>
						<StatusPill meta={meta} />
						<QaChip state={qa} stale={qaStale} />
					</div>
					<div
						style={{
							marginTop: 3,
							fontSize: 13,
							fontWeight: 600,
							color: P.text,
							lineHeight: 1.42,
						}}
					>
						{check.name}
					</div>
					<div style={{ marginTop: 6, fontSize: 10.5 }}>
						<span style={{ color: hasEvidence ? P.evidenceOn : P.muted }}>
							{hasEvidence ? "▣ evidence attached" : "□ no evidence yet"}
						</span>
						{/* Who WROTE the case. Both crew members author this list - dev from
						    the call sites it changed, qa from what a user would do - so this
						    is part of reading a case, not decoration. It sits here rather
						    than in the pill row above on purpose: a "qa" author chip next to
						    the "qa · ran" machine chip would read as one fact about qa when
						    they are two different ones. A case AO could not attribute shows
						    nothing rather than a guessed author. */}
						{author && (
							<span style={{ color: P.muted }} title={check.authoredBy ?? undefined}>
								{" · "}
								{author}
								{authoredWhen && ` · ${authoredWhen}`}
							</span>
						)}
						{/* qa's screenshots are worth advertising here: on a case its own
						    capture could not settle, they are what lets the person decide
						    without re-driving the app. Named as qa's, never merged into
						    "yours". */}
						{qaShots > 0 && (
							<span style={{ color: P.qaFg }}>
								{" · "}
								{qaShots} from qa
							</span>
						)}
					</div>
				</div>
				<span aria-hidden="true" style={{ flex: "none", fontSize: 14, color: P.secondary, width: 14 }}>
					{open ? "▾" : "▸"}
				</span>
			</div>

			{open && (
				<div style={{ borderTop: `1px solid ${P.borderExpand}`, padding: 14 }}>
					<WhyBox check={check} />
					{check.steps.length > 0 && <Steps steps={check.steps} />}
					{check.expected && <Expected expected={check.expected} />}
					{/* Sits above the human's own controls, which are untouched below: it
					    is context for playing the case, not a substitute for playing it. */}
					<QaBlock
						sessionId={sessionId}
						check={check}
						heads={heads}
						onReveal={(evidenceId, mode) => onRevealEvidence(evidenceId, mode)}
					/>
					<EvidenceSection
						sessionId={sessionId}
						check={check}
						onUpload={onUpload}
						onDelete={onDeleteEvidence}
						onReveal={onRevealEvidence}
					/>

					<textarea
						value={note}
						onChange={(e) => setNote(e.target.value)}
						placeholder="Add a note about what you saw (optional)…"
						aria-label={`Note for ${check.name}`}
						style={{
							width: "100%",
							minHeight: 60,
							marginTop: 12,
							resize: "vertical",
							background: P.cardBg,
							border: `1px solid ${P.borderPill}`,
							borderRadius: 8,
							padding: 9,
							outline: "none",
							color: P.text,
							fontSize: 12.5,
							lineHeight: 1.5,
							fontFamily: "inherit",
						}}
					/>

					<VerdictControls
						decided={decided}
						check={check}
						busy={busy}
						onDecide={(verdict) => {
							onDecide(verdict, note);
							// Collapse the case the moment a verdict is recorded so the
							// list stays scannable; re-open from the header to change it.
							setOpen(false);
						}}
						onChange={onChange}
					/>
				</div>
			)}
		</div>
	);
}

function StatusPill({ meta }: { meta: ReturnType<typeof verdictMeta> }) {
	return (
		<span
			style={{
				fontSize: 10.5,
				fontWeight: 600,
				color: meta.color,
				background: meta.pillBg,
				border: `1px solid ${meta.pillBorder}`,
				borderRadius: 999,
				padding: "1px 8px",
			}}
		>
			{meta.label}
		</span>
	);
}

/** Glyphs for the machine's lane. Drawn icons, where the human's verdicts are
 * the tab's typographic glyphs (✓ ✗ ○ ⊘) - so the two actors differ by SHAPE and
 * wording, not by colour alone. `Contrast` (a half-filled disc) is the shape the
 * design gives "the machine ran, a person has not": half of the answer. */
const QA_ICON: Record<Exclude<AgentState, "none">, typeof Contrast> = {
	pass: Contrast,
	fail: OctagonX,
	skip: CircleSlash,
	captured: Eye,
};

/**
 * The machine's mark on a collapsed case, beside the human's status pill.
 *
 * Never a check, never green, and never in the leading glyph box: that box is
 * the human's verdict and is the only mark on this tab that means "done".
 * A case the machine has not touched shows NOTHING here - an empty circle in
 * this slot would read "qa hasn't got to it yet" on a case where no machine
 * verdict is ever coming, which is the reading that sends a person to fix the
 * wrong thing.
 */
function QaChip({ state, stale }: { state: AgentState; stale: boolean }) {
	const meta = agentMeta(state);
	if (!meta) return null;
	const Icon = QA_ICON[state as Exclude<AgentState, "none">];
	return (
		<span
			title={
				stale
					? `${meta.headline}. ${meta.caption} It ran against an older commit.`
					: `${meta.headline}. ${meta.caption}`
			}
			style={{
				display: "inline-flex",
				alignItems: "center",
				gap: 5,
				fontSize: 10.5,
				fontWeight: 600,
				// A stale result is muted rather than recoloured: the word "stale"
				// carries the meaning, and dimming keeps it from competing with the
				// human's pill for attention.
				color: stale ? P.muted : meta.color,
				background: P.qaBg,
				border: `1px solid ${P.qaBorder}`,
				borderRadius: 999,
				padding: "1px 8px",
			}}
		>
			<Icon size={10} strokeWidth={2.2} aria-hidden="true" />
			{meta.label}
			{stale ? " · stale" : ""}
		</span>
	);
}

/**
 * One strip of machine captures, under its own label, with its own lightbox.
 *
 * Read-only: no × and no dropzone. Merging these into "your evidence" would
 * destroy the provenance you go back to when you distrust a verdict.
 */
function QaShotStrip({
	sessionId,
	checkId,
	label,
	caption,
	shots,
	onReveal,
}: {
	sessionId: string;
	checkId: string;
	label: string;
	caption?: string;
	shots: SmokeEvidence[];
	onReveal: (evidenceId: string, mode: "reveal" | "open") => void;
}) {
	const [lightboxIndex, setLightboxIndex] = useState<number | null>(null);
	const triggerRef = useRef<HTMLElement | null>(null);
	if (shots.length === 0) return null;
	return (
		<>
			<div style={{ marginTop: 9, fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.qaFg }}>
				{label}
				{caption && <span style={{ fontWeight: 500, color: P.muted, letterSpacing: 0 }}> · {caption}</span>}
			</div>
			<div style={{ marginTop: 8, display: "flex", gap: 8, flexWrap: "wrap" }}>
				{shots.map((ev, i) => (
					<EvidenceThumb
						key={ev.id}
						sessionId={sessionId}
						checkId={checkId}
						evidence={ev}
						onOpen={(trigger) => {
							triggerRef.current = trigger;
							setLightboxIndex(i);
						}}
						onReveal={() => onReveal(ev.id, "reveal")}
						onOpenFile={() => onReveal(ev.id, "open")}
					/>
				))}
			</div>
			{lightboxIndex !== null && (
				<MediaLightbox
					items={shots.map((e) => ({
						id: e.id,
						filename: e.filename,
						mime: e.mime,
						src: evidenceUrl(sessionId, checkId, e.id),
					}))}
					index={lightboxIndex}
					onIndexChange={setLightboxIndex}
					onClose={() => setLightboxIndex(null)}
					triggerRef={triggerRef}
				/>
			)}
		</>
	);
}

/** One EARLIER round, collapsed to a line and expandable to what it saw.
 *
 * Collapsed it carries the three facts that make a history worth having - which
 * round, what it concluded, and against which commit - so "this failed at
 * d44ad43 and passes at 9f10c22" is legible without opening anything. */
function QaRunRow({
	sessionId,
	check,
	run,
	now,
	onReveal,
}: {
	sessionId: string;
	check: SmokeCheck;
	run: SmokeRun;
	now: number;
	onReveal: (evidenceId: string, mode: "reveal" | "open") => void;
}) {
	const [open, setOpen] = useState(false);
	const state = runState(run);
	const meta = state === "open" ? null : agentMeta(state);
	const Icon = state === "open" ? Eye : QA_ICON[state as Exclude<AgentState, "none">];
	const shots = evidenceForRun(check, run.id);
	const when = relativeTime(run.recordedAt ?? run.createdAt, now);
	const expandable = Boolean(run.note) || shots.length > 0;

	return (
		<div style={{ borderTop: `1px solid ${P.qaBorder}` }}>
			<button
				type="button"
				data-testid={`qa-run-${run.id}`}
				onClick={() => expandable && setOpen((o) => !o)}
				disabled={!expandable}
				style={{
					width: "100%",
					display: "flex",
					alignItems: "center",
					gap: 7,
					flexWrap: "wrap",
					rowGap: 3,
					padding: "6px 0",
					background: "none",
					border: "none",
					textAlign: "left",
					cursor: expandable ? "pointer" : "default",
					font: "inherit",
				}}
			>
				<span style={{ fontFamily: MONO, fontSize: 9.5, fontWeight: 700, letterSpacing: ".05em", color: P.muted }}>
					{runTag(run.seq)}
				</span>
				<Icon size={11} strokeWidth={2.2} color={meta ? meta.color : P.muted} aria-hidden="true" />
				<span style={{ fontSize: 11.5, fontWeight: 600, color: meta ? meta.color : P.muted }}>
					{/* A round that never concluded says so, rather than borrowing the
					    "evidence only" wording from one that deliberately did not judge. */}
					{RUN_LABEL[state]}
				</span>
				{run.sha && <span style={{ fontFamily: MONO, fontSize: 10.5, color: P.muted }}>{shortSha(run.sha)}</span>}
				{when && <span style={{ fontSize: 10.5, color: P.muted }}>· {when}</span>}
				{shots.length > 0 && (
					<span style={{ fontSize: 10.5, color: P.muted }}>
						· {shots.length} shot{shots.length === 1 ? "" : "s"}
					</span>
				)}
				{/* The same chevron the case card uses, so a round with something to
				    read looks openable rather than looking like a dead line. */}
				{expandable && (
					<span aria-hidden="true" style={{ marginLeft: "auto", fontSize: 12, color: P.secondary }}>
						{open ? "▾" : "▸"}
					</span>
				)}
			</button>
			{open && (
				<div style={{ paddingBottom: 8 }}>
					{run.note && <div style={{ fontSize: 12, lineHeight: 1.5, color: P.qaFg }}>{run.note}</div>}
					<QaShotStrip
						sessionId={sessionId}
						checkId={check.id}
						label={`CAPTURED IN ${runTag(run.seq)}`}
						shots={shots}
						onReveal={onReveal}
					/>
				</div>
			)}
		</div>
	);
}

/**
 * The machine's result, in full, inside an expanded case: what it did, what it
 * said, what it captured, and against which commit - plus every EARLIER round it
 * ran, collapsed underneath.
 *
 * The history is the point. One overwritten result could not say that a case
 * failed at one commit and passes at another, so a person had to reconstruct it
 * from a sentence in a note. Flat and neutral - the verdict hues on this tab
 * belong to the person.
 */
function QaBlock({
	sessionId,
	check,
	heads,
	onReveal,
}: {
	sessionId: string;
	check: SmokeCheck;
	heads: HeadRef[];
	onReveal: (evidenceId: string, mode: "reveal" | "open") => void;
}) {
	const [now] = useState(() => Date.now());
	const state = agentState(check);
	const meta = agentMeta(state);
	const runs = runsNewestFirst(check);
	const current = latestRun(check);
	const orphans = unknownRunEvidence(check);
	// Nothing from a machine at all - no result, no run, no capture - renders
	// nothing. An empty machine lane on a case no machine has touched reads as
	// "qa hasn't got to it yet" on cases where nothing is coming.
	if (state === "none" && runs.length === 0 && (check.agentEvidence ?? []).length === 0) return null;

	const Icon = meta ? QA_ICON[state as Exclude<AgentState, "none">] : Eye;
	const stale = isAgentStale(check, heads);
	const head = headShaFor(check, heads);
	// The headline shows what THIS run captured. An earlier round's screenshots
	// under this verdict is exactly the mix-up the run history removes.
	const shots = current ? evidenceForRun(check, current.id) : [];
	const ran = relativeTime(check.agentRanAt, now);
	const earlier = runs.filter((r) => r.id !== current?.id);

	return (
		<div
			data-testid={`qa-block-${check.id}`}
			style={{
				marginTop: 12,
				border: `1px solid ${P.qaBorder}`,
				background: P.qaBg,
				borderRadius: 8,
				padding: "9px 11px",
			}}
		>
			<div style={{ display: "flex", alignItems: "baseline", gap: 8, flexWrap: "wrap", rowGap: 4 }}>
				<span style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.qaFg }}>WHAT QA SAW</span>
				<span style={{ fontSize: 10.5, color: P.muted }}>
					{ran ? `ran ${ran}` : "ran"}
					{check.agentSha ? " · " : ""}
					{check.agentSha && <span style={{ fontFamily: MONO }}>{shortSha(check.agentSha)}</span>}
					{runs.length > 1 && current ? ` · run ${current.seq} of ${runs.length}` : ""}
				</span>
			</div>

			{meta ? (
				<>
					<div style={{ marginTop: 7, display: "flex", alignItems: "flex-start", gap: 8 }}>
						<Icon
							size={13}
							strokeWidth={2.2}
							color={stale ? P.muted : meta.color}
							aria-hidden="true"
							style={{ flex: "none", marginTop: 2 }}
						/>
						<div style={{ minWidth: 0 }}>
							<div style={{ fontSize: 12.5, fontWeight: 600, color: stale ? P.muted : meta.color, lineHeight: 1.45 }}>
								{meta.headline}
							</div>
							<div style={{ marginTop: 3, fontSize: 11.5, lineHeight: 1.5, color: P.secondary2 }}>{meta.caption}</div>
						</div>
					</div>

					{check.agentNote && (
						<div style={{ marginTop: 8, fontSize: 12.5, lineHeight: 1.5, color: P.qaFg }}>{check.agentNote}</div>
					)}
				</>
			) : (
				/* Runs exist but none concluded: the machine opened a round, captured
				   into it and stopped. Saying so beats showing the captures under no
				   heading at all, which is how they used to arrive. */
				<div style={{ marginTop: 7, fontSize: 12, lineHeight: 1.5, color: P.muted }}>
					qa captured this and never recorded a result.
				</div>
			)}

			<QaShotStrip
				sessionId={sessionId}
				checkId={check.id}
				label="CAPTURED BY QA"
				caption="not yours, and not deletable"
				shots={shots}
				onReveal={onReveal}
			/>

			{stale && (
				<div
					style={{
						marginTop: 9,
						paddingTop: 8,
						borderTop: `1px solid ${P.qaBorder}`,
						fontSize: 11,
						lineHeight: 1.5,
						color: P.muted,
					}}
				>
					<b style={{ fontWeight: 600 }}>Stale.</b> Ran against{" "}
					<span style={{ fontFamily: MONO }}>{shortSha(check.agentSha)}</span>; head is now{" "}
					<span style={{ fontFamily: MONO }}>{shortSha(head)}</span>. The code moved after this ran.
				</div>
			)}

			{earlier.length > 0 && (
				<div style={{ marginTop: 10 }} data-testid={`qa-earlier-runs-${check.id}`}>
					<div style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.qaFg, marginBottom: 2 }}>
						{/* "EARLIER" is only true relative to a result shown above. With
						    nothing concluded, these rounds are all there is. */}
						{current ? "EARLIER RUNS" : "RUNS"}{" "}
						{current && (
							<span style={{ fontWeight: 500, color: P.muted, letterSpacing: 0 }}>· what it said before</span>
						)}
					</div>
					{earlier.map((run) => (
						<QaRunRow key={run.id} sessionId={sessionId} check={check} run={run} now={now} onReveal={onReveal} />
					))}
				</div>
			)}

			{orphans.length > 0 && (
				<div
					style={{ marginTop: 10, paddingTop: 8, borderTop: `1px solid ${P.qaBorder}` }}
					data-testid={`qa-unknown-run-${check.id}`}
				>
					<div style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.qaFg }}>UNKNOWN RUN</div>
					<div style={{ marginTop: 4, fontSize: 11, lineHeight: 1.5, color: P.muted }}>
						Captured before AO kept a run history, so which result these belong to is not recorded - and it may not be
						the one above. Read them on their own.
					</div>
					<QaShotStrip
						sessionId={sessionId}
						checkId={check.id}
						label="CAPTURED BY QA"
						caption="run unknown"
						shots={orphans}
						onReveal={onReveal}
					/>
				</div>
			)}
		</div>
	);
}

function WhyBox({ check }: { check: SmokeCheck }) {
	if (!check.why && !check.prNum && !check.fileRef) return null;
	return (
		<div
			style={{
				borderLeft: `2px solid ${ACCENT}`,
				background: P.whyBg,
				borderRadius: "0 8px 8px 0",
				padding: "9px 11px",
			}}
		>
			<div style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: ACCENT }}>
				WHY YOU&apos;RE CHECKING
			</div>
			{check.why && <div style={{ marginTop: 5, fontSize: 12.5, lineHeight: 1.5, color: P.body }}>{check.why}</div>}
			{(check.prNum > 0 || check.fileRef) && (
				<div style={{ marginTop: 7, display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
					{check.prNum > 0 && <RefChip text={`PR #${check.prNum}`} />}
					{check.fileRef && <RefChip text={check.fileRef} ellipsize />}
				</div>
			)}
		</div>
	);
}

function RefChip({ text, ellipsize }: { text: string; ellipsize?: boolean }) {
	return (
		<span
			title={text}
			style={{
				fontFamily: MONO,
				fontSize: 10.5,
				color: P.refChip,
				background: P.pillBg,
				border: `1px solid ${P.borderPill}`,
				borderRadius: 5,
				padding: "1px 6px",
				maxWidth: ellipsize ? 200 : undefined,
				overflow: "hidden",
				textOverflow: "ellipsis",
				whiteSpace: "nowrap",
			}}
		>
			{text}
		</span>
	);
}

function Steps({ steps }: { steps: string[] }) {
	return (
		<div style={{ marginTop: 12 }}>
			<div style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.secondary }}>STEPS TO PLAY</div>
			<div style={{ marginTop: 7, display: "flex", flexDirection: "column", gap: 7 }}>
				{steps.map((step, i) => (
					<div key={i} style={{ display: "flex", gap: 9, alignItems: "flex-start" }}>
						<span
							aria-hidden="true"
							style={{
								flex: "none",
								width: 18,
								height: 18,
								borderRadius: 6,
								display: "flex",
								alignItems: "center",
								justifyContent: "center",
								fontFamily: MONO,
								fontSize: 10,
								fontWeight: 700,
								color: P.secondary,
								background: P.pillBg,
								border: `1px solid ${P.borderPill}`,
							}}
						>
							{i + 1}
						</span>
						<span style={{ fontSize: 12.5, lineHeight: 1.5, color: P.body }}>{step}</span>
					</div>
				))}
			</div>
		</div>
	);
}

function Expected({ expected }: { expected: string }) {
	return (
		<div
			style={{
				marginTop: 12,
				border: `1px solid ${P.expectedBorder}`,
				borderRadius: 8,
				padding: "9px 11px",
				background: P.expectedBg,
			}}
		>
			<div style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.segPass }}>EXPECTED RESULT</div>
			<div style={{ marginTop: 5, fontSize: 12.5, lineHeight: 1.5, color: P.expectedBody }}>{expected}</div>
		</div>
	);
}

function EvidenceSection({
	sessionId,
	check,
	onUpload,
	onDelete,
	onReveal,
}: {
	sessionId: string;
	check: SmokeCheck;
	onUpload: (file: File) => void;
	onDelete: (evidenceId: string) => void;
	onReveal: (evidenceId: string, mode: "reveal" | "open") => void;
}) {
	const [dragOver, setDragOver] = useState(false);
	const inputRef = useRef<HTMLInputElement | null>(null);
	// Which evidence item (by index) the lightbox shows, or null when closed. The
	// triggering thumbnail is remembered so focus returns to it on close.
	const [lightboxIndex, setLightboxIndex] = useState<number | null>(null);
	const triggerRef = useRef<HTMLElement | null>(null);

	const acceptFiles = useCallback(
		(files: FileList | null | undefined) => {
			if (!files) return;
			for (const file of Array.from(files)) {
				if (file.type.startsWith("image/") || file.type.startsWith("video/")) onUpload(file);
			}
		},
		[onUpload],
	);

	const hasEvidence = check.evidence.length > 0;

	return (
		<div style={{ marginTop: 12 }}>
			<div style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.secondary }}>
				YOUR EVIDENCE{" "}
				<span style={{ fontWeight: 500, color: P.muted, letterSpacing: 0 }}>· screenshot or recording frame</span>
			</div>

			{hasEvidence && (
				<div style={{ marginTop: 8, display: "flex", gap: 8, flexWrap: "wrap" }}>
					{check.evidence.map((ev, i) => (
						<EvidenceThumb
							key={ev.id}
							sessionId={sessionId}
							checkId={check.id}
							evidence={ev}
							onOpen={(trigger) => {
								triggerRef.current = trigger;
								setLightboxIndex(i);
							}}
							onDelete={() => onDelete(ev.id)}
							onReveal={() => onReveal(ev.id, "reveal")}
							onOpenFile={() => onReveal(ev.id, "open")}
						/>
					))}
				</div>
			)}

			{lightboxIndex !== null && (
				<MediaLightbox
					items={check.evidence.map((e) => ({
						id: e.id,
						filename: e.filename,
						mime: e.mime,
						src: evidenceUrl(sessionId, check.id, e.id),
					}))}
					index={lightboxIndex}
					onIndexChange={setLightboxIndex}
					onClose={() => setLightboxIndex(null)}
					triggerRef={triggerRef}
				/>
			)}

			<div
				role="button"
				tabIndex={0}
				aria-label="Drop or paste evidence"
				onClick={() => inputRef.current?.click()}
				onDragOver={(e) => {
					e.preventDefault();
					setDragOver(true);
				}}
				onDragLeave={() => setDragOver(false)}
				onDrop={(e) => {
					e.preventDefault();
					setDragOver(false);
					acceptFiles(e.dataTransfer?.files);
				}}
				onPaste={(e) => {
					const files = e.clipboardData?.files;
					if (files && files.length > 0) acceptFiles(files);
				}}
				style={{
					marginTop: 8,
					// Once evidence exists the dropzone is a compact "add another" strip;
					// the tall first-run affordance would just waste vertical space.
					height: hasEvidence ? 60 : 172,
					borderRadius: 10,
					border: `1.5px dashed ${dragOver ? ACCENT : P.borderPill}`,
					background: dragOver ? accentMix(7) : P.cardBg,
					display: "flex",
					flexDirection: hasEvidence ? "row" : "column",
					alignItems: "center",
					justifyContent: "center",
					gap: hasEvidence ? 8 : 6,
					cursor: "pointer",
					color: P.muted,
					textAlign: "center",
				}}
			>
				<span aria-hidden="true" style={{ fontSize: hasEvidence ? 15 : 22, opacity: 0.7 }}>
					⬒
				</span>
				{hasEvidence ? (
					<span style={{ fontSize: 12, color: P.secondary2 }}>
						Add another <span style={{ color: P.muted2 }}>· drop, click, or paste</span>
					</span>
				) : (
					<>
						<span style={{ fontSize: 12.5, color: P.secondary2 }}>Drop a screenshot or recording frame</span>
						<span style={{ fontSize: 11, color: P.muted2 }}>or click to choose · paste also works</span>
					</>
				)}
				<input
					ref={inputRef}
					type="file"
					accept={ACCEPT}
					hidden
					onChange={(e) => {
						acceptFiles(e.target.files);
						e.target.value = "";
					}}
				/>
			</div>
		</div>
	);
}

function EvidenceThumb({
	sessionId,
	checkId,
	evidence,
	onOpen,
	onDelete,
	onReveal,
	onOpenFile,
}: {
	sessionId: string;
	checkId: string;
	evidence: SmokeEvidence;
	onOpen?: (trigger: HTMLElement) => void;
	onDelete?: () => void;
	onReveal?: () => void;
	onOpenFile?: () => void;
}) {
	const [hover, setHover] = useState(false);
	// The shared MediaThumb loads bytes; it reports failure so the reveal/open bar
	// stays hidden when the preview couldn't load (mirrors the old !failed gate).
	const [loadFailed, setLoadFailed] = useState(false);

	const style = {
		width: THUMB_W,
		height: THUMB_H,
		borderRadius: 8,
		border: `1px solid ${P.borderPill}`,
		objectFit: "cover" as const,
		background: "#000",
		display: "block",
	};

	const label = evidence.filename || "evidence";
	// The chrome below sits ON the media (letterboxed against #000), not on the
	// card, so it stays dark in both themes — the same reasoning as any OS image
	// viewer. These literals are deliberately not themed tokens.
	const actionBtn: CSSProperties = {
		display: "inline-flex",
		alignItems: "center",
		justifyContent: "center",
		width: 26,
		height: 20,
		borderRadius: 6,
		border: "1px solid rgba(255,255,255,.22)",
		background: "rgba(20,20,24,.86)",
		color: "#fff",
		padding: 0,
		cursor: "pointer",
	};

	return (
		<div
			style={{ position: "relative", width: THUMB_W, height: THUMB_H }}
			onMouseEnter={() => setHover(true)}
			onMouseLeave={() => setHover(false)}
		>
			{/* The media area opens the in-app lightbox; the × (a sibling, not nested)
			    deletes and stops propagation, so removing never opens the viewer. The
			    shared MediaThumb loads bytes via a blob: URL (CSP-safe on app://). */}
			<MediaThumb
				item={{
					id: evidence.id,
					filename: evidence.filename,
					mime: evidence.mime,
					src: evidenceUrl(sessionId, checkId, evidence.id),
				}}
				onOpen={onOpen}
				style={style}
				onLoadState={({ failed }) => setLoadFailed(failed)}
			/>
			{/* Hover action bar: Reveal the real file in Finder / Open it in the OS
			    default app (distinct from the in-app lightbox above). The stored blob is
			    extensionless, so the daemon exports a correctly-named copy first (see
			    revealEvidence). stopPropagation keeps a button click off the lightbox. */}
			{(onReveal || onOpenFile) && !loadFailed && (
				<div
					style={{
						position: "absolute",
						left: 0,
						right: 0,
						bottom: 0,
						display: "flex",
						gap: 5,
						padding: 5,
						justifyContent: "center",
						background: "linear-gradient(to top, rgba(8,8,10,.9), rgba(8,8,10,0))",
						borderBottomLeftRadius: 8,
						borderBottomRightRadius: 8,
						opacity: hover ? 1 : 0,
						transition: "opacity .12s ease",
						pointerEvents: hover ? "auto" : "none",
					}}
				>
					{onOpenFile && (
						<button
							type="button"
							aria-label={`Open ${label}`}
							title="Open"
							onClick={(e) => {
								e.stopPropagation();
								onOpenFile();
							}}
							style={actionBtn}
						>
							<ExternalLink size={12} strokeWidth={2.2} aria-hidden="true" />
						</button>
					)}
					{onReveal && (
						<button
							type="button"
							aria-label={`Reveal ${label} in Finder`}
							title="Reveal in Finder"
							onClick={(e) => {
								e.stopPropagation();
								onReveal();
							}}
							style={actionBtn}
						>
							<FolderOpen size={12} strokeWidth={2.2} aria-hidden="true" />
						</button>
					)}
				</div>
			)}
			{onDelete && (
				<button
					type="button"
					aria-label={`Remove ${evidence.filename || "evidence"}`}
					title="Remove evidence"
					onClick={(e) => {
						e.stopPropagation();
						onDelete();
					}}
					style={{
						position: "absolute",
						top: -7,
						right: -7,
						width: 19,
						height: 19,
						borderRadius: "50%",
						border: "1px solid rgba(255,255,255,.4)",
						background: "rgba(15,15,18,.9)",
						color: "#fff",
						fontSize: 12,
						lineHeight: 1,
						display: "flex",
						alignItems: "center",
						justifyContent: "center",
						padding: 0,
						cursor: "pointer",
						// Subtle when idle, solid on hover — a guard against accidental
						// clicks without a blocking confirm dialog (app dialog policy).
						opacity: hover ? 1 : 0.55,
						transition: "opacity .12s ease",
						boxShadow: "0 1px 3px rgba(0,0,0,.5)",
					}}
				>
					×
				</button>
			)}
		</div>
	);
}

function VerdictControls({
	decided,
	check,
	busy,
	onDecide,
	onChange,
}: {
	decided: boolean;
	check: SmokeCheck;
	busy: boolean;
	onDecide: (verdict: "pass" | "fail" | "skip") => void;
	onChange: () => void;
}) {
	const [now] = useState(() => Date.now());
	if (decided) {
		const meta = verdictMeta(check.verdict);
		const when = relativeTime(check.decidedAt, now);
		return (
			<div style={{ marginTop: 12, display: "flex", alignItems: "center", gap: 10 }}>
				<span
					style={{
						display: "inline-flex",
						alignItems: "center",
						gap: 8,
						fontSize: 12.5,
						fontWeight: 600,
						color: meta.color,
						background: meta.pillBg,
						border: `1px solid ${meta.pillBorder}`,
						borderRadius: 8,
						padding: "7px 11px",
					}}
				>
					<span aria-hidden="true">{meta.icon}</span>
					<span>{DECIDED_CAPTION[check.verdict] ?? meta.label}</span>
					<span style={{ color: P.caption, fontWeight: 500 }}>· by you{when ? ` · ${when}` : ""}</span>
				</span>
				<div style={{ flex: 1 }} />
				<button
					type="button"
					disabled={busy}
					onClick={onChange}
					style={{
						fontSize: 12,
						fontWeight: 600,
						color: P.secondary,
						background: "transparent",
						border: `1px solid ${P.borderPill}`,
						borderRadius: 7,
						padding: "6px 12px",
						cursor: "pointer",
					}}
				>
					Change
				</button>
			</div>
		);
	}

	const pass = verdictMeta("pass");
	const fail = verdictMeta("fail");
	return (
		<div style={{ marginTop: 12 }}>
			<div style={{ display: "flex", gap: 8 }}>
				<button
					type="button"
					disabled={busy}
					onClick={() => onDecide("pass")}
					style={verdictButton(pass.color, pass.pillBorder, P.passBtnBg)}
				>
					✓ Works — Pass
				</button>
				<button
					type="button"
					disabled={busy}
					onClick={() => onDecide("fail")}
					style={verdictButton(fail.color, fail.pillBorder, P.failBtnBg)}
				>
					✗ Broken — Fail
				</button>
			</div>
			<div style={{ marginTop: 9, textAlign: "center" }}>
				<button
					type="button"
					disabled={busy}
					onClick={() => onDecide("skip")}
					style={{
						fontSize: 12,
						color: P.secondary,
						background: "transparent",
						border: "none",
						cursor: "pointer",
						padding: "4px 8px",
					}}
				>
					⊘ Skip — this check doesn&apos;t apply
				</button>
			</div>
		</div>
	);
}

function verdictButton(color: string, border: string, bg: string): React.CSSProperties {
	return {
		flex: 1,
		display: "inline-flex",
		alignItems: "center",
		justifyContent: "center",
		gap: 6,
		fontSize: 13,
		fontWeight: 600,
		color,
		background: bg,
		border: `1px solid ${border}`,
		borderRadius: 8,
		padding: "9px 12px",
		cursor: "pointer",
	};
}

function ReportBar({
	progress,
	busy,
	jiraBusy,
	jiraLinked,
	onReport,
	onPostJira,
}: {
	progress: SmokeProgress;
	busy: boolean;
	jiraBusy: boolean;
	jiraLinked: boolean;
	onReport: () => void;
	onPostJira: () => void;
}) {
	const parts = [`${progress.checked} of ${progress.total} checked`, `${progress.pass} pass, ${progress.fail} fail`];
	if (progress.skip > 0) parts[1] += `, ${progress.skip} skipped`;
	return (
		<div
			style={{
				flex: "none",
				padding: "11px 14px",
				borderTop: `1px solid ${P.borderReport}`,
				background: P.reportBg,
				display: "flex",
				alignItems: "center",
				gap: 8,
				flexWrap: "wrap",
			}}
		>
			<span style={{ fontSize: 12, fontWeight: 600, color: P.body }}>{parts.join(" · ")}</span>
			<div style={{ flex: 1, minWidth: 8 }} />
			<button
				type="button"
				disabled={jiraBusy}
				onClick={onPostJira}
				title={
					jiraLinked
						? "Post these results to the linked Jira issue as a comment, with evidence attached"
						: "Link a Jira issue first, then post results"
				}
				style={{
					display: "inline-flex",
					alignItems: "center",
					gap: 6,
					fontSize: 12.5,
					fontWeight: 600,
					color: P.accentText,
					background: accentMix(10),
					border: `1px solid ${accentMix(38)}`,
					borderRadius: 8,
					padding: "8px 12px",
					cursor: "pointer",
					opacity: jiraBusy ? 0.7 : 1,
				}}
			>
				◈ Post to Jira
			</button>
			<button
				type="button"
				disabled={busy}
				onClick={onReport}
				style={{
					display: "inline-flex",
					alignItems: "center",
					gap: 6,
					fontSize: 12.5,
					fontWeight: 600,
					color: "var(--accent-fg)",
					background: ACCENT,
					border: "none",
					borderRadius: 8,
					padding: "8px 14px",
					cursor: "pointer",
					opacity: busy ? 0.7 : 1,
				}}
			>
				⚡ Report results to worker
			</button>
		</div>
	);
}

/**
 * Retired cases - out of the checklist, still on the record.
 *
 * Retiring is how a checklist shrinks: a case covered by a real test now, or one
 * whose steps no longer describe the product, leaves the play list instead of
 * being deleted. Deleting would take the human's verdict and screenshots with it
 * (the one part of a checklist AO cannot regenerate) and, more quietly, it
 * would make the shrinking invisible: "3 retired, now covered by tests" is
 * auditable, three cases vanishing between two visits is not.
 *
 * So they live here: at the FOOT of the list, collapsed, never in the counts,
 * never playable, each carrying the reason it went and the date it went. Being
 * collapsed and last is what keeps the live checklist's layout from shifting as
 * the retired pile grows.
 */
function RetiredSection({
	sessionId,
	checks,
	onRevealEvidence,
}: {
	sessionId: string;
	checks: SmokeCheck[];
	onRevealEvidence: (checkId: string, evidenceId: string, mode: "reveal" | "open") => void;
}) {
	const [open, setOpen] = useState(false);
	const [now] = useState(() => Date.now());
	return (
		<div style={{ marginTop: 14 }}>
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((o) => !o)}
				style={{
					width: "100%",
					display: "flex",
					alignItems: "center",
					gap: 8,
					// The caption drops to its own line on a narrow rail rather than
					// interleaving with the label mid-phrase.
					flexWrap: "wrap",
					rowGap: 2,
					background: "transparent",
					border: "none",
					padding: "6px 2px",
					cursor: "pointer",
					textAlign: "left",
				}}
			>
				<span aria-hidden="true" style={{ fontSize: 12, color: P.muted, width: 10 }}>
					{open ? "▾" : "▸"}
				</span>
				<span style={{ fontSize: 12, fontWeight: 600, color: P.secondary }}>
					{checks.length} retired from this checklist
				</span>
				<span style={{ fontSize: 11.5, color: P.muted }}>· kept for the record, not yours to play</span>
			</button>

			{open &&
				checks.map((check) => {
					const meta = verdictMeta(check.verdict);
					const when = relativeTime(check.retiredAt, now);
					const shots = [...check.evidence, ...(check.agentEvidence ?? [])];
					return (
						<div
							key={check.id}
							style={{
								marginTop: 8,
								border: `1px solid ${P.borderCard}`,
								borderRadius: 10,
								padding: "10px 12px",
								background: P.cardBg,
								// Retired is frozen; the whole card reads one step back from
								// the live ones so it never competes for attention.
								opacity: 0.78,
							}}
						>
							<div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", rowGap: 5 }}>
								<span
									style={{ fontFamily: MONO, fontSize: 10, fontWeight: 700, letterSpacing: ".05em", color: P.muted }}
								>
									{checkTag(check.seq)}
								</span>
								<span
									style={{
										fontSize: 10.5,
										fontWeight: 600,
										color: P.muted,
										background: P.pillBg,
										border: `1px solid ${P.borderPill}`,
										borderRadius: 999,
										padding: "1px 8px",
									}}
								>
									⊖ Retired{when ? ` · ${when}` : ""}
								</span>
								{/* A verdict a person recorded before it retired is kept and
								    shown: it is history, and history is the point of not
								    deleting. It counts towards nothing. */}
								{check.verdict !== "pending" && (
									<span style={{ fontSize: 10.5, color: P.muted }}>your verdict: {meta.label.toLowerCase()}</span>
								)}
							</div>
							<div style={{ marginTop: 4, fontSize: 12.5, color: P.secondary2, lineHeight: 1.42 }}>{check.name}</div>
							{check.retiredReason && (
								<div style={{ marginTop: 6, fontSize: 11.5, lineHeight: 1.5, color: P.body }}>
									<span style={{ color: P.muted }}>Why it went: </span>
									{check.retiredReason}
								</div>
							)}
							{shots.length > 0 && (
								<div style={{ marginTop: 8, display: "flex", gap: 8, flexWrap: "wrap" }}>
									{shots.map((ev) => (
										<EvidenceThumb
											key={ev.id}
											sessionId={sessionId}
											checkId={check.id}
											evidence={ev}
											onReveal={() => onRevealEvidence(check.id, ev.id, "reveal")}
											onOpenFile={() => onRevealEvidence(check.id, ev.id, "open")}
										/>
									))}
								</div>
							)}
						</div>
					);
				})}
		</div>
	);
}

// The empty tab, which used to be ONE panel saying two opposite things.
//
// "No smoke checks yet" was rendered whether nobody had decided what a person
// should look at, or somebody had looked and concluded there was nothing worth
// their eyes. Those are opposite answers, and a reader could not tell which one
// they were being shown - the whole reason `ao smoke stand-down` exists. So a
// stood-down checklist gets its own panel, in the app's own voice rather than
// the muted absence voice, and it names who decided and why.
function EmptyState({
	state,
	retired,
	standDown,
}: {
	state: ChecklistState;
	retired: number;
	standDown?: SmokeStandDown | null;
}) {
	if (state === "stood-down" && standDown) {
		return <StoodDownPanel standDown={standDown} retired={retired} />;
	}
	const allRetired = state === "all-retired";
	return (
		<div
			style={{
				display: "flex",
				flexDirection: "column",
				alignItems: "center",
				justifyContent: "center",
				padding: "80px 24px",
				textAlign: "center",
				color: P.muted2,
			}}
		>
			<div style={{ fontSize: 32, marginBottom: 14, opacity: 0.6 }}>✓</div>
			<div style={{ fontSize: 14, fontWeight: 600, color: P.secondary, marginBottom: 5 }}>
				{allRetired ? "Nothing left to play" : "No smoke checks yet"}
			</div>
			<div style={{ fontSize: 12.5, lineHeight: 1.5, maxWidth: 300 }}>
				{allRetired ? (
					<>
						All {retired} case{retired === 1 ? "" : "s"} in this checklist have been retired: covered elsewhere, or no
						longer describing the product. They are listed below with the reason each one went.
					</>
				) : (
					<>
						Nobody has decided what a person should look at yet. When the worker finishes a change whose behavior needs
						a live look, cases will appear here to play.
					</>
				)}
			</div>
		</div>
	);
}

// A DECISION, so it is drawn like one: a bordered card carrying the reason and
// the member who reached it, not the grey "nothing here" wash an absence gets.
// The distinction is the entire point of the panel - if it read as an absence it
// would be the ambiguity again with extra words.
function StoodDownPanel({ standDown, retired }: { standDown: SmokeStandDown; retired: number }) {
	const when = relativeTime(standDown.at, Date.now());
	return (
		<div
			style={{
				border: `1px solid ${P.qaBorder}`,
				background: P.qaBg,
				borderRadius: 11,
				padding: "18px 16px",
				margin: "24px 2px 0",
			}}
		>
			<div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 8 }}>
				<CircleSlash size={14} strokeWidth={2} color={P.qaFg} aria-hidden="true" />
				<span style={{ fontSize: 13, fontWeight: 700, color: P.textStrong }}>
					{standDownActor(standDown)} stood down
				</span>
				{when && <span style={{ fontSize: 11, color: P.muted }}>· {when}</span>}
			</div>
			<div style={{ fontSize: 12.5, lineHeight: 1.55, color: P.body }}>{standDown.reason}</div>
			<div style={{ marginTop: 10, fontSize: 11.5, lineHeight: 1.5, color: P.muted2 }}>
				This is an answer, not an empty list.
				{retired > 0 && (
					<>
						{" "}
						{retired} retired case{retired === 1 ? " is" : "s are"} listed below with the reason each one went.
					</>
				)}
			</div>
		</div>
	);
}
