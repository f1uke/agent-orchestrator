import { ChevronDown, ChevronLeft, ChevronRight } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type ChangedFile, useWorkspaceChanges } from "../hooks/useWorkspaceChanges";
import { useWorkspaceFileDiff } from "../hooks/useWorkspaceFileDiff";
import { apiErrorMessage } from "../lib/api-client";
import { activeIndexFromTops } from "../lib/active-section";
import { orderedFileItems } from "../lib/file-tree";
import { ACCENT, MONO, PALETTE as P, VIEWER as V, accentMix } from "../lib/comment-inbox";
import { DiffRows } from "./DiffRows";

/**
 * Per-file ceiling and cumulative budget for what opens automatically, in
 * changed lines. Chosen to keep a normal review fully readable on open while a
 * large pull request stays cheap: past the budget a file costs one ~34px header
 * row and zero requests until the reader opens it.
 */
const MAX_AUTO_FILE_LINES = 500;
const MAX_AUTO_TOTAL_LINES = 2000;

/** How far below the scroll container's top edge a section counts as "being read". */
const ACTIVE_ANCHOR_OFFSET = 12;

/**
 * Which files open expanded, walking the list top-down.
 *
 * A file wider than {@link MAX_AUTO_FILE_LINES} never auto-expands, and once the
 * cumulative {@link MAX_AUTO_TOTAL_LINES} budget is spent everything below stays
 * collapsed. Binary files render a one-line note rather than a diff, so they
 * cost nothing and consume no budget.
 */
export function autoExpandedPaths(files: readonly ChangedFile[]): ReadonlySet<string> {
	const expanded = new Set<string>();
	let budget = MAX_AUTO_TOTAL_LINES;
	for (const file of files) {
		const size = file.binary ? 0 : file.additions + file.deletions;
		if (size > MAX_AUTO_FILE_LINES) continue;
		if (size > budget) break;
		budget -= size;
		expanded.add(file.path);
	}
	return expanded;
}

export type ChangesFocus = { path: string; nonce: number };

/**
 * Every changed file's diff, stacked vertically in the center pane — GitLab's
 * merge-request Changes view.
 *
 * Each file is its own section with a sticky header and its own collapse
 * control; the rail's tree drives `focus` to scroll here, and this view reports
 * back which file the reader has scrolled to so the tree can highlight it.
 */
export function WorkspaceChangesView({
	sessionId,
	focus,
	onClose,
	onActivePathChange,
}: {
	sessionId: string;
	focus: ChangesFocus | null;
	onClose: () => void;
	onActivePathChange?: (path: string) => void;
}) {
	const query = useWorkspaceChanges(sessionId);
	// Stacked in the SAME order the rail's tree lists them, so scrolling here and
	// reading down the tree walk the files in one sequence. The raw API order
	// does not match — the tree groups directories before files at every level.
	const files = useMemo(() => orderedFileItems(query.data?.files ?? [], (f) => f.path), [query.data]);

	// The automatic decision is derived, never stored; an explicit toggle wins
	// over it, so a refetch cannot silently re-collapse what the reader opened.
	const auto = useMemo(() => autoExpandedPaths(files), [files]);
	const [overrides, setOverrides] = useState<ReadonlyMap<string, boolean>>(() => new Map());
	const isExpanded = useCallback((path: string) => overrides.get(path) ?? auto.has(path), [overrides, auto]);

	const toggle = (path: string) =>
		setOverrides((prev) => {
			const next = new Map(prev);
			next.set(path, !isExpanded(path));
			return next;
		});
	const setAll = (value: boolean) => setOverrides(new Map(files.map((f) => [f.path, value])));

	const scrollRef = useRef<HTMLDivElement | null>(null);
	const sectionRefs = useRef(new Map<string, HTMLElement>());
	const registerSection = useCallback((path: string, el: HTMLElement | null) => {
		if (el) sectionRefs.current.set(path, el);
		else sectionRefs.current.delete(path);
	}, []);

	// Scroll-spy. jsdom gives every element a zero-sized box, so the wiring here
	// is only meaningful in the real app; the decision it feeds
	// (`activeIndexFromTops`) is unit-tested on its own.
	const frameRef = useRef<number | null>(null);
	const reportActive = useCallback(() => {
		const container = scrollRef.current;
		if (!container || !onActivePathChange || files.length === 0) return;
		const anchor = container.getBoundingClientRect().top + ACTIVE_ANCHOR_OFFSET;
		const tops = files.map(
			(f) => sectionRefs.current.get(f.path)?.getBoundingClientRect().top ?? Number.POSITIVE_INFINITY,
		);
		const index = activeIndexFromTops(tops, anchor);
		if (index >= 0) onActivePathChange(files[index].path);
	}, [files, onActivePathChange]);

	const handleScroll = () => {
		if (frameRef.current != null) return;
		frameRef.current = requestAnimationFrame(() => {
			frameRef.current = null;
			reportActive();
		});
	};
	useEffect(() => () => (frameRef.current != null ? cancelAnimationFrame(frameRef.current) : undefined), []);

	// Scroll to whichever file the rail asked for, expanding it first — landing
	// on a collapsed header would show the reader nothing. The nonce lets the
	// same row be clicked twice and still re-scroll.
	const handledFocusRef = useRef<string>("");
	useEffect(() => {
		if (!focus) return;
		const token = `${focus.path}#${focus.nonce}`;
		if (handledFocusRef.current === token) return;
		const el = sectionRefs.current.get(focus.path);
		// Sections mount when the file list arrives; this effect re-runs then.
		if (!el) return;
		handledFocusRef.current = token;
		setOverrides((prev) => new Map(prev).set(focus.path, true));
		el.scrollIntoView?.({ block: "start" });
	}, [focus, files.length]);

	const additions = files.reduce((n, f) => n + (f.binary ? 0 : f.additions), 0);
	const deletions = files.reduce((n, f) => n + (f.binary ? 0 : f.deletions), 0);
	const allExpanded = files.length > 0 && files.every((f) => isExpanded(f.path));

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
			<div
				role="banner"
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
						flex: "none",
						color: ACCENT,
						background: accentMix(14),
						border: `1px solid ${accentMix(35)}`,
						borderRadius: 5,
						padding: "3px 7px",
					}}
				>
					CHANGES
				</span>
				<span style={{ fontSize: 12.5, color: V.pathFg, flex: "none" }}>
					{files.length} {files.length === 1 ? "file" : "files"}
				</span>
				{files.length > 0 ? (
					<span style={{ fontFamily: MONO, fontSize: 11.5, flex: "none" }}>
						<span style={{ color: V.addCount }}>+{additions}</span>{" "}
						<span style={{ color: V.delCount }}>−{deletions}</span>
					</span>
				) : null}
				<div style={{ flex: 1 }} />
				{files.length > 0 ? (
					<button
						type="button"
						onClick={() => setAll(!allExpanded)}
						style={{
							fontSize: 11.5,
							fontWeight: 500,
							color: V.chromeFg,
							background: "transparent",
							border: `1px solid ${P.connector}`,
							borderRadius: 7,
							padding: "4px 10px",
							cursor: "pointer",
							flex: "none",
						}}
					>
						{allExpanded ? "Collapse all" : "Expand all"}
					</button>
				) : null}
			</div>

			<div
				ref={scrollRef}
				onScroll={handleScroll}
				style={{ flex: 1, overflow: "auto", padding: "16px 24px 40%", minHeight: 0 }}
			>
				{query.isLoading ? <p style={{ fontSize: 12.5, color: P.muted2 }}>Loading changes…</p> : null}
				{query.error ? (
					<p style={{ fontSize: 12.5, color: P.red }}>{apiErrorMessage(query.error, "Unable to load changes")}</p>
				) : null}
				{query.data && !query.data.available && !query.isLoading ? (
					<p style={{ fontSize: 12.5, color: P.muted2 }}>
						There is nothing to diff for this session — see the Files tab for why.
					</p>
				) : null}
				{query.data?.available && files.length === 0 ? (
					<p style={{ fontSize: 12.5, color: P.muted2 }}>
						No changes against {query.data.targetBranch || "the target branch"}.
					</p>
				) : null}

				{files.map((file) => (
					<FileDiffSection
						key={file.path}
						sessionId={sessionId}
						file={file}
						expanded={isExpanded(file.path)}
						onToggle={() => toggle(file.path)}
						registerSection={registerSection}
					/>
				))}
			</div>
		</div>
	);
}

const STATUS_LETTER: Record<string, string> = { added: "A", modified: "M", deleted: "D", renamed: "R" };
const STATUS_COLOR: Record<string, string> = {
	added: "var(--green)",
	modified: ACCENT,
	deleted: "var(--red)",
	renamed: "var(--amber)",
};

function FileDiffSection({
	sessionId,
	file,
	expanded,
	onToggle,
	registerSection,
}: {
	sessionId: string;
	file: ChangedFile;
	expanded: boolean;
	onToggle: () => void;
	registerSection: (path: string, el: HTMLElement | null) => void;
}) {
	// A binary file has no diff to fetch, expanded or not.
	const wantsDiff = expanded && !file.binary;
	const q = useWorkspaceFileDiff(sessionId, file.path, wantsDiff);
	const lines = q.data?.lines ?? [];

	return (
		<section
			aria-label={file.path}
			ref={(el) => registerSection(file.path, el)}
			style={{
				maxWidth: 1040,
				marginBottom: 14,
				border: `1px solid ${P.borderCard}`,
				borderRadius: 10,
				/* `clip`, NOT `hidden`: an `overflow: hidden` ancestor becomes the
				   sticky containing block and would pin the header to this card
				   instead of the scroll container, silently killing the stick. */
				overflow: "clip",
				background: V.cardBg,
			}}
		>
			<button
				type="button"
				aria-expanded={expanded}
				aria-label={`${expanded ? "Collapse" : "Expand"} ${file.path}`}
				onClick={onToggle}
				className="mono"
				style={{
					position: "sticky",
					top: 0,
					zIndex: 1,
					display: "flex",
					width: "100%",
					alignItems: "center",
					gap: 8,
					padding: "9px 14px",
					background: P.fileHeader,
					borderBottom: `1px solid ${P.borderCard}`,
					fontFamily: MONO,
					fontSize: 11.5,
					color: V.chromeFg,
					cursor: "pointer",
					textAlign: "left",
				}}
			>
				{expanded ? (
					<ChevronDown aria-hidden="true" style={{ width: 13, height: 13, flex: "none" }} />
				) : (
					<ChevronRight aria-hidden="true" style={{ width: 13, height: 13, flex: "none" }} />
				)}
				<span
					aria-hidden="true"
					style={{
						flex: "none",
						width: 12,
						fontWeight: 700,
						textAlign: "center",
						color: STATUS_COLOR[file.status] ?? ACCENT,
					}}
				>
					{STATUS_LETTER[file.status] ?? "M"}
				</span>
				<span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0, flex: 1 }}>
					<bdi>{file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}</bdi>
				</span>
				{file.binary ? (
					<span style={{ flex: "none", color: P.muted2 }}>bin</span>
				) : (
					<span style={{ flex: "none" }}>
						<span style={{ color: V.addCount }}>+{file.additions}</span>{" "}
						<span style={{ color: V.delCount }}>−{file.deletions}</span>
					</span>
				)}
			</button>

			{file.binary ? (
				<p style={{ padding: "10px 14px", fontSize: 11.5, color: P.muted2, margin: 0 }}>
					Binary file — no diff to show.
				</p>
			) : !expanded ? null : q.isLoading ? (
				<p style={{ padding: "10px 14px", fontSize: 11.5, color: P.muted2, margin: 0 }}>Loading diff…</p>
			) : q.error ? (
				<p style={{ padding: "10px 14px", fontSize: 11.5, color: P.red, margin: 0 }}>
					{apiErrorMessage(q.error, "Unable to load diff")}
				</p>
			) : lines.length === 0 ? (
				<p style={{ padding: "10px 14px", fontSize: 11.5, color: P.muted2, margin: 0 }}>
					No diff to show for this file — it may be unchanged against the target branch.
				</p>
			) : (
				<>
					<DiffRows lines={lines} size="wide" />
					{q.data?.truncated ? (
						<div style={{ padding: "8px 14px", borderTop: `1px solid ${P.borderCard}`, fontSize: 11, color: P.muted2 }}>
							Diff truncated — showing the first lines only.
						</div>
					) : null}
				</>
			)}
		</section>
	);
}
