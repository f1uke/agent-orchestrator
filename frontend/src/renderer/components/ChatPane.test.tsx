import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatApi, ChatResult } from "../lib/chat-api";
import type { ChatEntry } from "../lib/chat-event";
import { foldEntries } from "../lib/chat-fold";
import type { ChatMux, ChatMuxListeners } from "../lib/chat-mux";
import { emptyOverlay } from "../lib/chat-stream";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { ChatPane, ChatViewToggle } from "./ChatPane";

vi.mock("../hooks/useSessionNames", () => ({
	useSessionNames: () => new Map([["billing-portal-12", "invoice CSV export"]]),
}));

vi.mock("../hooks/useWorkspaceFiles", () => ({
	useWorkspaceFiles: () => ({ isPending: false, data: { available: true, paths: [], truncated: false } }),
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
	vi.useFakeTimers({ toFake: ["requestAnimationFrame", "cancelAnimationFrame"] });
	listeners = undefined;
	seq = 0;
	useUiStore.setState({ chatViews: {} });
	return () => vi.useRealTimers();
});

const OK: ChatResult = { ok: true };

function fakeApi() {
	return {
		send: vi.fn<ChatApi["send"]>(async () => OK),
		answer: vi.fn<ChatApi["answer"]>(async () => OK),
		interrupt: vi.fn<ChatApi["interrupt"]>(async () => OK),
		setMode: vi.fn<ChatApi["setMode"]>(async () => OK),
		setModel: vi.fn<ChatApi["setModel"]>(async () => OK),
	};
}

function renderPane(view: "chat" | "events" = "chat", api = fakeApi()) {
	const utils = render(
		<ChatPane chatApi={api} createMux={createMux} daemonReady session={session} theme="dark" view={view} />,
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
	return { ...utils, feed, delta, api, user: userEvent.setup() };
}

const settle = () => act(async () => undefined);

const rowOf = (kind: string) => document.querySelectorAll(`[data-row="${kind}"]`);

describe("ChatPane", () => {
	it("draws the chat state it is given and opens no subscription of its own", () => {
		const mux = vi.fn(createMux);
		const chat = {
			transcript: foldEntries([{ seq: 1, ts: "", dir: "host", payload: { kind: "spawn", resume: "" } }]),
			overlay: emptyOverlay,
			entries: [],
			connection: "live" as const,
		};
		render(
			<ChatPane
				chat={chat}
				chatApi={fakeApi()}
				createMux={mux}
				daemonReady
				session={session}
				theme="dark"
				view="chat"
			/>,
		);
		expect(screen.getByText("Session started")).toBeInTheDocument();
		expect(screen.getByText("Live")).toBeInTheDocument();
		expect(mux).not.toHaveBeenCalled();
	});

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

const userIn = (uuid: string, text: string) => entry("in", { type: "user", uuid, message: { content: text } });
const lifecycle = (uuid: string, state: string) => out({ type: "command_lifecycle", command_uuid: uuid, state });
const bashRequest = () => [
	out({
		type: "assistant",
		message: { id: "m", content: [{ type: "tool_use", id: "t1", name: "Bash", input: { command: "go test ./..." } }] },
	}),
	out({
		type: "control_request",
		request_id: "r1",
		request: {
			subtype: "can_use_tool",
			tool_name: "Bash",
			tool_use_id: "t1",
			input: { command: "go test ./..." },
			permission_suggestions: [{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: "go test:*" }] }],
		},
	}),
];
const answered = (behavior: "allow" | "deny") =>
	entry("in", {
		type: "control_response",
		response: { subtype: "success", request_id: "r1", response: { behavior } },
	});
const hostDown: ChatResult = {
	ok: false,
	error: { kind: "host_not_running", message: "session host is not running" },
};

describe("ChatPane composer", () => {
	it("sends on Enter, keeps Shift+Enter as a newline, and clears once sent", async () => {
		const { api, user } = renderPane();
		const input = screen.getByRole("textbox", { name: "Message Claude" });
		await user.type(input, "first line{Shift>}{Enter}{/Shift}second");
		expect(api.send).not.toHaveBeenCalled();
		await user.keyboard("{Enter}");
		await settle();
		expect(api.send).toHaveBeenCalledWith("chat-1", "first line\nsecond", []);
		expect(input).toHaveValue("");
	});

	it("sends nothing for blank text", async () => {
		const { api, user } = renderPane();
		await user.type(screen.getByRole("textbox", { name: "Message Claude" }), "   {Enter}");
		expect(api.send).not.toHaveBeenCalled();
		expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
	});

	it("shows input sent while busy as a Queued row until it starts, then as a bubble", () => {
		const { feed } = renderPane();
		feed(userIn("u1", "Build it"), lifecycle("u1", "queued"), lifecycle("u1", "started"));
		feed(userIn("u2", "Also add a CHANGELOG entry"), lifecycle("u2", "queued"));

		const queued = screen.getAllByTestId("chat-queued");
		expect(queued).toHaveLength(1);
		expect(queued[0]).toHaveTextContent("Queued");
		expect(queued[0]).toHaveTextContent("Also add a CHANGELOG entry");
		expect(screen.getByTestId("chat-stream")).not.toHaveTextContent("Also add a CHANGELOG entry");

		feed(lifecycle("u1", "completed"), lifecycle("u2", "started"));
		expect(screen.queryByTestId("chat-queued")).toBeNull();
		const bubbles = [...document.querySelectorAll(".chat-user")];
		expect(bubbles.at(-1)).toHaveTextContent("Also add a CHANGELOG entry");
	});

	it("shows Stop only while busy, and Stop or Esc interrupts", async () => {
		const { feed, api, user } = renderPane();
		expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();

		feed(userIn("u1", "Build it"), lifecycle("u1", "started"));
		await user.click(screen.getByRole("button", { name: "Stop" }));
		await user.click(screen.getByRole("textbox", { name: "Message Claude" }));
		await user.keyboard("{Escape}");
		await settle();
		expect(api.interrupt).toHaveBeenCalledTimes(2);

		feed(lifecycle("u1", "cancelled"));
		expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
		await user.keyboard("{Escape}");
		expect(api.interrupt).toHaveBeenCalledTimes(2);
	});

	it("does not interrupt when Esc denies a focused permission card", async () => {
		const { feed, api, user } = renderPane();
		feed(userIn("u1", "Build it"), lifecycle("u1", "started"), ...bashRequest());
		screen.getByRole("group", { name: "Permission needed: Bash" }).focus();
		await user.keyboard("{Escape}");
		await settle();
		expect(api.answer).toHaveBeenCalledWith("chat-1", { requestId: "r1", behavior: "deny", message: "" });
		expect(api.interrupt).not.toHaveBeenCalled();
	});

	it("attaches a pasted image as a thumbnail, sends it, and refuses other files", async () => {
		vi.useRealTimers();
		const api = fakeApi();
		render(<ChatPane chatApi={api} createMux={createMux} daemonReady session={session} theme="dark" view="chat" />);
		const input = screen.getByRole("textbox", { name: "Message Claude" });
		const png = new File([new Uint8Array([137, 80, 78, 71])], "sketch.png", { type: "image/png" });
		const pdf = new File(["%PDF"], "spec.pdf", { type: "application/pdf" });
		fireEvent.paste(input, { clipboardData: { files: [png, pdf] } });

		const thumb = await screen.findByRole("img", { name: "sketch.png" });
		expect(thumb).toHaveAttribute("src", "data:image/png;base64,iVBORw==");
		expect(screen.getByRole("status")).toHaveTextContent("Only images can be attached. Skipped spec.pdf.");
		expect(screen.getAllByTestId("chat-attachment")).toHaveLength(1);

		await userEvent.type(input, "Use this sketch{Enter}");
		await settle();
		expect(api.send).toHaveBeenCalledWith("chat-1", "Use this sketch", [
			{ mediaType: "image/png", base64: "iVBORw==" },
		]);
		expect(screen.queryByTestId("chat-attachment")).toBeNull();
	});

	it("removes an attachment with its x", async () => {
		vi.useRealTimers();
		render(
			<ChatPane chatApi={fakeApi()} createMux={createMux} daemonReady session={session} theme="dark" view="chat" />,
		);
		const png = new File([new Uint8Array([1])], "a.png", { type: "image/png" });
		fireEvent.paste(screen.getByRole("textbox", { name: "Message Claude" }), { clipboardData: { files: [png] } });
		await userEvent.click(await screen.findByRole("button", { name: "Remove a.png" }));
		expect(screen.queryByTestId("chat-attachment")).toBeNull();
	});

	it("shows the transcript's mode and changes it through the picker", async () => {
		const { feed, api, user } = renderPane();
		feed(out({ type: "system", subtype: "init", session_id: "s", permissionMode: "default" }));
		await user.click(screen.getByRole("button", { name: "Model and mode: Ask before edits" }));
		await user.click(await screen.findByRole("menuitemradio", { name: "Plan" }));
		await settle();
		expect(api.setMode).toHaveBeenCalledWith("chat-1", "plan");

		feed(out({ type: "system", subtype: "status", status: null, permissionMode: "plan" }));
		expect(screen.getByRole("button", { name: "Model and mode: Plan" })).toBeInTheDocument();
	});
});

describe("ChatPane answers", () => {
	it("holds Sending… after the call returns, until the log shows the answer", async () => {
		const { feed, api, user } = renderPane();
		feed(...bashRequest());
		await user.click(screen.getByRole("button", { name: /Always allow/ }));
		await settle();
		expect(api.answer).toHaveBeenCalledWith("chat-1", {
			requestId: "r1",
			behavior: "allow",
			updatedPermissions: [{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: "go test:*" }] }],
		});
		const card = document.querySelector('[data-pending="permission"]')!;
		expect(card).toHaveTextContent("Sending…");
		expect(screen.getByRole("button", { name: /Allow once/ })).toBeDisabled();

		feed(answered("allow"));
		expect(document.querySelector("[data-pending]")).toBeNull();
		expect(document.querySelector('[data-resolved="allowed"]')).toHaveTextContent("Allowed Bash once");
	});

	it("shows the host's reason when it refuses an answer, and unlocks the card", async () => {
		const api = fakeApi();
		api.answer.mockResolvedValue({ ok: false, error: { kind: "rejected", message: "request r1 is not pending" } });
		const { feed, user } = renderPane("chat", api);
		feed(...bashRequest());
		await user.click(screen.getByRole("button", { name: /Allow once/ }));
		await settle();
		expect(screen.getByRole("alert")).toHaveTextContent("request r1 is not pending");
		expect(screen.getByRole("button", { name: /Allow once/ })).toBeEnabled();
	});

	it("approves a plan, then sets the mode it was approved for", async () => {
		const { feed, api, user } = renderPane();
		const plan = { plan: "# Plan", planFilePath: "p.md" };
		feed(
			out({
				type: "control_request",
				request_id: "r1",
				request: { subtype: "can_use_tool", tool_name: "ExitPlanMode", tool_use_id: "t9", input: plan },
			}),
		);
		await user.click(screen.getByRole("button", { name: "Approve · auto-accept edits" }));
		await settle();
		expect(api.answer).toHaveBeenCalledWith("chat-1", { requestId: "r1", behavior: "allow" });
		expect(api.setMode).toHaveBeenCalledWith("chat-1", "acceptEdits");
		expect(api.answer.mock.invocationCallOrder[0]).toBeLessThan(api.setMode.mock.invocationCallOrder[0]);
	});

	it("shows the asleep banner and locks every input when the host is down, until the stream is live again", async () => {
		const api = fakeApi();
		api.send.mockResolvedValue(hostDown);
		const { feed, user } = renderPane("chat", api);
		act(() => listeners!.live());
		feed(userIn("u1", "Build it"), lifecycle("u1", "started"), ...bashRequest());
		const input = screen.getByRole("textbox", { name: "Message Claude" });
		await user.type(input, "hello{Enter}");
		await settle();

		expect(screen.getByRole("alert")).toHaveTextContent("Session is asleep or restarting");
		expect(input).toBeDisabled();
		expect(input).toHaveValue("hello");
		expect(screen.getByRole("button", { name: "Stop" })).toBeDisabled();
		expect(screen.getByRole("button", { name: /Model and mode/ })).toBeDisabled();
		expect(screen.getByRole("button", { name: /Allow once/ })).toBeDisabled();

		act(() => listeners!.offline());
		act(() => listeners!.live());
		expect(screen.queryByText(/Session is asleep or restarting/)).toBeNull();
		expect(input).toBeEnabled();
		expect(screen.getByRole("button", { name: /Allow once/ })).toBeEnabled();
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
