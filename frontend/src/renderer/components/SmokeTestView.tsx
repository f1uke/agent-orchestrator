import { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage, getApiBaseUrl } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
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

type SmokeResponse = components["schemas"]["ListSmokeChecksResponse"];

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

const ACCEPT = "image/png,image/jpeg,image/gif,image/webp,video/mp4,video/webm,video/quicktime";

const DECIDED_CAPTION: Record<string, string> = {
	pass: "Passed — behaves as expected",
	fail: "Failed — needs another look",
	skip: "Skipped — doesn't apply",
};

/**
 * Tests tab — the "Smoke test" checklist: a worker authored 3–6 manual cases the
 * user plays live in the app, attaching evidence (drop/paste an image or short
 * clip), noting what they saw, and marking Pass / Fail / Skip. A report-back bar
 * composes the results and delivers them to the worker. Pixel-matched to the
 * Tests.dc.html design (exact dark palette + #3b82f6 accent), mirroring the
 * sibling Comments tab's inline-style approach. Always visible with an empty
 * state, even when the session has no checklist.
 */
export function SmokeTestView({ sessionId, worker }: { sessionId: string; worker?: string }) {
	const queryClient = useQueryClient();
	const [toast, setToast] = useState<string | null>(null);
	const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const showToast = useCallback((text: string) => {
		setToast(text);
		if (toastTimer.current) clearTimeout(toastTimer.current);
		toastTimer.current = setTimeout(() => setToast(null), 2600);
	}, []);
	useEffect(() => () => void (toastTimer.current && clearTimeout(toastTimer.current)), []);

	const query = useQuery({
		queryKey: ["session-smoke", sessionId],
		refetchInterval: (q) => {
			if (usePreviewData) return false;
			const data = q.state.data as SmokeResponse | undefined;
			return (data?.checks ?? []).some((c) => c.verdict === "pending") ? 6000 : false;
		},
		queryFn: async () => {
			if (usePreviewData) return mockSmokeChecks(sessionId, worker);
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/smoke-checks", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load smoke checks"));
			return data ?? ({ worker: worker ?? "", checks: [] } satisfies SmokeResponse);
		},
	});

	const invalidate = useCallback(() => {
		void queryClient.invalidateQueries({ queryKey: ["session-smoke", sessionId] });
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
						/>
					))}
			</div>

			{progress.checked > 0 && (
				<ReportBar progress={progress} busy={report.isPending} onReport={() => report.mutate()} />
			)}

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
}: {
	sessionId: string;
	check: SmokeCheck;
	busy: boolean;
	onDecide: (verdict: "pass" | "fail" | "skip", note: string) => void;
	onChange: () => void;
	onUpload: (file: File) => void;
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
					<EvidenceSection sessionId={sessionId} check={check} onUpload={onUpload} />

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
						onDecide={(verdict) => onDecide(verdict, note)}
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
}: {
	sessionId: string;
	check: SmokeCheck;
	onUpload: (file: File) => void;
}) {
	const [dragOver, setDragOver] = useState(false);
	const inputRef = useRef<HTMLInputElement | null>(null);

	const acceptFiles = useCallback(
		(files: FileList | null | undefined) => {
			if (!files) return;
			for (const file of Array.from(files)) {
				if (file.type.startsWith("image/") || file.type.startsWith("video/")) onUpload(file);
			}
		},
		[onUpload],
	);

	return (
		<div style={{ marginTop: 12 }}>
			<div style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.secondary }}>
				YOUR EVIDENCE{" "}
				<span style={{ fontWeight: 500, color: P.muted, letterSpacing: 0 }}>· screenshot or recording frame</span>
			</div>

			{check.evidence.length > 0 && (
				<div style={{ marginTop: 8, display: "flex", gap: 8, flexWrap: "wrap" }}>
					{check.evidence.map((ev) => (
						<EvidenceThumb key={ev.id} sessionId={sessionId} checkId={check.id} evidence={ev} />
					))}
				</div>
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
					height: 172,
					borderRadius: 10,
					border: `1.5px dashed ${dragOver ? ACCENT : P.borderPill}`,
					background: dragOver ? accentMix(7) : P.cardBg,
					display: "flex",
					flexDirection: "column",
					alignItems: "center",
					justifyContent: "center",
					gap: 6,
					cursor: "pointer",
					color: P.muted,
					textAlign: "center",
				}}
			>
				<span aria-hidden="true" style={{ fontSize: 22, opacity: 0.7 }}>
					⬒
				</span>
				<span style={{ fontSize: 12.5, color: P.secondary2 }}>Drop a screenshot or recording frame</span>
				<span style={{ fontSize: 11, color: P.muted2 }}>or click to choose · paste also works</span>
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

			<div style={{ marginTop: 8, display: "flex", gap: 8 }}>
				<CaptureButton label="⎙ Record screen" />
				<CaptureButton label="▣ Grab screenshot" />
			</div>
		</div>
	);
}

// Phase 2 — native capture. Present but disabled with a "coming soon" hint.
function CaptureButton({ label }: { label: string }) {
	return (
		<button
			type="button"
			disabled
			title="Coming soon"
			style={{
				flex: 1,
				fontSize: 11.5,
				fontWeight: 600,
				color: P.muted2,
				background: "transparent",
				border: `1px solid ${P.borderPill}`,
				borderRadius: 7,
				padding: "7px 10px",
				cursor: "not-allowed",
				opacity: 0.55,
			}}
		>
			{label}
		</button>
	);
}

function EvidenceThumb({
	sessionId,
	checkId,
	evidence,
}: {
	sessionId: string;
	checkId: string;
	evidence: SmokeEvidence;
}) {
	const src = `${getApiBaseUrl()}/api/v1/sessions/${encodeURIComponent(sessionId)}/smoke-checks/${encodeURIComponent(checkId)}/evidence/${encodeURIComponent(evidence.id)}`;
	const style = {
		width: 96,
		height: 68,
		borderRadius: 8,
		border: `1px solid ${P.borderPill}`,
		objectFit: "cover" as const,
		background: "#000",
	};
	if (isVideoMime(evidence.mime)) {
		return (
			<video
				src={src}
				style={style}
				muted
				controls={false}
				playsInline
				aria-label={evidence.filename || "evidence clip"}
			/>
		);
	}
	return <img src={src} alt={evidence.filename || "evidence"} style={style} />;
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

function ReportBar({ progress, busy, onReport }: { progress: SmokeProgress; busy: boolean; onReport: () => void }) {
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
				gap: 12,
			}}
		>
			<span style={{ fontSize: 12, fontWeight: 600, color: P.body }}>{parts.join(" · ")}</span>
			<div style={{ flex: 1 }} />
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

// Mock checklist for the VITE_NO_ELECTRON renderer harness (no daemon). Only the
// primary demo worker has a checklist; other sessions render the empty state
// (not every worker authors one).
function mockSmokeChecks(sessionId: string, worker?: string): SmokeResponse {
	const now = new Date().toISOString();
	if (sessionId !== "demo-working") {
		return { worker: worker || "worker", checks: [] };
	}
	return {
		worker: worker || "fix gl note render",
		checks: [
			{
				id: "gitlab-mr-appears",
				sessionId,
				projectId: "agent-orchestrator",
				seq: 1,
				name: "A fresh GitLab MR shows up in Reviews on its own",
				why: "The fix broadens re-polling to every open MR; this confirms one appears without a manual refresh.",
				steps: [
					"Open the gitlab-mr-review project and go to the Reviews tab.",
					"On GitLab, open a brand-new MR against the tracked branch.",
					"Wait one review interval (~60s) without touching the app.",
				],
				expected: "The new MR appears in Reviews automatically, with CI + review status filled in.",
				prNum: 36,
				fileRef: "scmobserver.go:936",
				verdict: "pass",
				note: "Appeared after ~55s, statuses correct.",
				evidence: [],
				decidedAt: now,
				createdAt: now,
				updatedAt: now,
			},
			{
				id: "canceling-pipeline",
				sessionId,
				projectId: "agent-orchestrator",
				seq: 2,
				name: 'A canceling pipeline reads as "In progress", never "Unknown"',
				why: "A canceling GitLab pipeline briefly reported Unknown before; this verifies it stays In progress.",
				steps: ["Trigger a pipeline then cancel it.", "Watch the badge during the cancel."],
				expected: 'The badge shows "In progress" then the terminal state — never "Unknown".',
				prNum: 36,
				fileRef: "normalize.go:451",
				verdict: "fail",
				note: "Flashed Unknown for ~1s before In progress.",
				evidence: [
					{
						id: "ev_demo1",
						checkId: "canceling-pipeline",
						sessionId,
						kind: "image",
						filename: "unknown-flash.png",
						mime: "image/png",
						sizeBytes: 84213,
						createdAt: now,
					},
				],
				decidedAt: now,
				createdAt: now,
				updatedAt: now,
			},
			{
				id: "reviewers-unchanged",
				sessionId,
				projectId: "agent-orchestrator",
				seq: 3,
				name: "GitHub PRs still review exactly as before",
				why: "The change only touches the GitLab path; GitHub review flow must be untouched.",
				steps: ["Open a GitHub-backed session with an open PR.", "Trigger a review and watch it complete."],
				expected: "GitHub review behaves identically to before the change.",
				prNum: 34,
				fileRef: "observer.go:201",
				verdict: "skip",
				note: "No GitHub project handy right now.",
				evidence: [],
				decidedAt: now,
				createdAt: now,
				updatedAt: now,
			},
			{
				id: "ios-sim",
				sessionId,
				projectId: "agent-orchestrator",
				seq: 4,
				name: "iOS simulator smoke of the share sheet",
				why: "Native share-sheet timing can't be unit-tested.",
				steps: ["Open the app in the iOS simulator.", "Tap Share."],
				expected: "The share sheet opens without a frame drop.",
				prNum: 31,
				fileRef: "ShareView.swift:88",
				verdict: "pending",
				note: "",
				evidence: [],
				createdAt: now,
				updatedAt: now,
			},
		],
	};
}
