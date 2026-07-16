import { create } from "zustand";

export type Theme = "light" | "dark";
/** Worker detail view toggles — Changes (Git rail) is the default. */
export type WorkbenchTab = "changes" | "files" | "terminal";

// Selection (which project/session is open) now lives in the URL — the router
// is the single source of truth, read via route params. This store holds only
// ephemeral, route-independent UI: theme, sidebar/inspector collapse, and the
// active workbench tab within a session.
type UiState = {
	workbenchTab: WorkbenchTab;
	isSidebarOpen: boolean;
	isInspectorOpen: boolean;
	theme: Theme;
	restartingProjectIds: ReadonlySet<string>;
	/** Projects whose sidebar section is collapsed (heading only). Absent = expanded. */
	collapsedProjectIds: ReadonlySet<string>;
	/**
	 * User's custom sidebar project order (project ids). Empty = daemon default.
	 * Projects absent from this list sort after the listed ones (see
	 * `orderWorkspaces`). Set by drag-and-drop reorder in the sidebar.
	 */
	projectOrder: readonly string[];
	orchestratorReplacementErrors: Record<string, string>;
	setWorkbenchTab: (tab: WorkbenchTab) => void;
	setTheme: (theme: Theme) => void;
	toggleTheme: () => void;
	toggleSidebar: () => void;
	toggleInspector: () => void;
	toggleProjectCollapsed: (projectId: string) => void;
	setProjectOrder: (orderedProjectIds: readonly string[]) => void;
	setProjectRestarting: (projectId: string, restarting: boolean) => void;
	setOrchestratorReplacementError: (projectId: string, message: string | null) => void;
};

const sidebarStorageKey = "ao.sidebar.open";
const inspectorStorageKey = "ao.inspector.open";
const themeStorageKey = "ao.theme";
const collapsedProjectsStorageKey = "ao.projects.collapsed";
const projectOrderStorageKey = "ao.projects.order";

function getLocalStorage() {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

function initialSidebarOpen() {
	return getLocalStorage()?.getItem(sidebarStorageKey) !== "false";
}

function initialInspectorOpen() {
	return getLocalStorage()?.getItem(inspectorStorageKey) !== "false";
}

function initialCollapsedProjectIds(): Set<string> {
	const raw = getLocalStorage()?.getItem(collapsedProjectsStorageKey);
	if (!raw) return new Set();
	try {
		const parsed = JSON.parse(raw);
		return Array.isArray(parsed) ? new Set(parsed.filter((id): id is string => typeof id === "string")) : new Set();
	} catch {
		return new Set();
	}
}

function initialProjectOrder(): string[] {
	const raw = getLocalStorage()?.getItem(projectOrderStorageKey);
	if (!raw) return [];
	try {
		const parsed = JSON.parse(raw);
		return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === "string") : [];
	} catch {
		return [];
	}
}

function systemTheme(): Theme {
	if (typeof window === "undefined") return "dark";
	return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

function initialTheme(): Theme {
	const stored = getLocalStorage()?.getItem(themeStorageKey);
	if (stored === "light" || stored === "dark") return stored;
	return systemTheme();
}

export function readStoredTheme(): Theme | null {
	const stored = getLocalStorage()?.getItem(themeStorageKey);
	return stored === "light" || stored === "dark" ? stored : null;
}

export const useUiStore = create<UiState>((set) => ({
	workbenchTab: "changes",
	isSidebarOpen: initialSidebarOpen(),
	isInspectorOpen: initialInspectorOpen(),
	theme: initialTheme(),
	restartingProjectIds: new Set<string>(),
	collapsedProjectIds: initialCollapsedProjectIds(),
	projectOrder: initialProjectOrder(),
	orchestratorReplacementErrors: {},
	setWorkbenchTab: (workbenchTab) => set({ workbenchTab }),
	setTheme: (theme) => {
		getLocalStorage()?.setItem(themeStorageKey, theme);
		set({ theme });
	},
	toggleTheme: () =>
		set((state) => {
			const theme = state.theme === "dark" ? "light" : "dark";
			getLocalStorage()?.setItem(themeStorageKey, theme);
			return { theme };
		}),
	toggleSidebar: () =>
		set((state) => {
			const isSidebarOpen = !state.isSidebarOpen;
			getLocalStorage()?.setItem(sidebarStorageKey, String(isSidebarOpen));
			return { isSidebarOpen };
		}),
	toggleInspector: () =>
		set((state) => {
			const isInspectorOpen = !state.isInspectorOpen;
			getLocalStorage()?.setItem(inspectorStorageKey, String(isInspectorOpen));
			return { isInspectorOpen };
		}),
	toggleProjectCollapsed: (projectId) =>
		set((state) => {
			const collapsedProjectIds = new Set(state.collapsedProjectIds);
			if (collapsedProjectIds.has(projectId)) {
				collapsedProjectIds.delete(projectId);
			} else {
				collapsedProjectIds.add(projectId);
			}
			getLocalStorage()?.setItem(collapsedProjectsStorageKey, JSON.stringify([...collapsedProjectIds]));
			return { collapsedProjectIds };
		}),
	setProjectOrder: (orderedProjectIds) => {
		const projectOrder = [...orderedProjectIds];
		getLocalStorage()?.setItem(projectOrderStorageKey, JSON.stringify(projectOrder));
		set({ projectOrder });
	},
	setProjectRestarting: (projectId, restarting) =>
		set((state) => {
			const restartingProjectIds = new Set(state.restartingProjectIds);
			if (restarting) {
				restartingProjectIds.add(projectId);
			} else {
				restartingProjectIds.delete(projectId);
			}
			return { restartingProjectIds };
		}),
	setOrchestratorReplacementError: (projectId, message) =>
		set((state) => {
			const orchestratorReplacementErrors = { ...state.orchestratorReplacementErrors };
			if (message) {
				orchestratorReplacementErrors[projectId] = message;
			} else {
				delete orchestratorReplacementErrors[projectId];
			}
			return { orchestratorReplacementErrors };
		}),
}));
