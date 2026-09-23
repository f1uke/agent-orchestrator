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

import { readFoldedBoardLanes, useUiStore } from "../stores/ui-store";
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

async function openDoneLane() {
	await userEvent.click(screen.getByRole("button", { name: /^Expand Done, / }));
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
	localStorage.clear();
	// What a first visit gets: every lane open except Done.
	useUiStore.setState({ collapsedBoardLanes: readFoldedBoardLanes() });
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

		await openDoneLane();
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

		await openDoneLane();
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

		await openDoneLane();
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

		await openDoneLane();
		await userEvent.click(screen.getByRole("button", { name: "Reopen session" }));

		await waitFor(() => expect(screen.getByText(/Couldn.t reopen/i)).toBeInTheDocument());
		expect(screen.queryByRole("button", { name: "Delete anyway" })).not.toBeInTheDocument();
	});

	it("shows no Reopen action once a session leaves the done bucket", () => {
		// After reopen, restore + auto-claim flip the session to an active status; it
		// then renders in a live lane, not the Done lane, so its Reopen action goes.
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...doneSession("sess-1"), status: "pr_open" }] }],
			isError: false,
		});
		renderBoard();

		expect(screen.getByRole("button", { name: "Expand Done, 0 cards" })).toBeInTheDocument();
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
				body: { discardUncommitted: false },
			}),
		);
	});

	// THE INCIDENT, at the gesture that caused it. "Move to Done" on a session
	// holding work nobody has seen used to close the menu, refetch, and leave the
	// card exactly where it was with nothing said. The refusal must reach the
	// human, with the files.
	it("explains why a card holding undelivered work will not move to Done", async () => {
		postMock.mockResolvedValue({
			error: {
				error: "conflict",
				code: "SESSION_HAS_UNDELIVERED_WORK",
				message: "sess-9 still holds 2 uncommitted files that no pull request carries",
				details: {
					reason: "workspace_dirty",
					files: [
						{ path: "Sources/WebViewZoom.swift", status: "modified" },
						{ path: "Sources/NewFile.swift", status: "untracked" },
					],
				},
			},
		});
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-9")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: "Session actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Move to Done/i }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Confirm/i }));

		expect(await screen.findByText(/still holds undelivered work/i)).toBeInTheDocument();
		expect(screen.getByText("Sources/WebViewZoom.swift")).toBeInTheDocument();
		expect(screen.getByText("Sources/NewFile.swift")).toBeInTheDocument();
		// And the way out is offered rather than described.
		expect(screen.getByRole("button", { name: /Discard and move to Done/i })).toBeInTheDocument();

		// The dialog is portaled but mounted inside the card's open-on-click
		// wrapper, so a click in it must not also open the session — the same trap
		// the card menu documents.
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(navigateMock).not.toHaveBeenCalled();
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

		await openDoneLane();
		await userEvent.click(screen.getByRole("button", { name: "Clear all" }));
		await userEvent.click(screen.getByRole("button", { name: "Delete all" }));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledTimes(2));
	});

	it("renders a suspended session in its real lane (not the Done lane) with a paused affordance", () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...activeSession("sess-9", "needs_input"), isSuspended: true }] }],
			isError: false,
		});
		renderBoard();

		// The card renders directly in its lane; the Done lane starts folded, so a
		// visible card title (no expansion click) proves it did NOT archive.
		expect(screen.getByText("active sess-9")).toBeInTheDocument();
		expect(screen.getByText("Paused")).toBeInTheDocument();
		// A non-terminated suspended session produces no done sessions at all, so the
		// Done lane counts none - the card cannot be hiding there.
		expect(screen.getByRole("button", { name: "Expand Done, 0 cards" })).toBeInTheDocument();
	});

	// A crew member that has NEVER RUN is not "paused to free resources", and
	// opening its card will not bring it back, because there is nothing to bring
	// back: starting an agent for the first time is a decision, so it waits for
	// Start. The chip must not promise a resume that will not happen.
	it("says a never-started crew member is not started, not paused-open-to-resume", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "proj-1",
					sessions: [
						{
							...activeSession("sess-9", "needs_input"),
							isSuspended: true,
							sleepReason: "idle",
							crew: { id: "sess-1", role: "qa", hasRun: false },
						},
					],
				},
			],
			isError: false,
		});
		renderBoard();

		expect(screen.getByText("Not started")).toBeInTheDocument();
		expect(screen.queryByText("Paused")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Not started")).toHaveAttribute("title", expect.stringContaining("Start"));
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

describe("SessionsBoard card header", () => {
	function cardOf(title: string): HTMLElement {
		const card = screen.getByText(title).closest("div.group");
		expect(card).not.toBeNull();
		return card as HTMLElement;
	}

	// Widths are a paint question jsdom cannot answer; what it can pin is which
	// line each item lives on, which is what the two old defects were about: the
	// agent dropping to a line of its own, and the wide chip riding the status line.
	it("keeps the agent on the status line and gives the undelivered chip a line of its own", () => {
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "proj-1",
					sessions: [
						{
							...activeSession("sess-1", "needs_input"),
							title: "cookie to keychain",
							isSuspended: true,
							sleepReason: "undelivered",
						},
					],
				},
			],
			isError: false,
		});
		renderBoard();

		const card = cardOf("cookie to keychain");
		const statusLine = card.querySelector("[data-status]")!.parentElement as HTMLElement;
		expect(within(statusLine).getByText("Claude")).toBeInTheDocument();
		expect(within(statusLine).queryByText("Undelivered")).toBeNull();

		const chipLine = card.querySelector<HTMLElement>("[data-card-chips]")!;
		expect(within(chipLine).getByText("Undelivered")).toBeInTheDocument();
		expect(within(chipLine).getByRole("button", { name: "Move to Done" })).toBeInTheDocument();
	});

	it("leaves the chip line empty when a card has no chips, so it takes no room", () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-1", "working")] }],
			isError: false,
		});
		renderBoard();

		const chipLine = cardOf("active sess-1").querySelector<HTMLElement>("[data-card-chips]")!;
		// `empty:hidden` only collapses a line with no children at all.
		expect(chipLine.childNodes).toHaveLength(0);
	});
});

describe("SessionsBoard lane collapse", () => {
	it("folds a lane to its glyph, count and name, remembers it, and opens it again", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-1", "needs_input")] }],
			isError: false,
		});
		const { unmount } = renderBoard();

		await userEvent.click(screen.getByRole("button", { name: "Collapse Needs you, 1 card" }));

		const expand = screen.getByRole("button", { name: "Expand Needs you, 1 card" });
		expect(expand).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("active sess-1")).toBeNull();
		expect(JSON.parse(localStorage.getItem("ao.board.collapsedLanes")!)).toEqual({ action: true, done: true });

		// Still folded on the next visit to the board.
		unmount();
		useUiStore.setState({ collapsedBoardLanes: readFoldedBoardLanes() });
		renderBoard();
		await userEvent.click(screen.getByRole("button", { name: "Expand Needs you, 1 card" }));

		expect(screen.getByText("active sess-1")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Collapse Needs you, 1 card" })).toHaveAttribute("aria-expanded", "true");
		expect(JSON.parse(localStorage.getItem("ao.board.collapsedLanes")!)).toEqual({ done: true });
	});

	it("folds from anywhere on the header strip, and from the keyboard", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [activeSession("sess-1", "needs_input")] }],
			isError: false,
		});
		renderBoard();

		// The lane's name is part of the control, not a label beside a small icon.
		await userEvent.click(screen.getByText("Needs you"));
		expect(screen.getByRole("button", { name: "Expand Needs you, 1 card" })).toBeInTheDocument();

		screen.getByRole("button", { name: "Expand Needs you, 1 card" }).focus();
		await userEvent.keyboard("{Enter}");
		const header = screen.getByRole("button", { name: "Collapse Needs you, 1 card" });
		header.focus();
		await userEvent.keyboard(" ");
		expect(screen.getByRole("button", { name: "Expand Needs you, 1 card" })).toBeInTheDocument();
	});
});

function finished(id: string, overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return { ...doneSession(id), ...overrides };
}

describe("SessionsBoard Done lane", () => {
	const ended = (at: string) => ({ source: "ao" as const, reason: "kill", at });

	function withDone(...sessions: WorkspaceSession[]) {
		workspaceQueryMock.mockReturnValue({ data: [{ id: "proj-1", sessions }], isError: false });
	}

	function doneTitles() {
		const lane = document.querySelector<HTMLElement>('[data-lane="done"]')!;
		return [...lane.querySelectorAll("[data-done-card]")].map(
			(card) => within(card as HTMLElement).getAllByRole("button")[0].textContent,
		);
	}

	it("starts folded, counting what it holds, and draws no card until opened", () => {
		withDone(finished("a"), finished("b"), { ...activeSession("live") });
		renderBoard();

		const strip = screen.getByRole("button", { name: "Expand Done, 2 cards" });
		expect(strip).toHaveAttribute("aria-expanded", "false");
		expect(document.querySelectorAll("[data-done-card]")).toHaveLength(0);
	});

	it("lists finished sessions newest-ended first, by the recorded ending", async () => {
		withDone(
			finished("old", { title: "ended first", termination: ended("2026-06-01T00:00:00Z") }),
			// Its row was touched long after it ended; the ending is what ranks it.
			finished("touched", {
				title: "ended second",
				updatedAt: "2026-06-20T00:00:00Z",
				termination: ended("2026-06-02T00:00:00Z"),
			}),
			finished("new", { title: "ended last", status: "merged", termination: ended("2026-06-03T00:00:00Z") }),
		);
		renderBoard();
		await openDoneLane();

		expect(doneTitles()).toEqual(["ended last", "ended second", "ended first"]);
	});

	it("remembers that the human opened it", async () => {
		withDone(finished("a"));
		renderBoard();
		await openDoneLane();

		expect(JSON.parse(localStorage.getItem("ao.board.collapsedLanes")!)).toEqual({ done: false });
		expect(readFoldedBoardLanes().has("done")).toBe(false);
	});

	it("searches by name, id, branch and Jira key, and says when nothing matches", async () => {
		withDone(
			finished("ao-201", { title: "Fix sidebar footer", branch: "fix/footer" }),
			finished("ao-202", { title: "Retry loop", branch: "feat/notif-retry", issueId: "jira:STAR-77" }),
			finished("ao-203", { title: "Ship pets" }),
		);
		renderBoard();
		await openDoneLane();
		const box = screen.getByRole("searchbox", { name: "Search finished sessions" });

		await userEvent.type(box, "footer");
		expect(doneTitles()).toEqual(["Fix sidebar footer"]);
		expect(screen.getByText("1 of 3")).toBeInTheDocument();

		await userEvent.clear(box);
		await userEvent.type(box, "star-77");
		expect(doneTitles()).toEqual(["Retry loop"]);

		await userEvent.clear(box);
		await userEvent.type(box, "ao-203");
		expect(doneTitles()).toEqual(["Ship pets"]);

		await userEvent.clear(box);
		await userEvent.type(box, "websocket");
		expect(doneTitles()).toEqual([]);
		expect(screen.getByText("Nothing finished matches “websocket”")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Clear search" }));
		expect(doneTitles()).toHaveLength(3);
		expect(screen.getByText("3 finished")).toBeInTheDocument();
	});

	it("clears the search on Escape, and never folds the lane from its search box", async () => {
		withDone(finished("a", { title: "alpha" }), finished("b", { title: "beta" }));
		renderBoard();
		await openDoneLane();
		const box = screen.getByRole("searchbox", { name: "Search finished sessions" });

		await userEvent.click(box);
		await userEvent.type(box, "alp");
		expect(doneTitles()).toEqual(["alpha"]);
		await userEvent.keyboard("{Escape}");

		expect(box).toHaveValue("");
		expect(doneTitles()).toHaveLength(2);
		expect(screen.getByRole("button", { name: "Collapse Done, 2 cards" })).toHaveAttribute("aria-expanded", "true");
	});

	it("clears only the sessions the search shows", async () => {
		deleteMock.mockResolvedValue({ error: undefined });
		withDone(finished("keep", { title: "keep me" }), finished("drop", { title: "drop me" }));
		renderBoard();
		await openDoneLane();

		await userEvent.type(screen.getByRole("searchbox", { name: "Search finished sessions" }), "drop");
		await userEvent.click(screen.getByRole("button", { name: "Clear shown" }));
		expect(screen.getByText(/Permanently remove 1 finished session/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Delete all" }));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledTimes(1));
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}", {
			params: { path: { sessionId: "drop" }, query: { force: false } },
		});
	});

	it("draws a finished card with no live parts", async () => {
		withDone(finished("a", { title: "alpha", issueId: "jira:STAR-9", branch: "feat/alpha-thing" }));
		renderBoard();
		await openDoneLane();

		const card = document.querySelector<HTMLElement>("[data-done-card]")!;
		expect(within(card).getByText("terminated")).toBeInTheDocument();
		expect(within(card).getByTitle(/^Ended /)).toBeInTheDocument();
		expect(within(card).getByText("STAR-9")).toBeInTheDocument();
		expect(within(card).getByText("feat/alpha-thing")).toBeInTheDocument();
		// No status gutter, agent label, crew strip or PR footer.
		expect(card.querySelector("[data-card-status-glyph], [data-status], [data-card-chips]")).toBeNull();
		expect(within(card).queryByText("Claude")).toBeNull();
		expect(within(card).queryByText("no PR yet")).toBeNull();
	});
});
