import type { ReactNode, Ref } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { SessionView } from "./SessionView";
import { useUiStore } from "../stores/ui-store";
import { leaf, paneSessionIds, splitPane, type SplitNode } from "../lib/split-layout";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

type FakePanelHandle = {
	collapse: Mock;
	expand: Mock;
	getSize: Mock;
	isCollapsed: Mock;
	resize: Mock;
};

type PanelEntry = {
	handle: FakePanelHandle;
	onResize?: (size: { asPercentage: number; inPixels: number }) => void;
};

const { workspaces, panels } = vi.hoisted(() => {
	const worker = {
		id: "sess-1",
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "do the thing",
		provider: "claude-code",
		kind: "worker",
		branch: "ao/sess-1",
		status: "working",
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
	} satisfies WorkspaceSession;
	const orchestrator = {
		...worker,
		id: "sess-orch",
		kind: "orchestrator",
		title: "orchestrate",
	} satisfies WorkspaceSession;
	const todo = {
		...worker,
		id: "sess-todo",
		title: "queued-task",
		status: "todo",
		isTodo: true,
		baseBranch: "main-fluke",
		prTarget: "main-fluke",
		prompt: "do the queued thing",
	} satisfies WorkspaceSession;
	const worker2 = { ...worker, id: "sess-2", title: "second thing", branch: "ao/sess-2" } satisfies WorkspaceSession;
	const worker3 = { ...worker, id: "sess-3", title: "third thing", branch: "ao/sess-3" } satisfies WorkspaceSession;
	// Enough live sessions to build a layout at MAX_SPLIT_PANES (10).
	const extraWorkers = Array.from(
		{ length: 10 },
		(_, i) => ({ ...worker, id: `sess-x${i}`, title: `extra ${i}` }) satisfies WorkspaceSession,
	);
	const workspaces: WorkspaceSummary[] = [
		{
			id: "proj-1",
			name: "my-app",
			path: "/p",
			type: "main",
			hasWebUI: true,
			sessions: [worker, worker2, worker3, orchestrator, todo, ...extraWorkers],
		},
	];
	return { workspaces, panels: new Map<string, PanelEntry>() };
});

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<object>()),
	useNavigate: () => navigateMock,
}));

// Drives the reveal routing: what CenterPane hands back from a clicked terminal
// file reference, and what the Files tab's changed-file list currently holds.
const { openFileArg, changesData } = vi.hoisted(() => ({
	openFileArg: { current: undefined as unknown },
	changesData: { current: undefined as { files: { path: string }[] } | undefined },
}));

// The terminal and inspector body pull in xterm/SSE machinery irrelevant to
// the split under test. (The topbar is shell-owned — see ShellTopbar.)
vi.mock("./CenterPane", () => ({
	CenterPane: ({
		onOpenWorkspaceFile,
		splitControls,
		pane,
		session,
	}: {
		onOpenWorkspaceFile?: (f: unknown) => void;
		splitControls?: ReactNode;
		pane?: { focused: boolean };
		session?: { id: string };
	}) => (
		<div data-pane-focused={pane ? String(pane.focused) : undefined} data-testid={`center-${session?.id ?? "none"}`}>
			terminal center
			<button type="button" onClick={() => onOpenWorkspaceFile?.(openFileArg.current)}>
				open workspace file
			</button>
			{splitControls}
		</div>
	),
}));
vi.mock("./TodoSessionPane", () => ({
	TodoSessionPane: ({ session }: { session: { id: string } }) => <div>todo editor for {session.id}</div>,
}));
vi.mock("./BrowserPanel", () => ({
	BrowserPanelView: ({
		poppedOut,
		onTogglePopOut,
	}: {
		poppedOut: boolean;
		onTogglePopOut: (next: boolean) => void;
	}) => (
		<button type="button" onClick={() => onTogglePopOut(!poppedOut)}>
			{poppedOut ? "browser center" : "browser rail"}
		</button>
	),
}));
const browserDestroy = vi.hoisted(() => vi.fn());
vi.mock("../hooks/useBrowserView", () => ({
	useBrowserView: () => ({
		viewId: "browser:sess-1",
		navState: {
			viewId: "browser:sess-1",
			url: "http://127.0.0.1:4173/",
			title: "Calculator",
			canGoBack: false,
			canGoForward: false,
			isLoading: false,
		},
		slotRef: vi.fn(),
		navigate: vi.fn(),
		goBack: vi.fn(),
		goForward: vi.fn(),
		reload: vi.fn(),
		stop: vi.fn(),
		destroy: browserDestroy,
	}),
}));
vi.mock("./SessionInspector", () => ({
	SessionInspector: ({
		onToggleBrowserPopOut,
		view,
		hasWebUI,
	}: {
		onToggleBrowserPopOut?: () => void;
		view?: string;
		hasWebUI?: boolean;
	}) => (
		<button type="button" data-has-web-ui={String(Boolean(hasWebUI))} data-view={view} onClick={onToggleBrowserPopOut}>
			pop browser
		</button>
	),
}));
vi.mock("../lib/shell-context", () => ({
	useShell: () => ({ daemonStatus: { state: "ready" } }),
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: workspaces, isLoading: false }),
	workspaceQueryKey: ["workspaces"],
}));

// The wake-on-open effect touches the daemon (POST /wake) and invalidates the
// workspace query; both are stubbed so the effect is observable without a real
// QueryClientProvider or daemon.
const { wakeMock, invalidateMock, queryClientMock } = vi.hoisted(() => {
	const invalidateMock = vi.fn();
	// Stable identity, like the real useQueryClient: a fresh object per render
	// would re-fire every effect that lists the client in its deps.
	return { wakeMock: vi.fn(), invalidateMock, queryClientMock: { invalidateQueries: invalidateMock } };
});
vi.mock("../lib/api-client", () => ({ apiClient: { POST: wakeMock } }));
vi.mock("@tanstack/react-query", () => ({
	useQueryClient: () => queryClientMock,
	useQuery: () => ({ data: changesData.current }),
}));

// jsdom has no layout engine, so the real react-resizable-panels would never
// produce meaningful sizes — record the props SessionView passes and expose a
// fake imperative handle per panel instead.
vi.mock("./ui/resizable", () => ({
	ResizablePanelGroup: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
	ResizableHandle: ({ elementRef }: { elementRef?: Ref<HTMLDivElement | null> }) => (
		<div
			data-separator="inactive"
			data-testid="resize-handle"
			ref={(el) => {
				if (elementRef && typeof elementRef === "object") {
					(elementRef as { current: HTMLDivElement | null }).current = el;
				}
			}}
		/>
	),
	ResizablePanel: ({
		children,
		id,
		defaultSize,
		minSize,
		maxSize,
		collapsible,
		panelRef,
		onResize,
		style: _style,
		...rest
	}: {
		children?: ReactNode;
		id: string;
		defaultSize?: number | string;
		minSize?: number | string;
		maxSize?: number | string;
		collapsible?: boolean;
		panelRef?: Ref<FakePanelHandle | null>;
		onResize?: (size: { asPercentage: number; inPixels: number }) => void;
		style?: React.CSSProperties;
	}) => {
		let entry = panels.get(id);
		if (!entry) {
			entry = {
				handle: {
					collapse: vi.fn(),
					expand: vi.fn(),
					getSize: vi.fn(() => ({ asPercentage: 28, inPixels: 280 })),
					isCollapsed: vi.fn(() => false),
					resize: vi.fn(),
				},
			};
			panels.set(id, entry);
		}
		entry.onResize = onResize;
		if (panelRef && typeof panelRef === "object") {
			(panelRef as { current: FakePanelHandle | null }).current = entry.handle;
		}
		return (
			<div data-testid={`panel-${id}`} data-collapsible={collapsible ? "true" : undefined} {...rest}>
				<span data-testid={`panel-${id}-sizes`}>
					{JSON.stringify([defaultSize, minSize, maxSize].filter((s) => s !== undefined))}
				</span>
				{children}
			</div>
		);
	},
}));

function panelSizes(id: string): unknown[] {
	return JSON.parse(screen.getByTestId(`panel-${id}-sizes`).textContent ?? "[]") as unknown[];
}

describe("SessionView", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ isInspectorOpen: true, splitLayouts: {} });
		panels.clear();
		browserDestroy.mockReset();
		navigateMock.mockReset();
		wakeMock.mockReset().mockResolvedValue({ error: undefined });
		invalidateMock.mockReset();
		openFileArg.current = undefined;
		changesData.current = undefined;
	});

	// Regression: react-resizable-panels v4 treats bare numeric sizes as PIXELS
	// (numbers were percentages in the older API the shadcn examples use).
	// defaultSize={28}/maxSize={45} clamped the inspector rail to a 45px sliver.
	// Every size must be an explicit percentage string.
	it("sizes the terminal/inspector split in percentages, not pixels", () => {
		render(<SessionView sessionId="sess-1" />);

		for (const panelId of ["terminal", "inspector"]) {
			const sizes = panelSizes(panelId);
			expect(sizes.length).toBeGreaterThan(0);
			for (const size of sizes) {
				expect(size, `${panelId} size ${String(size)} must be a percentage string`).toMatch(/^\d+(\.\d+)?%$/);
			}
		}
	});

	it("marks the inspector collapsible and renders the resize handle", () => {
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-collapsible", "true");
		expect(screen.getByTestId("resize-handle")).toBeInTheDocument();
		expect(screen.getByTestId("panel-inspector")).not.toHaveAttribute("inert");
	});

	it("mounts collapsed and inert when the store says closed", () => {
		useUiStore.setState({ isInspectorOpen: false });
		render(<SessionView sessionId="sess-1" />);

		expect(panelSizes("inspector")[0]).toBe("0%");
		const pane = screen.getByTestId("panel-inspector");
		expect(pane).toHaveAttribute("inert");
		expect(pane).toHaveAttribute("aria-hidden", "true");
		expect(panels.get("inspector")!.handle.collapse).toHaveBeenCalled();
	});

	it("toggles the inspector with mod+shift+B through the imperative panel API", () => {
		render(<SessionView sessionId="sess-1" />);
		const handle = panels.get("inspector")!.handle;

		fireEvent.keyDown(window, { key: "B", metaKey: true, shiftKey: true });
		expect(useUiStore.getState().isInspectorOpen).toBe(false);
		expect(handle.collapse).toHaveBeenCalledTimes(1);

		fireEvent.keyDown(window, { key: "B", ctrlKey: true, shiftKey: true });
		expect(useUiStore.getState().isInspectorOpen).toBe(true);
		expect(handle.expand).toHaveBeenCalled();

		// Plain ⌘B belongs to the sidebar — the inspector must not react.
		fireEvent.keyDown(window, { key: "b", metaKey: true });
		expect(useUiStore.getState().isInspectorOpen).toBe(true);
	});

	it("syncs drag resizes back into the store and persists the split", () => {
		render(<SessionView sessionId="sess-1" />);
		const entry = panels.get("inspector")!;
		// rrp marks the separator active for the duration of a pointer drag.
		screen.getByTestId("resize-handle").setAttribute("data-separator", "active");

		// Dragging past minSize collapses the panel → store follows.
		act(() => entry.onResize?.({ asPercentage: 0, inPixels: 0 }));
		expect(useUiStore.getState().isInspectorOpen).toBe(false);

		// Dragging it back open reopens + persists the width.
		act(() => entry.onResize?.({ asPercentage: 31.5, inPixels: 400 }));
		expect(useUiStore.getState().isInspectorOpen).toBe(true);
		expect(window.localStorage.getItem("ao.inspector.split")).toBe("31.5");
	});

	// Regression: rrp v4 reports observed DOM sizes, so the flex-grow
	// transition animating an imperative collapse fires onResize with transient
	// non-zero sizes. Mirroring those into the store re-opened the panel
	// mid-animation — the topbar toggle looked dead and a mount-time 0-size
	// event flipped a fresh profile to collapsed. Only drag events (separator
	// active) may write back.
	it("ignores onResize churn while the separator is not being dragged", () => {
		render(<SessionView sessionId="sess-1" />);
		const entry = panels.get("inspector")!;

		// Mount-time/layout event at 0% must not collapse the store…
		act(() => entry.onResize?.({ asPercentage: 0, inPixels: 0 }));
		expect(useUiStore.getState().isInspectorOpen).toBe(true);

		// …and a mid-collapse transition frame must not re-open or persist.
		act(() => useUiStore.getState().toggleInspector());
		act(() => entry.onResize?.({ asPercentage: 12.4, inPixels: 160 }));
		expect(useUiStore.getState().isInspectorOpen).toBe(false);
		expect(window.localStorage.getItem("ao.inspector.split")).toBeNull();
	});

	it("restores the persisted split width", () => {
		window.localStorage.setItem("ao.inspector.split", "40");
		render(<SessionView sessionId="sess-1" />);
		expect(panelSizes("inspector")[0]).toBe("40%");
	});

	// Regression: rrp only derives a panel's constraints one commit after it
	// registers into a live group. Driving the imperative API in the commit
	// where the inspector mounts (orchestrator → worker navigation; SessionView
	// itself stays mounted) threw "Panel constraints not found for Panel
	// inspector" and unwound the route to the error boundary. The panel must
	// mount already in sync via defaultSize instead.
	it("mounts the inspector in sync when navigating from an orchestrator session, without the imperative API", () => {
		useUiStore.setState({ isInspectorOpen: false });
		const { rerender } = render(<SessionView sessionId="sess-orch" />);
		expect(screen.queryByTestId("panel-inspector")).not.toBeInTheDocument();

		// Toggled open while on the orchestrator (shell topbar button) — the
		// panel that mounts later must pick this up from defaultSize alone.
		act(() => useUiStore.getState().toggleInspector());
		rerender(<SessionView sessionId="sess-1" />);

		expect(panelSizes("inspector")[0]).toMatch(/^[1-9]\d*(\.\d+)?%$/);
		const handle = panels.get("inspector")!.handle;
		expect(handle.expand).not.toHaveBeenCalled();
		expect(handle.collapse).not.toHaveBeenCalled();
		expect(handle.resize).not.toHaveBeenCalled();
	});

	it("renders no inspector panel or handle for orchestrator sessions", () => {
		render(<SessionView sessionId="sess-orch" />);

		expect(screen.queryByTestId("panel-inspector")).not.toBeInTheDocument();
		expect(screen.queryByTestId("resize-handle")).not.toBeInTheDocument();

		// The shortcut is inactive without an inspector.
		fireEvent.keyDown(window, { key: "B", metaKey: true, shiftKey: true });
		expect(useUiStore.getState().isInspectorOpen).toBe(true);
	});

	it("maximizes the browser over the whole app window and returns to the rail", () => {
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByText("terminal center")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "pop browser" }));

		// The maximized overlay appears; the terminal stays mounted behind it.
		expect(screen.getByRole("button", { name: "browser center" })).toBeInTheDocument();
		expect(screen.getByText("terminal center")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "browser center" }));
		expect(screen.queryByRole("button", { name: "browser center" })).not.toBeInTheDocument();
		expect(screen.getByText("terminal center")).toBeInTheDocument();
		expect(browserDestroy).not.toHaveBeenCalled();
	});

	// Regression: selecting/opening a session that already carries a preview URL
	// (a past `ao preview`, streamed on load) must NOT jump the rail to the
	// Browser tab. Only a live `ao preview` while the session is open may.
	it("does not auto-open the Browser tab when selecting a session that already has a preview URL", () => {
		const worker = workspaces[0].sessions[0];
		worker.previewUrl = "http://localhost:5173/";
		worker.previewRevision = 2;
		try {
			useUiStore.setState({ isInspectorOpen: false });
			render(<SessionView sessionId="sess-1" />);

			// The rail stays collapsed on its default (Summary) tab — no auto-switch.
			// (Closed inspector is aria-hidden/inert, so include hidden in the query.)
			expect(useUiStore.getState().isInspectorOpen).toBe(false);
			expect(screen.getByRole("button", { name: "pop browser", hidden: true })).toHaveAttribute("data-view", "summary");
		} finally {
			delete worker.previewUrl;
			delete worker.previewRevision;
		}
	});

	it("reveals the Browser tab when `ao preview` fires live on the open session, not the center pane", () => {
		const worker = workspaces[0].sessions[0];
		worker.previewUrl = undefined;
		worker.previewRevision = 1;
		try {
			useUiStore.setState({ isInspectorOpen: false });
			const { rerender } = render(<SessionView sessionId="sess-1" />);

			// Baseline: the freshly opened session has no live preview yet.
			// (Closed inspector is aria-hidden/inert, so include hidden in the query.)
			expect(useUiStore.getState().isInspectorOpen).toBe(false);
			expect(screen.getByRole("button", { name: "pop browser", hidden: true })).toHaveAttribute("data-view", "summary");

			// `ao preview <url>` streams in: previewUrl set + revision bumped.
			worker.previewUrl = "http://localhost:5173/";
			worker.previewRevision = 2;
			rerender(<SessionView sessionId="sess-1" />);

			// Center pane keeps the terminal — the preview must not pop out over it.
			expect(screen.getByText("terminal center")).toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "browser center" })).not.toBeInTheDocument();
			// Rail opened and switched to the Browser tab.
			expect(useUiStore.getState().isInspectorOpen).toBe(true);
			expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-view", "browser");
		} finally {
			delete worker.previewUrl;
			delete worker.previewRevision;
		}
	});

	// The project's web-UI fact rides the project row, and the rail needs it to
	// decide whether to offer a Browser tab at all.
	it("passes the project's web-UI fact down to the inspector", () => {
		render(<SessionView sessionId="sess-1" />);
		expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-has-web-ui", "true");

		workspaces[0].hasWebUI = false;
		try {
			cleanup();
			render(<SessionView sessionId="sess-1" />);
			expect(screen.getByRole("button", { name: "pop browser" })).toHaveAttribute("data-has-web-ui", "false");
		} finally {
			workspaces[0].hasWebUI = true;
		}
	});

	// A session can still hold a previewUrl from before its project switched the
	// web UI off (the target is left in place, not destroyed). The reveal effect
	// selects the Browser tab — a tab that no longer exists — and force-opens the
	// rail, so it must not fire.
	it("does not reveal the Browser tab when the project has no web UI", () => {
		const worker = workspaces[0].sessions[0];
		workspaces[0].hasWebUI = false;
		worker.previewUrl = undefined;
		worker.previewRevision = 1;
		try {
			useUiStore.setState({ isInspectorOpen: false });
			const { rerender } = render(<SessionView sessionId="sess-1" />);

			worker.previewUrl = "http://localhost:5173/";
			worker.previewRevision = 2;
			rerender(<SessionView sessionId="sess-1" />);

			expect(useUiStore.getState().isInspectorOpen).toBe(false);
			expect(screen.getByRole("button", { name: "pop browser", hidden: true })).toHaveAttribute("data-view", "summary");
		} finally {
			workspaces[0].hasWebUI = true;
			delete worker.previewUrl;
			delete worker.previewRevision;
		}
	});

	it("wakes the session on open, then refetches the workspace so the reset/resume shows", async () => {
		await act(async () => {
			render(<SessionView sessionId="sess-1" />);
		});
		expect(wakeMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/wake", {
			params: { path: { sessionId: "sess-1" } },
		});
		await waitFor(() => expect(invalidateMock).toHaveBeenCalledWith({ queryKey: ["workspaces"] }));
	});

	it("re-wakes when the user selects a different session", async () => {
		let rerender!: (ui: React.ReactElement) => void;
		await act(async () => {
			({ rerender } = render(<SessionView sessionId="sess-1" />));
		});
		wakeMock.mockClear();
		await act(async () => {
			rerender(<SessionView sessionId="sess-orch" />);
		});
		expect(wakeMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/wake", {
			params: { path: { sessionId: "sess-orch" } },
		});
	});

	it("skips the refetch when the wake call fails (benign no-op)", async () => {
		wakeMock.mockResolvedValue({ error: { code: "SESSION_NOT_FOUND", message: "gone" } });
		await act(async () => {
			render(<SessionView sessionId="sess-1" />);
		});
		expect(wakeMock).toHaveBeenCalled();
		expect(invalidateMock).not.toHaveBeenCalled();
	});

	// A not-started TODO has no worktree/tmux, so the terminal would sit forever
	// on "Preparing the worker terminal". The center pane must show the editable
	// spec instead — while keeping the inspector rail sensible for a TODO.
	it("renders the editable TODO spec instead of the terminal for a not-started TODO", () => {
		render(<SessionView sessionId="sess-todo" />);

		expect(screen.getByText("todo editor for sess-todo")).toBeInTheDocument();
		expect(screen.queryByText("terminal center")).not.toBeInTheDocument();
		// The Summary/inspector rail still renders for a TODO session.
		expect(screen.getByTestId("panel-inspector")).toBeInTheDocument();
	});

	it("renders the terminal, not the TODO editor, for a started session", () => {
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByText("terminal center")).toBeInTheDocument();
		expect(screen.queryByText(/todo editor/)).not.toBeInTheDocument();
	});
});

// --- routing a clicked terminal file reference --------------------------------
//
// This is the whole of Feature 1's decision: a reference INSIDE the project
// reveals in the Files tab; anything else keeps today's standalone viewer,
// untouched. Getting it wrong in the permissive direction hijacks the rail on a
// file the tab cannot even show.
describe("SessionView terminal file reference routing", () => {
	const clickOpen = async () => {
		await act(async () => {
			fireEvent.click(screen.getByText("open workspace file"));
		});
	};

	it("reveals the Files tab for an in-project file that the tab can show", async () => {
		changesData.current = { files: [{ path: "src/a.ts" }] };
		openFileArg.current = { path: "src/a.ts", line: 12, inWorkspace: true };
		render(<SessionView sessionId="sess-1" />);

		await clickOpen();
		await waitFor(() => expect(screen.getByText("pop browser")).toHaveAttribute("data-view", "files"));
	});

	// The verdict comes from the server; a path that merely LOOKS relative is not
	// enough, and a file outside the project must not touch the rail at all.
	it("leaves the rail alone for a file outside the project", async () => {
		changesData.current = { files: [{ path: "src/a.ts" }] };
		openFileArg.current = { path: "/etc/hosts", inWorkspace: false };
		render(<SessionView sessionId="sess-1" />);

		await clickOpen();
		expect(screen.getByText("pop browser")).toHaveAttribute("data-view", "summary");
	});

	// The Files tab is CHANGES-only. An in-project file that does not differ from
	// the target branch has no row, so switching to the tab would strand the user
	// on a list that does not contain what they clicked.
	it("leaves the rail alone for an in-project file with no row in the tab", async () => {
		changesData.current = { files: [{ path: "src/other.ts" }] };
		openFileArg.current = { path: "src/unchanged.ts", inWorkspace: true };
		render(<SessionView sessionId="sess-1" />);

		await clickOpen();
		expect(screen.getByText("pop browser")).toHaveAttribute("data-view", "summary");
	});
});

// Precedence: the SERVER's verdict wins over a path that merely matches a row.
// In today's data the two can't disagree — an out-of-workspace candidate comes
// back absolute and the changed list is workspace-relative, so membership alone
// would happen to be sufficient. This pins the rule anyway, because "it happens
// to line up" is not the contract: inWorkspace is the confinement decision, and
// a row match is only about whether the tab can display it.
describe("SessionView reveal precedence", () => {
	it("refuses to reveal a file the server says is outside, even if a row matches", async () => {
		changesData.current = { files: [{ path: "src/a.ts" }] };
		openFileArg.current = { path: "src/a.ts", inWorkspace: false };
		render(<SessionView sessionId="sess-1" />);

		await act(async () => {
			fireEvent.click(screen.getByText("open workspace file"));
		});
		expect(screen.getByText("pop browser")).toHaveAttribute("data-view", "summary");
	});
});

describe("SessionView split view", () => {
	const removeLabel = "Remove from split (session keeps running)";
	const splitEntryLabel = "Split — watch another running session";

	function hsplit(first: SplitNode, second: SplitNode): SplitNode {
		return { kind: "split", orientation: "horizontal", ratio: 0.5, first, second };
	}

	function chainLayout(ids: readonly string[]): SplitNode {
		let root: SplitNode = leaf(ids[0]);
		for (let i = 1; i < ids.length; i += 1) {
			root = splitPane(root, ids[i - 1], "right", ids[i]);
		}
		return root;
	}

	function storedLayout(): SplitNode | undefined {
		return useUiStore.getState().splitLayouts["proj-1"];
	}

	function wakeCallSessionIds(): string[] {
		return wakeMock.mock.calls.map(
			(call) => (call[1] as { params?: { path?: { sessionId?: string } } } | undefined)?.params?.path?.sessionId ?? "",
		);
	}

	it("renders one pane per layout leaf, marks the focused pane, and wakes every pane session", () => {
		useUiStore.getState().setSplitLayout("proj-1", hsplit(leaf("sess-1"), leaf("sess-2")));
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("center-sess-1")).toHaveAttribute("data-pane-focused", "true");
		expect(screen.getByTestId("center-sess-2")).toHaveAttribute("data-pane-focused", "false");
		expect(wakeCallSessionIds()).toContain("sess-2");
		// The split view's only daemon traffic is wake — nothing else is touched.
		for (const call of wakeMock.mock.calls) {
			expect(call[0]).toBe("/api/v1/sessions/{sessionId}/wake");
		}
	});

	it("removing a pane updates the layout only — no daemon call, no navigation, session untouched", () => {
		useUiStore.getState().setSplitLayout("proj-1", hsplit(leaf("sess-1"), leaf("sess-2")));
		render(<SessionView sessionId="sess-1" />);
		wakeMock.mockClear();

		fireEvent.click(within(screen.getByTestId("center-sess-2")).getByLabelText(removeLabel));

		expect(storedLayout()).toBeUndefined();
		expect(screen.queryByTestId("center-sess-2")).not.toBeInTheDocument();
		expect(wakeMock).not.toHaveBeenCalled();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("removing the FOCUSED pane hands focus to the first remaining pane", () => {
		useUiStore.getState().setSplitLayout("proj-1", hsplit(leaf("sess-1"), hsplit(leaf("sess-2"), leaf("sess-3"))));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(within(screen.getByTestId("center-sess-1")).getByLabelText(removeLabel));

		expect(navigateMock).toHaveBeenCalledWith(
			expect.objectContaining({ params: { projectId: "proj-1", sessionId: "sess-2" }, replace: true }),
		);
		expect(paneSessionIds(storedLayout()!)).toEqual(["sess-2", "sess-3"]);
	});

	it("navigating to a session outside the layout adds it as a pane beside the focused one", () => {
		useUiStore.getState().setSplitLayout("proj-1", hsplit(leaf("sess-1"), leaf("sess-2")));
		const { rerender } = render(<SessionView sessionId="sess-1" />);

		rerender(<SessionView sessionId="sess-3" />);

		expect(paneSessionIds(storedLayout()!)).toEqual(["sess-1", "sess-3", "sess-2"]);
	});

	it("at the pane cap, navigation swaps into the focused pane and announces it", () => {
		const ids = Array.from({ length: 10 }, (_, i) => `sess-x${i}`);
		useUiStore.getState().setSplitLayout("proj-1", chainLayout(ids));
		const { rerender } = render(<SessionView sessionId="sess-x0" />);

		rerender(<SessionView sessionId="sess-1" />);

		const after = paneSessionIds(storedLayout()!);
		expect(after).toHaveLength(10);
		expect(after).toContain("sess-1");
		expect(after).not.toContain("sess-x0");
		expect(screen.getByText(/Split view is full/)).toBeInTheDocument();
	});

	it("prunes layout sessions that no longer exist, keeping the rest", async () => {
		useUiStore.getState().setSplitLayout("proj-1", hsplit(leaf("sess-1"), hsplit(leaf("ghost"), leaf("sess-2"))));
		render(<SessionView sessionId="sess-1" />);

		await waitFor(() => expect(paneSessionIds(storedLayout()!)).toEqual(["sess-1", "sess-2"]));
	});

	it("drops the layout entirely when pruning leaves a single pane", async () => {
		useUiStore.getState().setSplitLayout("proj-1", hsplit(leaf("sess-1"), leaf("ghost")));
		render(<SessionView sessionId="sess-1" />);

		await waitFor(() => expect(storedLayout()).toBeUndefined());
		expect(screen.getByTestId("center-sess-1")).not.toHaveAttribute("data-pane-focused");
	});

	it("single view stays single: one pane, the split entry, no remove control", () => {
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("center-sess-1")).not.toHaveAttribute("data-pane-focused");
		expect(screen.queryByTestId("center-sess-2")).not.toBeInTheDocument();
		expect(screen.getByLabelText(splitEntryLabel)).toBeInTheDocument();
		expect(screen.queryByLabelText(removeLabel)).not.toBeInTheDocument();
	});

	it("splitting from the single view creates the layout and wakes the added session", async () => {
		render(<SessionView sessionId="sess-1" />);
		wakeMock.mockClear();

		fireEvent.click(screen.getByLabelText(splitEntryLabel));
		fireEvent.click(await screen.findByText("second thing"));

		expect(paneSessionIds(storedLayout()!)).toEqual(["sess-1", "sess-2"]);
		expect(wakeCallSessionIds()).toContain("sess-2");
	});

	it("offers only eligible sessions in the picker: no todos, no on-screen sessions", async () => {
		useUiStore.getState().setSplitLayout("proj-1", hsplit(leaf("sess-1"), leaf("sess-2")));
		render(<SessionView sessionId="sess-1" />);

		fireEvent.click(within(screen.getByTestId("center-sess-1")).getByLabelText(splitEntryLabel));
		await screen.findByText("third thing");

		expect(screen.getByText("Orchestrator")).toBeInTheDocument();
		expect(screen.queryByText("queued-task")).not.toBeInTheDocument();
		// On-screen sessions are structurally absent from the list: "second
		// thing" (sess-2) appears nowhere in the picker because its only other
		// occurrence is its own pane toolbar, which the CenterPane stub elides.
		expect(screen.queryByText("second thing")).not.toBeInTheDocument();
	});
});

describe("SessionView unsplit", () => {
	it("keeps the pane whose picker asked, not the focused one", async () => {
		useUiStore
			.getState()
			.setSplitLayout("proj-1", { kind: "split", orientation: "horizontal", ratio: 0.5, first: leaf("sess-1"), second: leaf("sess-2") });
		render(<SessionView sessionId="sess-1" />);

		// Open the UNFOCUSED pane's picker (controls act without moving focus).
		fireEvent.click(
			within(screen.getByTestId("center-sess-2")).getByLabelText("Split — watch another running session"),
		);
		fireEvent.click(await screen.findByText(/Unsplit — keep only this pane/));

		expect(navigateMock).toHaveBeenCalledWith(
			expect.objectContaining({ params: { projectId: "proj-1", sessionId: "sess-2" }, replace: true }),
		);
		expect(useUiStore.getState().splitLayouts["proj-1"]).toBeUndefined();
	});
});
