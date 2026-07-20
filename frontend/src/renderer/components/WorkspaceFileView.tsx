import { type CSSProperties, useEffect, useMemo, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { ACCENT, MONO, PALETTE as P, accentMix } from "../lib/comment-inbox";
import { type ChangeMark, DiffRows } from "./DiffRows";

type WorkspaceFile = components["schemas"]["WorkspaceFileResponse"];

// Why a file that resolved still cannot be rendered. Reported inline rather
// than as a toast: navigation has already happened, so the viewer itself is
// where the explanation belongs. Both states are non-blocking — the back
// button returns to an untouched terminal.
const UNAVAILABLE_MESSAGE: Record<string, string> = {
	too_large: "This file is too large to display.",
	binary: "This looks like a binary file, so it can’t be displayed.",
};

/**
 * A path that truncates its DIRECTORY, never its filename. Paths here can be
 * long absolute ones (a file outside the worktree), and a plain tail ellipsis
 * would eat the one part that identifies the file.
 */
function PathLabel({ path, style }: { path: string; style?: CSSProperties }) {
	const slash = path.lastIndexOf("/");
	const dir = slash >= 0 ? path.slice(0, slash + 1) : "";
	const base = slash >= 0 ? path.slice(slash + 1) : path;
	return (
		<span title={path} style={{ display: "flex", minWidth: 0, ...style }}>
			{dir !== "" && <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{dir}</span>}
			<span style={{ flex: "none", whiteSpace: "nowrap" }}>{base}</span>
		</span>
	);
}

/**
 * A file opened from a clickable terminal file reference, shown in the center
 * pane (in place of the terminal) until dismissed — the same placement the
 * Reviews "Expand full file" view uses. Reuses the Reviews code viewer
 * (`DiffRows`) read-only, and overlays an Xcode-style gutter bar on lines that
 * are modified-but-not-committed (working tree vs HEAD), fetched with the file.
 *
 * `path` is workspace-relative for a file inside the session's workspace and
 * absolute for one outside it (a knowledge-store note, another session's
 * worktree). A file that is not inside any git repository simply has no change
 * markers.
 */
export function WorkspaceFileView({
	sessionId,
	path,
	line,
	onClose,
}: {
	sessionId: string;
	path: string;
	line?: number;
	onClose: () => void;
}) {
	const q = useQuery({
		queryKey: ["workspace-file", sessionId, path],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/file", {
				params: { path: { sessionId }, query: { path } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load file"));
			return data as WorkspaceFile;
		},
	});
	const file = q.data;
	const lines = useMemo(() => file?.lines ?? [], [file]);

	// Map the backend's new-side change ranges to a per-row-index marker map.
	// A "removed" marker is zero-height and anchors to the row now occupying the
	// boundary (clamped to the last row for a trailing deletion).
	const changeMarks = useMemo(() => {
		const map = new Map<number, ChangeMark>();
		for (const c of file?.changedLines ?? []) {
			const kind = c.kind as ChangeMark;
			if (kind === "removed") {
				const idx = Math.min(Math.max(c.start - 1, 0), lines.length - 1);
				if (idx >= 0 && !map.has(idx)) map.set(idx, "removed");
				continue;
			}
			for (let ln = c.start; ln <= c.end; ln++) {
				const idx = ln - 1;
				if (idx >= 0 && idx < lines.length) map.set(idx, kind);
			}
		}
		return map;
	}, [file?.changedLines, lines.length]);

	const changedCount = useMemo(() => {
		let n = 0;
		for (const c of file?.changedLines ?? []) {
			n += c.kind === "removed" ? 1 : c.end - c.start + 1;
		}
		return n;
	}, [file?.changedLines]);

	// Jump to the referenced line once rendered, via a zero-height anchor node
	// pinned to that row (reusing DiffRows' anchor mechanism).
	const anchorRef = useRef<HTMLDivElement | null>(null);
	const anchorIndex = line != null && line >= 1 && line <= lines.length ? line - 1 : undefined;
	useEffect(() => {
		if (anchorIndex == null) return;
		const el = anchorRef.current;
		if (!el || typeof el.scrollIntoView !== "function") return;
		el.scrollIntoView({ block: "center" });
	}, [anchorIndex, lines.length]);

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
					FILE
				</span>
				<PathLabel path={path} style={{ fontFamily: MONO, fontSize: 12.5, color: "#c7c7cc" }} />
				{line != null && <span style={{ fontFamily: MONO, fontSize: 12, color: ACCENT, flex: "none" }}>:{line}</span>}
				<div style={{ flex: 1 }} />
				{changedCount > 0 && (
					<span style={{ fontFamily: MONO, fontSize: 11.5, color: ACCENT, flex: "none" }}>
						{changedCount} uncommitted
					</span>
				)}
			</div>

			{/* body */}
			<div style={{ flex: 1, overflow: "auto", padding: "20px 24px", minHeight: 0 }}>
				{q.isLoading && <p style={{ fontSize: 12.5, color: P.muted2 }}>Loading file…</p>}
				{q.error && <p style={{ fontSize: 12.5, color: P.red }}>{apiErrorMessage(q.error, "Unable to load file")}</p>}
				{file && (!file.available || lines.length === 0) && !q.isLoading && (
					<p style={{ fontSize: 12.5, color: P.muted2 }}>
						{(file.reason && UNAVAILABLE_MESSAGE[file.reason]) || "This file can’t be displayed."}
					</p>
				)}
				{file && file.available && lines.length > 0 && (
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
								minWidth: 0,
							}}
						>
							<PathLabel path={path} />
						</div>
						<DiffRows
							lines={lines}
							size="wide"
							changeMarks={changeMarks}
							anchorIndex={anchorIndex}
							anchorNode={<div ref={anchorRef} style={{ height: 0 }} />}
						/>
						{file.truncated && (
							<div
								style={{ padding: "8px 14px", borderTop: `1px solid ${P.borderCard}`, fontSize: 11, color: P.muted2 }}
							>
								File truncated — showing the first lines only.
							</div>
						)}
					</div>
				)}
			</div>
		</div>
	);
}
