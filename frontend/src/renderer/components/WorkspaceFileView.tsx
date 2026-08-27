import { type CSSProperties, lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, FolderOpen, GitCompare } from "lucide-react";
import { useSaveWorkspaceFile } from "../hooks/useSaveWorkspaceFile";
import { useWorkspaceFile, workspaceFileQueryKey } from "../hooks/useWorkspaceFile";
import { useWorkspaceFileDiff } from "../hooks/useWorkspaceFileDiff";
import { apiErrorMessage } from "../lib/api-client";
import { ACCENT, MONO, PALETTE as P, VIEWER as V, accentMix } from "../lib/comment-inbox";
import { branchLaneLines, firstHunkLine, hunksOf, originalTextFrom } from "../lib/editor/change-lanes";
import { editabilityOf } from "../lib/editor/editability";
import { fileBytes, modelTextFrom } from "../lib/editor/save-file";
import type { SaveFailure } from "../lib/editor/save-errors";
import { languageDisplayName, languageServerName, lspLanguageForPath } from "../lib/lsp/language-ids";
import type { LanguageServerHandle } from "../lib/lsp/use-language-server";
import type { WorkspaceFileOpen } from "../lib/open-workspace-file";
import { useUiStore } from "../stores/ui-store";
import { type Drift, FileDriftBanner } from "./FileDriftBanner";
import type { EditorHandle } from "./MonacoFileEditor";

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

/** How long "Saved" stays before clearing. A persistent badge would compete with the dirty dot. */
const SAVED_FLASH_MS = 1400;

/**
 * Below this the header sheds what it can afford to. This pane sits between a
 * sidebar and a rail, so it is routinely far narrower than the window — a media
 * query would be measuring the wrong box.
 */
const COMPACT_WIDTH = 760;

type Mode = "browse" | "changes";

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
		// 🗝 The whole label must be able to SHRINK, not only ellipsise its
		// directory. `flex: none` on the basename made this row overflow its own
		// header at 1000px and below, where the filename alone is wider than what
		// is left — the mode toggle then painted straight over the path. The
		// basename still has shrink PRIORITY (it gives way only once the directory
		// is gone), which was the original intent; it just no longer refuses.
		<span title={path} style={{ display: "flex", flex: "0 1 auto", minWidth: 0, overflow: "hidden", ...style }}>
			{dir !== "" && (
				<span
					style={{ flex: "1 1 auto", minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
				>
					{dir}
				</span>
			)}
			<span
				style={{ flex: "0 1 auto", minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
			>
				{base}
			</span>
		</span>
	);
}

/**
 * A workspace file, open in the center pane in place of the terminal — the same
 * placement the Reviews "Expand full file" view uses. The surface is Monaco:
 * real syntax highlighting (shiki grammars, the app's own `--code-*` palette),
 * folding, find, an Xcode-style minimap that bands `// MARK:` sections, and —
 * for a file inside this session's workspace — editing with a save that goes
 * through the daemon's hash-preconditioned write route.
 *
 * 🗝 The two kinds of "changed" are never merged into one thing:
 *
 * - the **branch** level, merge-base(target, HEAD) .. working tree, is a neutral
 *   hairline in the outer gutter lane: everything this branch did, committed or
 *   not, which is what the Changes rail lists;
 * - the **uncommitted** level, HEAD .. working tree, is the kind-coloured bar
 *   inboard of it, and it is the one you can click to discard.
 *
 * `path` is workspace-relative for a file inside the session's workspace and
 * absolute for one outside it (a knowledge-store note, another session's
 * worktree). An absolute one opens READ-ONLY: the read route is deliberately
 * unconfined and the write route is deliberately not.
 */
export function WorkspaceFileView({
	sessionId,
	path,
	line,
	column,
	focus,
	workspaceRoot,
	onClose,
	onOpenFile,
}: {
	sessionId: string;
	path: string;
	line?: number;
	/** 1-based column, carried by a go-to-definition target. */
	column?: number;
	/** "first-hunk" lands on what the branch changed instead of on line 1. */
	focus?: "first-hunk";
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
	const [problems, setProblems] = useState<{ errors: number; warnings: number }>({ errors: 0, warnings: 0 });
	const [dirty, setDirty] = useState(false);
	const [mode, setMode] = useState<Mode>("browse");
	const [drift, setDrift] = useState<Drift | null>(null);
	const [failure, setFailure] = useState<SaveFailure | null>(null);
	const [savedFlash, setSavedFlash] = useState(false);
	/** The hash the next save is preconditioned on. Moves on every successful save. */
	const [baseHash, setBaseHash] = useState<string | undefined>(undefined);
	const handleRef = useRef<EditorHandle | null>(null);
	const queryClient = useQueryClient();
	const rootRef = useRef<HTMLDivElement | null>(null);
	const [compact, setCompact] = useState(false);

	// What the header drops when it runs out of room, in priority order: the FILE
	// badge and the uncommitted count go first (both are repeated elsewhere), then
	// the mode toggle's labels. The PATH and SAVE never go — one says which file
	// this is and the other is the only way to keep your work.
	useEffect(() => {
		const root = rootRef.current;
		if (!root || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver((entries) => {
			const width = entries[0]?.contentRect.width ?? 0;
			if (width > 0) setCompact(width < COMPACT_WIDTH);
		});
		observer.observe(root);
		return () => observer.disconnect();
	}, []);

	// Stable, because MonacoFileEditor reports through these from effects.
	const handleServerState = useCallback(
		(next: { state: LanguageServerHandle["state"]; detail?: string }) => setServerState(next),
		[],
	);
	const handleEditorHandle = useCallback((next: EditorHandle | null) => {
		handleRef.current = next;
	}, []);
	const handleDiagnostics = useCallback((counts: { errors: number; warnings: number }) => setProblems(counts), []);

	// A language server needs an on-disk path. Inside the workspace the viewer
	// carries a relative one; outside it (a knowledge-store note, GOROOT) the path
	// already IS absolute.
	const absolutePath = useMemo(
		() => (path.startsWith("/") ? path : workspaceRoot ? `${workspaceRoot}/${path}` : undefined),
		[path, workspaceRoot],
	);
	const inWorkspace = !path.startsWith("/") && !path.startsWith("~");

	/**
	 * 🗝 Every state a reader could act on gets its OWN words, and none of them is
	 * silence. `unavailable` is the deliberate exception: a Markdown file has no
	 * language server and announcing that on every open would be noise, not
	 * information.
	 */
	const serverLabel = useMemo(() => {
		const languageId = lspLanguageForPath(path) ?? "";
		const language = languageDisplayName(languageId);
		const server = languageServerName(languageId);
		switch (serverState?.state) {
			case "starting":
			case "initializing":
				return { text: `starting ${server}…`, tone: P.muted2, title: `The ${language} language server is starting.` };
			case "indexing":
				return {
					// 🗝 Not cosmetic on Swift. sourcekit-lsp answers ⌘click in ~60 ms
					// once its index has loaded and MISSES a target outright before
					// then, and it announces none of that itself - so this pill is the
					// only thing telling a reader that a ⌘click which did nothing was a
					// wait rather than a broken feature.
					text: "loading index…",
					tone: P.muted2,
					title:
						serverState.detail ||
						// 🗝 Completion is deliberately NOT in this sentence's caveat.
						// Measured on the real iOS app: `workspace/synchronize` costs
						// ~6 s and does not help completion at all (1 019 ms to a first
						// answer without the gate, 1 333 ms with it), because completion
						// needs compile arguments rather than an index. So it works
						// while this pill is showing, and saying otherwise would send a
						// reader off to wait for nothing.
						`The ${language} language server is still loading its index. Completion already works; go to definition and symbol search settle once it finishes.`,
				};
			case "ready":
				return {
					text: `${language.toLowerCase()} ⌘click · ⌃space`,
					tone: ACCENT,
					// The tooltip is where the rest of the surface is discoverable.
					// ⌥F12 and ⇧F12 are Monaco's own bindings and need Fn on a Mac
					// laptop, so the context menu is named first: it is the path that
					// works on every keyboard.
					title:
						serverState.detail ||
						"Go to definition (⌘click), completion (⌃space), the type under the pointer on hover, " +
							"and — from the editor's right-click menu — Peek Definition (⌥F12) and " +
							"Go to References (⇧F12).",
				};
			case "failed":
				return {
					text: "no language server",
					tone: P.red,
					// The reason, verbatim: on Swift it is the actionable half - which
					// build is missing, or which tool to install.
					// `||`, not `??`: an empty reason must still say something.
					title: serverState.detail || `The ${language} language server could not be started.`,
				};
			default:
				return null;
		}
	}, [serverState, path]);

	// The file itself. Polled only while there is something to lose: an AO
	// worktree has agents writing in it, so a dirty buffer's base can go stale
	// under the reader, and telling them BEFORE they press save is worth more
	// than handling the 409 well afterwards.
	const q = useWorkspaceFile(sessionId, path, { watch: dirty });
	const file = q.data;
	const lines = useMemo(() => file?.lines ?? [], [file]);
	const savedText = useMemo(() => modelTextFrom(lines), [lines]);
	const changedLines = useMemo(() => file?.changedLines ?? [], [file]);

	// The two change levels, each from its own call. `fullContext` on the branch
	// one, because Changes mode replays its ORIGINAL side out of that payload and
	// a windowed diff is missing everything between the hunks.
	const branchDiff = useWorkspaceFileDiff(sessionId, path, inWorkspace, { base: "target", fullContext: true });
	const headDiff = useWorkspaceFileDiff(sessionId, path, inWorkspace, { base: "head" });
	const branchLines = useMemo(() => branchLaneLines(branchDiff.data), [branchDiff.data]);
	const hunks = useMemo(() => hunksOf(headDiff.data), [headDiff.data]);
	const targetOriginal = useMemo(() => originalTextFrom(branchDiff.data), [branchDiff.data]);

	const editability = useMemo(() => editabilityOf(file, path), [file, path]);
	const save = useSaveWorkspaceFile(sessionId);

	// Adopt the file's hash whenever the pane is showing that file's content.
	// This is also the drift detector: a hash that moved while the buffer is
	// CLEAN is rebased silently (there is nothing to lose), and one that moved
	// while it is DIRTY raises the banner instead of quietly winning or quietly
	// losing.
	useEffect(() => {
		const hash = file?.contentHash;
		if (!hash) return;
		setBaseHash((previous) => {
			if (previous === undefined || previous === hash) return hash;
			if (!dirty) return hash;
			return previous;
		});
		if (dirty && baseHash !== undefined && baseHash !== hash) {
			setDrift((previous) => ({
				hash,
				size: previous?.size,
				modifiedAt: previous?.modifiedAt,
				reviewing: previous?.reviewing ?? false,
			}));
		}
	}, [file?.contentHash, dirty, baseHash]);

	// A new file in the pane starts from scratch: nothing about the last one's
	// save state, drift or mode should survive.
	useEffect(() => {
		setDirty(false);
		setDrift(null);
		setFailure(null);
		setSavedFlash(false);
		setMode("browse");
		setBaseHash(undefined);
		// 🗝 Cleared per file, not per server. The previous file's squiggles are
		// gone from the editor the moment its model is; a header that kept
		// counting them would be the one thing on screen still claiming they exist.
		setProblems({ errors: 0, warnings: 0 });
	}, [sessionId, path]);

	useEffect(() => {
		if (!savedFlash) return undefined;
		const timer = window.setTimeout(() => setSavedFlash(false), SAVED_FLASH_MS);
		return () => window.clearTimeout(timer);
	}, [savedFlash]);

	const doSave = useCallback(() => {
		if (!editability.editable || save.isPending) return;
		const text = handleRef.current?.getValue();
		// Guarded twice on purpose. `buildSaveRequest` refuses a non-string too,
		// but a save that cannot succeed should not even become a request: the
		// realistic caller of the daemon's own "absent content emptied the file"
		// bug was this component with a model that had not loaded.
		if (typeof text !== "string") return;
		setFailure(null);
		// 🗝 While resolving a conflict the save preconditions on the hash of the
		// version the reader was JUST SHOWN, not on the stale one they started
		// from. That is the entire answer to "how do you get past a 409 with no
		// force flag": the route refuses a blind clobber, not an informed one, so
		// the only way through is to have actually looked — and this is where
		// having looked is cashed in.
		const hash = drift?.reviewing ? drift.hash : baseHash;
		save.mutate(
			{ path, text: fileBytes(text, file?.trailingNewline ?? true), baseHash: hash },
			{
				onSuccess: (result) => {
					// Write the result straight into the file query rather than
					// refetching. The route returns `contentHash` and `changedLines`
					// for exactly this: a second GET here would widen the window in
					// which an agent's write lands between our save and our read, and
					// arrive as a surprise instead of as drift.
					queryClient.setQueryData(workspaceFileQueryKey(sessionId, path), (previous) => {
						if (!previous || typeof previous !== "object") return previous;
						const rows = text.replace(/\n$/, "").split("\n");
						return {
							...previous,
							contentHash: result.contentHash,
							changedLines: result.changedLines,
							lines: rows.map((row, i) => ({ kind: "context", text: row, oldLine: i + 1, newLine: i + 1 })),
						};
					});
					setBaseHash(result.contentHash);
					setDirty(false);
					setDrift(null);
					setSavedFlash(true);
					setMode("browse");
				},
				onError: (error) => {
					if (error.failure.kind === "conflict") {
						setDrift({ ...error.failure.current, reviewing: false });
						// Fetch what is actually on disk now, so "Review changes" has
						// bytes to compare against rather than only a hash.
						void q.refetch();
						return;
					}
					setFailure(error.failure);
				},
			},
		);
	}, [editability.editable, save, path, sessionId, file?.trailingNewline, baseHash, drift, q, queryClient]);

	// The pane's own ⌘S, so the shortcut works with focus in the chrome as well
	// as inside the editor.
	const saveRef = useRef(doSave);
	saveRef.current = doSave;
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "s") return;
			event.preventDefault();
			saveRef.current();
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, []);

	const changedCount = useMemo(() => {
		let n = 0;
		for (const c of changedLines) {
			n += c.kind === "removed" ? 1 : c.end - c.start + 1;
		}
		return n;
	}, [changedLines]);

	// Where the editor should land. An explicit line always wins: a terminal
	// `:42` reference and a go-to-definition target both name a line the reader
	// asked for, and a Changes row's "first hunk" is only a default.
	const landOn = line ?? (focus === "first-hunk" ? (firstHunkLine(branchDiff.data) ?? undefined) : undefined);

	// Changes mode compares against the target branch; the resolve view compares
	// against the bytes now on disk. Both are the same diff editor over the same
	// one buffer.
	const diffOriginal = drift?.reviewing
		? { text: savedText, label: "On disk" }
		: mode === "changes" && targetOriginal !== null
			? { text: targetOriginal, label: branchDiff.data?.path ? "target branch" : "target branch" }
			: null;
	const editorMode = diffOriginal ? "diff" : "code";
	const changesUnavailable =
		!inWorkspace || branchDiff.isPending
			? "Changes mode needs this file's diff against the target branch."
			: targetOriginal === null
				? "This file's diff is too large to show side by side."
				: null;

	return (
		<div
			ref={rootRef}
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
				{!compact && (
					<span
						style={{
							flex: "none",
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
				)}
				<PathLabel
					path={path}
					// A floor, so the filename never shrinks to nothing: a header that
					// no longer says which file is open has dropped the one thing it
					// exists for.
					style={{ fontFamily: MONO, fontSize: 12.5, color: V.pathFg, minWidth: 84 }}
				/>
				{line != null && <span style={{ fontFamily: MONO, fontSize: 12, color: ACCENT, flex: "none" }}>:{line}</span>}
				{dirty && (
					<span
						aria-label="unsaved changes"
						title="Unsaved changes"
						style={{ flex: "none", width: 6, height: 6, borderRadius: 3, background: ACCENT }}
					/>
				)}
				<div style={{ flex: 1 }} />

				{file?.available && (
					<ModeToggle
						mode={mode}
						onMode={setMode}
						disabledReason={changesUnavailable}
						busy={drift?.reviewing}
						compact={compact}
					/>
				)}

				{changedCount > 0 && !compact && (
					<span style={{ fontFamily: MONO, fontSize: 11.5, color: ACCENT, flex: "none" }}>
						{changedCount} uncommitted
					</span>
				)}
				{problems.errors + problems.warnings > 0 && (
					<span
						data-testid="lsp-problems"
						title={`${problems.errors} error${problems.errors === 1 ? "" : "s"} and ${problems.warnings} warning${
							problems.warnings === 1 ? "" : "s"
						} reported by the language server. Hover a squiggle for the message.`}
						style={{
							fontFamily: MONO,
							fontSize: 11.5,
							flex: "none",
							color: problems.errors > 0 ? P.red : P.amber,
						}}
					>
						{problems.errors > 0 && `${problems.errors}⨯`}
						{problems.errors > 0 && problems.warnings > 0 && " "}
						{problems.warnings > 0 && `${problems.warnings}⚠`}
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
				{!editability.editable && file != null && (
					<span
						data-testid="read-only-chip"
						title={editability.detail}
						style={{ fontFamily: MONO, fontSize: 11.5, color: P.muted2, flex: "none" }}
					>
						{editability.chip}
					</span>
				)}
				{editability.editable && (
					<button
						type="button"
						data-testid="save-file"
						onClick={doSave}
						disabled={!dirty || save.isPending}
						title={dirty ? "Save (⌘S)" : "No unsaved changes"}
						style={{
							flex: "none",
							fontSize: 12,
							fontWeight: 600,
							padding: "5px 12px",
							borderRadius: 7,
							border: `1px solid ${dirty ? accentMix(45) : P.borderPill}`,
							background: dirty ? accentMix(16) : "transparent",
							color: dirty ? ACCENT : P.muted2,
							cursor: dirty && !save.isPending ? "pointer" : "default",
						}}
					>
						{save.isPending ? "Saving…" : savedFlash ? "Saved" : "Save"}
					</button>
				)}
			</div>

			{drift && (
				<FileDriftBanner
					drift={drift}
					onReview={() => setDrift({ ...drift, reviewing: true })}
					onDiscardMine={() => {
						setDrift(null);
						setDirty(false);
						// Adopting the disk hash and refetching puts the pane back on the
						// agent's version. The reader asked for this in two clicks.
						setBaseHash(drift.hash);
						void q.refetch();
					}}
					onDismiss={() => (drift.reviewing ? setDrift({ ...drift, reviewing: false }) : setDrift(null))}
				/>
			)}

			{failure && (
				<div
					data-testid="save-failure"
					role="alert"
					style={{
						flex: "none",
						padding: "10px 20px",
						background: `color-mix(in oklab, ${P.red} 8%, transparent)`,
						borderBottom: `1px solid color-mix(in oklab, ${P.red} 26%, transparent)`,
					}}
				>
					<p style={{ margin: 0, fontSize: 12.5, fontWeight: 600, color: P.red }}>{failure.title}</p>
					<p style={{ margin: "2px 0 0", fontSize: 11.5, color: P.secondary }}>{failure.detail}</p>
				</div>
			)}

			{!editability.editable && file?.available && (
				<p
					data-testid="read-only-detail"
					style={{
						flex: "none",
						margin: 0,
						padding: "8px 20px",
						fontSize: 11.5,
						color: P.muted2,
						borderBottom: `1px solid ${P.borderRail}`,
					}}
				>
					{editability.detail}
				</p>
			)}

			{/* body — the editor fills the pane; an editor in a card would give the
			    minimap and the code half the width they need. */}
			{/* `isPending`, not `isLoading`: between react-query retries a query has no
			    data, no error, and `isLoading` false, so a viewer keyed on isLoading
			    renders every branch falsy and shows a BLANK pane. */}
			{q.isPending && !q.error && (
				<p style={{ padding: "20px 24px", fontSize: 12.5, color: P.muted2 }}>Loading file…</p>
			)}
			{q.error && (
				<p style={{ padding: "20px 24px", fontSize: 12.5, color: P.red }}>
					{apiErrorMessage(q.error, "Unable to load file")}
				</p>
			)}
			{file && (!file.available || lines.length === 0) && (
				<p style={{ padding: "20px 24px", fontSize: 12.5, color: P.muted2 }}>
					{(file.reason && UNAVAILABLE_MESSAGE[file.reason]) || "This file can’t be displayed."}
				</p>
			)}
			{file && file.available && lines.length > 0 && (
				<Suspense fallback={<p style={{ padding: "20px 24px", fontSize: 12.5, color: P.muted2 }}>Opening editor…</p>}>
					<MonacoFileEditor
						sessionId={sessionId}
						path={path}
						text={savedText}
						changedLines={changedLines}
						branchLines={branchLines}
						hunks={hunks}
						line={landOn}
						column={column}
						theme={theme}
						readOnly={!editability.editable}
						mode={editorMode}
						diffOriginal={diffOriginal}
						absolutePath={absolutePath}
						workspaceRoot={workspaceRoot}
						onOpenFile={onOpenFile}
						onServerState={handleServerState}
						onDiagnostics={handleDiagnostics}
						onDirtyChange={setDirty}
						onSave={doSave}
						onHandle={handleEditorHandle}
					/>
				</Suspense>
			)}
		</div>
	);
}

/**
 * Browse ⇄ Changes, in the same shape as the rail's segmented control.
 *
 * Both modes are the same buffer — Changes is a diff editor whose MODIFIED side
 * is the very model Browse edits, so a fix made while reviewing is still there
 * when you switch back, and is saved by the same button.
 */
function ModeToggle({
	mode,
	onMode,
	disabledReason,
	busy,
	compact,
}: {
	mode: Mode;
	onMode: (mode: Mode) => void;
	disabledReason: string | null;
	/** The resolve view owns the editor; the toggle waits rather than fighting it. */
	busy?: boolean;
	/** Icons only, for a pane too narrow to carry the labels as well as the path. */
	compact?: boolean;
}) {
	const button = (value: Mode, label: string, Icon: typeof FolderOpen) => {
		const active = mode === value && !busy;
		const disabled = Boolean(busy) || (value === "changes" && disabledReason !== null);
		return (
			<button
				key={value}
				type="button"
				role="tab"
				aria-selected={active}
				disabled={disabled}
				onClick={() => onMode(value)}
				aria-label={label}
				title={value === "changes" && disabledReason ? disabledReason : label}
				style={{
					display: "inline-flex",
					alignItems: "center",
					gap: 5,
					fontSize: 11.5,
					fontWeight: 500,
					padding: "4px 9px",
					borderRadius: 6,
					border: "1px solid transparent",
					background: active ? accentMix(16) : "transparent",
					color: disabled ? P.muted3 : active ? ACCENT : P.secondary,
					cursor: disabled ? "default" : "pointer",
				}}
			>
				<Icon aria-hidden="true" style={{ width: 12, height: 12 }} />
				{!compact && label}
			</button>
		);
	};
	return (
		<div
			role="tablist"
			aria-label="Editor mode"
			style={{
				flex: "none",
				display: "flex",
				gap: 2,
				padding: 2,
				borderRadius: 8,
				border: `1px solid ${P.borderPill}`,
			}}
		>
			{button("browse", "Browse", FolderOpen)}
			{button("changes", "Changes", GitCompare)}
		</div>
	);
}
