import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";

const { navigateMock, workspaceQueryMock, deleteMock, postMock } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
	deleteMock: vi.fn(),
	postMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: workspaceQueryMock,
	workspaceQueryKey: ["workspaces"],
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { DELETE: deleteMock, POST: postMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		if (error && typeof error === "object" && "message" in error)
			return String((error as { message?: unknown }).message);
		return fallback;
	},
}));

import { SessionsBoard } from "./SessionsBoard";

function doneSession(id: string): WorkspaceSession {
	return {
		id,
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: `finished ${id}`,
		provider: "claude-code",
		kind: "worker",
		branch: `ao/${id}`,
		status: "terminated",
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
	};
}

function activeSession(id: string, status: WorkspaceSession["status"] = "working"): WorkspaceSession {
	return {
		id,
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: `active ${id}`,
		provider: "claude-code",
		kind: "worker",
		branch: `ao/${id}`,
		status,
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
	};
}

function renderBoard() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return render(
		<QueryClientProvider client={queryClient}>
			<SessionsBoard />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	navigateMock.mockReset();
	deleteMock.mockReset();
	postMock.mockReset();
	workspaceQueryMock.mockReset().mockReturnValue({ data: [], isError: false });
});

describe("SessionsBoard", () => {
	it("does not show an agent setup warning on the board", () => {
		renderBoard();

		expect(screen.queryByText(/reload agents/i)).not.toBeInTheDocument();
	});

	it("deletes a done session after confirm", async () => {
		deleteMock.mockResolvedValue({ error: undefined });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [doneSession("sess-1")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Delete session" }));
		expect(deleteMock).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));

		await waitFor(() =>
			expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId: "sess-1" }, query: { force: false } },
			}),
		);
	});

	it("reopens a done session by restoring it", async () => {
		postMock.mockResolvedValue({ error: undefined });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [doneSession("sess-1")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Reopen session" }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/restore", {
				params: { path: { sessionId: "sess-1" } },
			}),
		);
	});

	it("treats an already-active merged session as reopened without surfacing an error", async () => {
		// A merged session still live on disk is not terminated, so restore is a no-op
		// (SESSION_NOT_RESTORABLE); the daemon auto-claims the newer PR behind the
		// scenes, so the chip must not show a failure.
		postMock.mockResolvedValue({ error: { code: "SESSION_NOT_RESTORABLE", message: "Session is not restorable" } });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...doneSession("m1"), status: "merged" }] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Reopen session" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(screen.queryByText(/not restorable/i)).not.toBeInTheDocument();
		expect(screen.queryByText(/Reopen failed/i)).not.toBeInTheDocument();
	});

	it("surfaces a reopen failure without offering to delete the session", async () => {
		// A restore that genuinely fails (e.g. INTERNAL_ERROR) must show a reopen
		// error on its own — never delete's inline "Delete anyway", which would let a
		// mis-click permanently delete a session the user only tried to reopen.
		postMock.mockResolvedValue({ error: { code: "INTERNAL_ERROR", message: "Internal server error" } });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [doneSession("sess-1")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Reopen session" }));

		await waitFor(() => expect(screen.getByText(/Couldn.t reopen/i)).toBeInTheDocument());
		expect(screen.queryByRole("button", { name: "Delete anyway" })).not.toBeInTheDocument();
	});

	it("shows no Reopen action once a session leaves the done bucket", () => {
		// After reopen, restore + auto-claim flip the session to an active status; it
		// then renders in a column, not the done bar, so its Reopen chip disappears.
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...doneSession("sess-1"), status: "pr_open" }] }],
			isError: false,
		});
		renderBoard();

		expect(screen.queryByText(/Done \/ Terminated/i)).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Reopen session" })).not.toBeInTheDocument();
	});

	it("moves an active session to Done via the card menu, arming a confirm before killing", async () => {
		postMock.mockResolvedValue({ data: { ok: true, sessionId: "sess-9", freed: true }, error: undefined });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-9")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: "Session actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Move to Done/i }));
		// Arm-confirm: the first click only arms — nothing is terminated yet.
		expect(postMock).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("menuitem", { name: /Confirm/i }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: "sess-9" } },
			}),
		);
	});

	it("keeps you on the board — interacting with the card menu never opens the session", async () => {
		// The menu content is portaled, but React events still bubble along the React
		// tree (the menu lives inside the card's open-on-click wrapper). Without a
		// propagation stop, choosing a menu item would also navigate into the session.
		postMock.mockResolvedValue({ data: { ok: true, sessionId: "sess-9", freed: true }, error: undefined });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-9")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: "Session actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Move to Done/i }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Confirm/i }));

		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("surfaces a Move to Done failure inside the menu instead of silently dropping it", async () => {
		postMock.mockResolvedValue({ error: { code: "INTERNAL_ERROR", message: "runtime destroy failed" } });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-9")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: "Session actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Move to Done/i }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Confirm/i }));

		await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("runtime destroy failed"));
	});

	it("does not terminate when the Move to Done confirm is cancelled", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-9")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: "Session actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Move to Done/i }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Cancel/i }));

		expect(postMock).not.toHaveBeenCalled();
	});

	it("clears all done sessions", async () => {
		deleteMock.mockResolvedValue({ error: undefined });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [doneSession("s1"), doneSession("s2")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Clear all" }));
		await userEvent.click(screen.getByRole("button", { name: "Delete all" }));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledTimes(2));
	});

	it("renders a suspended session in its real lane (not the Done bar) with a paused affordance", () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...activeSession("sess-9", "needs_input"), isSuspended: true }] }],
			isError: false,
		});
		renderBoard();

		// The card renders directly in its lane; the Done bar is collapsed, so a
		// visible card title (no expansion click) proves it did NOT archive.
		expect(screen.getByText("active sess-9")).toBeInTheDocument();
		expect(screen.getByText("Paused")).toBeInTheDocument();
		// A non-terminated suspended session produces no done sessions at all, so the
		// Done/Terminated bar is absent — the card cannot be hiding there.
		expect(screen.queryByRole("button", { name: /Done \/ Terminated/i })).not.toBeInTheDocument();
	});

	// A crew member asleep for TURN reasons is not "paused to free resources", and
	// opening its card will not bring it back - the daemon leaves it asleep on
	// purpose. So the chip must not promise that it will.
	it("says a turn-asleep crew member is asleep, not paused-open-to-resume", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "proj-1",
					sessions: [{ ...activeSession("sess-9", "needs_input"), isSuspended: true, sleepReason: "turn" }],
				},
			],
			isError: false,
		});
		renderBoard();

		expect(screen.getByText("Asleep")).toBeInTheDocument();
		expect(screen.queryByText("Paused")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Asleep")).toHaveAttribute("title", expect.stringContaining("turn"));
	});

	it("shows an escalating idle countdown when a live session nears suspension", () => {
		const soon = new Date(Date.now() + 40 * 60_000).toISOString(); // 40m out
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...activeSession("sess-9", "needs_input"), idleCloseAt: soon }] }],
			isError: false,
		});
		renderBoard();

		expect(screen.getByLabelText(/^Auto-suspends in/)).toBeInTheDocument();
	});

	it("hides the countdown for a session far from suspension", () => {
		const far = new Date(Date.now() + 60 * 60 * 60_000).toISOString(); // 60h out
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...activeSession("sess-9", "needs_input"), idleCloseAt: far }] }],
			isError: false,
		});
		renderBoard();

		expect(screen.queryByLabelText(/^Auto-suspends in/)).not.toBeInTheDocument();
	});
});

// The board used to say "which status is this?" with a 3px coloured left edge on
// each card (plus a coloured rail and a hue wash on the column). Those are
// retired: a card now leads with a status glyph whose SHAPE is the status, and
// the status word beside it in text. These tests pin the replacement — the fact
// must survive with colour removed, which is exactly what the bar could not do.
describe("SessionsBoard status conveyance", () => {
	// status → the words a human must be able to read off the card.
	const STATUS_TEXT: [WorkspaceSession["status"], string][] = [
		["working", "Working"],
		["idle", "Working"],
		["needs_input", "Input needed"],
		["no_signal", "No signal"],
		["ci_failed", "CI failed"],
		["changes_requested", "Changes requested"],
		["review_pending", "Review pending"],
		["pr_open", "PR open"],
		["draft", "Draft PR"],
		["approved", "Approved"],
		["mergeable", "Ready"],
	];

	it.each(STATUS_TEXT)("states %s as readable text on the card, not only as a colour", (status, text) => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-1", status)] }],
			isError: false,
		});
		renderBoard();

		// Scoped to the card, so a column header that happens to share the word
		// (WORKING) cannot stand in for the card stating its own status.
		const card = screen.getByText("active sess-1").closest("div.group");
		expect(card).not.toBeNull();
		expect(within(card as HTMLElement).getByText(text)).toBeInTheDocument();
	});

	it("keeps the four NEEDS YOU statuses apart instead of collapsing them into one lane colour", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "proj-1",
					sessions: [
						activeSession("sess-1", "needs_input"),
						activeSession("sess-2", "no_signal"),
						activeSession("sess-3", "ci_failed"),
						activeSession("sess-4", "changes_requested"),
					],
				},
			],
			isError: false,
		});
		renderBoard();

		for (const text of ["Input needed", "No signal", "CI failed", "Changes requested"]) {
			expect(screen.getByText(text), text).toBeInTheDocument();
		}
	});

	it("paints no coloured edge on a card or a column", () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-1", "needs_input")] }],
			isError: false,
		});
		const { container } = renderBoard();

		// The retired bars were inline styles, so their absence is checkable here.
		// (Whether the NEW glyph is actually visible is a paint question jsdom
		// cannot answer — that is verified in a real browser, not here.)
		for (const el of container.querySelectorAll<HTMLElement>("[style]")) {
			expect(el.style.borderLeftWidth, el.className).not.toBe("3px");
			expect(el.style.borderTopWidth, el.className).not.toBe("3px");
			expect(el.style.borderLeft).toBe("");
			expect(el.style.borderTop).not.toMatch(/lane-/);
		}
	});
});
