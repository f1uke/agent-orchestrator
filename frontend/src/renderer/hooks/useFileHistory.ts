import { useCallback, useRef, useState } from "react";
import {
	type FileHistory,
	type HistoryEntry,
	backTarget,
	canGoBack,
	canGoForward,
	emptyHistory,
	forwardTarget,
	goBack,
	goForward,
	pushHistory,
} from "../lib/editor/file-history";
import type { WorkspaceFileOpen } from "../lib/open-workspace-file";

/**
 * Every task's back/forward stack, for as long as the app runs.
 *
 * Module level, not component state, so switching to a crewmate's terminal and
 * back — which unmounts nothing less than the whole center pane — does not throw
 * away where you had been. Keyed by `taskKeyOf`, the same identity the rail's
 * tree state and selected tab use.
 *
 * NOT persisted across a restart, deliberately: a back stack is "where I have
 * just been", and one restored from last week is noise wearing the same buttons.
 */
const stacks = new Map<string, FileHistory>();

/** Only for tests — the module map outlives any single render tree. */
export function __resetFileHistories(): void {
	stacks.clear();
}

/** Where the reader's cursor actually is, so Back returns to it rather than to line 1. */
export type Departure = { path: string; line: number; column?: number };

export type FileHistoryNav = {
	canBack: boolean;
	canForward: boolean;
	/** The entries Back and Forward would take you to — for the buttons' titles. */
	back: HistoryEntry | null;
	forward: HistoryEntry | null;
	/** Records a jump. Returns nothing: the caller already knows what it opened. */
	record: (file: WorkspaceFileOpen) => void;
	/** Moves back/forward and returns the entry to open, or null if there is none. */
	goBack: () => HistoryEntry | null;
	goForward: () => HistoryEntry | null;
	/** Reports the live cursor. A ref write — never a re-render, and Monaco fires this a lot. */
	setPosition: (departure: Departure | null) => void;
};

export function useFileHistory(taskKey: string | undefined): FileHistoryNav {
	const [, bump] = useState(0);
	const positionRef = useRef<Departure | null>(null);
	const history = (taskKey ? stacks.get(taskKey) : undefined) ?? emptyHistory;

	const commit = useCallback(
		(next: FileHistory) => {
			if (!taskKey || next === stacks.get(taskKey)) return;
			stacks.set(taskKey, next);
			bump((n) => n + 1);
		},
		[taskKey],
	);

	const record = useCallback(
		(file: WorkspaceFileOpen) => {
			if (!taskKey) return;
			commit(pushHistory(history, file, positionRef.current ?? undefined));
			// The cursor report belongs to the file being left, never to the one
			// arriving: a stale departure would rewrite the new entry's line the
			// moment the next jump happened.
			positionRef.current = null;
		},
		[commit, history, taskKey],
	);

	const step = useCallback(
		(move: typeof goBack, target: typeof backTarget) => (): HistoryEntry | null => {
			if (!taskKey) return null;
			const entry = target(history);
			if (!entry) return null;
			commit(move(history, positionRef.current ?? undefined));
			positionRef.current = null;
			return entry;
		},
		[commit, history, taskKey],
	);

	return {
		canBack: canGoBack(history),
		canForward: canGoForward(history),
		back: backTarget(history),
		forward: forwardTarget(history),
		record,
		goBack: step(goBack, backTarget),
		goForward: step(goForward, forwardTarget),
		setPosition: useCallback((departure: Departure | null) => {
			positionRef.current = departure;
		}, []),
	};
}
