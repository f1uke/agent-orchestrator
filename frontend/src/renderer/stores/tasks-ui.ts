import { create } from "zustand";

/** What the Tasks tab shows for one session. Nothing here is stored: a reload starts from the list. */
export type SessionTasksUi = {
	/** The job drilled into, by its call id or task id; null is the list. */
	focus: string | null;
	/** The inspector is widened for this tab. */
	expanded: boolean;
	/** Finished jobs hidden by Clear. Only the view forgets them, the log keeps them. */
	cleared: readonly string[];
};

const FRESH: SessionTasksUi = { focus: null, expanded: false, cleared: [] };

type TasksUiState = {
	bySession: Readonly<Record<string, SessionTasksUi>>;
	focus: (sessionId: string, ref: string | null) => void;
	setExpanded: (sessionId: string, expanded: boolean) => void;
	clear: (sessionId: string, refs: readonly string[]) => void;
};

export const useTasksUi = create<TasksUiState>((set) => {
	const patch = (sessionId: string, change: Partial<SessionTasksUi>) =>
		set((state) => ({
			bySession: { ...state.bySession, [sessionId]: { ...(state.bySession[sessionId] ?? FRESH), ...change } },
		}));
	return {
		bySession: {},
		focus: (sessionId, focus) => patch(sessionId, { focus }),
		setExpanded: (sessionId, expanded) => patch(sessionId, { expanded }),
		clear: (sessionId, refs) =>
			set((state) => {
				const now = state.bySession[sessionId] ?? FRESH;
				const cleared = [...new Set([...now.cleared, ...refs])];
				return { bySession: { ...state.bySession, [sessionId]: { ...now, cleared } } };
			}),
	};
});

export const useSessionTasksUi = (sessionId: string): SessionTasksUi =>
	useTasksUi((state) => state.bySession[sessionId] ?? FRESH);
