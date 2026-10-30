import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChatEntry } from "../lib/chat-event";
import { foldEntries } from "../lib/chat-fold";
import { mockChatEntries } from "../lib/chat-mock";
import { ChatInspector } from "./ChatInspector";

function loadFixture(name: string): ChatEntry[] {
	const testdata = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
	return readFileSync(join(testdata, name), "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line) => JSON.parse(line) as ChatEntry);
}

const section = (name: string) => within(screen.getByRole("region", { name }));

describe("ChatInspector", () => {
	it("reads sensibly before anything has happened", () => {
		render(<ChatInspector onJump={() => undefined} transcript={foldEntries([])} />);
		expect(section("Todo").getByText("Claude has not written a todo list yet.")).toBeInTheDocument();
		expect(
			section("Subagents & background").getByText("No subagents or background commands in this session."),
		).toBeInTheDocument();
		expect(section("Context").getByText("Known after Claude's first response.")).toBeInTheDocument();
		expect(section("Cost & usage").getByText("Known after the first turn completes.")).toBeInTheDocument();
		expect(section("Permissions").getByText("unknown")).toBeInTheDocument();
		expect(section("Permissions").getByText("No rules added through Always allow.")).toBeInTheDocument();
		expect(section("History").getByText("No claude process has started yet.")).toBeInTheDocument();
		expect(screen.queryByRole("meter")).toBeNull();
	});

	it("renders every section of the dev stub's session, and a history row jumps to its process", () => {
		const transcript = foldEntries(mockChatEntries(loadFixture("first_session.jsonl")));
		const onJump = vi.fn();
		render(<ChatInspector onJump={onJump} transcript={transcript} />);

		const todo = section("Todo");
		expect(todo.getByText("3 / 6")).toBeInTheDocument();
		const items = todo.getAllByRole("listitem");
		expect(items.map((li) => li.getAttribute("data-status"))).toEqual([
			"completed",
			"completed",
			"completed",
			"in_progress",
			"pending",
			"pending",
		]);
		expect(items[3]).toHaveTextContent("Fix parked-attempt count and rename callers");

		const activity = section("Subagents & background");
		expect(activity.getByText("1 running")).toBeInTheDocument();
		expect(activity.getByText("Bash · background").closest("[data-state]")).toHaveAttribute("data-state", "running");
		expect(activity.getByText("go test -run TestDeliver -count=50 ./internal/webhook")).toBeInTheDocument();
		expect(activity.getByText("Explore").closest("[data-state]")).toHaveTextContent("done · 3 tools");
		expect(activity.getByText("1 done")).toBeInTheDocument();

		expect(section("Context").getByText("124k / 200k")).toBeInTheDocument();
		expect(screen.getByRole("meter", { name: "Context used" })).toHaveAttribute("aria-valuenow", "62");

		const usage = section("Cost & usage");
		expect(usage.getByText("$1.84")).toBeInTheDocument();
		expect(usage.getByText("31.2k")).toBeInTheDocument();
		expect(usage.getByText("1.9M")).toBeInTheDocument();

		expect(section("Permissions").getByText(transcript.permissionMode)).toBeInTheDocument();

		const row = section("History").getByRole("button");
		expect(row).toHaveTextContent("Started");
		expect(row).toHaveTextContent("12e38fc9");
		expect(row).toHaveTextContent("6 turns · session cost $1.84");
		fireEvent.click(row);
		expect(onJump).toHaveBeenCalledWith(0);
		expect(transcript.items[0]).toMatchObject({ kind: "notice", notice: "spawn" });
	});

	it("lists a rule an Always allow added, and shows tokens without a percentage when the window is unknown", () => {
		const entries: ChatEntry[] = [
			{
				seq: 1,
				ts: "",
				dir: "out",
				payload: {
					type: "assistant",
					message: { id: "m1", content: [{ type: "tool_use", id: "t1", name: "Bash", input: {} }] },
				},
			},
			{
				seq: 2,
				ts: "",
				dir: "out",
				payload: {
					type: "control_request",
					request_id: "r1",
					request: { subtype: "can_use_tool", tool_name: "Bash", tool_use_id: "t1", input: {} },
				},
			},
			{
				seq: 3,
				ts: "",
				dir: "in",
				payload: {
					type: "control_response",
					response: {
						subtype: "success",
						request_id: "r1",
						response: {
							behavior: "allow",
							updatedPermissions: [
								{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: "go test:*" }], behavior: "allow" },
							],
						},
					},
				},
			},
			{
				seq: 4,
				ts: "",
				dir: "out",
				payload: {
					type: "result",
					subtype: "success",
					session_id: "s",
					total_cost_usd: 0.5,
					usage: {
						iterations: [{ input_tokens: 2, cache_read_input_tokens: 23_000, cache_creation_input_tokens: 998 }],
					},
				},
			},
		];
		render(<ChatInspector onJump={() => undefined} transcript={foldEntries(entries)} />);
		const permissions = section("Permissions");
		expect(permissions.getByText("Bash(go test:*)")).toBeInTheDocument();
		expect(permissions.getByText("allow")).toHaveAttribute("data-behavior", "allow");
		expect(permissions.queryByText("No rules added through Always allow.")).toBeNull();
		expect(section("Context").getByText("24k tokens")).toBeInTheDocument();
		expect(screen.queryByRole("meter")).toBeNull();
	});
});

describe("ChatInspector on the recorded long session", () => {
	const entries = loadFixture("long_session.jsonl");

	it("lists running work, folds finished work into one count, and never shows a raw task type", () => {
		const running = foldEntries(entries.filter((e) => e.seq <= 25));
		const { unmount } = render(<ChatInspector onJump={() => undefined} transcript={running} />);
		const live = section("Subagents & background");
		expect(live.getByText("1 running")).toBeInTheDocument();
		expect(live.getByText("Explore").closest("[data-state]")).toHaveAttribute("data-state", "running");
		expect(live.getByText("1 done")).toBeInTheDocument();
		unmount();

		render(<ChatInspector onJump={() => undefined} transcript={foldEntries(entries)} />);
		const done = section("Subagents & background");
		const fold = done.getByText("2 done").closest("details")!;
		expect(fold).not.toHaveAttribute("open");
		expect(within(fold).getAllByText(/Bash · background|Explore/)).toHaveLength(2);
		expect(document.body).not.toHaveTextContent("local_bash");
		expect(document.body).not.toHaveTextContent("local_agent");
	});

	it("reads the todo list written with TaskCreate and TaskUpdate", () => {
		render(<ChatInspector onJump={() => undefined} transcript={foldEntries(entries)} />);
		const todo = section("Todo");
		expect(todo.getByText("3 / 3")).toBeInTheDocument();
		expect(todo.getAllByRole("listitem").map((li) => li.textContent)).toEqual([
			"Find failing tests",
			"Fix the cause",
			"Verify and review results",
		]);
	});

	it("shows the context before the first turn has ended", () => {
		const early = foldEntries(entries.filter((e) => e.seq < 99));
		render(<ChatInspector onJump={() => undefined} transcript={early} />);
		expect(section("Context").queryByText(/Known after/)).toBeNull();
		expect(section("Context").getByText(/tokens$/)).toBeInTheDocument();
	});
});
