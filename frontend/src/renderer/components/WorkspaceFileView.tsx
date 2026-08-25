import { type CSSProperties, lazy, Suspense, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { ACCENT, MONO, PALETTE as P, VIEWER as V, accentMix } from "../lib/comment-inbox";
import { useUiStore } from "../stores/ui-store";

type WorkspaceFile = components["schemas"]["WorkspaceFileResponse"];

// Monaco and its grammars are ~an order of magnitude larger than the rest of the
// renderer, so the editor is a lazy chunk: the app's cold start never pays for
// it, only the first file opened does.
const MonacoFileEditor = lazy(() => import("./MonacoFileEditor"));

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
 * Reviews "Expand full file" view uses. The surface is a read-only Monaco
 * editor: real syntax highlighting (shiki grammars, the app's own `--code-*`
 * palette), folding, find, and an Xcode-style minimap that bands `// MARK:`
 * sections. Lines that are modified-but-not-committed (working tree vs HEAD,
 * fetched with the file) carry a gutter bar.
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
	const theme = useUiStore((s) => s.theme);
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
	const text = useMemo(() => lines.map((l) => l.text).join("\n"), [lines]);
	const changedLines = useMemo(() => file?.changedLines ?? [], [file]);

	const changedCount = useMemo(() => {
		let n = 0;
		for (const c of changedLines) {
			n += c.kind === "removed" ? 1 : c.end - c.start + 1;
		}
		return n;
	}, [changedLines]);

	return (
		<div
			style={{
				position: "relative",
				display: "flex",
				flexDirection: "column",
				height: "100%",
				minHeight: 0,
				background: V.bg,
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
						color: V.chromeFg,
						background: "transparent",
						border: `1px solid ${P.connector}`,
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
				<PathLabel path={path} style={{ fontFamily: MONO, fontSize: 12.5, color: V.pathFg }} />
				{line != null && <span style={{ fontFamily: MONO, fontSize: 12, color: ACCENT, flex: "none" }}>:{line}</span>}
				<div style={{ flex: 1 }} />
				{changedCount > 0 && (
					<span style={{ fontFamily: MONO, fontSize: 11.5, color: ACCENT, flex: "none" }}>
						{changedCount} uncommitted
					</span>
				)}
				{file?.truncated && (
					<span style={{ fontFamily: MONO, fontSize: 11.5, color: P.muted2, flex: "none" }}>truncated</span>
				)}
			</div>

			{/* body — the editor fills the pane; an editor in a card would give the
			    minimap and the code half the width they need. */}
			{q.isLoading && <p style={{ padding: "20px 24px", fontSize: 12.5, color: P.muted2 }}>Loading file…</p>}
			{q.error && (
				<p style={{ padding: "20px 24px", fontSize: 12.5, color: P.red }}>
					{apiErrorMessage(q.error, "Unable to load file")}
				</p>
			)}
			{file && (!file.available || lines.length === 0) && !q.isLoading && (
				<p style={{ padding: "20px 24px", fontSize: 12.5, color: P.muted2 }}>
					{(file.reason && UNAVAILABLE_MESSAGE[file.reason]) || "This file can’t be displayed."}
				</p>
			)}
			{file && file.available && lines.length > 0 && (
				<Suspense fallback={<p style={{ padding: "20px 24px", fontSize: 12.5, color: P.muted2 }}>Opening editor…</p>}>
					<MonacoFileEditor
						sessionId={sessionId}
						path={path}
						text={text}
						changedLines={changedLines}
						line={line}
						theme={theme}
					/>
				</Suspense>
			)}
		</div>
	);
}
