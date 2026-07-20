import { useQuery } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { ACCENT, MONO, PALETTE as P, accentMix } from "../lib/comment-inbox";
import { DiffRows } from "./DiffRows";

type DiffContext = components["schemas"]["DiffContextResponse"];

/**
 * One changed file's diff against the session's target branch, shown in the
 * center pane (in place of the terminal) — the same placement the Reviews
 * "Expand full file" view and the terminal's clickable file references use.
 *
 * It reads workspace/file-diff rather than diff-context because that route
 * requires a PR, and Changes mode has to work for a worker mid-task that has
 * not opened one. Rendering reuses `DiffRows` at its wide density, so a diff
 * here looks identical to a diff in the Reviews tab.
 */
export function WorkspaceFileDiffView({
	sessionId,
	path,
	onClose,
}: {
	sessionId: string;
	path: string;
	onClose: () => void;
}) {
	const q = useQuery({
		queryKey: ["workspace-file-diff", sessionId, path],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/file-diff", {
				params: { path: { sessionId }, query: { path } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load diff"));
			return data as DiffContext;
		},
	});
	const diff = q.data;
	const lines = diff?.lines ?? [];
	const additions = lines.filter((l) => l.kind === "add").length;
	const deletions = lines.filter((l) => l.kind === "del").length;
	// An all-deletions patch means the file is gone on this branch.
	const deleted = lines.length > 0 && additions === 0 && deletions > 0;

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
						border: "1px solid #26262c",
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
						flex: "none",
						color: deleted ? P.red : ACCENT,
						background: deleted ? "rgba(239,106,99,.13)" : accentMix(14),
						border: `1px solid ${deleted ? "rgba(239,106,99,.34)" : accentMix(35)}`,
						borderRadius: 5,
						padding: "3px 7px",
					}}
				>
					{deleted ? "DELETED" : "DIFF"}
				</span>
				<span
					title={path}
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
					{path}
				</span>
				<div style={{ flex: 1 }} />
				{lines.length > 0 && (
					<span style={{ fontFamily: MONO, fontSize: 11.5, flex: "none" }}>
						<span style={{ color: "#7fd8a0" }}>+{additions}</span>{" "}
						<span style={{ color: "#e88f8f" }}>−{deletions}</span>
					</span>
				)}
			</div>

			<div style={{ flex: 1, overflow: "auto", padding: "20px 24px", minHeight: 0 }}>
				{q.isLoading && <p style={{ fontSize: 12.5, color: P.muted2 }}>Loading diff…</p>}
				{q.error && <p style={{ fontSize: 12.5, color: P.red }}>{apiErrorMessage(q.error, "Unable to load diff")}</p>}
				{diff && !diff.available && !q.isLoading && (
					<p style={{ fontSize: 12.5, color: P.muted2 }}>
						No diff to show for this file — it may be binary, or unchanged against the target branch.
					</p>
				)}
				{diff?.available && lines.length > 0 && (
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
							{path}
						</div>
						<DiffRows lines={lines} size="wide" />
						{diff.truncated && (
							<div
								style={{ padding: "8px 14px", borderTop: `1px solid ${P.borderCard}`, fontSize: 11, color: P.muted2 }}
							>
								Diff truncated — showing the first lines only.
							</div>
						)}
						{deleted && (
							<div
								style={{ padding: "8px 14px", borderTop: `1px solid ${P.borderCard}`, fontSize: 11, color: P.muted2 }}
							>
								File deleted in this branch.
							</div>
						)}
					</div>
				)}
			</div>
		</div>
	);
}
