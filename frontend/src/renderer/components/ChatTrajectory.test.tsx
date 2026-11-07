import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ModelContext } from "../lib/chat-api";
import type { ChatEntry } from "../lib/chat-event";
import { foldEntries } from "../lib/chat-fold";
import { centerViewOf, useUiStore } from "../stores/ui-store";
import { ChatTrajectory } from "./ChatTrajectory";

let seq = 0;
const at = (sec: number) => new Date(Date.UTC(2026, 9, 30, 10, 0, sec)).toISOString();
const e = (sec: number, dir: ChatEntry["dir"], payload: object): ChatEntry => ({
	seq: ++seq,
	ts: at(sec),
	dir,
	payload,
});
const use = (sec: number, id: string, name: string, input: object, parent: string | null = null) =>
	e(sec, "out", {
		type: "assistant",
		parent_tool_use_id: parent,
		message: { id: `m-${id}`, content: [{ type: "tool_use", id, name, input }] },
	});
const done = (sec: number, id: string, content: string, isError = false, parent: string | null = null) =>
	e(sec, "out", {
		type: "user",
		parent_tool_use_id: parent,
		message: { content: [{ type: "tool_result", tool_use_id: id, content, is_error: isError }] },
	});

const LONG_RESULT = Array.from({ length: 40 }, (_, i) => `result line ${i + 1}`).join("\n");

function log(): ChatEntry[] {
	seq = 0;
	return [
		e(0, "in", { type: "user", uuid: "u1", message: { content: "Fix the bug" } }),
		e(0, "out", { type: "command_lifecycle", command_uuid: "u1", state: "started" }),
		use(1, "t1", "Read", { file_path: "/w/a.go" }),
		done(2, "t1", LONG_RESULT),
		use(3, "t2", "Agent", { subagent_type: "Explore", description: "scan" }),
		use(4, "t3", "Grep", { pattern: "foo", path: "/w" }, "t2"),
		done(5, "t3", "Found 2 files", false, "t2"),
		done(6, "t2", "scanned"),
		use(7, "t4", "Bash", { command: "go test ./..." }),
		done(8, "t4", "Exit code 1\nFAIL ledger", true),
		e(9, "out", { type: "result", subtype: "success", is_error: false, total_cost_usd: 0.2 }),
	];
}

function renderTrajectory(entries = log()) {
	const transcript = foldEntries(entries);
	return render(<ChatTrajectory entries={entries} root="/w" transcript={transcript} />);
}

const rows = () => [...document.querySelectorAll<HTMLElement>(".chat-traj__row")];
const rowOf = (text: string) => rows().find((r) => r.textContent?.includes(text))!;

beforeEach(() => {
	seq = 0;
});
afterEach(() => vi.restoreAllMocks());

describe("ChatTrajectory", () => {
	it("draws one line per step, with the Agent's calls folded under a count", () => {
		renderTrajectory();
		expect(rows().map((r) => r.getAttribute("data-kind"))).toEqual(["user", "tool", "tool", "tool", "turn_end"]);
		expect(rowOf("Agent")).toHaveTextContent("1 call");
		expect(screen.queryByText(/"foo" in \/w/)).toBeNull();
		expect(rowOf("Bash")).toHaveAttribute("data-tone", "error");
		expect(rowOf("Fix the bug").querySelector(".chat-traj__gutter")).toHaveTextContent("Turn 1");
	});

	it("opens the Agent's calls in place and shows them indented", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		await user.click(screen.getByRole("button", { name: "Show 1 sub-agent call" }));
		expect(rowOf("Grep")).toHaveClass("chat-traj__row--nested");
		await user.click(screen.getByRole("button", { name: "Hide 1 sub-agent call" }));
		expect(screen.queryByText(/"foo" in \/w/)).toBeNull();
	});

	it("opens a row to its full input and a capped result, which Show all expands", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		expect(screen.queryByTestId("chat-trajectory-detail")).toBeNull();
		await user.click(within(rowOf("Read")).getAllByRole("button")[0]);
		const detail = screen.getByTestId("chat-trajectory-detail");
		expect(detail.querySelector('[data-section="input"]')).toHaveTextContent('"file_path": "/w/a.go"');
		const result = detail.querySelector('[data-section="result"]')!;
		expect(result).toHaveTextContent("result line 12");
		expect(result).not.toHaveTextContent("result line 13");
		await user.click(screen.getByRole("button", { name: /Show all \(28 more lines\)/ }));
		expect(result).toHaveTextContent("result line 40");
		await user.click(within(rowOf("Read")).getAllByRole("button")[0]);
		expect(screen.queryByTestId("chat-trajectory-detail")).toBeNull();
	});

	it("shows a failed result in the danger tone", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		await user.click(within(rowOf("Bash")).getAllByRole("button")[0]);
		expect(screen.getByTestId("chat-trajectory-detail").querySelector('[data-section="result"]')).toHaveClass(
			"chat-traj__pre--error",
		);
	});

	it("links an opened row to the raw log entries it came from", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		await user.click(within(rowOf("Read")).getAllByRole("button")[0]);
		expect(screen.queryByTestId("chat-trajectory-raw")).toBeNull();
		await user.click(screen.getByRole("button", { name: "Raw events" }));
		const raw = screen.getByTestId("chat-trajectory-raw");
		expect([...raw.querySelectorAll("[data-seq]")].map((n) => n.getAttribute("data-seq"))).toEqual(["3", "4"]);
		expect(raw).toHaveTextContent("tool_use");
	});

	it("narrows by search, and Esc clears it", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		const search = screen.getByRole("searchbox", { name: "Search steps" });
		await user.type(search, "go test");
		expect(rows().map((r) => r.getAttribute("data-kind"))).toEqual(["tool"]);
		await user.keyboard("{Escape}");
		expect(search).toHaveValue("");
		expect(rows()).toHaveLength(5);
		await user.type(search, "nothing like it");
		expect(screen.getByText("No steps match.")).toBeInTheDocument();
	});

	it("narrows by the All, Tools, Errors and Turns chips, which count what they hold", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		const group = screen.getByRole("group", { name: "Show steps" });
		expect(within(group).getByRole("button", { name: "Tools 4" })).toBeInTheDocument();
		await user.click(within(group).getByRole("button", { name: "Errors 1" }));
		expect(rows().map((r) => r.getAttribute("data-kind"))).toEqual(["tool"]);
		await user.click(within(group).getByRole("button", { name: "Turns 1" }));
		expect(rows().map((r) => r.getAttribute("data-kind"))).toEqual(["user", "turn_end"]);
		await user.click(within(group).getByRole("button", { name: "All" }));
		expect(rows()).toHaveLength(5);
	});

	it("draws the strip with three lanes and one Tools bar per call, in Steps mode first", () => {
		renderTrajectory();
		const strip = screen.getByTestId("chat-trajectory-strip");
		expect(strip).toHaveTextContent("Input");
		expect(strip).toHaveTextContent("Model");
		expect(strip).toHaveTextContent("Tools");
		expect(strip.querySelectorAll('[data-lane="tools"][data-key]')).toHaveLength(3 + 1);
		expect(screen.getByTestId("chat-trajectory")).toHaveAttribute("data-calls", "4");
		expect(screen.getByRole("button", { name: "Steps" })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("button", { name: "Duration" })).toHaveAttribute("aria-pressed", "false");
		expect(screen.getByTestId("chat-trajectory")).toHaveAttribute("data-axis", "steps");
	});

	it("switches to Duration and back", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		await user.click(screen.getByRole("button", { name: "Duration" }));
		expect(screen.getByRole("button", { name: "Duration" })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("button", { name: "Steps" })).toHaveAttribute("aria-pressed", "false");
		expect(screen.getByTestId("chat-trajectory")).toHaveAttribute("data-axis", "duration");
		await user.click(screen.getByRole("button", { name: "Steps" }));
		expect(screen.getByTestId("chat-trajectory")).toHaveAttribute("data-axis", "steps");
	});

	it("colours each bar by what it is, and a failed tool red", () => {
		renderTrajectory();
		const strip = screen.getByTestId("chat-trajectory-strip");
		const tone = (label: RegExp) => screen.getByRole("button", { name: label }).getAttribute("data-tone");
		expect(tone(/^Input:/)).toBe("input");
		expect(tone(/^Tools: Read/)).toBe("tool");
		expect(tone(/^Tools: Bash go test/)).toBe("error");
		expect(strip.querySelector('button[data-lane="model"]')).toHaveAttribute("data-tone", "model");
	});

	it("marks where a new turn starts with a divider, in both modes", async () => {
		const user = userEvent.setup();
		const entries = [
			...log(),
			e(12, "in", { type: "user", uuid: "u2", message: { content: "again" } }),
			e(12, "out", { type: "command_lifecycle", command_uuid: "u2", state: "started" }),
			use(13, "t5", "Read", { file_path: "/w/b.go" }),
		];
		renderTrajectory(entries);
		expect(screen.getAllByTestId("chat-trajectory-divider")).toHaveLength(1);
		await user.click(screen.getByRole("button", { name: "Duration" }));
		expect(screen.getAllByTestId("chat-trajectory-divider")).toHaveLength(1);
	});

	it("draws no divider for a single turn", () => {
		renderTrajectory();
		expect(screen.queryByTestId("chat-trajectory-divider")).toBeNull();
	});

	it("scrolls a clicked bar's row into view and flashes it", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		const bash = screen.getByRole("button", { name: /^Tools: Bash go test/ });
		const key = bash.getAttribute("data-key")!;
		await user.click(bash);
		expect(document.querySelector(`.chat-traj__row[data-key="${key}"]`)).toHaveAttribute("data-flash");
	});

	it("opens a sub-agent's folded call when its bar is clicked", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		await user.click(screen.getByRole("button", { name: /^Tools: Grep/ }));
		expect(rowOf("Grep")).toHaveClass("chat-traj__row--nested");
		expect(rowOf("Grep")).toHaveAttribute("data-flash");
	});

	it("tells what a bar is when the pointer rests on it", async () => {
		const user = userEvent.setup();
		renderTrajectory();
		await user.hover(screen.getByRole("button", { name: /^Tools: Bash go test/ }));
		const tip = screen.getByRole("tooltip");
		expect(tip).toHaveTextContent("Tools");
		expect(tip).toHaveTextContent("Bash go test ./...");
		expect(tip).toHaveTextContent("Turn 1");
	});

	it("says so when the log holds nothing yet", () => {
		renderTrajectory([]);
		expect(screen.getByText("Nothing in this session's log yet.")).toBeInTheDocument();
	});

	it("shows a running tool with its elapsed time", () => {
		vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
		vi.setSystemTime(Date.UTC(2026, 9, 30, 10, 0, 40));
		const entries = [
			e(0, "in", { type: "user", uuid: "u1", message: { content: "go" } }),
			e(0, "out", { type: "command_lifecycle", command_uuid: "u1", state: "started" }),
			use(30, "t1", "Bash", { command: "sleep 60" }),
		];
		try {
			renderTrajectory(entries);
			expect(rowOf("sleep 60")).toHaveTextContent("running");
			expect(rowOf("sleep 60").querySelector(".chat-traj__meta")).toHaveTextContent("10s");
			act(() => vi.advanceTimersByTime(5000));
			expect(rowOf("sleep 60").querySelector(".chat-traj__meta")).toHaveTextContent("15s");
		} finally {
			vi.useRealTimers();
		}
	});
});

describe("ChatTrajectory with the model's context", () => {
	const context: ModelContext = {
		shapes: [
			{
				ts: at(0),
				system: ["You are a coding agent.", "Run the tests."],
				tools: [
					{ name: "Read", description: "Reads a file.", inputSchema: { type: "object", properties: { path: {} } } },
					{ name: "Bash", description: "Runs a command.\n\nLong detail nobody reads." },
				],
			},
		],
		injections: [
			{
				ts: at(0),
				source: "skill_listing",
				role: "system",
				text: "<system-reminder>\nThe following skills are available:\n- deploy\n</system-reminder>",
				truncated: false,
			},
			{ ts: at(5), source: "task_reminder", role: "system", text: "Use the task tools.", truncated: false },
		],
		truncated: false,
	};

	async function renderWithContext(load = vi.fn(async () => context)) {
		const entries = log();
		const view = render(
			<ChatTrajectory entries={entries} loadContext={load} root="/w" transcript={foldEntries(entries)} />,
		);
		await screen.findByText("Initial System Prompt");
		return { ...view, load, entries };
	}

	it("adds the system prompt first and each injection as a context row in its turn", async () => {
		await renderWithContext();
		expect(rows().map((r) => r.getAttribute("data-kind"))).toEqual([
			"system",
			"user",
			"context",
			"tool",
			"tool",
			"context",
			"tool",
			"turn_end",
		]);
		expect(rowOf("Skill catalog")).toHaveTextContent("The following skills are available:");
		const strip = screen.getByTestId("chat-trajectory-strip");
		expect(strip.querySelectorAll('[data-lane="input"][data-tone="context"]')).toHaveLength(3);
	});

	it("opens a context row in the side pane, with its summary and its raw record", async () => {
		const user = userEvent.setup();
		await renderWithContext();
		await user.click(within(rowOf("Skill catalog")).getAllByRole("button")[0]);
		const pane = screen.getByTestId("chat-trajectory-pane");
		expect(within(pane).getByText("CONTEXT")).toBeInTheDocument();
		expect(pane).toHaveTextContent("Turn 1");
		expect(within(pane).getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "true");
		expect(pane).toHaveTextContent("Skill catalog");
		expect(pane).toHaveTextContent("skill_listing");
		expect(pane.querySelector('[data-section="preview"]')).toHaveTextContent("- deploy");
		expect(screen.queryByTestId("chat-trajectory-detail")).toBeNull();

		await user.click(within(pane).getByRole("tab", { name: "Raw" }));
		const raw = pane.querySelector('[data-section="raw"]')!;
		expect(JSON.parse(raw.textContent!)).toEqual(context.injections[0]);
	});

	it("opens the system row to its prompt and lists the tools by name", async () => {
		const user = userEvent.setup();
		await renderWithContext();
		await user.click(within(rowOf("Initial System Prompt")).getAllByRole("button")[0]);
		const pane = screen.getByTestId("chat-trajectory-pane");
		expect(within(pane).getByText("SYSTEM")).toBeInTheDocument();
		expect(pane).toHaveTextContent("You are a coding agent.");
		await user.click(within(pane).getByRole("tab", { name: "Tools" }));
		const tools = within(pane).getByRole("list", { name: "Tools" });
		expect(
			within(tools)
				.getAllByRole("listitem")
				.map((li) => li.querySelector(".chat-traj__toolname")?.textContent),
		).toEqual(["Read", "Bash"]);
		expect(tools).not.toHaveTextContent("Long detail nobody reads.");
		await user.click(within(tools).getAllByRole("button", { name: "Input schema" })[0]);
		expect(tools).toHaveTextContent('"properties"');
	});

	it("closes the pane with Esc and with the close button", async () => {
		const user = userEvent.setup();
		await renderWithContext();
		await user.click(within(rowOf("Task reminder")).getAllByRole("button")[0]);
		expect(screen.getByTestId("chat-trajectory-pane")).toBeInTheDocument();
		await user.keyboard("{Escape}");
		expect(screen.queryByTestId("chat-trajectory-pane")).toBeNull();
		await user.click(within(rowOf("Task reminder")).getAllByRole("button")[0]);
		await user.click(screen.getByRole("button", { name: "Close" }));
		expect(screen.queryByTestId("chat-trajectory-pane")).toBeNull();
	});

	it("reads the context again when a turn ends, and shows nothing extra when there is none", async () => {
		const load = vi.fn(async (): Promise<ModelContext | null> => null);
		const entries = log().slice(0, -1);
		const { rerender } = render(
			<ChatTrajectory entries={entries} loadContext={load} root="/w" transcript={foldEntries(entries)} />,
		);
		await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
		expect(rows().some((r) => r.getAttribute("data-kind") === "context")).toBe(false);

		load.mockResolvedValue(context);
		const ended = log();
		rerender(<ChatTrajectory entries={ended} loadContext={load} root="/w" transcript={foldEntries(ended)} />);
		await screen.findByText("Initial System Prompt");
		expect(load).toHaveBeenCalledTimes(2);
	});
});

describe("a stored events view", () => {
	it("opens Trajectory, so nobody lands on a view that is gone", () => {
		useUiStore.setState({ chatViews: { s: "events" } });
		expect(centerViewOf({ id: "s", transport: "chat" }, useUiStore.getState().chatViews)).toBe("trajectory");
		useUiStore.setState({ chatViews: {} });
	});
});
