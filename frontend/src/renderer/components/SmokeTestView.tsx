import { type CSSProperties, useCallback, useEffect, useRef, useState } from "react";
import { ExternalLink, FolderOpen } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage, getApiBaseUrl } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { sessionSmokeQueryKey, useSessionSmokeChecks, type SmokeChecksResponse } from "../hooks/useSessionSmokeChecks";
import {
	ACCENT,
	MONO,
	PALETTE as P,
	accentMix,
	checkTag,
	progressFor,
	progressSegments,
	relativeTime,
	verdictMeta,
	type SmokeCheck,
	type SmokeEvidence,
	type SmokeProgress,
} from "../lib/smoke-test";
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

	const data = query.data;
	const checks = data?.checks ?? [];
	const progress = progressFor(checks);
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
			<Header worker={workerLabel} progress={progress} />

			<div style={{ flex: 1, overflowY: "auto", padding: "12px 12px 24px" }}>
				{query.isLoading && <p style={{ padding: 16, fontSize: 12.5, color: P.muted2 }}>Loading smoke checks…</p>}
				{query.error && (
					<p style={{ padding: 16, fontSize: 12.5, color: P.danger }}>
						{apiErrorMessage(query.error, "Unable to load smoke checks")}
					</p>
				)}
				{!query.isLoading && !query.error && checks.length === 0 && <EmptyState />}
				{!query.isLoading &&
					!query.error &&
					checks.map((check) => (
						<CaseCard
							key={check.id}
							sessionId={sessionId}
							check={check}
							busy={setVerdict.isPending || resetCheck.isPending}
							onDecide={(verdict, note) => decide(check, verdict, note)}
							onChange={() => resetCheck.mutate(check.id)}
							onUpload={(file) => uploadEvidence(check.id, file)}
							onDeleteEvidence={(evidenceId) => deleteEvidence.mutate({ checkId: check.id, evidenceId })}
							onRevealEvidence={(evidenceId, mode) => revealEvidence(check.id, evidenceId, mode)}
						/>
					))}
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

function Header({ worker, progress }: { worker: string; progress: SmokeProgress }) {
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
				<span style={{ fontSize: 12, color: P.secondary2, lineHeight: 1.4 }}>
					Checklist from <b style={{ color: P.body, fontWeight: 600 }}>{worker}</b> · run these live &amp; attach
					evidence
				</span>
			</div>

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
				{progress.total > 0 &&
					segments.map((seg, i) =>
						seg.count > 0 ? (
							<div key={i} style={{ width: `${(seg.count / progress.total) * 100}%`, background: seg.color }} />
						) : null,
					)}
			</div>

			<div style={{ marginTop: 9, display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap", fontSize: 11.5 }}>
				<span style={{ color: P.body }}>
					<b style={{ color: P.textStrong, fontWeight: 700 }}>{progress.checked}</b> of {progress.total} verified
				</span>
				{progress.fail > 0 && <CountChip color={P.segFail} text={`${progress.fail} failed`} />}
				{progress.skip > 0 && <CountChip color={P.muted2} text={`${progress.skip} skipped`} />}
				{progress.pending > 0 && <CountChip color={P.segSkip} text={`${progress.pending} to check`} />}
			</div>
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
	busy,
	onDecide,
	onChange,
	onUpload,
	onDeleteEvidence,
	onRevealEvidence,
}: {
	sessionId: string;
	check: SmokeCheck;
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
					<div style={{ display: "flex", alignItems: "center", gap: 8 }}>
						<span style={{ fontFamily: MONO, fontSize: 10, fontWeight: 700, letterSpacing: ".05em", color: P.muted }}>
							{checkTag(check.seq)}
						</span>
						<StatusPill meta={meta} />
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
					<div style={{ marginTop: 6, fontSize: 10.5, color: hasEvidence ? P.evidenceOn : P.muted }}>
						{hasEvidence ? "▣ evidence attached" : "□ no evidence yet"}
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

function EmptyState() {
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
			<div style={{ fontSize: 14, fontWeight: 600, color: P.secondary, marginBottom: 5 }}>No smoke checks yet</div>
			<div style={{ fontSize: 12.5, lineHeight: 1.5, maxWidth: 300 }}>
				The worker hasn&apos;t authored a checklist for this session. When it finishes a change whose behavior needs a
				live look, cases will appear here to play.
			</div>
		</div>
	);
}
