import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatEntry } from "../lib/chat-event";
import type { ChatMux, ChatMuxListeners } from "../lib/chat-mux";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { ChatPane, ChatViewToggle } from "./ChatPane";
import { TooltipProvider } from "./ui/tooltip";

vi.mock("../hooks/useSessionNames", () => ({
	useSessionNames: () => new Map([["billing-portal-12", "invoice CSV export"]]),
}));

const session: WorkspaceSession = {
	id: "chat-1",
	transport: "chat",
	workspaceId: "ws",
	workspaceName: "ws",
	title: "chat",
	provider: "claude-code",
	branch: "b",
	status: "working",
	updatedAt: "now",
	prs: [],
};

let listeners: ChatMuxListeners | undefined;
const createMux = (): ChatMux => ({
	open: () => undefined,
	close: () => undefined,
	listen: (_id, l) => {
		listeners = l;
		return () => undefined;
	},
	onConnectionChange: () => () => undefined,
	dispose: () => undefined,
});

let seq = 0;
const entry = (dir: ChatEntry["dir"], payload: object): ChatEntry => ({
	seq: ++seq,
	ts: "2026-10-30T10:00:00.000Z",
	dir,
	payload,
});
const out = (payload: object) => entry("out", payload);

beforeEach(() => {
	vi.useFakeTimers();
	listeners = undefined;
	seq = 0;
	useUiStore.setState({ chatViews: {} });
	return () => vi.useRealTimers();
});

function renderPane(view: "chat" | "events" = "chat") {
	const utils = render(
		<TooltipProvider>
			<ChatPane createMux={createMux} daemonReady session={session} theme="dark" view={view} />
		</TooltipProvider>,
	);
	const feed = (...entries: ChatEntry[]) =>
		act(() => {
			entries.forEach((e) => listeners!.entry(e));
			vi.advanceTimersToNextFrame();
		});
	const delta = (...events: object[]) =>
		act(() => {
			events.forEach((e) => listeners!.delta({ type: "stream_event", parent_tool_use_id: null, event: e }));
			vi.advanceTimersToNextFrame();
		});
	return { ...utils, feed, delta };
}

const rowOf = (kind: string) => document.querySelectorAll(`[data-row="${kind}"]`);

describe("ChatPane", () => {
	it("draws user input, peer and orchestrator messages, notices and the turn's cost", () => {
		const { feed } = renderPane();
		feed(
			entry("host", { kind: "spawn", resume: "" }),
			entry("in", { type: "user", uuid: "u1", message: { content: "Make hello.txt" } }),
			entry("host", { kind: "user_origin", uuid: "u2", origin: { via: "ao_send", from: "billing-portal-12" } }),
			entry("in", { type: "user", uuid: "u2", message: { content: "[from @billing-portal-12] Use the new name." } }),
			entry("host", { kind: "user_origin", uuid: "u3", origin: { via: "ao_send", from: "proj-orchestrator" } }),
			entry("in", { type: "user", uuid: "u3", message: { content: "[from @proj-orchestrator] Open the PR." } }),
			out({ type: "assistant", message: { id: "m1", content: [{ type: "thinking", thinking: "plan it" }] } }),
			out({ type: "assistant", message: { id: "m1", content: [{ type: "text", text: "Done **now**." }] } }),
			out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.0441 }),
			entry("host", { kind: "exit", code: 0 }),
		);

		expect(screen.getByText("Session started")).toBeInTheDocument();
		const user = document.querySelector(".chat-user")!;
		expect(user).toHaveTextContent("Make hello.txt");
		expect(user).toHaveTextContent("cancelled");

		const peer = document.querySelector(".chat-peer:not(.chat-peer--orchestrator)")!;
		expect(peer).toHaveTextContent("invoice CSV export · peer message");
		expect(peer).toHaveTextContent("Use the new name.");
		expect(peer).not.toHaveTextContent("[from @");
		const orchestrator = document.querySelector(".chat-peer--orchestrator")!;
		expect(orchestrator).toHaveTextContent("orchestrator · orchestrator message");

		expect(screen.getByText("plan it").closest("details")).not.toHaveAttribute("open");
		expect(screen.getByText("now").tagName).toBe("STRONG");
		expect(screen.getByText("Turn complete · $0.044 this session")).toBeInTheDocument();
		expect(screen.getByText("Claude exited · exit code 0")).toBeInTheDocument();
		expect(screen.getByText(/Read-only view\. Send with/)).toBeInTheDocument();
		expect(screen.getByText("$0.044 this session")).toBeInTheDocument();
	});

	it("streams a reply with a caret until the logged block replaces it", () => {
		const { feed, delta } = renderPane();
		delta(
			{ type: "message_start", message: { id: "m1" } },
			{ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "Hello, " } },
			{ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "world" } },
		);
		const streaming = document.querySelector("[data-streaming]")!;
		expect(streaming).toHaveTextContent("Hello, world");
		expect(streaming.querySelector(".chat-caret")).not.toBeNull();

		feed(out({ type: "assistant", message: { id: "m1", content: [{ type: "text", text: "Hello, world" }] } }));
		expect(document.querySelector("[data-streaming]")).toBeNull();
		expect(document.querySelector(".chat-caret")).toBeNull();
		expect(rowOf("assistant_text")).toHaveLength(1);
	});

	it("folds a subagent's tool calls into its Agent card instead of the column", () => {
		const { feed } = renderPane();
		const tool = (id: string, name: string, parent: string | null) =>
			out({
				type: "assistant",
				parent_tool_use_id: parent,
				message: { id: `m-${id}`, content: [{ type: "tool_use", id, name, input: {} }] },
			});
		feed(
			tool("agent", "Agent", null),
			tool("s1", "Grep", "agent"),
			tool("s2", "Read", "agent"),
			out({ type: "user", message: { content: [{ type: "tool_result", tool_use_id: "agent", content: "found" }] } }),
		);
		expect(rowOf("tool_call")).toHaveLength(1);
		expect(document.querySelector('[data-tool="Agent"]')).toHaveTextContent("done · 2 tool uses");
	});

	it("shows a pending request inline, then its one-line outcome once answered", () => {
		const { feed } = renderPane();
		feed(
			out({
				type: "assistant",
				message: { id: "m", content: [{ type: "tool_use", id: "t1", name: "Bash", input: { command: "rm x" } }] },
			}),
			out({
				type: "control_request",
				request_id: "r1",
				request: { subtype: "can_use_tool", tool_name: "Bash", tool_use_id: "t1", input: { command: "rm x" } },
			}),
		);
		expect(document.querySelector('[data-pending="permission"]')).toHaveTextContent("Permission needed · Bash");
		expect(screen.getByRole("button", { name: "Allow once" })).toBeDisabled();

		feed(
			entry("in", {
				type: "control_response",
				response: { subtype: "success", request_id: "r1", response: { behavior: "deny", message: "no" } },
			}),
		);
		expect(document.querySelector("[data-pending]")).toBeNull();
		expect(document.querySelector('[data-resolved="denied"]')).toHaveTextContent("Denied Bash: no");
	});

	it("lists raw entries in the Events view", () => {
		const { feed } = renderPane("events");
		feed(
			out({ type: "system", subtype: "init", session_id: "s" }),
			out({ type: "control_request", request_id: "r", request: { subtype: "can_use_tool", tool_name: "Bash" } }),
			entry("host", { kind: "exit", code: 1 }),
		);
		const types = [...screen.getByTestId("chat-events").querySelectorAll(".chat-ev__type")];
		expect(types.map((t) => t.textContent)).toEqual(["system/init", "control_request/can_use_tool", "host/exit"]);
		expect(types[1]).toHaveClass("chat-ev__type--control");
		expect(types[2]).toHaveClass("chat-ev__type--host");
		expect(document.querySelector('[data-seq="3"]')).toHaveTextContent("host");
	});
});

describe("ChatViewToggle", () => {
	it("switches a chat session's view in the store", async () => {
		vi.useRealTimers();
		render(<ChatViewToggle session={session} />);
		expect(screen.getByRole("button", { name: "Chat" })).toHaveAttribute("aria-pressed", "true");
		await userEvent.click(screen.getByRole("button", { name: "Events" }));
		expect(useUiStore.getState().chatViews["chat-1"]).toBe("events");
		expect(screen.getByRole("button", { name: "Events" })).toHaveAttribute("aria-pressed", "true");
	});

	it("is absent for a terminal session", () => {
		const { container } = render(<ChatViewToggle session={{ ...session, transport: "terminal" }} />);
		expect(container).toBeEmptyDOMElement();
		const legacy = render(<ChatViewToggle session={{ ...session, transport: undefined }} />);
		expect(legacy.container).toBeEmptyDOMElement();
	});
});
