import { type CSSProperties, lazy, Suspense, useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { ACCENT, MONO, PALETTE as P, VIEWER as V, accentMix } from "../lib/comment-inbox";
import type { LanguageServerHandle } from "../lib/lsp/use-language-server";
import type { WorkspaceFileOpen } from "../lib/open-workspace-file";
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
	column,
	workspaceRoot,
	onClose,
	onOpenFile,
}: {
	sessionId: string;
	path: string;
	line?: number;
	/** 1-based column, carried by a go-to-definition target. */
	column?: number;
	/** The session's worktree root, absolute. Without it there is no language server. */
	workspaceRoot?: string;
	onClose: () => void;
	/** The file-open seam, so a ⌘click target opens the way every other file does. */
	onOpenFile?: (file: WorkspaceFileOpen) => void;
}) {
	const theme = useUiStore((s) => s.theme);
	const [serverState, setServerState] = useState<{ state: LanguageServerHandle["state"]; detail?: string } | null>(
		null,
	);
	// Stable, because MonacoFileEditor reports through it from an effect.
	const handleServerState = useCallback(
		(next: { state: LanguageServerHandle["state"]; detail?: string }) => setServerState(next),
		[],
	);

	// A language server needs an on-disk path. Inside the workspace the viewer
	// carries a relative one; outside it (a knowledge-store note, GOROOT) the path
	// already IS absolute.
	const absolutePath = useMemo(
		() => (path.startsWith("/") ? path : workspaceRoot ? `${workspaceRoot}/${path}` : undefined),
		[path, workspaceRoot],
	);

	/**
	 * 🗝 Every state a reader could act on gets its OWN words, and none of them is
	 * silence. `unavailable` is the deliberate exception: a Markdown file has no
	 * language server and announcing that on every open would be noise, not
	 * information.
	 */
	const serverLabel = useMemo(() => {
		switch (serverState?.state) {
			case "starting":
			case "initializing":
				return { text: "starting gopls…", tone: P.muted2, title: "The Go language server is starting." };
			case "indexing":
				return { text: "indexing…", tone: P.muted2, title: "The Go language server is still loading packages." };
			case "ready":
				return { text: "go ⌘click", tone: ACCENT, title: serverState.detail ?? "Go to definition is available." };
			case "failed":
				return {
					text: "no language server",
					tone: P.red,
					title: serverState.detail ?? "The Go language server could not be started.",
				};
			default:
				return null;
		}
	}, [serverState]);
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
				// Explicit, because this root carries an editor whose whole layout —
				// minimap width, and with it whether a `// MARK:` label fits — is a
				// function of how wide it ends up. In the app it is a block child of a
				// flex column and fills the pane either way; dropped into a flex ROW it
				// would shrink-wrap to its content instead and silently ignore the width
				// it was given, which is exactly how the e2e harness spent a day
				// measuring 630px while believing it measured 1240.
				width: "100%",
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
				{serverLabel && (
					<span
						data-testid="lsp-status"
						style={{ fontFamily: MONO, fontSize: 11.5, color: serverLabel.tone, flex: "none" }}
						title={serverLabel.title}
					>
						{serverLabel.text}
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
						column={column}
						theme={theme}
						absolutePath={absolutePath}
						workspaceRoot={workspaceRoot}
						onOpenFile={onOpenFile}
						onServerState={handleServerState}
					/>
				</Suspense>
			)}
		</div>
	);
}
