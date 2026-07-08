import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationCenter } from "./NotificationCenter";
import type { NativeNotificationClickPayload } from "../../main/native-notifications";
import type { NotificationDTO } from "../lib/notifications";

const { navigateMock, markReadMock } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	markReadMock: vi.fn().mockResolvedValue(undefined),
}));

const notifications: NotificationDTO[] = [
	{
		id: "ntf_1",
		sessionId: "sess-1",
		projectId: "proj-1",
		prUrl: "",
		type: "needs_input",
		title: "Needs input",
		body: "",
		status: "unread",
		createdAt: "2026-06-16T10:00:00Z",
		target: { kind: "session", sessionId: "sess-1" },
	},
	{
		id: "ntf_2",
		sessionId: "sess-2",
		projectId: "proj-1",
		prUrl: "",
		type: "ready_to_merge",
		title: "Ready to merge",
		body: "",
		status: "unread",
		createdAt: "2026-06-16T11:00:00Z",
		target: { kind: "session", sessionId: "sess-2" },
	},
];

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));

vi.mock("../hooks/useNotificationsQuery", () => ({
	useMarkAllNotificationsReadMutation: () => ({ isPending: false, mutateAsync: vi.fn() }),
	useMarkNotificationReadMutation: () => ({ isPending: false, mutateAsync: markReadMock }),
	useNotificationsQuery: () => ({ data: notifications, isError: false }),
}));

vi.mock("../lib/notifications", async (importOriginal) => ({
	...((await importOriginal()) as object),
	createNotificationsTransport: () => ({ connect: () => undefined }),
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

let clickListener: ((payload: NativeNotificationClickPayload) => void) | undefined;

beforeEach(() => {
	clickListener = undefined;
	navigateMock.mockClear();
	markReadMock.mockClear();
	// aoBridge holds the same object as window.ao, so mutating the property here is
	// visible to the component; capture the listener it registers on mount.
	window.ao!.notifications.onClick = (listener) => {
		clickListener = listener as (payload: NativeNotificationClickPayload) => void;
		return () => undefined;
	};
});

function renderNotificationCenter(extra?: React.ReactNode) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<NotificationCenter />
			{extra}
		</QueryClientProvider>,
	);
}

// Stand-in for the xterm surface: focusable, and it grabs focus on pointer-down
// the way xterm's helper textarea does when you click the terminal.
function Terminal() {
	return (
		<div data-testid="terminal" onPointerDown={(event) => event.currentTarget.focus()} tabIndex={-1}>
			terminal
		</div>
	);
}

describe("NotificationCenter", () => {
	it("renders a filled bell with a text-only yellow unread count", () => {
		renderNotificationCenter();

		const trigger = screen.getByRole("button", { name: "2 unread notifications" });
		const bell = trigger.querySelector("svg");
		const count = screen.getByText("2");

		expect(bell).toHaveClass("fill-current");
		expect(count).toHaveClass("text-[11px]");
		expect(count).toHaveClass("text-warning");
		expect(count).not.toHaveClass("bg-warning");
		expect(count).not.toHaveClass("rounded-full");
		expect(count).not.toHaveClass("text-background");
	});

	it("does not disable outside pointer events while open, so a click can reach the terminal", async () => {
		const user = userEvent.setup();
		renderNotificationCenter();

		await user.click(screen.getByRole("button", { name: "2 unread notifications" }));
		expect(await screen.findByText("Notifications")).toBeInTheDocument();

		// Modal menus set body { pointer-events: none }, which swallows the first
		// click on the terminal. A non-modal menu leaves outside clicks alone.
		expect(document.body.style.pointerEvents).not.toBe("none");
	});

	it("closes on a single outside click and lets that click focus the terminal, without stealing focus back to the bell", async () => {
		const user = userEvent.setup();
		renderNotificationCenter(<Terminal />);
		const bell = screen.getByRole("button", { name: "2 unread notifications" });

		await user.click(bell);
		expect(await screen.findByText("Notifications")).toBeInTheDocument();

		const terminal = screen.getByTestId("terminal");
		await user.click(terminal);

		await waitFor(() => expect(screen.queryByText("Notifications")).not.toBeInTheDocument());
		expect(terminal).toHaveFocus();
		expect(bell).not.toHaveFocus();
	});

	it("returns focus to the bell when closed with Escape (keyboard accessibility)", async () => {
		const user = userEvent.setup();
		renderNotificationCenter();
		const bell = screen.getByRole("button", { name: "2 unread notifications" });

		await user.click(bell);
		expect(await screen.findByText("Notifications")).toBeInTheDocument();

		await user.keyboard("{Escape}");

		await waitFor(() => expect(screen.queryByText("Notifications")).not.toBeInTheDocument());
		expect(bell).toHaveFocus();
	});

	it("marks read and navigates to the target when a native notification is clicked", async () => {
		renderNotificationCenter();
		expect(clickListener).toBeDefined();

		await act(async () => {
			clickListener?.({ id: "ntf_1", route: { kind: "session", sessionId: "sess-1", projectId: "proj-1" } });
		});

		expect(markReadMock).toHaveBeenCalledWith("ntf_1");
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-1" },
		});
	});
});
