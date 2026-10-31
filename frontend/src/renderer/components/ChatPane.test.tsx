import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatApi, ChatResult } from "../lib/chat-api";
import type { ChatEntry } from "../lib/chat-event";
import { foldEntries } from "../lib/chat-fold";
import type { ChatMux, ChatMuxListeners } from "../lib/chat-mux";
import { emptyOverlay } from "../lib/chat-stream";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import type { SubscriptionUsage } from "../lib/chat-usage";
import type { WorkspaceSession } from "../types/workspace";
import { ChatPane, ChatViewToggle } from "./ChatPane";

vi.mock("../hooks/useSessionNames", () => ({
	useSessionNames: () => new Map([["billing-portal-12", "invoice CSV export"]]),
}));

let sharedUsage: SubscriptionUsage | undefined;
vi.mock("../hooks/useChatUsage", () => ({ useChatUsage: () => sharedUsage }));

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
	sharedUsage = undefined;
	seq = 0;
	useUiStore.setState({ chatViews: {}, inspectorViewRequest: null });
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

function renderPane(view: "chat" | "events" = "chat", api = fakeApi(), who: WorkspaceSession = session) {
	const utils = render(
		<ChatPane chatApi={api} createMux={createMux} daemonReady session={who} theme="dark" view={view} />,
	);
	act(() => listeners?.live());
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
			replaying: false,
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

	it("draws user input, peer and orchestrator messages, notices and the session's cost", () => {
		const { feed } = renderPane();
		feed(
			entry("host", { kind: "spawn", resume: "" }),
			entry("in", { type: "user", uuid: "b", message: { content: "# Goal\nGreet the user." } }),
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

		expect(screen.getByRole("button", { name: /Thought/ })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("plan it")).toBeNull();
		expect(screen.getByText("now").tagName).toBe("STRONG");
		expect(screen.getByText("Claude exited · exit code 0")).toBeInTheDocument();
		expect(screen.getByText("$0.044 this session")).toBeInTheDocument();
	});

	it("draws a pasted image in the user bubble as a thumbnail, not as a count", async () => {
		const { feed, user } = renderPane();
		const png = (data: string) => ({ type: "image", source: { type: "base64", media_type: "image/png", data } });
		feed(
			entry("in", {
				type: "user",
				uuid: "u1",
				message: { content: [{ type: "text", text: "What is wrong here?" }, png("iVBORw=="), png("AAAA")] },
			}),
			out({ type: "command_lifecycle", command_uuid: "u1", state: "started" }),
		);
		const bubble = document.querySelector(".chat-user")!;
		expect(bubble).toHaveTextContent("What is wrong here?");
		expect(bubble).not.toHaveTextContent(/\d images?/);
		const thumbs = within(bubble as HTMLElement).getAllByRole("img");
		expect(thumbs.map((t) => t.getAttribute("src"))).toEqual([
			"data:image/png;base64,iVBORw==",
			"data:image/png;base64,AAAA",
		]);

		await user.click(within(bubble as HTMLElement).getByRole("button", { name: "View Pasted image 2" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText("2 / 2")).toBeInTheDocument();
		expect(within(dialog).getByRole("img")).toHaveAttribute("src", "data:image/png;base64,AAAA");
	});

	it("ends a turn with how long it took, never a money amount", () => {
		const { feed } = renderPane();
		const at = (dir: ChatEntry["dir"], sec: number, payload: object): ChatEntry => ({
			seq: ++seq,
			ts: new Date(Date.parse("2026-10-30T10:00:00.000Z") + sec * 1000).toISOString(),
			dir,
			payload,
		});
		feed(
			at("in", 0, { type: "user", uuid: "u1", message: { content: "go" } }),
			at("out", 0, { type: "command_lifecycle", command_uuid: "u1", state: "started" }),
			at("out", 30, { type: "assistant", message: { id: "m1", content: [{ type: "text", text: "done" }] } }),
			at("out", 134, { type: "result", subtype: "success", num_turns: 1, total_cost_usd: 23.45 }),
			at("in", 140, { type: "user", uuid: "u2", message: { content: "again" } }),
			at("out", 140, { type: "command_lifecycle", command_uuid: "u2", state: "started" }),
			at("out", 152, { type: "result", subtype: "error_during_execution", is_error: true, num_turns: 1 }),
		);
		const ends = [...rowOf("turn_end")];
		expect(ends.map((e) => e.querySelector("[data-notice]")?.textContent)).toEqual([
			"Completed in 2m 14s",
			"Turn ended: error during execution · 12s",
		]);
		expect(ends.every((e) => !e.textContent?.includes("$"))).toBe(true);
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

const toolUse = (id: string, name: string, input: object, parent: string | null = null) =>
	out({
		type: "assistant",
		parent_tool_use_id: parent,
		message: { id: `m-${id}`, content: [{ type: "tool_use", id, name, input }] },
	});
const toolDone = (id: string, content = "ok", isError = false) =>
	out({ type: "user", message: { content: [{ type: "tool_result", tool_use_id: id, content, is_error: isError }] } });
const saying = (text: string, parent: string | null = null) =>
	out({
		type: "assistant",
		parent_tool_use_id: parent,
		message: { id: `m-${text}`, content: [{ type: "text", text }] },
	});
const rooted = { ...session, workspacePath: "/work/project" };

describe("ChatPane calm view", () => {
	it("opens the whole Thought row on a click and never draws an empty one", async () => {
		const { feed, user } = renderPane();
		feed(
			out({ type: "assistant", message: { id: "e", content: [{ type: "thinking", thinking: "" }] } }),
			out({ type: "assistant", message: { id: "m1", content: [{ type: "thinking", thinking: "plan it" }] } }),
		);
		expect(screen.getAllByRole("button", { name: /Thought/ })).toHaveLength(1);
		await user.click(screen.getByRole("button", { name: /Thought/ }));
		expect(screen.getByText("plan it")).toBeInTheDocument();
	});

	it("shows the latest line of a thinking stream on the closed row", () => {
		const { delta } = renderPane();
		delta(
			{ type: "message_start", message: { id: "m1" } },
			{
				type: "content_block_delta",
				index: 0,
				delta: { type: "thinking_delta", thinking: "first idea\n\nsecond idea\n" },
			},
		);
		const row = screen.getByRole("button", { name: /Thinking/ });
		expect(row).toHaveTextContent("second idea");
		expect(row).not.toHaveTextContent("first idea");
	});

	it("groups the tool calls between replies behind one counted line", async () => {
		const { feed, user } = renderPane();
		feed(
			userIn("u1", "fix it"),
			saying("looking"),
			toolUse("a", "Read", { file_path: "a.go" }),
			toolDone("a"),
			toolUse("b", "Bash", { command: "go test" }),
			toolDone("b"),
			toolUse("c", "Bash", { command: "go vet" }),
			toolDone("c"),
		);
		const head = document.querySelector("[data-group] .chat-group__head")!;
		expect(head).toHaveTextContent(/^Ran 2 commands, read a file$/);
		expect(head).toHaveAttribute("aria-expanded", "false");
		expect(document.querySelector("[data-tool]")).toBeNull();
		await user.click(head);
		expect(document.querySelectorAll("[data-tool]")).toHaveLength(3);
	});

	it("keeps a failed call visible under a closed group header and counts it", () => {
		const { feed } = renderPane();
		feed(
			userIn("u1", "fix it"),
			toolUse("a", "Read", { file_path: "a.go" }),
			toolDone("a"),
			toolUse("b", "Bash", { command: "go test" }),
			toolDone("b", "Exit code 1\nFAIL", true),
		);
		expect(document.querySelector("[data-group] .chat-group__head")).toHaveTextContent(
			/^Ran a command, read a file \(1 failed\)$/,
		);
		const shown = document.querySelectorAll("[data-tool]");
		expect(shown).toHaveLength(1);
		expect(shown[0]).toHaveAttribute("data-tool", "Bash");
	});

	it("shows the running call as a plain verb with a timer that ticks each second", () => {
		vi.useFakeTimers({
			toFake: ["Date", "setInterval", "clearInterval", "requestAnimationFrame", "cancelAnimationFrame"],
		});
		vi.setSystemTime(Date.parse("2026-10-30T10:01:25.000Z"));
		const { feed } = renderPane("chat", fakeApi(), rooted);
		feed(
			userIn("u1", "go"),
			toolUse("a", "Read", { file_path: "/work/project/a.go" }),
			toolDone("a"),
			toolUse("b", "Bash", { command: "cd /work/project && go test ./..." }),
		);
		const head = document.querySelector("[data-group] .chat-group__head")!;
		expect(head).toHaveTextContent(/^Running a command 1m 25s$/);
		act(() => vi.advanceTimersByTime(1000));
		expect(head).toHaveTextContent(/^Running a command 1m 26s$/);
	});

	describe("the live Thinking row", () => {
		const at = (sec: number, e: ChatEntry): ChatEntry => ({
			...e,
			ts: new Date(Date.parse("2026-10-31T11:53:00.000Z") + sec * 1000).toISOString(),
		});
		const tokens = (sec: number) =>
			at(sec, out({ type: "system", subtype: "thinking_tokens", estimated_tokens: 50, estimated_tokens_delta: 50 }));
		const afterTools = () => [
			at(0, userIn("u1", "find the drift")),
			at(0, lifecycle("u1", "started")),
			at(1, toolUse("a", "Read", { file_path: "/work/project/a.go" })),
			at(2, toolDone("a")),
			at(3, toolUse("b", "Grep", { pattern: "parked" })),
			at(10, toolDone("b")),
		];
		const liveRows = () => [...document.querySelectorAll(".chat-live-row")];
		const thinkingRow = () => liveRows().find((r) => r.querySelector(".chat-live__verb")?.textContent === "Thinking");

		beforeEach(() => {
			vi.useFakeTimers({
				toFake: ["Date", "setInterval", "clearInterval", "requestAnimationFrame", "cancelAnimationFrame"],
			});
			vi.setSystemTime(Date.parse("2026-10-31T11:53:40.000Z"));
		});

		it("appears with only thinking_tokens after a tool result, ticks, and goes on the next tool_use", () => {
			const { feed } = renderPane("chat", fakeApi(), rooted);
			feed(...afterTools());
			expect(thinkingRow()).toBeUndefined();

			feed(tokens(12), tokens(12), tokens(20));
			expect(liveRows()).toHaveLength(1);
			expect(thinkingRow()).toHaveTextContent(/^Thinking 28s$/);
			act(() => vi.advanceTimersByTime(1000));
			expect(thinkingRow()).toHaveTextContent(/^Thinking 29s$/);

			feed(at(41, toolUse("c", "Bash", { command: "go test ./internal/webhook/..." })));
			expect(thinkingRow()).toBeUndefined();
			expect(liveRows()).toHaveLength(1);
		});

		it("goes when claude starts to answer", () => {
			const { feed } = renderPane("chat", fakeApi(), rooted);
			feed(...afterTools(), tokens(12));
			expect(thinkingRow()).toBeDefined();
			feed(at(30, saying("The reset races the read.")));
			expect(thinkingRow()).toBeUndefined();
		});

		it("shows the latest line of streamed thinking text while collapsed", () => {
			const { feed, delta } = renderPane("chat", fakeApi(), rooted);
			feed(...afterTools(), tokens(12));
			delta(
				{ type: "message_start", message: { id: "m9" } },
				{ type: "content_block_start", index: 0, content_block: { type: "thinking", thinking: "" } },
				{
					type: "content_block_delta",
					index: 0,
					delta: { type: "thinking_delta", thinking: "The counter is read twice.\n\nMaybe the reset races.\n" },
				},
			);
			expect(liveRows()).toHaveLength(1);
			expect(thinkingRow()?.querySelector(".chat-live__detail")).toHaveTextContent(/^Maybe the reset races\.$/);
			expect(screen.queryByRole("button", { name: /Thinking…/ })).toBeNull();
		});

		it("renders through the same live row as the Waiting on and Running rows", () => {
			const shapeOf = (row: Element) => ({
				classes: row.getAttribute("class"),
				children: [...row.children].map((c) => c.getAttribute("class")),
				parts: [...row.querySelectorAll("[class]")]
					.map((c) => (c.getAttribute("class") ?? "").split(" ").find((n) => /^chat-(dots|live|chev)/.test(n)))
					.filter((n) => n !== undefined),
			});
			const thinking = renderPane("chat", fakeApi(), rooted);
			thinking.feed(...afterTools(), tokens(12));
			const thinkingShape = shapeOf(liveRows()[0]);
			thinking.unmount();

			const running = renderPane("chat", fakeApi(), rooted);
			running.feed(...afterTools(), at(30, toolUse("c", "Bash", { command: "go test" })));
			const runningShape = shapeOf(liveRows()[0]);
			running.unmount();

			const waiting = renderPane("chat", fakeApi(), rooted);
			waiting.feed(
				userIn("u1", "soak"),
				toolUse("bg", "Bash", { command: "go test -run Soak ./...", run_in_background: true }),
				out({
					type: "system",
					subtype: "task_started",
					task_id: "t1",
					tool_use_id: "bg",
					task_type: "local_bash",
					is_backgrounded: true,
					description: "soak",
				}),
				toolDone("bg", "Command running in background with ID: t1."),
				saying("started it"),
				out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 }),
			);
			const waitingShape = shapeOf(liveRows()[0]);

			expect(thinkingShape.classes).toContain("chat-live-row");
			expect(thinkingShape.parts).toEqual([
				"chat-live",
				"chat-dots",
				"chat-live__verb",
				"chat-live__time",
				"chat-chev",
			]);
			for (const other of [runningShape, waitingShape]) {
				expect(other.classes).toBe(thinkingShape.classes);
				expect(other.children).toEqual(thinkingShape.children);
				expect(other.parts).toEqual(thinkingShape.parts);
			}
		});
	});

	it("folds a finished turn's work between the user's message and the last reply", async () => {
		const { feed, user } = renderPane();
		feed(
			userIn("u1", "fix it"),
			lifecycle("u1", "started"),
			saying("on it"),
			toolUse("a", "Bash", { command: "go test" }),
			toolDone("a"),
			toolUse("b", "Bash", { command: "go vet" }),
			toolDone("b"),
			saying("all fixed"),
			out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 }),
		);
		const head = document.querySelector("[data-turn] .chat-group__head")!;
		expect(head).toHaveTextContent(/^Worked for 0s$/);
		expect(head).toHaveAttribute("title", "Ran 2 commands, 1 message");
		expect(screen.getByText("all fixed")).toBeInTheDocument();
		expect(screen.queryByText("on it")).toBeNull();
		await user.click(head);
		expect(screen.getByText("on it")).toBeInTheDocument();
		expect(document.querySelector("[data-group]")).not.toBeNull();
	});

	it("opens a turn whose work is one group straight to its calls, with no inner sentence", async () => {
		const { feed, user } = renderPane();
		feed(
			userIn("u1", "check it"),
			lifecycle("u1", "started"),
			toolUse("a", "Bash", { command: "go test" }),
			toolDone("a"),
			toolUse("b", "Bash", { command: "go vet" }),
			toolDone("b"),
			saying("all green"),
			out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 }),
		);
		const turn = document.querySelector("[data-turn]")!;
		await user.click(turn.querySelector(".chat-group__head")!);
		expect(turn.querySelector("[data-group]")).toBeNull();
		expect(turn).not.toHaveTextContent("Ran 2 commands");
		expect(turn.querySelectorAll("[data-tool]")).toHaveLength(2);
	});

	it("keeps the sentence line of each group when an opened turn holds several", async () => {
		const { feed, user } = renderPane();
		feed(
			userIn("u1", "fix it"),
			lifecycle("u1", "started"),
			toolUse("a", "Bash", { command: "go test" }),
			toolDone("a"),
			toolUse("b", "Read", { file_path: "a.go" }),
			toolDone("b"),
			saying("found it"),
			toolUse("c", "Edit", { file_path: "a.go", old_string: "x", new_string: "y" }),
			toolDone("c"),
			toolUse("d", "Bash", { command: "go test" }),
			toolDone("d"),
			saying("fixed"),
			out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 }),
		);
		const turn = document.querySelector("[data-turn]")!;
		await user.click(turn.querySelector(".chat-group__head")!);
		const heads = [...turn.querySelectorAll("[data-group] .chat-group__head")].map((h) => h.textContent);
		expect(heads).toEqual(["Ran a command, read a file", "Ran a command, edited a file"]);
		expect(turn.querySelectorAll("[data-tool]")).toHaveLength(0);
	});

	it("says how long a finished turn worked, and how many of its calls failed", () => {
		const { feed } = renderPane();
		const at = (sec: number, e: ChatEntry): ChatEntry => ({
			...e,
			ts: new Date(Date.parse("2026-10-30T10:00:00.000Z") + sec * 1000).toISOString(),
		});
		feed(
			at(0, userIn("u1", "fix it")),
			at(0, lifecycle("u1", "started")),
			at(2, toolUse("a", "Bash", { command: "go test" })),
			at(9, toolDone("a", "Exit code 1\nFAIL", true)),
			at(10, toolUse("b", "Bash", { command: "go vet" })),
			at(12, toolDone("b")),
			at(30, saying("fixed")),
			at(36, out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 })),
		);
		const head = document.querySelector("[data-turn] .chat-group__head")!;
		expect(head).toHaveTextContent(/^Worked for 36s · 1 failed$/);
		expect(head.querySelector(".chat-group__failed")).toHaveTextContent("1 failed");
	});

	describe("the task brief", () => {
		const spawn = (resume = "") => entry("host", { kind: "spawn", argv: ["claude"], resume, pid: 1 });
		const brief = "/pstack:poteto-mode # Goal\nFix the **flaky** test.\n\n## Rules\n- one\n- two";

		it("folds the brief AO spawned the session with, and opens it as markdown", async () => {
			const { feed, user } = renderPane();
			feed(
				spawn(),
				userIn("b", brief),
				lifecycle("b", "completed"),
				userIn("u2", "thanks"),
				lifecycle("u2", "completed"),
			);
			const row = screen.getByRole("button", { name: /Task brief/ });
			expect(row).toHaveAttribute("aria-expanded", "false");
			expect(row).toHaveTextContent("pstack:poteto-mode");
			expect(row).toHaveTextContent("# Goal");
			expect(screen.queryByText("Rules")).toBeNull();
			expect(screen.getByText("thanks")).toBeInTheDocument();
			await user.click(row);
			expect(screen.getByRole("heading", { name: "Rules" })).toBeInTheDocument();
			expect(screen.getByText("flaky").tagName).toBe("STRONG");
		});

		it("shows the first input after a restore as an ordinary message", () => {
			const { feed } = renderPane();
			feed(spawn("claude-session-1"), userIn("u1", "go on with the plan"), lifecycle("u1", "completed"));
			expect(screen.queryByRole("button", { name: /Task brief/ })).toBeNull();
			expect(screen.getByText("go on with the plan")).toBeInTheDocument();
		});
	});

	describe("a turn that ends with background work still running", () => {
		const turnWithBackground = (finished: boolean): ChatEntry[] => [
			userIn("u1", "run the soak test"),
			toolUse("bg", "Bash", { command: "go test -run Soak ./...", run_in_background: true }),
			out({
				type: "system",
				subtype: "task_started",
				task_id: "t1",
				tool_use_id: "bg",
				task_type: "local_bash",
				is_backgrounded: true,
				description: "Run the soak test",
			}),
			toolDone("bg", "Command running in background with ID: t1."),
			saying("started it"),
			out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 }),
			...(finished
				? [out({ type: "system", subtype: "task_notification", task_id: "t1", tool_use_id: "bg", status: "completed" })]
				: []),
		];

		it("says what claude waits on, and opens to the command", async () => {
			const { feed, user } = renderPane();
			feed(...turnWithBackground(false));
			const row = document.querySelector("[data-waiting] .chat-group__head")!;
			expect(row).toHaveTextContent('Waiting on background command "Run the soak test"');
			await user.click(row);
			expect(document.querySelector("[data-waiting]")).toHaveTextContent("$ go test -run Soak ./...");
		});

		it("shows no waiting row once the background task has ended", () => {
			const { feed } = renderPane();
			feed(...turnWithBackground(true));
			expect(document.querySelector("[data-waiting]")).toBeNull();
		});

		it("shows no waiting row for a turn that left nothing running", () => {
			const { feed } = renderPane();
			feed(
				userIn("u1", "fix it"),
				toolUse("a", "Bash", { command: "go test" }),
				toolDone("a"),
				saying("fixed"),
				out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 }),
			);
			expect(document.querySelector("[data-waiting]")).toBeNull();
		});
	});

	it("shows a finished agent's task message as a quiet card that opens to its summary", async () => {
		const { feed, user } = renderPane();
		const notification = [
			"<task-notification>",
			"<task-id>a1</task-id>",
			"<tool-use-id>agent</tool-use-id>",
			"<status>completed</status>",
			'<summary>Agent "Summarize docs directory" finished</summary>',
			"<result>Two short Markdown files.</result>",
			"</task-notification>",
		].join("\n");
		feed(
			userIn("u1", "go"),
			toolUse("agent", "Agent", { description: "Summarize docs directory", subagent_type: "Explore" }),
			out({
				type: "system",
				subtype: "task_started",
				task_id: "a1",
				tool_use_id: "agent",
				task_type: "local_agent",
				description: "Summarize docs directory",
			}),
			toolDone("agent", "Async agent launched"),
			out({ type: "user", isReplay: true, message: { role: "user", content: notification } }),
		);
		const card = document.querySelector("[data-task-message]")!;
		const head = card.querySelector("button")!;
		expect(head).toHaveTextContent(/^Agent finished\s*Summarize docs directory\s*\d\d:\d\d$/);
		expect(head).toHaveAttribute("aria-expanded", "false");
		expect(card).not.toHaveTextContent("Two short Markdown files.");
		await user.click(head);
		expect(card).toHaveTextContent('Agent "Summarize docs directory" finished');
		expect(card).toHaveTextContent("Two short Markdown files.");
	});

	it("says a failed background command's task message failed, in red", () => {
		const { feed } = renderPane();
		feed(
			userIn("u1", "go"),
			out({
				type: "user",
				isReplay: true,
				message: {
					role: "user",
					content:
						'<task-notification>\n<task-id>k1</task-id>\n<status>failed</status>\n<summary>Background command "Run slow check" failed with exit code 1</summary>\n</task-notification>',
				},
			}),
		);
		const head = document.querySelector("[data-task-message] button")!;
		expect(head).toHaveTextContent(/^Task message received\s*Run slow check\s*Failed/);
		expect(head.querySelector(".chat-task__failed")).toHaveTextContent("Failed");
	});

	it("ends a finished turn's reply with a bar that copies it and shows the turn's usage and end time", async () => {
		const write = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		const { feed, user } = renderPane();
		feed(
			userIn("u1", "fix it"),
			lifecycle("u1", "started"),
			toolUse("a", "Bash", { command: "go test" }),
			toolDone("a"),
			saying("**All fixed.**"),
			out({
				type: "result",
				subtype: "success",
				num_turns: 1,
				total_cost_usd: 0.01,
				usage: {
					input_tokens: 1200,
					output_tokens: 800,
					cache_read_input_tokens: 10000,
					cache_creation_input_tokens: 300,
				},
			}),
		);
		const bar = document.querySelector("[data-answer-bar]")!;
		expect(bar).toHaveTextContent(/Usage 12.3k tok\s*\d\d:\d\d$/);
		await user.click(within(bar as HTMLElement).getByRole("button", { name: "Copy reply" }));
		expect(write).toHaveBeenCalledWith("**All fixed.**");
	});

	it("draws no answer bar while a turn runs or for a turn with no reply", () => {
		const { feed } = renderPane();
		feed(userIn("u1", "go"), lifecycle("u1", "started"), saying("working on it"));
		expect(document.querySelector("[data-answer-bar]")).toBeNull();
		feed(toolUse("a", "Bash", { command: "go test" }), toolDone("a"), out({ type: "result", subtype: "success" }));
		expect(document.querySelector("[data-answer-bar]")).not.toBeNull();
		feed(userIn("u2", "again"), lifecycle("u2", "started"), out({ type: "result", subtype: "success" }));
		expect(document.querySelectorAll("[data-answer-bar]")).toHaveLength(1);
	});

	it("leaves a subagent's text and thinking out of the main column", () => {
		const { feed } = renderPane();
		feed(
			userIn("u1", "go"),
			toolUse("agent", "Agent", { description: "look" }),
			out({
				type: "assistant",
				parent_tool_use_id: "agent",
				message: { id: "s", content: [{ type: "thinking", thinking: "sub thought" }] },
			}),
			saying("sub answer", "agent"),
			saying("main answer"),
		);
		expect(screen.getByText("main answer")).toBeInTheDocument();
		expect(screen.queryByText("sub answer")).toBeNull();
		expect(screen.queryByRole("button", { name: /Thought/ })).toBeNull();
	});

	it("reads a background command from its task, and counts it on the status line", async () => {
		const { feed, user } = renderPane();
		feed(
			userIn("u1", "go"),
			toolUse("bg", "Bash", { command: "./slow.sh", run_in_background: true }),
			out({
				type: "system",
				subtype: "task_started",
				task_id: "k1",
				tool_use_id: "bg",
				task_type: "local_bash",
				is_backgrounded: true,
			}),
			toolDone("bg", "Command running in background with ID: k1"),
		);
		expect(document.querySelector('[data-tool="Bash"]')).toHaveTextContent("running in background");
		const statusLine = document.querySelector(".chat-statusline") as HTMLElement;
		const chip = within(statusLine).getByText("1 running in background");
		expect(within(statusLine).queryByRole("button", { name: /running in background/ })).toBeNull();
		await user.click(chip);
		expect(useUiStore.getState().inspectorViewRequest).toBeNull();

		feed(
			out({
				type: "system",
				subtype: "task_notification",
				task_id: "k1",
				tool_use_id: "bg",
				status: "completed",
				summary: "Background command completed (exit code 0)",
			}),
		);
		expect(document.querySelector('[data-tool="Bash"]')).toHaveTextContent("done · exit 0");
		expect(screen.queryByText(/running in background/)).toBeNull();
	});
});

describe("ChatPane composer footer and todo bar", () => {
	const rateLimit = (ts: string, fiveHour: number) => ({
		...out({
			type: "rate_limit_event",
			rate_limit_info: {
				unifiedWindows: { five_hour: { utilization: fiveHour, resetsAt: 1793428800, status: "allowed" } },
			},
		}),
		ts,
	});
	const usageAt = (updatedAt: string, fiveHour: number): SubscriptionUsage => ({
		fiveHour: { utilization: fiveHour, resetsAt: "2026-10-31T06:40:00Z", status: "allowed" },
		updatedAt,
	});
	const ctxAnswer = (percentage: number) =>
		out({
			type: "control_response",
			response: {
				subtype: "success",
				request_id: "ctx-1",
				response: {
					categories: [{ id: "messages", name: "Messages", tokens: 30_000, kind: "used" }],
					totalTokens: 30_000,
					maxTokens: 1_000_000,
					percentage,
				},
			},
		});
	const assistantUsage = () =>
		out({
			type: "assistant",
			message: {
				id: "m-u",
				content: [{ type: "text", text: "hi" }],
				usage: { input_tokens: 10, cache_read_input_tokens: 41_990 },
			},
		});
	const meter = (name: RegExp) => screen.queryByRole("button", { name });

	it("shows nothing in the footer before any context or usage is known", () => {
		renderPane();
		expect(meter(/Context window/)).toBeNull();
		expect(meter(/Subscription usage/)).toBeNull();
	});

	it("shows the exact context percent once claude answers, and the estimate before that", () => {
		const { feed } = renderPane();
		feed(assistantUsage());
		expect(meter(/Context window/)).toHaveTextContent(/^~42k$/);
		feed(ctxAnswer(3));
		expect(meter(/Context window/)).toHaveTextContent(/^3%$/);
	});

	it("shows the daemon's account usage, and the session's own when it is newer", () => {
		sharedUsage = usageAt("2026-10-31T04:00:00Z", 0.2);
		const { feed } = renderPane();
		expect(meter(/Subscription usage/)).toHaveTextContent(/^5h 20%$/);
		feed(rateLimit("2026-10-31T03:00:00.000Z", 0.1));
		expect(meter(/Subscription usage/)).toHaveTextContent(/^5h 20%$/);
		feed(rateLimit("2026-10-31T05:00:00.000Z", 0.3));
		expect(meter(/Subscription usage/)).toHaveTextContent(/^5h 30%$/);
	});

	it("puts the todo bar right above the composer", () => {
		const { feed } = renderPane();
		expect(screen.queryByRole("button", { name: /To-dos/ })).toBeNull();
		feed(
			toolUse("w", "TodoWrite", { todos: [{ content: "Read", activeForm: "Reading", status: "in_progress" }] }),
			toolDone("w"),
		);
		const bar = screen.getByRole("button", { name: /To-dos/ });
		expect(bar).toHaveTextContent("1 in progress");
		const composer = screen.getByRole("textbox", { name: "Message Claude" });
		expect(bar.compareDocumentPosition(composer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});
});

describe("ChatPane visuals", () => {
	const WIDGET = "mcp__ao__show_widget";
	const draw = (id: string, title: string) => toolUse(id, WIDGET, { title, widget_code: "<svg></svg>" });
	const frameOf = (title: string) => screen.getByTitle(title) as HTMLIFrameElement;
	const fromFrame = (title: string, data: unknown) =>
		act(() => {
			window.dispatchEvent(new MessageEvent("message", { data, source: frameOf(title).contentWindow }));
		});
	const composer = () => screen.getByRole("textbox", { name: "Message Claude" });

	it("draws a show_widget call as a visual instead of a tool card", () => {
		const { feed } = renderPane();
		feed(userIn("u1", "draw it"), draw("w1", "flow"), toolDone("w1", "Shown to the user."));
		expect(frameOf("flow").getAttribute("sandbox")).toBe("allow-scripts");
		expect(document.querySelector('[data-tool="mcp__ao__show_widget"]')).toBeNull();
	});

	it("puts a prompt from the frame in the composer and sends nothing", async () => {
		const { feed, api } = renderPane();
		feed(userIn("u1", "draw it"), draw("w1", "flow"), toolDone("w1"));
		fromFrame("flow", { type: "ao-widget/prompt", text: "Why does the curve bend?" });
		await settle();
		expect(composer()).toHaveValue("Why does the curve bend?");
		expect(composer()).toHaveFocus();
		expect(api.send).not.toHaveBeenCalled();
	});

	it("puts the same prompt in again after the user cleared the box", async () => {
		const { feed, user } = renderPane();
		feed(userIn("u1", "draw it"), draw("w1", "flow"), toolDone("w1"));
		fromFrame("flow", { type: "ao-widget/prompt", text: "Again" });
		await settle();
		await user.clear(composer());
		fromFrame("flow", { type: "ao-widget/prompt", text: "Again" });
		await settle();
		expect(composer()).toHaveValue("Again");
	});

	it("ignores a prompt that did not come from a visual's frame", async () => {
		const { feed, api } = renderPane();
		feed(userIn("u1", "draw it"), draw("w1", "flow"), toolDone("w1"));
		act(() => {
			window.dispatchEvent(
				new MessageEvent("message", { data: { type: "ao-widget/prompt", text: "spoof" }, source: window }),
			);
		});
		await settle();
		expect(composer()).toHaveValue("");
		expect(api.send).not.toHaveBeenCalled();
	});

	it("draws a failed call as a tool card, not a frame", () => {
		const { feed } = renderPane();
		feed(
			userIn("u1", "draw it"),
			draw("w1", "flow"),
			toolDone("w1", "widget_code is 200000 bytes; the limit is 131072.", true),
		);
		expect(screen.queryByTitle("flow")).toBeNull();
		expect(document.querySelector('[data-tool="mcp__ao__show_widget"]')).not.toBeNull();
	});

	it("keeps a visual on screen after its turn folds", () => {
		const { feed } = renderPane();
		feed(
			userIn("u1", "draw it"),
			lifecycle("u1", "started"),
			toolUse("r1", "Read", { file_path: "a.go" }),
			toolDone("r1"),
			toolUse("r2", "Bash", { command: "ls" }),
			toolDone("r2"),
			draw("w1", "flow"),
			toolDone("w1"),
			saying("Here it is."),
			out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 }),
		);
		expect(document.querySelector("[data-turn]")).not.toBeNull();
		expect(frameOf("flow")).toBeInTheDocument();
	});
});

describe("ChatPane chapters, tasks and wrap-up", () => {
	const mark = (id: string, title: string) => [
		toolUse(id, "mcp__ao__mark_chapter", { title, summary: `about ${title}` }),
		toolDone(id, "Chapter marked."),
	];
	const wrapUp = (id: string, replies: string[]) => [
		toolUse(id, "mcp__ao__wrap_up", { recap: `Recap ${id}`, ask: "Commit these changes?", replies }),
		toolDone(id, "Box shown."),
	];
	const composer = () => screen.getByRole("textbox", { name: "Message Claude" });

	it("draws a chapter as a divider instead of a tool card", () => {
		const { feed } = renderPane();
		feed(userIn("u1", "go"), ...mark("c1", "Codebase exploration"));
		const divider = document.querySelector("[data-chapter]");
		expect(divider).toHaveTextContent("Codebase exploration");
		expect(divider).toHaveAttribute("title", "about Codebase exploration");
		expect(document.querySelector('[data-tool="mcp__ao__mark_chapter"]')).toBeNull();
	});

	it("keeps a chapter divider on screen after its turn folds", () => {
		const { feed } = renderPane();
		feed(
			userIn("u1", "go"),
			lifecycle("u1", "started"),
			...mark("c1", "Explore"),
			toolUse("r1", "Read", { file_path: "a.go" }),
			toolDone("r1"),
			toolUse("r2", "Bash", { command: "ls" }),
			toolDone("r2"),
			saying("Done."),
			out({ type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0.01 }),
		);
		expect(document.querySelector("[data-turn]")).not.toBeNull();
		expect(document.querySelector("[data-chapter]")).toHaveTextContent("Explore");
	});

	it("offers the chapters in a table of contents once there are two", () => {
		const { feed } = renderPane();
		feed(userIn("u1", "go"), ...mark("c1", "Explore"));
		expect(screen.queryByRole("navigation", { name: "Chapters" })).toBeNull();
		feed(saying("x"), ...mark("c2", "Build"));
		expect(screen.getByRole("navigation", { name: "Chapters" })).toBeInTheDocument();
	});

	it("shows the todos once, in the bar above the composer, not again in the stream", () => {
		const { feed } = renderPane();
		feed(
			userIn("u1", "go"),
			toolUse("t1", "TaskCreate", { subject: "Read the fold", description: "d" }),
			toolDone("t1", "Task #1 created successfully: Read the fold"),
			toolUse("t2", "TaskCreate", { subject: "Add the frame", description: "d" }),
			toolDone("t2", "Task #2 created successfully: Add the frame"),
			toolUse("t3", "TaskUpdate", { taskId: "1", status: "completed" }),
			toolDone("t3"),
		);
		expect(screen.getByRole("button", { name: /To-dos/ })).toHaveTextContent("1 completed · 1 pending");
		expect(screen.getByTestId("chat-stream")).not.toHaveTextContent("Read the fold");
	});

	it("fills the composer from a wrap-up reply and sends nothing", async () => {
		const { feed, api, user } = renderPane();
		feed(userIn("u1", "go"), ...wrapUp("w1", ["Yes, commit", "Not yet"]));
		expect(screen.getByText("Commit these changes?")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Yes, commit" }));
		await settle();
		expect(composer()).toHaveValue("Yes, commit");
		expect(api.send).not.toHaveBeenCalled();
	});

	it("keeps the buttons of an older wrap-up disabled and the latest one live", async () => {
		const { feed, api, user } = renderPane();
		feed(userIn("u1", "go"), ...wrapUp("w1", ["Old reply"]), userIn("u2", "next"), ...wrapUp("w2", ["New reply"]));
		const old = screen.getByRole("button", { name: "Old reply" });
		expect(old).toHaveAttribute("aria-disabled", "true");
		await user.click(old);
		await settle();
		expect(composer()).toHaveValue("");
		expect(screen.getByRole("button", { name: "New reply" })).not.toHaveAttribute("aria-disabled");
		expect(api.send).not.toHaveBeenCalled();
	});
});
