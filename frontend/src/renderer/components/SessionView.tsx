import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { X } from "lucide-react";
import type { PanelImperativeHandle, PanelSize } from "react-resizable-panels";
import { BrowserPanelView } from "./BrowserPanel";
import { CenterPane } from "./CenterPane";
import { TodoSessionPane } from "./TodoSessionPane";
import type { FileDiffTarget } from "./ReviewsView";
import { FileDiffView } from "./FileDiffView";
import { WorkspaceFileView } from "./WorkspaceFileView";
import { type ChangesFocus, WorkspaceChangesView } from "./WorkspaceChangesView";
import type { WorkspaceFileOpen } from "../lib/open-workspace-file";
import { SessionInspector, type InspectorView } from "./SessionInspector";
import { SplitSessionPicker } from "./SplitSessionPicker";
import { SplitTreeView } from "./SplitTreeView";
import { Toast } from "./inbox-ui";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "./ui/resizable";
import { useUiStore } from "../stores/ui-store";
import { useShell } from "../lib/shell-context";
import { useBrowserView } from "../hooks/useBrowserView";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useWorkspaceChanges } from "../hooks/useWorkspaceChanges";
import { apiClient } from "../lib/api-client";
import {
	addPane,
	containsSession,
	eligibleSplitSessions,
	leaf,
	MAX_SPLIT_PANES,
	paneCount,
	paneSessionIds,
	pruneToSessions,
	removePane,
	setRatioAtPath,
	splitPane,
	type SplitDirection,
} from "../lib/split-layout";
import { isOrchestratorSession } from "../types/workspace";
import type { TerminalTarget } from "../types/terminal";

const INSPECTOR_MIN_PERCENT = 22;
const INSPECTOR_MAX_PERCENT = 45;
const inspectorSplitStorageKey = "ao.inspector.split";

function initialSplitPercent(): number {
	const raw = typeof window === "undefined" ? null : window.localStorage?.getItem(inspectorSplitStorageKey);
	const parsed = raw === null ? Number.NaN : Number(raw);
	if (!Number.isFinite(parsed)) return 28;
	return Math.min(INSPECTOR_MAX_PERCENT, Math.max(INSPECTOR_MIN_PERCENT, parsed));
}

type SessionViewProps = {
	sessionId: string;
};

// The session detail screen: terminal + git rail, under the shell-owned
// ShellTopbar. Rendered by both the project-scoped and cross-project session
// routes. TerminalPane owns the terminal lifetime and remounts by terminal
// handle so each session gets a clean xterm/mux binding.
//
// The split is shadcn's resizable (react-resizable-panels v4) with a fully
// collapsible inspector: the panel is `collapsible` and driven to 0% via the
// imperative API from the ui-store (topbar button / ⌘⇧B), animated by the
// flex-grow transition in styles.css. Content keeps a stable min-width inside
// the clipped panel so nothing reflows mid-animation; split width persists.
export function SessionView({ sessionId }: SessionViewProps) {
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	const workspaces = workspaceQuery.data ?? [];
	const { theme } = useUiStore();
	const isInspectorOpen = useUiStore((state) => state.isInspectorOpen);
	const toggleInspector = useUiStore((state) => state.toggleInspector);
	const { daemonStatus } = useShell();
	const inspectorRef = useRef<PanelImperativeHandle | null>(null);
	const inspectorSeparatorRef = useRef<HTMLDivElement | null>(null);
	const [terminalTarget, setTerminalTarget] = useState<TerminalTarget>({ kind: "worker" });
	const [browserPoppedOut, setBrowserPoppedOut] = useState(false);
	const [inspectorView, setInspectorView] = useState<InspectorView>("summary");
	// A review comment "expanded to full file" takes over the center pane (in
	// place of the terminal) until dismissed; cleared on session switch below.
	const [fileView, setFileView] = useState<FileDiffTarget | null>(null);
	// A file opened from a clicked terminal reference takes over the same center
	// slot (priority over fileView); cleared on session switch below.
	const [workspaceFile, setWorkspaceFile] = useState<WorkspaceFileOpen | null>(null);
	// A Changes-mode row takes over the same center slot, showing EVERY changed
	// file's diff stacked and scrolled to the clicked one; cleared on session
	// switch below. The nonce lets the same row be clicked twice and still
	// re-scroll. `activeChangedPath` is the reverse channel: the stacked view
	// reports what the reader has scrolled to, so the rail's tree can follow.
	const [changesFocus, setChangesFocus] = useState<ChangesFocus | null>(null);
	const [activeChangedPath, setActiveChangedPath] = useState<string | null>(null);
	// A file clicked in the terminal that lives INSIDE the project is also
	// revealed in the rail's Files tab — the tab is selected, the tree expands to
	// it and scrolls it into view. The nonce lets the same ref be clicked twice
	// and still re-reveal, exactly like changesFocus. Cleared on session switch.
	const [revealInTree, setRevealInTree] = useState<ChangesFocus | null>(null);

	const session = workspaces.flatMap((workspace) => workspace.sessions).find((s) => s.id === sessionId);
	// The terminal's "Open in…" menu opens the session's worktree; when the daemon
	// did not report one (e.g. an orchestrator), fall back to the project root.
	const workspace = session ? workspaces.find((w) => w.id === session.workspaceId) : undefined;
	const directory = session?.workspacePath ?? workspace?.path;
	const navigate = useNavigate();
	const projectId = workspace?.id;
	// Multi-terminal split: the project's layout tree, one session per pane, the
	// FOCUSED pane being the session in the URL (this component's sessionId).
	// A stored root can transiently hold a single leaf (pruning); anything that
	// isn't a real multi-pane tree renders the unchanged single view.
	const splitLayouts = useUiStore((state) => state.splitLayouts);
	const setSplitLayout = useUiStore((state) => state.setSplitLayout);
	const storedSplitRoot = projectId ? splitLayouts[projectId] : undefined;
	const splitRoot = storedSplitRoot && paneCount(storedSplitRoot) > 1 ? storedSplitRoot : undefined;
	const [splitToast, setSplitToast] = useState<string | null>(null);
	const isOrchestrator = session ? isOrchestratorSession(session) : false;
	// Orchestrator sessions are terminal-only; only worker sessions have the rail.
	const hasInspector = !isOrchestrator;
	// Whether this session's project renders in a browser (ProjectConfig.hasWebUI,
	// opt-in). It gates the whole browser surface: the rail's Browser tab, the
	// BrowserView host, and the `ao preview` reveal below. A session may still
	// hold a previewUrl from before its project switched the web UI off — the
	// stored target is left alone rather than destroyed, so switching back on
	// restores it — which is exactly why every consumer has to gate on this and
	// not on the URL's presence.
	const hasWebUI = workspace?.hasWebUI ?? false;
	const previewUrl = session?.previewUrl?.trim() || undefined;
	const previewRevision = session?.previewRevision;
	const sessionLoaded = Boolean(session);
	// Baseline of the last preview state observed for the *currently loaded*
	// session, keyed by sessionId. Only a change against this baseline counts as a
	// live `ao preview` (see the reveal effect below).
	const previewRevealRef = useRef<{ sessionId: string; revision: number; url: string } | null>(null);
	// Reveal needs this because the Files tab is CHANGES-only: a file that does not
	// differ from the target branch has no row to scroll to, and switching the rail
	// to a tab that cannot show the clicked file is worse than not switching.
	//
	// Cost, stated honestly: this shares the Files tab's cache entry (same query
	// key), so it is free whenever that tab or the stacked diff view is already
	// open — but on a worker session where neither is ever opened it does add one
	// `workspace/changes` call (a `git merge-base` + three `git diff`s + a
	// `git status`) per session-view mount. It is deliberate: the alternative is
	// deciding reveal from the path's shape, and the answer would be wrong exactly
	// when a repo is unusual.
	const workspaceChanges = useWorkspaceChanges(sessionId, hasInspector);
	const changedPaths = useMemo(
		() => new Set((workspaceChanges.data?.files ?? []).map((f) => f.path)),
		[workspaceChanges.data],
	);

	// A terminal file reference always opens in the center viewer (unchanged). A
	// reference INSIDE the project additionally reveals the file in the rail's
	// Files tab. The viewer is deliberately kept for both: it shows the whole file
	// and honours the ref's `:line`, whereas the Changes view shows only diff
	// hunks and could not scroll to a line that is not part of one.
	const openWorkspaceFile = useCallback(
		(file: WorkspaceFileOpen) => {
			setWorkspaceFile(file);
			// inWorkspace is the SERVER's containment verdict; never re-derive it
			// from the path's shape here.
			if (!file.inWorkspace || !changedPaths.has(file.path)) return;
			setInspectorView("files");
			if (!useUiStore.getState().isInspectorOpen) toggleInspector();
			setRevealInTree((prev) => ({ path: file.path, nonce: (prev?.nonce ?? 0) + 1 }));
		},
		[changedPaths, toggleInspector],
	);

	const browserView = useBrowserView({
		sessionId,
		active: Boolean(session && hasInspector && hasWebUI && (browserPoppedOut || isInspectorOpen)),
		poppedOut: browserPoppedOut,
		terminated: session?.status === "terminated",
		previewUrl,
		previewRevision,
	});

	useEffect(() => {
		setTerminalTarget({ kind: "worker" });
		setBrowserPoppedOut(false);
		setInspectorView("summary");
		setFileView(null);
		setWorkspaceFile(null);
		setChangesFocus(null);
		setActiveChangedPath(null);
		setRevealInTree(null);
	}, [sessionId]);

	// Opening/selecting a session counts as activity: POST /wake so the daemon
	// resumes it in place if the idle sweep suspended it (recreate tmux, clear the
	// paused flag), or resets its idle-close countdown if it is live. Then refetch
	// the workspace so the fresh read model (isSuspended cleared, idleCloseAt
	// pushed forward) lands immediately — a live-session touch changes no
	// CDC-watched column, so we invalidate explicitly rather than wait for an
	// event. Idempotent, so React StrictMode's double-invoke is harmless.
	useEffect(() => {
		let cancelled = false;
		void (async () => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/wake", {
				params: { path: { sessionId } },
			});
			// A 404 (session not loaded yet / already gone) is a benign no-op; only a
			// successful wake needs the refetch to reflect the reset/resume.
			if (!cancelled && !error) {
				void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			}
		})();
		return () => {
			cancelled = true;
		};
	}, [sessionId, queryClient]);

	// A restored layout may name sessions that no longer exist (cleaned up since
	// the layout was saved): prune them, the sibling takes the space. Terminated
	// sessions that still exist KEEP their pane — the terminal shows the ended
	// strip with Restore, which beats a silently vanishing pane. Identity check
	// makes this idempotent, so re-running on refetches costs nothing.
	useEffect(() => {
		if (!projectId || !workspace || !storedSplitRoot) return;
		const alive = new Set(workspace.sessions.map((s) => s.id));
		const pruned = pruneToSessions(storedSplitRoot, alive);
		if (pruned === storedSplitRoot) return;
		setSplitLayout(projectId, pruned && paneCount(pruned) > 1 ? pruned : null);
	}, [projectId, workspace, storedSplitRoot, setSplitLayout]);

	// While the split view is active, NAVIGATING to a project session that is
	// not on screen adds it as a new pane — "let me see this one too" (explicit
	// user decision; covers the sidebar, terminal @session links, deep links).
	// At the pane cap it swaps into the focused pane instead, and says so — the
	// fallback must never look like a silent replacement. Keyed on an actual
	// sessionId TRANSITION: layout changes with the URL unchanged (removing a
	// pane, pruning) must not re-add the routed session.
	const previousRoutedRef = useRef<string | undefined>(undefined);
	useEffect(() => {
		const previous = previousRoutedRef.current;
		previousRoutedRef.current = sessionId;
		if (sessionId === previous) return;
		if (!projectId || !session || !splitRoot || containsSession(splitRoot, sessionId)) return;
		const focusedBefore =
			previous && containsSession(splitRoot, previous) ? previous : (paneSessionIds(splitRoot).at(-1) as string);
		const { root, mode } = addPane(splitRoot, focusedBefore, sessionId);
		if (mode === "noop") return;
		setSplitLayout(projectId, root);
		if (mode === "swapped") {
			const replaced = workspace?.sessions.find((s) => s.id === focusedBefore);
			const replacedName = replaced && isOrchestratorSession(replaced) ? "Orchestrator" : replaced?.title;
			setSplitToast(
				`Split view is full (${MAX_SPLIT_PANES} panes) — replaced ${replacedName ?? "the focused pane"}. The session keeps running.`,
			);
		}
	}, [sessionId, projectId, session, splitRoot, workspace, setSplitLayout]);

	useEffect(() => {
		if (!splitToast) return undefined;
		const timer = window.setTimeout(() => setSplitToast(null), 5000);
		return () => window.clearTimeout(timer);
	}, [splitToast]);

	// Being placed in a pane counts as being watched: wake each pane's session
	// once (resume if the idle sweep suspended it, else reset its idle
	// countdown) — the same contract as the routed session's wake above.
	const wokenPanesRef = useRef(new Set<string>());
	useEffect(() => {
		if (!splitRoot) return;
		for (const paneSessionId of paneSessionIds(splitRoot)) {
			if (wokenPanesRef.current.has(paneSessionId)) continue;
			wokenPanesRef.current.add(paneSessionId);
			void apiClient.POST("/api/v1/sessions/{sessionId}/wake", {
				params: { path: { sessionId: paneSessionId } },
			});
		}
	}, [splitRoot]);

	// Focus movement IS navigation (replace, not push: moving focus between
	// panes should not stack history entries the back button then replays).
	const goToSession = useCallback(
		(toSessionId: string) => {
			if (!projectId) return;
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId: toSessionId },
				replace: true,
			});
		},
		[navigate, projectId],
	);

	// Split a pane: replace it with itself + the picked session, side by side.
	// From the single view this CREATES the layout (base = the lone session).
	const splitFromPane = useCallback(
		(targetSessionId: string, direction: SplitDirection, newSessionId: string) => {
			if (!projectId) return;
			const base = splitRoot ?? leaf(targetSessionId);
			const next = splitPane(base, targetSessionId, direction, newSessionId);
			if (next === base) return;
			setSplitLayout(projectId, next);
			void apiClient.POST("/api/v1/sessions/{sessionId}/wake", {
				params: { path: { sessionId: newSessionId } },
			});
		},
		[projectId, splitRoot, setSplitLayout],
	);

	// Remove a pane FROM VIEW. Pure layout surgery: no API call, no lifecycle —
	// the pane unmounts, its mux attachment closes, and the daemon-side session
	// keeps running (backend terminal/attachment.go close() never touches the
	// runtime session). Removing the focused pane hands focus to the first
	// remaining pane before the tree shrinks.
	const removeFromSplit = useCallback(
		(paneSessionId: string) => {
			if (!projectId || !splitRoot) return;
			const next = removePane(splitRoot, paneSessionId);
			if (next === splitRoot || next === null) return;
			if (paneSessionId === sessionId) {
				const fallback = paneSessionIds(next)[0];
				if (fallback) goToSession(fallback);
			}
			setSplitLayout(projectId, paneCount(next) > 1 ? next : null);
		},
		[projectId, splitRoot, sessionId, goToSession, setSplitLayout],
	);

	// Collapse to ONE pane — the pane whose picker asked, which need not be the
	// focused one (controls act without moving focus). Sessions keep running.
	const unsplitTo = useCallback(
		(surviving: string) => {
			if (!projectId) return;
			if (surviving !== sessionId) goToSession(surviving);
			setSplitLayout(projectId, null);
		},
		[projectId, sessionId, goToSession, setSplitLayout],
	);

	// Persist a settled divider drag. Reads the live store (not the render's
	// splitRoot) so the callback identity survives ratio updates — an unstable
	// identity would re-register the rrp panels on every drag frame.
	const handleSplitRatioChange = useCallback(
		(path: string, ratio: number) => {
			if (!projectId) return;
			const current = useUiStore.getState().splitLayouts[projectId];
			if (!current) return;
			useUiStore.getState().setSplitLayout(projectId, setRatioAtPath(current, path, ratio));
		},
		[projectId],
	);

	const eligibleForSplit = useMemo(
		() => eligibleSplitSessions(workspace?.sessions ?? [], splitRoot ?? (session ? leaf(session.id) : null)),
		[workspace, splitRoot, session],
	);
	const splitAtCap = splitRoot !== undefined && paneCount(splitRoot) >= MAX_SPLIT_PANES;

	// `ao preview` sets session.previewUrl and bumps previewRevision (streamed over
	// CDC); surface a *live* preview in the inspector rail's Browser tab (opening
	// the rail if collapsed), not the center pane. Only a change observed while
	// THIS session's data is already loaded counts: selecting or spawning a
	// session — or a late async load of one that ran `ao preview` earlier — just
	// records the baseline and must NEVER steal the Browser tab. The baseline is
	// keyed by sessionId, so switching sessions can't look like a fresh preview,
	// and a re-run of the same target still reveals (revision advances) while a
	// manual tab switch sticks until the next `ao preview`. `ao preview clear`
	// (empty url) advances the baseline but does not reveal. Older daemons omit
	// previewRevision, so a URL change is also treated as a fresh preview.
	useEffect(() => {
		if (!sessionLoaded) return;
		// No web UI means no Browser tab to reveal: selecting it would leave the rail
		// on a view that does not exist.
		if (!hasWebUI) return;
		const revision = previewRevision ?? 0;
		const url = previewUrl ?? "";
		const prev = previewRevealRef.current;
		previewRevealRef.current = { sessionId, revision, url };
		if (!prev || prev.sessionId !== sessionId) return;
		if (prev.revision === revision && prev.url === url) return;
		if (!url) return;
		setInspectorView("browser");
		if (!useUiStore.getState().isInspectorOpen) toggleInspector();
	}, [sessionLoaded, sessionId, hasWebUI, previewRevision, previewUrl, toggleInspector]);

	// Computed when the inspector panel mounts and frozen while it stays
	// mounted: rrp re-registers the panel (a layout effect keyed on defaultSize,
	// among others) whenever this prop's identity changes, and the imperative
	// collapse()/expand() below can race that re-registration within the same
	// commit — rrp then throws "Panel constraints not found for Panel
	// inspector", which unwinds the whole route to the router's CatchBoundary
	// (the toggle button looks dead and the session view is torn down).
	// Re-derived per panel mount (not once per SessionView mount — navigating
	// orchestrator → worker keeps this component mounted while the panel
	// remounts) so a freshly mounted panel reflects the store on its own,
	// without an imperative fix-up in the mount commit. Afterwards the
	// imperative API owns the size, so this must never track live open state.
	const inspectorDefaultSizeRef = useRef<string | null>(null);
	if (!hasInspector) {
		inspectorDefaultSizeRef.current = null;
	} else if (inspectorDefaultSizeRef.current === null) {
		inspectorDefaultSizeRef.current = isInspectorOpen ? `${initialSplitPercent()}%` : "0%";
	}
	const inspectorDefaultSize = inspectorDefaultSizeRef.current ?? "0%";

	useEffect(() => {
		if (!hasInspector) return;
		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.key.toLowerCase() !== "b" || !event.shiftKey) return;
			if (!event.metaKey && !event.ctrlKey) return;
			event.preventDefault();
			toggleInspector();
		};
		window.addEventListener("keydown", handleKeyDown);
		return () => window.removeEventListener("keydown", handleKeyDown);
	}, [hasInspector, toggleInspector]);

	// Drive the collapsible panel from the store so the topbar button, ⌘⇧B, and
	// drag-to-collapse all stay in sync. hasInspector must NOT be a dep: when
	// the inspector panel mounts into the already-live group (orchestrator →
	// worker navigation), rrp only derives the new panel's constraints in the
	// next commit, so an expand()/collapse() in the mount commit throws "Panel
	// constraints not found for Panel inspector" and unwinds the route. The
	// panel mounts in sync via inspectorDefaultSize above; only later toggles
	// need the imperative API, by which point registration has settled.
	useEffect(() => {
		const panel = inspectorRef.current;
		if (!panel) return;
		if (isInspectorOpen) {
			panel.expand();
			// expand() restores the "most recent" size, which is 0 when the panel
			// mounted collapsed — fall back to the persisted split.
			if (panel.getSize().asPercentage === 0) panel.resize(`${initialSplitPercent()}%`);
		} else {
			panel.collapse();
		}
	}, [isInspectorOpen]);

	// Persist drags and mirror collapse state (dragging past minSize collapses)
	// back into the store. Read the store imperatively to avoid a stale closure.
	// Gated on an actively dragged separator: rrp v4 derives sizes from the
	// observed DOM layout, so the flex-grow transition that animates
	// expand()/collapse() (styles.css) fires onResize with transient
	// mid-animation sizes too. Writing those back turned the imperative
	// collapse into a feedback loop — a mid-collapse size read as "dragged
	// back open", re-toggled the store, and the panel bounced back (the
	// topbar button looked dead). rrp marks the separator
	// data-separator="active" only during a pointer drag — the same hook the
	// transition-suppressing CSS keys on, so drag writes are never transition
	// frames.
	// Also wrapped in useCallback: rrp v4's panel registration useLayoutEffect
	// includes onResize in its dep array, so an unstable reference would
	// de-register/re-register the inspector panel on every render and race
	// with the expand()/collapse() effect above.
	const handleInspectorResize = useCallback(
		(size: PanelSize) => {
			if (inspectorSeparatorRef.current?.getAttribute("data-separator") !== "active") return;
			const open = useUiStore.getState().isInspectorOpen;
			if (size.asPercentage > 0) {
				window.localStorage?.setItem(inspectorSplitStorageKey, String(size.asPercentage));
				if (!open) toggleInspector();
			} else if (open) {
				toggleInspector();
			}
		},
		[toggleInspector],
	);

	if (!session && !workspaceQuery.isLoading) {
		return (
			<div className="grid h-full place-items-center bg-background p-6 text-center font-mono text-[12px] text-passive">
				Session not found. It may have been cleaned up — pick another from the sidebar.
			</div>
		);
	}

	// Toolbar tail shared by every pane AND the single view: the split picker
	// (the single view's only piece of split UI — the entry point), plus, once a
	// split exists, the remove-from-view button. Remove is a plain ghost control
	// with no confirm — it is instantly reversible (add the session back) and
	// must never resemble the destructive Kill beside it.
	const paneSplitControls = (paneSessionId: string): ReactNode => (
		<>
			<SplitSessionPicker
				atCap={splitAtCap}
				eligible={eligibleForSplit}
				onSplit={(direction, newSessionId) => splitFromPane(paneSessionId, direction, newSessionId)}
				onUnsplit={splitRoot ? () => unsplitTo(paneSessionId) : undefined}
			/>
			{splitRoot ? (
				<button
					aria-label="Remove from split (session keeps running)"
					className="terminal-toolbar__control terminal-toolbar__control--icon"
					onClick={() => removeFromSplit(paneSessionId)}
					title="Remove from split — session keeps running"
					type="button"
				>
					<X aria-hidden="true" className="h-3.5 w-3.5" />
				</button>
			) : null}
		</>
	);

	// The focused center content: identical branch chain in the single view and
	// the focused split pane — a review comment expanded to a full file, a
	// clicked terminal file reference, a Changes row, or the terminal. The
	// takeover views follow the FOCUSED pane (they are driven by rail and
	// terminal interactions, which are focused-session interactions), and are
	// cleared on focus switch by the sessionId reset effect above.
	const renderFocusedCenter = (split: boolean): ReactNode => {
		if (workspaceFile) {
			return (
				<WorkspaceFileView
					sessionId={sessionId}
					path={workspaceFile.path}
					line={workspaceFile.line}
					onClose={() => setWorkspaceFile(null)}
				/>
			);
		}
		if (changesFocus) {
			return (
				<WorkspaceChangesView
					sessionId={sessionId}
					focus={changesFocus}
					onActivePathChange={setActiveChangedPath}
					onClose={() => {
						setChangesFocus(null);
						setActiveChangedPath(null);
					}}
				/>
			);
		}
		if (fileView) {
			return <FileDiffView sessionId={sessionId} target={fileView} onClose={() => setFileView(null)} />;
		}
		if (session?.isTodo) {
			// A not-started TODO has no worktree/tmux/agent, so the terminal
			// would sit forever on "Preparing the worker terminal". Show the
			// editable WORKER SPEC instead; Start materializes in place and the
			// refetch flips isTodo off, swapping in the terminal below.
			return <TodoSessionPane session={session} />;
		}
		return (
			<CenterPane
				active
				daemonReady={daemonStatus.state === "ready"}
				directory={directory}
				onSelectWorkerTerminal={() => setTerminalTarget({ kind: "worker" })}
				onOpenWorkspaceFile={openWorkspaceFile}
				pane={split ? { focused: true } : undefined}
				session={session}
				splitControls={session ? paneSplitControls(session.id) : undefined}
				terminalTarget={terminalTarget}
				theme={theme}
			/>
		);
	};

	// An unfocused pane: terminal only, streaming live, slim toolbar. No file
	// linkification and no reviewer target — those are focused-pane
	// interactions, and one click on the pane focuses it.
	const renderSplitPane = (paneSessionId: string, focused: boolean): ReactNode => {
		if (focused) return renderFocusedCenter(true);
		const paneSession = workspace?.sessions.find((s) => s.id === paneSessionId);
		if (paneSession?.isTodo) return <TodoSessionPane session={paneSession} />;
		return (
			<CenterPane
				active={false}
				daemonReady={daemonStatus.state === "ready"}
				directory={paneSession?.workspacePath ?? workspace?.path}
				pane={{ focused: false }}
				session={paneSession}
				splitControls={paneSplitControls(paneSessionId)}
				theme={theme}
			/>
		);
	};

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<ResizablePanelGroup className="session-split min-h-0 flex-1" id="session-workspace" orientation="horizontal">
				{/* react-resizable-panels v4: bare numbers are PIXELS; percentages must
            be strings. Numeric sizes here once clamped the inspector to 45px. */}
				<ResizablePanel defaultSize="72%" id="terminal" minSize="45%">
					{splitRoot ? (
						<SplitTreeView
							focusedSessionId={sessionId}
							onFocusPane={goToSession}
							onRatioChange={handleSplitRatioChange}
							renderPane={renderSplitPane}
							root={splitRoot}
						/>
					) : (
						renderFocusedCenter(false)
					)}
				</ResizablePanel>
				{hasInspector ? (
					<>
						<ResizableHandle
							className="session-inspector__resize-handle focus-visible:ring-0 focus-visible:ring-offset-0"
							elementRef={inspectorSeparatorRef}
						/>
						<ResizablePanel
							aria-hidden={!isInspectorOpen}
							collapsible
							defaultSize={inspectorDefaultSize}
							id="inspector"
							inert={!isInspectorOpen}
							maxSize={`${INSPECTOR_MAX_PERCENT}%`}
							minSize={`${INSPECTOR_MIN_PERCENT}%`}
							onResize={handleInspectorResize}
							panelRef={inspectorRef}
							style={{ overflow: "hidden" }}
						>
							{/* Stable content width while the panel animates (yyork pattern):
                  the pane clips instead of reflowing the inspector mid-collapse. */}
							<div className="h-full min-w-[280px]">
								<SessionInspector
									browserPoppedOut={browserPoppedOut}
									hasWebUI={hasWebUI}
									isInspectorVisible={isInspectorOpen}
									onOpenReviewerTerminal={({ handleId, harness }) =>
										setTerminalTarget({ kind: "reviewer", handleId, harness })
									}
									onToggleBrowserPopOut={setBrowserPoppedOut}
									onViewChange={setInspectorView}
									onOpenFile={setFileView}
									onOpenChangedFile={({ path }) => {
										setActiveChangedPath(path);
										setChangesFocus((prev) => ({ path, nonce: (prev?.nonce ?? 0) + 1 }));
									}}
									selectedChangedPath={activeChangedPath ?? undefined}
									revealInTree={revealInTree}
									view={inspectorView}
									browserView={browserView}
									session={session}
								/>
							</div>
						</ResizablePanel>
					</>
				) : null}
			</ResizablePanelGroup>
			{/* Maximized browser: a fixed overlay across the whole app window,
          portaled to <body> so it escapes the shell layout (covering the
          sidebar + topbar, not just the session area) and sits outside any
          `[data-panel]` column, so the native WebContentsView is not clamped
          and fills the entire window. */}
			{splitToast ? <Toast text={splitToast} /> : null}
			{browserPoppedOut && hasWebUI && session
				? createPortal(
						<div className="browser-popout-overlay">
							<BrowserPanelView
								active
								browserView={browserView}
								onTogglePopOut={setBrowserPoppedOut}
								poppedOut
								session={session}
							/>
						</div>,
						document.body,
					)
				: null}
		</div>
	);
}
