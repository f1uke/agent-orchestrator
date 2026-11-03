import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatSessionState } from "../hooks/useChatSession";
import type { ChatApi, ChatResult } from "../lib/chat-api";
import type { ChatEntry } from "../lib/chat-event";
import { foldEntries } from "../lib/chat-fold";
import { tasksEntries } from "../lib/chat-mock-tasks";
import { emptyOverlay } from "../lib/chat-stream";
import { useTasksUi } from "../stores/tasks-ui";
import { TasksPanel } from "./TasksPanel";

vi.mock("../hooks/useSessionNames", () => ({ useSessionNames: () => new Map() }));

const SESSION = "tasks-1";
const OK: ChatResult = { ok: true };

const stateOf = (entries: readonly ChatEntry[]): ChatSessionState => ({
	transcript: foldEntries(entries),
	overlay: emptyOverlay,
	entries,
	connection: "live",
	replaying: false,
});

function fakeApi() {
	return {
		send: vi.fn<ChatApi["send"]>(async () => OK),
		answer: vi.fn<ChatApi["answer"]>(async () => OK),
		interrupt: vi.fn<ChatApi["interrupt"]>(async () => OK),
		setMode: vi.fn<ChatApi["setMode"]>(async () => OK),
		setModel: vi.fn<ChatApi["setModel"]>(async () => OK),
		stopTask: vi.fn<ChatApi["stopTask"]>(async () => OK),
		taskOutput: vi.fn<ChatApi["taskOutput"]>(async () => ({
			text: "=== RUN TestSoak\n",
			size: 16,
			truncated: false,
			modifiedAt: "2026-11-03T09:00:00Z",
		})),
		upload: vi.fn<ChatApi["upload"]>(async () => ({ ok: true, file: { path: "/x", name: "x", size: 1 } })),
	};
}

function renderPanel(entries: readonly ChatEntry[] = tasksEntries(), api = fakeApi()) {
	const view = (chat: ChatSessionState) => (
		<TasksPanel chat={chat} chatApi={api} root="/work/project" sessionId={SESSION} theme="dark" />
	);
	const utils = render(view(stateOf(entries)));
	return {
		...utils,
		api,
		user: userEvent.setup(),
		feed: (more: readonly ChatEntry[]) => utils.rerender(view(stateOf([...entries, ...more]))),
	};
}

beforeEach(() => {
	useTasksUi.setState({ bySession: {} });
});
afterEach(cleanup);

const entry = (seq: number, payload: object): ChatEntry => ({
	seq,
	ts: "2026-11-03T09:10:00.000Z",
	dir: "out",
	payload,
});
const lastSeq = () => tasksEntries().length;

describe("TasksPanel list", () => {
	it("lists the running tasks with a stop button each, and the finished one without", () => {
		renderPanel();
		expect(screen.getByRole("heading", { name: "Running 2" })).toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "Finished 1" })).toBeInTheDocument();
		const running = within(screen.getAllByRole("list")[0]).getAllByRole("listitem");
		expect(running.map((li) => li.textContent)).toEqual([
			expect.stringContaining("Run the soak test"),
			expect.stringContaining("Audit the webhook retry paths"),
		]);
		expect(screen.getByRole("button", { name: "Stop Run the soak test" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Stop Audit the webhook retry paths" })).toBeInTheDocument();
		const finished = within(screen.getAllByRole("list")[1]).getByRole("listitem");
		expect(finished).toHaveTextContent("Map the sender callers");
		expect(finished).toHaveTextContent("done");
		expect(within(finished).queryByRole("button", { name: /^Stop/ })).toBeNull();
	});

	it("says so when the session has no background tasks", () => {
		renderPanel([]);
		expect(screen.getByText("No background tasks in this session.")).toBeInTheDocument();
	});

	it("hides the finished tasks on Clear, for this session only, and keeps the running ones", async () => {
		const { user } = renderPanel();
		await user.click(screen.getByRole("button", { name: "Clear finished tasks" }));
		expect(screen.queryByRole("heading", { name: /Finished/ })).toBeNull();
		expect(screen.queryByText("Map the sender callers")).toBeNull();
		expect(screen.getByRole("heading", { name: "Running 2" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Clear finished tasks" })).toBeDisabled();
		cleanup();
		render(<TasksPanel chat={stateOf(tasksEntries())} chatApi={fakeApi()} root="" sessionId="other" theme="dark" />);
		expect(screen.getByRole("heading", { name: "Finished 1" })).toBeInTheDocument();
	});
});

describe("TasksPanel drill-in", () => {
	it("opens a subagent to its prompt, its model and its own conversation, read-only, and goes back to the list", async () => {
		const { user } = renderPanel();
		await user.click(screen.getByRole("button", { name: /^Audit the webhook retry paths/ }));
		expect(screen.getByRole("heading", { name: "Audit the webhook retry paths" })).toBeInTheDocument();
		expect(screen.getByText(/Audit the retry paths of the webhook sender/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show more" })).toBeInTheDocument();
		expect(screen.getByText("Model Sonnet 5")).toBeInTheDocument();
		expect(screen.getByText(/The sender retries on a fixed two second sleep/)).toBeInTheDocument();
		expect(screen.getByText(/Ran a command, read a file, ran a search/)).toBeInTheDocument();
		expect(screen.queryByRole("textbox")).toBeNull();
		expect(screen.getByRole("status", { name: "Running" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Back to the task list" }));
		expect(screen.getByRole("heading", { name: "Running 2" })).toBeInTheDocument();
	});

	it("folds a long prompt to a few lines, and shows all of it on Show more", async () => {
		const { user } = renderPanel();
		await user.click(screen.getByRole("button", { name: /^Audit the webhook retry paths/ }));
		const text = document.querySelector(".tasks-panel__prompt-text")!;
		expect(text).toHaveClass("tasks-panel__prompt-text--folded");
		await user.click(screen.getByRole("button", { name: "Show more" }));
		expect(text).not.toHaveClass("tasks-panel__prompt-text--folded");
		await user.click(screen.getByRole("button", { name: "Show less" }));
		expect(text).toHaveClass("tasks-panel__prompt-text--folded");
	});

	it("has no Show more for a short prompt", async () => {
		const { user } = renderPanel();
		await user.click(screen.getByRole("button", { name: /^Map the sender callers/ }));
		expect(
			screen.getByText("Find every caller of Sender.Deliver and report file and line for each."),
		).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
		expect(screen.queryByRole("status", { name: "Running" })).toBeNull();
	});

	it("opens a background command to its command and the polled output", async () => {
		const { user, api } = renderPanel();
		await user.click(screen.getByRole("button", { name: /^Run the soak test/ }));
		expect(screen.getByText(/\$ go test .*-run Soak/)).toBeInTheDocument();
		await waitFor(() => expect(api.taskOutput).toHaveBeenCalledWith(SESSION, "tasks-soak"));
		expect(await screen.findByText(/=== RUN TestSoak/)).toBeInTheDocument();
	});

	it("opens on the job a Ran agent row named, by its call id", () => {
		useTasksUi.getState().focus(SESSION, "t-agent-run");
		renderPanel();
		expect(screen.getByRole("heading", { name: "Audit the webhook retry paths" })).toBeInTheDocument();
	});

	it("says so for a job the log no longer holds", () => {
		useTasksUi.getState().focus(SESSION, "gone");
		renderPanel();
		expect(screen.getByText("This task is no longer in the session log.")).toBeInTheDocument();
	});
});

describe("TasksPanel stop", () => {
	it("sends the stop for the task id, shows Stopping, then lists the task as finished once claude ends it", async () => {
		const { user, api, feed } = renderPanel();
		await user.click(screen.getByRole("button", { name: "Stop Run the soak test" }));
		expect(api.stopTask).toHaveBeenCalledWith(SESSION, "tasks-soak");
		expect(screen.getByRole("status")).toHaveTextContent("Stopping…");
		expect(screen.queryByRole("button", { name: "Stop Run the soak test" })).toBeNull();
		const n = lastSeq();
		feed([
			entry(n + 1, { type: "system", subtype: "task_updated", task_id: "tasks-soak", patch: { status: "killed" } }),
			entry(n + 2, {
				type: "system",
				subtype: "task_notification",
				task_id: "tasks-soak",
				tool_use_id: "t-bash-soak",
				status: "stopped",
				summary: "Stopped by the user",
			}),
		]);
		expect(screen.getByRole("heading", { name: "Running 1" })).toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "Finished 2" })).toBeInTheDocument();
		expect(screen.queryByText("Stopping…")).toBeNull();
		expect(within(screen.getAllByRole("list")[1]).getByText("Run the soak test")).toBeInTheDocument();
		expect(within(screen.getAllByRole("list")[1]).getByText("stopped")).toBeInTheDocument();
	});

	it("shows claude's refusal inline and keeps the button so it can be pressed again", async () => {
		const api = fakeApi();
		api.stopTask.mockResolvedValueOnce({
			ok: false,
			error: { kind: "rejected", message: "stop_task is not supported in this context" },
		});
		const { user } = renderPanel(tasksEntries(), api);
		await user.click(screen.getByRole("button", { name: "Stop Audit the webhook retry paths" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("stop_task is not supported in this context");
		expect(screen.getByRole("button", { name: "Stop Audit the webhook retry paths" })).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Stop Audit the webhook retry paths" }));
		expect(api.stopTask).toHaveBeenCalledTimes(2);
	});

	it("stops from inside the drill-in, and shows the error there", async () => {
		const api = fakeApi();
		api.stopTask.mockResolvedValueOnce({ ok: false, error: { kind: "failed", message: "Failed to fetch" } });
		const { user } = renderPanel(tasksEntries(), api);
		await user.click(screen.getByRole("button", { name: /^Run the soak test/ }));
		await user.click(screen.getByRole("button", { name: "Stop Run the soak test" }));
		expect(api.stopTask).toHaveBeenCalledWith(SESSION, "tasks-soak");
		expect(await screen.findByRole("alert")).toHaveTextContent("Failed to fetch");
	});
});

describe("TasksPanel expand", () => {
	it("flips the session's expanded flag, which the session view turns into the inspector's width", async () => {
		const { user } = renderPanel();
		await user.click(screen.getByRole("button", { name: "Widen the panel" }));
		expect(useTasksUi.getState().bySession[SESSION].expanded).toBe(true);
		await user.click(screen.getByRole("button", { name: "Restore the panel width" }));
		expect(useTasksUi.getState().bySession[SESSION].expanded).toBe(false);
	});
});

describe("the same panel on a log with a nested agent", () => {
	it("opens a nested Ran agent row inside the subagent view on that subagent", async () => {
		const nested: ChatEntry[] = [
			entry(1, {
				type: "assistant",
				parent_tool_use_id: null,
				message: {
					id: "m1",
					content: [{ type: "tool_use", id: "outer", name: "Agent", input: { description: "Outer job", prompt: "p" } }],
				},
			}),
			entry(2, {
				type: "system",
				subtype: "task_started",
				task_id: "to",
				tool_use_id: "outer",
				task_type: "local_agent",
				is_backgrounded: true,
				description: "Outer job",
			}),
			entry(3, {
				type: "assistant",
				parent_tool_use_id: "outer",
				message: {
					id: "m2",
					content: [
						{
							type: "tool_use",
							id: "inner",
							name: "Agent",
							input: { description: "Inner job", prompt: "inner prompt" },
						},
					],
				},
			}),
			entry(4, {
				type: "assistant",
				parent_tool_use_id: "inner",
				message: { id: "m3", content: [{ type: "text", text: "inner says hi" }] },
			}),
		];
		const { user } = renderPanel(nested);
		await user.click(screen.getByRole("button", { name: /^Outer job/ }));
		await user.click(screen.getByRole("button", { name: "Inner job" }));
		expect(screen.getByRole("heading", { name: "Inner job" })).toBeInTheDocument();
		expect(screen.getByText("inner says hi")).toBeInTheDocument();
		await act(async () => undefined);
	});
});
