import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { TerminalEndedStrip, TerminalPane, providerScrollsByKeyboard, terminalEndedMessage } from "./TerminalPane";

const { navigateMock, terminalStateMock } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	// Mutable so a test can drive the terminal into its "exited" state (which
	// surfaces the ended-terminal strip) without re-mocking the module.
	terminalStateMock: { value: "idle" as string },
}));

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return { ...actual, useNavigate: () => navigateMock };
});

vi.mock("./XtermTerminal", () => ({
	XtermTerminal: () => <div data-testid="xterm" />,
}));

vi.mock("../hooks/useTerminalSession", () => ({
	useTerminalSession: () => ({
		attach: vi.fn(),
		state: terminalStateMock.value,
		error: undefined,
	}),
}));

beforeEach(() => {
	terminalStateMock.value = "idle";
});

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
	title: "orchestrate",
	kind: "orchestrator",
} satisfies WorkspaceSession;

function renderPane(session?: WorkspaceSession) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const previousAO = window.ao;
	window.ao = {} as typeof window.ao;
	const result = render(
		<QueryClientProvider client={queryClient}>
			<TerminalPane daemonReady fontSize={12} session={session} theme="dark" />
		</QueryClientProvider>,
	);
	return {
		...result,
		restore: () => {
			window.ao = previousAO;
		},
	};
}

describe("TerminalPane empty states", () => {
	it("shows a no-selection message when no session is selected", () => {
		const view = renderPane();
		try {
			expect(screen.getByText("Agent Orchestrator")).toBeInTheDocument();
			expect(screen.getByText("No session selected. Pick a worker to attach its terminal.")).toBeInTheDocument();
		} finally {
			view.restore();
		}
	});

	it("shows a startup message when a selected session has no terminal handle yet", () => {
		const view = renderPane(worker);
		try {
			expect(screen.getByText("Starting session")).toBeInTheDocument();
			expect(
				screen.getByText(
					"Preparing the worker terminal. This can take a moment while AO creates the worktree and starts the agent.",
				),
			).toBeInTheDocument();
			expect(screen.queryByText("No session selected. Pick a worker to attach its terminal.")).not.toBeInTheDocument();
		} finally {
			view.restore();
		}
	});

	it("shows orchestrator-specific startup copy for a pending orchestrator terminal", () => {
		const view = renderPane(orchestrator);
		try {
			expect(screen.getByText("Starting session")).toBeInTheDocument();
			expect(
				screen.getByText(
					"Preparing the orchestrator terminal. This can take a moment while AO creates the worktree and starts the agent.",
				),
			).toBeInTheDocument();
			expect(screen.queryByText(/worker terminal/i)).not.toBeInTheDocument();
		} finally {
			view.restore();
		}
	});
});

describe("providerScrollsByKeyboard", () => {
	// opencode and its fork kilocode share a TUI that scrolls its own transcript
	// by keyboard and ignores SGR wheel reports, so both must opt into the
	// PageUp/PageDown wheel routing (see XtermTerminal's paneScrollsByKeyboard).
	it("is true for keyboard-scroll TUIs (opencode and its kilocode fork)", () => {
		expect(providerScrollsByKeyboard("opencode")).toBe(true);
		expect(providerScrollsByKeyboard("kilocode")).toBe(true);
	});

	it("is false for mouse-report/native-scroll providers", () => {
		expect(providerScrollsByKeyboard("codex")).toBe(false);
		expect(providerScrollsByKeyboard("claude-code")).toBe(false);
	});

	it("is false when the provider is unknown", () => {
		expect(providerScrollsByKeyboard(undefined)).toBe(false);
	});
});

const RESTORE_COPY = "Restore the session to attach a live terminal and continue writing.";
const MERGED_COPY = "This session is done (PR merged). Restore it to attach a live terminal and continue.";
const NOT_TERMINATED_COPY = "This terminal process ended, but the session is not marked terminated yet.";
const REVIEWER_COPY =
	"This reviewer terminal has ended. Re-run review from the summary panel, or switch back to the agent terminal.";

describe("terminalEndedMessage", () => {
	it("offers a plain restore for a terminated (non-merged) session", () => {
		expect(terminalEndedMessage({ canRestore: true, status: "terminated", variant: "session" })).toBe(RESTORE_COPY);
	});

	it("offers Done-specific copy when the session's PR merged", () => {
		expect(terminalEndedMessage({ canRestore: true, status: "merged", variant: "session" })).toBe(MERGED_COPY);
	});

	it("shows the not-yet-terminated dead-end only when restore is unavailable", () => {
		expect(terminalEndedMessage({ canRestore: false, status: "working", variant: "session" })).toBe(
			NOT_TERMINATED_COPY,
		);
	});

	it("always shows the reviewer message on the reviewer terminal", () => {
		expect(terminalEndedMessage({ canRestore: false, status: "idle", variant: "reviewer" })).toBe(REVIEWER_COPY);
	});
});

describe("TerminalEndedStrip", () => {
	it("shows a Restore button + Done copy for a merged/done session", () => {
		render(
			<TerminalEndedStrip canRestore isRestoring={false} onRestore={() => {}} status="merged" variant="session" />,
		);
		expect(screen.getByRole("button", { name: "Restore session" })).toBeInTheDocument();
		expect(screen.getByText(MERGED_COPY)).toBeInTheDocument();
	});

	it("hides the Restore button and shows the dead-end when restore is unavailable", () => {
		render(<TerminalEndedStrip canRestore={false} isRestoring={false} onRestore={() => {}} variant="session" />);
		expect(screen.queryByRole("button", { name: "Restore session" })).not.toBeInTheDocument();
		expect(screen.getByText(NOT_TERMINATED_COPY)).toBeInTheDocument();
	});
});

// The bug: a Done/merged session is `isTerminated` in the store but derives the
// display status "merged" (not "terminated"), so gating the Restore affordance on
// status alone hid it. Gating on `isTerminated` restores it for BOTH cases.
describe("TerminalPane ended-terminal restore gate", () => {
	function renderExited(session: WorkspaceSession) {
		terminalStateMock.value = "exited";
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const previousAO = window.ao;
		window.ao = {} as typeof window.ao;
		render(
			<QueryClientProvider client={queryClient}>
				<TerminalPane daemonReady fontSize={12} session={session} theme="dark" />
			</QueryClientProvider>,
		);
		return () => {
			window.ao = previousAO;
		};
	}

	it("offers Restore on a Done/merged session (status merged, isTerminated true)", () => {
		const restore = renderExited({ ...worker, status: "merged", isTerminated: true, prs: [] });
		try {
			expect(screen.getByRole("button", { name: "Restore session" })).toBeInTheDocument();
			expect(screen.getByText(MERGED_COPY)).toBeInTheDocument();
		} finally {
			restore();
		}
	});

	it("offers Restore on a terminated (non-merged) session", () => {
		const restore = renderExited({ ...worker, status: "terminated", isTerminated: true });
		try {
			expect(screen.getByRole("button", { name: "Restore session" })).toBeInTheDocument();
			expect(screen.getByText(RESTORE_COPY)).toBeInTheDocument();
		} finally {
			restore();
		}
	});

	it("does not offer Restore when the process exited but the session is not terminated", () => {
		const restore = renderExited({ ...worker, status: "working", isTerminated: false });
		try {
			expect(screen.queryByRole("button", { name: "Restore session" })).not.toBeInTheDocument();
			expect(screen.getByText(NOT_TERMINATED_COPY)).toBeInTheDocument();
		} finally {
			restore();
		}
	});
});
