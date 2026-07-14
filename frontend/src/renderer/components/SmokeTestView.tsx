import { useCallback, useEffect, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { ChevronLeft, ChevronRight, RotateCcw, X, ZoomIn, ZoomOut } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage, getApiBaseUrl } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { sessionSmokeQueryKey, useSessionSmokeChecks, type SmokeChecksResponse } from "../hooks/useSessionSmokeChecks";
import {
	ACCENT,
	MONO,
	PALETTE as P,
	accentMix,
	checkTag,
	isVideoMime,
	progressFor,
	progressSegments,
	relativeTime,
	verdictMeta,
	type SmokeCheck,
	type SmokeEvidence,
	type SmokeProgress,
} from "../lib/smoke-test";
import { Toast } from "./inbox-ui";
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
 * Fetch evidence bytes (connect-src) and expose them as a `blob:` object URL. The
 * renderer runs on the secure `app://` scheme, where a direct
 * <img>/<video src=http://127.0.0.1…> subresource is CSP-blocked (loopback http
 * lives only in connect-src, not img-src/media-src). So both the thumbnail and the
 * lightbox load bytes this way — the blob URL is allowed by img-src/media-src (see
 * src/shared/renderer-csp.ts). Revokes on unmount / src change; reports fetch
 * failure so callers can show a framed placeholder rather than a broken element.
 */
function useEvidenceObjectUrl(src: string): { url: string | null; failed: boolean } {
	const [url, setUrl] = useState<string | null>(null);
	const [failed, setFailed] = useState(false);
	useEffect(() => {
		if (!src) return;
		let alive = true;
		let objectUrl: string | null = null;
		setUrl(null);
		setFailed(false);
		fetch(src)
			.then((r) => (r.ok ? r.blob() : Promise.reject(new Error(`evidence ${r.status}`))))
			.then((blob) => {
				if (!alive) return;
				objectUrl = URL.createObjectURL(blob);
				setUrl(objectUrl);
			})
			.catch(() => {
				if (alive) setFailed(true);
			});
		return () => {
			alive = false;
			if (objectUrl) URL.revokeObjectURL(objectUrl);
		};
	}, [src]);
	return { url, failed };
}

/**
 * Tests tab — the "Smoke test" checklist: a worker authored 3–6 manual cases the
 * user plays live in the app, attaching evidence (drop/paste an image or short
 * clip), noting what they saw, and marking Pass / Fail / Skip. A report-back bar
 * composes the results and delivers them to the worker. Pixel-matched to the
 * Tests.dc.html design (exact dark palette + #3b82f6 accent), mirroring the
 * sibling Comments tab's inline-style approach. Always visible with an empty
 * state, even when the session has no checklist.
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
			showToast(`Posted ${rows} result${rows === 1 ? "" : "s"} to ${data.key}`);
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
					<p style={{ padding: 16, fontSize: 12.5, color: "#e88f8f" }}>
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
						color: "#fff",
						background: "linear-gradient(135deg, #3b82f6, #5b8def)",
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
}: {
	sessionId: string;
	check: SmokeCheck;
	busy: boolean;
	onDecide: (verdict: "pass" | "fail" | "skip", note: string) => void;
	onChange: () => void;
	onUpload: (file: File) => void;
	onDeleteEvidence: (evidenceId: string) => void;
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
					<EvidenceSection sessionId={sessionId} check={check} onUpload={onUpload} onDelete={onDeleteEvidence} />

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
				color: "#b7b7bc",
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
				background: "rgba(79,174,116,.06)",
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
}: {
	sessionId: string;
	check: SmokeCheck;
	onUpload: (file: File) => void;
	onDelete: (evidenceId: string) => void;
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
						/>
					))}
				</div>
			)}

			{lightboxIndex !== null && (
				<EvidenceLightbox
					sessionId={sessionId}
					checkId={check.id}
					items={check.evidence}
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
}: {
	sessionId: string;
	checkId: string;
	evidence: SmokeEvidence;
	onOpen?: (trigger: HTMLElement) => void;
	onDelete?: () => void;
}) {
	// Load the bytes through the CORS-gated fetch and render from a blob: URL — a
	// direct http://127.0.0.1 <img>/<video> is CSP-blocked on the app:// scheme
	// (see useEvidenceObjectUrl). A fetch failure shows a framed placeholder, never
	// the CSP-blocked direct URL (which would just render broken too).
	const { url: resolved, failed } = useEvidenceObjectUrl(evidenceUrl(sessionId, checkId, evidence.id));
	const [hover, setHover] = useState(false);
	// Only image/clip thumbnails that actually loaded are openable in the lightbox.
	const openable = Boolean(onOpen) && !failed;

	const style = {
		width: THUMB_W,
		height: THUMB_H,
		borderRadius: 8,
		border: `1px solid ${P.borderPill}`,
		objectFit: "cover" as const,
		background: "#000",
		display: "block",
	};

	const media = failed ? (
		<div
			style={{
				...style,
				display: "flex",
				alignItems: "center",
				justifyContent: "center",
				padding: 4,
				fontSize: 10,
				lineHeight: 1.3,
				textAlign: "center",
				color: P.muted2,
				overflow: "hidden",
			}}
			aria-label={evidence.filename || "evidence"}
		>
			{evidence.filename || "couldn't load"}
		</div>
	) : !resolved ? (
		// Loading the bytes — a framed placeholder avoids a broken-image flash.
		<div style={style} aria-label={evidence.filename || "evidence"} />
	) : isVideoMime(evidence.mime) ? (
		<video
			src={resolved}
			style={style}
			muted
			controls={false}
			playsInline
			aria-label={evidence.filename || "evidence clip"}
		/>
	) : (
		<img src={resolved} alt={evidence.filename || "evidence"} style={style} />
	);

	return (
		<div
			style={{ position: "relative", width: THUMB_W, height: THUMB_H }}
			onMouseEnter={() => setHover(true)}
			onMouseLeave={() => setHover(false)}
		>
			{/* The media area opens the lightbox; the × (a sibling, not nested) deletes
			    and stops propagation, so removing never opens the viewer. */}
			<div
				role={openable ? "button" : undefined}
				tabIndex={openable ? 0 : undefined}
				aria-label={openable ? `View ${evidence.filename || "evidence"}` : undefined}
				onClick={openable ? (e) => onOpen!(e.currentTarget) : undefined}
				onKeyDown={
					openable
						? (e) => {
								if (e.key === "Enter" || e.key === " ") {
									e.preventDefault();
									onOpen!(e.currentTarget);
								}
							}
						: undefined
				}
				style={{ display: "block", borderRadius: 8, outline: "none", cursor: openable ? "zoom-in" : "default" }}
			>
				{media}
			</div>
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

const ZOOM_MIN = 1;
const ZOOM_MAX = 4;
const ZOOM_STEP = 1.5;

function clamp(v: number, lo: number, hi: number): number {
	return Math.min(hi, Math.max(lo, v));
}

/**
 * Centered, full-screen modal viewer for one check's evidence. Built on the app's
 * Radix Dialog primitive, so focus trap, Esc-to-close, body-scroll lock, focus
 * restore, and role=dialog/aria-modal all come for free and match the app's other
 * modals. The Content fills the viewport (transparent) over a dimmed Overlay;
 * clicking the padding around the media (target === currentTarget) closes, while
 * clicking the media itself does not. Images support wheel/pinch + button +
 * double-click zoom with drag-to-pan; videos play inline with standard controls.
 * Left/Right (keys and arrows) page between items of the SAME check, wrapping at
 * the ends. Zoom/pan reset whenever the shown item changes.
 */
function EvidenceLightbox({
	sessionId,
	checkId,
	items,
	index,
	onIndexChange,
	onClose,
	triggerRef,
}: {
	sessionId: string;
	checkId: string;
	items: SmokeEvidence[];
	index: number;
	onIndexChange: (next: number) => void;
	onClose: () => void;
	triggerRef: React.MutableRefObject<HTMLElement | null>;
}) {
	const count = items.length;
	const safeIndex = count > 0 ? clamp(index, 0, count - 1) : 0;
	const current = items[safeIndex];

	const [scale, setScale] = useState(1);
	const [pan, setPan] = useState({ x: 0, y: 0 });
	const [dragging, setDragging] = useState(false);
	const viewportRef = useRef<HTMLDivElement | null>(null);
	// Latest scale/pan for the wheel + pan math (event closures would go stale).
	const scaleRef = useRef(1);
	const panRef = useRef({ x: 0, y: 0 });
	const dragRef = useRef<{ x: number; y: number; px: number; py: number } | null>(null);
	scaleRef.current = scale;
	panRef.current = pan;

	const { url, failed } = useEvidenceObjectUrl(current ? evidenceUrl(sessionId, checkId, current.id) : "");
	const isVideo = current ? isVideoMime(current.mime) : false;
	const zoomable = Boolean(url) && !failed && !isVideo;

	// Reset zoom/pan whenever the shown item changes (and on first open).
	useEffect(() => {
		setScale(1);
		setPan({ x: 0, y: 0 });
	}, [safeIndex]);

	// If the case's evidence empties out from under us, there is nothing to show.
	useEffect(() => {
		if (count === 0) onClose();
	}, [count, onClose]);

	const goto = useCallback(
		(next: number) => {
			if (count <= 1) return;
			onIndexChange(((next % count) + count) % count); // wrap at both ends
		},
		[count, onIndexChange],
	);

	const clampPan = useCallback((p: { x: number; y: number }, s: number) => {
		const el = viewportRef.current;
		const maxX = ((s - 1) * (el?.clientWidth ?? 0)) / 2;
		const maxY = ((s - 1) * (el?.clientHeight ?? 0)) / 2;
		return { x: clamp(p.x, -maxX, maxX), y: clamp(p.y, -maxY, maxY) };
	}, []);

	// Zoom to `nextScale`, keeping the content point under (dx,dy) — an offset from
	// the viewport center — fixed. Buttons pass no anchor (zoom about center).
	const zoomTo = useCallback(
		(nextScale: number, anchor?: { dx: number; dy: number }) => {
			const s = scaleRef.current;
			const s2 = clamp(nextScale, ZOOM_MIN, ZOOM_MAX);
			if (s2 === 1) {
				setScale(1);
				setPan({ x: 0, y: 0 });
				return;
			}
			const a = anchor ?? { dx: 0, dy: 0 };
			const p = panRef.current;
			const np = clampPan({ x: a.dx - ((a.dx - p.x) / s) * s2, y: a.dy - ((a.dy - p.y) / s) * s2 }, s2);
			setScale(s2);
			setPan(np);
		},
		[clampPan],
	);

	// Native wheel listener so it can preventDefault (React onWheel is passive).
	useEffect(() => {
		const el = viewportRef.current;
		if (!el || !zoomable) return;
		const onWheel = (e: WheelEvent) => {
			e.preventDefault();
			const rect = el.getBoundingClientRect();
			const dx = e.clientX - (rect.left + rect.width / 2);
			const dy = e.clientY - (rect.top + rect.height / 2);
			zoomTo(scaleRef.current * Math.exp(-e.deltaY * 0.0015), { dx, dy });
		};
		el.addEventListener("wheel", onWheel, { passive: false });
		return () => el.removeEventListener("wheel", onWheel);
	}, [zoomable, zoomTo, safeIndex]);

	const onDoubleClick = (e: React.MouseEvent) => {
		if (!zoomable) return;
		const el = viewportRef.current;
		if (!el) return;
		if (scaleRef.current > 1) {
			zoomTo(1);
			return;
		}
		const rect = el.getBoundingClientRect();
		zoomTo(2.5, { dx: e.clientX - (rect.left + rect.width / 2), dy: e.clientY - (rect.top + rect.height / 2) });
	};

	const onPointerDown = (e: React.PointerEvent) => {
		if (scaleRef.current <= 1 || !zoomable) return;
		e.currentTarget.setPointerCapture?.(e.pointerId);
		dragRef.current = { x: panRef.current.x, y: panRef.current.y, px: e.clientX, py: e.clientY };
		setDragging(true);
	};
	const onPointerMove = (e: React.PointerEvent) => {
		const d = dragRef.current;
		if (!d) return;
		setPan(clampPan({ x: d.x + (e.clientX - d.px), y: d.y + (e.clientY - d.py) }, scaleRef.current));
	};
	const endDrag = (e: React.PointerEvent) => {
		if (!dragRef.current) return;
		e.currentTarget.releasePointerCapture?.(e.pointerId);
		dragRef.current = null;
		setDragging(false);
	};

	const onKeyDown = (e: React.KeyboardEvent) => {
		if (e.key === "ArrowLeft") {
			e.preventDefault();
			goto(safeIndex - 1);
		} else if (e.key === "ArrowRight") {
			e.preventDefault();
			goto(safeIndex + 1);
		}
	};

	return (
		<Dialog.Root open onOpenChange={(o) => !o && onClose()}>
			<Dialog.Portal>
				<Dialog.Overlay
					style={{
						position: "fixed",
						inset: 0,
						zIndex: 1000,
						background: "rgba(6,6,8,.82)",
						backdropFilter: "blur(2px)",
					}}
				/>
				<Dialog.Content
					aria-label={`Evidence viewer${current?.filename ? `: ${current.filename}` : ""}`}
					aria-describedby={undefined}
					onKeyDown={onKeyDown}
					onCloseAutoFocus={(e) => {
						// Return focus to the thumbnail that opened the viewer.
						e.preventDefault();
						triggerRef.current?.focus?.();
					}}
					onClick={(e) => {
						if (e.target === e.currentTarget) onClose();
					}}
					style={{
						position: "fixed",
						inset: 0,
						zIndex: 1001,
						display: "flex",
						alignItems: "center",
						justifyContent: "center",
						padding: "56px 72px",
						outline: "none",
						border: "none",
						background: "transparent",
					}}
				>
					<Dialog.Title style={SR_ONLY}>Evidence viewer</Dialog.Title>

					<button
						type="button"
						aria-label="Close viewer"
						title="Close (Esc)"
						onClick={onClose}
						style={cornerBtn("top")}
					>
						<X size={18} aria-hidden="true" />
					</button>

					{count > 1 && (
						<>
							<span aria-hidden="true" style={counterStyle}>
								{safeIndex + 1} / {count}
							</span>
							<button
								type="button"
								aria-label="Previous evidence"
								title="Previous (←)"
								onClick={() => goto(safeIndex - 1)}
								style={edgeBtn("left")}
							>
								<ChevronLeft size={26} aria-hidden="true" />
							</button>
							<button
								type="button"
								aria-label="Next evidence"
								title="Next (→)"
								onClick={() => goto(safeIndex + 1)}
								style={edgeBtn("right")}
							>
								<ChevronRight size={26} aria-hidden="true" />
							</button>
						</>
					)}

					<div
						ref={viewportRef}
						onDoubleClick={onDoubleClick}
						onPointerDown={onPointerDown}
						onPointerMove={onPointerMove}
						onPointerUp={endDrag}
						onPointerCancel={endDrag}
						style={{
							position: "relative",
							display: "flex",
							alignItems: "center",
							justifyContent: "center",
							maxWidth: "min(90vw, 1400px)",
							maxHeight: "82vh",
							overflow: "hidden",
							borderRadius: 10,
							touchAction: "none",
						}}
					>
						{failed ? (
							<div style={failedBox} aria-label={current?.filename || "evidence"}>
								Couldn&apos;t load {current?.filename || "this evidence"}
							</div>
						) : !url ? (
							<div style={{ ...failedBox, color: P.muted }} aria-live="polite">
								Loading…
							</div>
						) : isVideo ? (
							<video
								src={url}
								controls
								autoPlay
								muted
								playsInline
								aria-label={current?.filename || "evidence clip"}
								style={{
									maxWidth: "min(90vw, 1400px)",
									maxHeight: "82vh",
									borderRadius: 10,
									background: "#000",
									display: "block",
								}}
							/>
						) : (
							<img
								src={url}
								alt={current?.filename || "evidence"}
								draggable={false}
								style={{
									maxWidth: "min(90vw, 1400px)",
									maxHeight: "82vh",
									objectFit: "contain",
									display: "block",
									userSelect: "none",
									transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`,
									transformOrigin: "center center",
									transition: dragging ? "none" : "transform .1s ease-out",
									cursor: scale > 1 ? (dragging ? "grabbing" : "grab") : "zoom-in",
									willChange: "transform",
								}}
							/>
						)}
					</div>

					{zoomable && (
						<div style={zoomBarStyle}>
							<button
								type="button"
								aria-label="Zoom out"
								title="Zoom out"
								onClick={() => zoomTo(scale / ZOOM_STEP)}
								disabled={scale <= ZOOM_MIN}
								style={zoomBtn(scale <= ZOOM_MIN)}
							>
								<ZoomOut size={16} aria-hidden="true" />
							</button>
							<span aria-hidden="true" style={{ minWidth: 42, textAlign: "center", fontSize: 12, color: P.body }}>
								{Math.round(scale * 100)}%
							</span>
							<button
								type="button"
								aria-label="Zoom in"
								title="Zoom in"
								onClick={() => zoomTo(scale * ZOOM_STEP)}
								disabled={scale >= ZOOM_MAX}
								style={zoomBtn(scale >= ZOOM_MAX)}
							>
								<ZoomIn size={16} aria-hidden="true" />
							</button>
							<button
								type="button"
								aria-label="Reset zoom"
								title="Reset zoom"
								onClick={() => zoomTo(1)}
								disabled={scale === ZOOM_MIN}
								style={zoomBtn(scale === ZOOM_MIN)}
							>
								<RotateCcw size={15} aria-hidden="true" />
							</button>
						</div>
					)}
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}

const SR_ONLY: React.CSSProperties = {
	position: "absolute",
	width: 1,
	height: 1,
	padding: 0,
	margin: -1,
	overflow: "hidden",
	clip: "rect(0 0 0 0)",
	whiteSpace: "nowrap",
	border: 0,
};

const counterStyle: React.CSSProperties = {
	position: "absolute",
	top: 20,
	left: "50%",
	transform: "translateX(-50%)",
	fontSize: 12.5,
	fontWeight: 600,
	color: P.body,
	background: "rgba(15,15,18,.8)",
	border: `1px solid ${P.borderPill}`,
	borderRadius: 999,
	padding: "3px 12px",
};

const failedBox: React.CSSProperties = {
	display: "flex",
	alignItems: "center",
	justifyContent: "center",
	minWidth: 240,
	minHeight: 140,
	padding: 24,
	fontSize: 13,
	textAlign: "center",
	color: P.muted2,
	background: P.cardBg,
	border: `1px solid ${P.borderPill}`,
	borderRadius: 10,
};

const zoomBarStyle: React.CSSProperties = {
	position: "absolute",
	bottom: 22,
	left: "50%",
	transform: "translateX(-50%)",
	display: "flex",
	alignItems: "center",
	gap: 6,
	background: "rgba(15,15,18,.85)",
	border: `1px solid ${P.borderPill}`,
	borderRadius: 999,
	padding: "5px 8px",
	backdropFilter: "blur(4px)",
};

function cornerBtn(_pos: "top"): React.CSSProperties {
	return {
		position: "absolute",
		top: 16,
		right: 16,
		width: 36,
		height: 36,
		borderRadius: "50%",
		display: "flex",
		alignItems: "center",
		justifyContent: "center",
		color: "#fff",
		background: "rgba(15,15,18,.8)",
		border: `1px solid ${P.borderPill}`,
		cursor: "pointer",
		padding: 0,
	};
}

function edgeBtn(side: "left" | "right"): React.CSSProperties {
	return {
		position: "absolute",
		top: "50%",
		[side]: 16,
		transform: "translateY(-50%)",
		width: 44,
		height: 44,
		borderRadius: "50%",
		display: "flex",
		alignItems: "center",
		justifyContent: "center",
		color: "#fff",
		background: "rgba(15,15,18,.8)",
		border: `1px solid ${P.borderPill}`,
		cursor: "pointer",
		padding: 0,
	};
}

function zoomBtn(disabled: boolean): React.CSSProperties {
	return {
		width: 30,
		height: 30,
		borderRadius: 8,
		display: "flex",
		alignItems: "center",
		justifyContent: "center",
		color: disabled ? P.muted2 : "#fff",
		background: "transparent",
		border: "none",
		cursor: disabled ? "default" : "pointer",
		padding: 0,
		opacity: disabled ? 0.5 : 1,
	};
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
					<span style={{ color: P.muted, fontWeight: 500 }}>· by you{when ? ` · ${when}` : ""}</span>
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

	return (
		<div style={{ marginTop: 12 }}>
			<div style={{ display: "flex", gap: 8 }}>
				<button
					type="button"
					disabled={busy}
					onClick={() => onDecide("pass")}
					style={verdictButton("#68c48c", "rgba(79,174,116,.4)", "rgba(79,174,116,.12)")}
				>
					✓ Works — Pass
				</button>
				<button
					type="button"
					disabled={busy}
					onClick={() => onDecide("fail")}
					style={verdictButton("#e88f8f", "rgba(224,101,94,.45)", "rgba(224,101,94,.12)")}
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
					color: ACCENT,
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
					color: "#fff",
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
