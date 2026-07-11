import { useEffect, useMemo, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { useInboxActions } from "../hooks/useInboxActions";
import {
	ACCENT,
	MONO,
	PALETTE as P,
	accentMix,
	avatarBg,
	initialsFor,
	relativeTime,
	splitBodyRuns,
} from "../lib/comment-inbox";
import { DiffRows } from "./DiffRows";
import { CommentActions, Toast } from "./inbox-ui";
import type { FileDiffTarget } from "./ReviewsView";

type DiffCtx = components["schemas"]["DiffContextResponse"];

/**
 * Full-file diff for a single review comment, shown in the center pane (in place
 * of the terminal). Opened by the Comments inbox's "Expand full file" action:
 * the whole changed file renders syntax-highlighted with the review comment
 * pinned inline at its line, and the same Resolve / Reply / Send-to-worker
 * actions available there as in the rail.
 */
export function FileDiffView({
	sessionId,
	target,
	onClose,
}: {
	sessionId: string;
	target: FileDiffTarget;
	onClose: () => void;
}) {
	const { thread, prUrl, htmlUrl, prNumber, provider } = target;
	const actions = useInboxActions(sessionId);

	const q = useQuery({
		queryKey: ["diff-context", sessionId, prUrl, thread.path, thread.line, "file"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/diff-context", {
				params: { path: { sessionId }, query: { prUrl, path: thread.path, line: thread.line, mode: "file" } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load file"));
			return data as DiffCtx;
		},
	});
	const ctx = q.data;
	const lines = useMemo(() => ctx?.lines ?? [], [ctx]);
	const addCount = lines.filter((l) => l.kind === "add").length;
	const delCount = lines.filter((l) => l.kind === "del").length;
	// Pin the comment to its line (new-side); fall back to the last added line,
	// then the last line, so a comment always lands somewhere sensible.
	const anchorIndex = useMemo(() => {
		if (!lines.length) return -1;
		const byNew = lines.findIndex((l) => l.newLine === thread.line);
		if (byNew >= 0) return byNew;
		let lastAdd = -1;
		lines.forEach((l, i) => {
			if (l.kind === "add") lastAdd = i;
		});
		return lastAdd >= 0 ? lastAdd : lines.length - 1;
	}, [lines, thread.line]);

	const first = thread.comments[0];
	const author = first?.author ?? "unknown";
	const providerHref = htmlUrl || prUrl;

	// Jump straight to the comment once the file's rendered, so opening a
	// full-file diff lands on the reviewed line instead of the top of the file.
	const anchorRef = useRef<HTMLDivElement | null>(null);
	useEffect(() => {
		if (anchorIndex < 0) return;
		const el = anchorRef.current;
		if (!el || typeof el.scrollIntoView !== "function") return;
		el.scrollIntoView({ block: "center" });
	}, [anchorIndex, lines.length]);

	const anchoredComment = (
		<div
			ref={anchorRef}
			style={{
				margin: "8px 14px 10px 20px",
				border: `1px solid #26262c`,
				borderLeft: `2px solid ${ACCENT}`,
				borderRadius: 9,
				background: "#0e0e12",
				padding: 15,
			}}
		>
			<div style={{ display: "flex", gap: 12 }}>
				<div
					style={{
						flex: "none",
						width: 26,
						height: 26,
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
				<div style={{ flex: 1, minWidth: 0 }}>
					{thread.comments.map((c, i) => (
						<div key={c.id} style={{ marginTop: i === 0 ? 0 : 10 }}>
							<div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
								<span style={{ fontSize: 13.5, fontWeight: 600, color: P.text }}>{c.author || "unknown"}</span>
								<span style={{ fontSize: 11.5, color: P.muted2 }}>{relativeTime(c.createdAt, Date.now())}</span>
							</div>
							<div style={{ fontSize: 13.5, lineHeight: 1.6, color: P.body, wordBreak: "break-word" }}>
								{splitBodyRuns(c.body).map((run, j) =>
									run.code ? (
										<code
											key={j}
											style={{
												fontFamily: MONO,
												fontSize: 12,
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
					))}
					<CommentActions
						prUrl={prUrl}
						threadId={thread.threadId}
						path={thread.path}
						line={thread.line}
						author={author}
						seedBody={first?.body ?? ""}
						busy={actions.busy}
						onResolve={actions.doResolve}
						onReply={actions.doReply}
						onSendQuick={actions.doSendQuick}
						onSendPrompt={actions.doSendPrompt}
					/>
				</div>
			</div>
		</div>
	);

	return (
		<div
			style={{
				position: "relative",
				display: "flex",
				flexDirection: "column",
				height: "100%",
				minHeight: 0,
				background: "#060607",
				color: P.text,
			}}
		>
			{/* header */}
			<div
				style={{
					height: 52,
					flex: "none",
					display: "flex",
					alignItems: "center",
					gap: 12,
					padding: "0 20px",
					borderBottom: `1px solid ${P.borderRail}`,
				}}
			>
				<button
					type="button"
					onClick={onClose}
					style={{
						display: "inline-flex",
						alignItems: "center",
						gap: 6,
						fontSize: 12.5,
						fontWeight: 500,
						color: "#b7b7bc",
						background: "transparent",
						border: `1px solid #26262c`,
						borderRadius: 7,
						padding: "5px 11px",
						cursor: "pointer",
					}}
				>
					<ChevronLeft aria-hidden="true" style={{ width: 14, height: 14 }} />
					agent
				</button>
				<span
					style={{
						fontSize: 10,
						fontWeight: 700,
						letterSpacing: ".05em",
						color: ACCENT,
						background: accentMix(14),
						border: `1px solid ${accentMix(35)}`,
						borderRadius: 5,
						padding: "3px 7px",
					}}
				>
					DIFF
				</span>
				<span
					title={thread.path}
					style={{
						fontFamily: MONO,
						fontSize: 12.5,
						color: "#c7c7cc",
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
						minWidth: 0,
					}}
				>
					{thread.path}
				</span>
				<span style={{ fontFamily: MONO, fontSize: 12, color: ACCENT, flex: "none" }}>:{thread.line}</span>
				<div style={{ flex: 1 }} />
				<span style={{ fontFamily: MONO, fontSize: 11.5, color: "#84cfa0" }}>+{addCount}</span>
				<span style={{ fontFamily: MONO, fontSize: 11.5, color: "#e69696" }}>−{delCount}</span>
				<span style={{ width: 1, height: 16, background: "#26262c" }} />
				<a
					href={providerHref}
					target="_blank"
					rel="noopener noreferrer"
					style={{ fontSize: 11.5, color: P.secondary2, whiteSpace: "nowrap", textDecoration: "none" }}
				>
					{provider?.toLowerCase() === "gitlab" ? "MR" : "PR"} #{prNumber} ↗
				</a>
			</div>

			{/* body */}
			<div style={{ flex: 1, overflow: "auto", padding: "20px 24px", minHeight: 0 }}>
				{q.isLoading && <p style={{ fontSize: 12.5, color: P.muted2 }}>Loading file…</p>}
				{q.error && <p style={{ fontSize: 12.5, color: P.red }}>{apiErrorMessage(q.error, "Unable to load file")}</p>}
				{ctx && (!ctx.available || lines.length === 0) && !q.isLoading && (
					<p style={{ fontSize: 12.5, color: P.muted2 }}>No file diff available for this comment.</p>
				)}
				{ctx && ctx.available && lines.length > 0 && (
					<div
						style={{
							maxWidth: 1040,
							border: `1px solid ${P.borderCard}`,
							borderRadius: 10,
							overflow: "hidden",
							background: "#0b0b0e",
						}}
					>
						<div
							className="mono"
							style={{
								display: "flex",
								alignItems: "center",
								gap: 8,
								padding: "9px 14px",
								background: P.fileHeader,
								borderBottom: `1px solid ${P.borderCard}`,
								fontFamily: MONO,
								fontSize: 11.5,
								color: "#b7b7bc",
							}}
						>
							{thread.path}
						</div>
						<DiffRows lines={lines} size="wide" anchorIndex={anchorIndex} anchorNode={anchoredComment} />
						{ctx.truncated && (
							<div
								style={{ padding: "8px 14px", borderTop: `1px solid ${P.borderCard}`, fontSize: 11, color: P.muted2 }}
							>
								Diff truncated — open the {provider?.toLowerCase() === "gitlab" ? "MR" : "PR"} to see the full file.
							</div>
						)}
					</div>
				)}
			</div>

			{actions.toast && <Toast text={actions.toast} />}
		</div>
	);
}
