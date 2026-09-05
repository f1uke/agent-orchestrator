import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { BookText } from "lucide-react";
import { useDaemonStatus } from "../hooks/useDaemonStatus";
import {
	useCompleteWikiTask,
	useSaveWikiTasksSettings,
	useWikiAgentControls,
	useWikiFiles,
	useWikiNote,
	useWikiStatus,
	useWikiTasks,
	useWikiTasksSettings,
	wikiFilesQueryKey,
	wikiNoteQueryKey,
	wikiTasksQueryKey,
	type WikiFiles,
} from "../hooks/useWiki";
import { useUiStore } from "../stores/ui-store";
import { WikiAgentControl, type WikiAgentState } from "./WikiAgentControl";
import { WikiAgentPicker } from "./WikiAgentPicker";
import { WikiNoteView } from "./WikiNoteView";
import { WikiTasksPanel } from "./WikiTasksPanel";
import { WikiTerminal } from "./WikiTerminal";
import { WikiVaultRail } from "./WikiVaultRail";

/**
 * The Wiki: ask an agent about your own notes.
 *
 * 🗝 This is a DESTINATION, not a session. There is no branch, no worktree, no
 * PR, no board card and no crew behind it — the centre's terminal is a bare
 * runtime handle the daemon opens in the vault (see `service/wiki`).
 *
 * The centre follows the same branch chain `SessionView.renderFocusedCenter`
 * uses: an open note takes the whole centre, else the terminal, else the agent
 * picker. The page topbar sits above all three, so the agent control never
 * moves and never disappears.
 */

const TERMINAL_FONT_SIZE_KEY = "ao.terminal.fontSize";
const DEFAULT_TERMINAL_FONT_SIZE = 12;

/**
 * How long after the pane's last output the agent still reads as "thinking".
 * Long enough to bridge the gaps between an agent's own output frames, short
 * enough that the dot settles once it has actually finished.
 */
const THINKING_LINGER_MS = 1200;

export function WikiPage() {
	const queryClient = useQueryClient();
	const theme = useUiStore((state) => state.theme);
	const daemonStatus = useDaemonStatus(queryClient);
	const statusQuery = useWikiStatus();
	const status = statusQuery.data;
	const { start, restart, stop } = useWikiAgentControls();

	const configured = status?.configured === true;
	const running = status?.running === true;
	const handleId = status?.handleId ?? "";

	const filesQuery = useWikiFiles(configured);
	const [railQuery, setRailQuery] = useState("");

	// The Tasks tab's own data. It is fetched alongside the file index rather
	// than only when the tab is opened: the scan is bounded to one configured
	// folder, and a tab that spends a second loading every time you glance at
	// it is a tab you stop glancing at.
	const tasksQuery = useWikiTasks(configured);
	const tasksSettingsQuery = useWikiTasksSettings(configured);
	const saveTasksSettings = useSaveWikiTasksSettings();
	const completeTask = useCompleteWikiTask();

	// The reading history behind the file bar's chevrons: a stack of note paths
	// with a cursor, so Back returns to the note you came FROM.
	const [history, setHistory] = useState<{ paths: string[]; index: number }>({ paths: [], index: -1 });
	const openPath = history.index >= 0 ? (history.paths[history.index] ?? null) : null;
	const noteQuery = useWikiNote(openPath);

	// The picker is shown when nothing is running, and also on demand from the
	// pill's "Switch agent…" while something still is.
	const [switching, setSwitching] = useState(false);
	const showPicker = !running || switching;

	/**
	 * The row a click on the Tasks tab's source line asked for, kept beside the
	 * history rather than inside it: it is about ONE opening of a note, not
	 * about where the reader is, so going Back does not re-scroll.
	 *
	 * `at` is a nonce so that clicking the same source line twice — the reader
	 * scrolled away and wants to be shown the row again — is two requests.
	 */
	const [reveal, setReveal] = useState<{ path: string; line: number; raw: string; at: number } | null>(null);

	const openNote = useCallback((path: string) => {
		setHistory((current) => {
			if (current.paths[current.index] === path) return current;
			const paths = [...current.paths.slice(0, current.index + 1), path];
			return { paths, index: paths.length - 1 };
		});
	}, []);

	const closeNote = useCallback(() => {
		setReveal(null);
		setHistory({ paths: [], index: -1 });
	}, []);

	/**
	 * Open a task row's note AT the row. The line is only a hint — the note may
	 * have changed since the list read it — so it travels with the row's own
	 * text, which is what `WikiNoteView` actually locates it by.
	 */
	const openSource = useCallback(
		(path: string, line: number, raw: string) => {
			openNote(path);
			setReveal({ path, line, raw, at: Date.now() });
		},
		[openNote],
	);

	// A `[[wikilink]]` names a note, not a path: resolve it against the vault
	// index the way the vault's own editor does — an exact path first, then a
	// path with `.md` added, then any note whose basename matches.
	const resolveWikilink = useCallback(
		(target: string) => {
			const resolved = resolveNotePath(target, filesQuery.data);
			if (resolved) openNote(resolved);
		},
		[filesQuery.data, openNote],
	);

	// A tag has no page of its own; it hands the reader to the rail's search,
	// which is where finding notes lives.
	const openTag = useCallback((tag: string) => setRailQuery(tag), []);

	// "The agent is thinking" is derived from the pane producing output, because
	// a session-less pane has no activity hooks feeding the daemon. It is an
	// honest, local signal: output is happening right now.
	const [thinking, setThinking] = useState(false);
	const thinkingTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const noteOutput = useCallback(() => {
		setThinking(true);
		if (thinkingTimer.current) clearTimeout(thinkingTimer.current);
		thinkingTimer.current = setTimeout(() => setThinking(false), THINKING_LINGER_MS);
	}, []);
	useEffect(
		() => () => {
			if (thinkingTimer.current) clearTimeout(thinkingTimer.current);
		},
		[],
	);
	useEffect(() => {
		if (!running) setThinking(false);
	}, [running]);

	const agentState: WikiAgentState = !running ? "stopped" : thinking ? "thinking" : "running";
	const busy = start.isPending || restart.isPending || stop.isPending;
	const controlError = errorText(start.error) ?? errorText(restart.error) ?? errorText(stop.error);

	const fontSize = useMemo(readTerminalFontSize, []);

	if (statusQuery.isPending) {
		return <div className="wiki-page wiki-page--message">Reading your wiki settings…</div>;
	}

	if (!configured) {
		return (
			<div className="wiki-page wiki-page--message">
				<div className="wiki-page__unset">
					<BookText aria-hidden="true" className="wiki-page__unset-icon" />
					<div className="wiki-page__unset-title">No vault is set up</div>
					<p className="wiki-page__unset-body">
						Point AO at a folder of markdown notes in Settings › System › Wiki vault, and this page opens an agent
						inside it.
					</p>
				</div>
			</div>
		);
	}

	return (
		<div className="wiki-page">
			{/* The page topbar. It stays put whatever the centre is showing, which is
			    what keeps the agent control reachable while reading a note. */}
			<div className="wiki-topbar">
				<BookText aria-hidden="true" className="wiki-topbar__icon" />
				<span className="wiki-topbar__title">Wiki</span>
				<span className="wiki-topbar__path" title={status?.vaultPath}>
					{elideVaultPath(status?.displayPath || status?.vaultPath || "")}
				</span>
				<div className="wiki-topbar__spacer" />
				<WikiAgentControl
					state={agentState}
					harness={status?.harness ?? ""}
					startedAt={status?.startedAt}
					onRestart={() => restart.mutate()}
					onSwitch={() => setSwitching(true)}
					onStop={() => {
						setSwitching(false);
						stop.mutate();
					}}
					onStart={() => setSwitching(true)}
				/>
			</div>

			<div className="wiki-body">
				<div className="wiki-centre">
					{openPath ? (
						<WikiNoteView
							note={noteQuery.data}
							loading={noteQuery.isPending}
							error={errorText(noteQuery.error)}
							theme={theme}
							back={
								history.index > 0
									? {
											to: history.paths[history.index - 1] ?? "",
											go: () => setHistory((h) => ({ ...h, index: h.index - 1 })),
										}
									: null
							}
							forward={
								history.index >= 0 && history.index < history.paths.length - 1
									? {
											to: history.paths[history.index + 1] ?? "",
											go: () => setHistory((h) => ({ ...h, index: h.index + 1 })),
										}
									: null
							}
							onClose={closeNote}
							onReload={() => void queryClient.invalidateQueries({ queryKey: wikiNoteQueryKey(openPath) })}
							onOpenNote={resolveWikilink}
							onOpenTag={openTag}
							reveal={reveal && reveal.path === openPath ? reveal : null}
						/>
					) : showPicker ? (
						<WikiAgentPicker
							vaultPath={elideVaultPath(status?.displayPath || status?.vaultPath || "")}
							vaultPathTitle={status?.vaultPath ?? ""}
							lastHarness={status?.harness ?? ""}
							busy={busy}
							error={controlError}
							onLaunch={(harness) =>
								start.mutate(harness, {
									onSuccess: () => setSwitching(false),
								})
							}
						/>
					) : (
						<WikiTerminal
							key={handleId}
							handleId={handleId}
							theme={theme}
							fontSize={fontSize}
							daemonReady={daemonStatus.state === "ready"}
							onOutput={noteOutput}
						/>
					)}
				</div>

				<WikiVaultRail
					files={filesQuery.data}
					loading={filesQuery.isPending}
					openPath={openPath}
					onOpenNote={openNote}
					onRefresh={() => void queryClient.invalidateQueries({ queryKey: wikiFilesQueryKey })}
					query={railQuery}
					onQueryChange={setRailQuery}
					tasks={
						<WikiTasksPanel
							tasks={tasksQuery.data}
							settings={tasksSettingsQuery.data}
							loading={tasksQuery.isPending}
							error={tasksQuery.error as Error | null}
							onRefresh={() => void queryClient.invalidateQueries({ queryKey: wikiTasksQueryKey })}
							onComplete={async (row) => {
								const result = await completeTask.mutateAsync({ path: row.path, line: row.line, raw: row.raw });
								return { moved: result.moved };
							}}
							onSaveSettings={(next) => saveTasksSettings.mutateAsync(next)}
							savingSettings={saveTasksSettings.isPending}
							settingsError={saveTasksSettings.error ? saveTasksSettings.error.message : null}
							onOpenSource={openSource}
							onOpenWikilink={resolveWikilink}
						/>
					}
				/>
			</div>
		</div>
	);
}

/**
 * The vault path a `[[wikilink]]` names. Obsidian resolves a link by note name
 * rather than by path, so all four spellings a note might use land on the same
 * file — matching how the backend computes backlinks.
 */
export function resolveNotePath(target: string, files: WikiFiles | undefined): string | null {
	const notes = files?.notes ?? [];
	const needle = target.trim().toLowerCase();
	if (needle === "") return null;
	const paths = notes.map((note) => note.path);
	const exact = paths.find((path) => path.toLowerCase() === needle);
	if (exact) return exact;
	const withExtension = paths.find((path) => path.toLowerCase() === `${needle}.md`);
	if (withExtension) return withExtension;
	return (
		paths.find((path) => {
			const base = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
			return base === needle || base === `${needle}.md`;
		}) ?? null
	);
}

/**
 * A vault path at topbar width.
 *
 * The middle is elided rather than the tail: the last segment is the vault's
 * NAME, which is the part worth reading, and a plain `text-overflow: ellipsis`
 * eats exactly that. (`direction: rtl` keeps the tail but reorders the leading
 * "/" onto the end, which reads as a typo.) The full path is on the title.
 */
export function elideVaultPath(path: string, maxSegments = 4): string {
	const leading = path.startsWith("/") ? "/" : "";
	const segments = path.replace(/^\//, "").split("/").filter(Boolean);
	if (segments.length <= maxSegments) return path;
	const head = segments[0];
	const tail = segments.slice(-2);
	return `${leading}${head}/…/${tail.join("/")}`;
}

function readTerminalFontSize(): number {
	if (typeof window === "undefined") return DEFAULT_TERMINAL_FONT_SIZE;
	const raw = window.localStorage?.getItem(TERMINAL_FONT_SIZE_KEY);
	const parsed = raw === null ? Number.NaN : Number(raw);
	return Number.isFinite(parsed) ? Math.min(20, Math.max(10, parsed)) : DEFAULT_TERMINAL_FONT_SIZE;
}

function errorText(error: unknown): string | undefined {
	if (!error) return undefined;
	return error instanceof Error ? error.message : String(error);
}
