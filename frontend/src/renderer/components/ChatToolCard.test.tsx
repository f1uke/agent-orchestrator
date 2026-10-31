import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { BackgroundTask, Pending, PermissionOutcome, ToolCallItem } from "../lib/chat-fold";
import { PendingRequestCard, type RequestState, ResolvedLine, ToolCard } from "./ChatToolCard";

const call = (name: string, input: unknown, result?: ToolCallItem["result"], permission?: PermissionOutcome) =>
	({
		kind: "tool_call",
		id: `t-${name}`,
		name,
		parentToolUseId: "",
		input,
		result,
		permission,
		startedTs: "",
	}) satisfies ToolCallItem;

function card(
	item: ToolCallItem,
	{
		open,
		subagentTools = 0,
		root = "",
		task,
	}: { open?: boolean; subagentTools?: number; root?: string; task?: BackgroundTask } = {},
) {
	const onToggle = vi.fn();
	render(
		<ToolCard
			call={item}
			onToggle={onToggle}
			open={open}
			root={root}
			subagentTools={subagentTools}
			task={task}
			theme="dark"
		/>,
	);
	return { head: document.querySelector<HTMLElement>(".chat-tool__head")!, onToggle };
}

describe("ToolCard", () => {
	it("titles a failed Bash call by its description and opens to the command, its exit code and the output", () => {
		const failed = call(
			"Bash",
			{ command: "go test ./...", description: "Run the tests" },
			{ isError: true, content: "Exit code 1\n--- FAIL: TestX\nok  \tpkg" },
		);
		const closed = card(failed).head;
		expect(closed).toHaveAttribute("aria-expanded", "false");
		expect(closed).toHaveTextContent("Run the tests");
		expect(within(closed).getByText("Failed")).toHaveClass("chat-tool__status--error");
		cleanup();
		card(failed, { open: true });
		const body = document.querySelector(".chat-tool__body")! as HTMLElement;
		expect(within(body).getByText("go test ./...")).toBeInTheDocument();
		expect(within(body).getByRole("button", { name: /^Copy command/ })).toBeInTheDocument();
		expect(within(body).getByText("Exit code 1")).toHaveClass("chat-tool__exit");
		expect(within(body).getByText(/--- FAIL: TestX/)).toHaveClass("chat-term__line--fail");
	});

	it("titles a Bash call without a description by its command, and shows no exit line when it passed", () => {
		card(call("Bash", { command: "ls -la" }, { isError: false, content: "a\nb" }), { open: true });
		expect(document.querySelector(".chat-tool__head")).toHaveTextContent("ls -la");
		expect(document.querySelector(".chat-tool__exit")).toBeNull();
	});

	it("keeps a passing Bash call shut until it is toggled", async () => {
		const { head, onToggle } = card(call("Bash", { command: "ls" }, { isError: false, content: "a\nb" }));
		expect(head).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("$ ls")).toBeNull();
		await userEvent.click(head);
		expect(onToggle).toHaveBeenCalledWith(true);
	});

	it("keeps an Edit shut until it is toggled", () => {
		const { head } = card(call("Edit", { file_path: "a.go", old_string: "one", new_string: "two" }));
		expect(head).toHaveAttribute("aria-expanded", "false");
		expect(document.querySelector(".chat-tool__body")).toBeNull();
	});

	it("draws an Edit as a diff of old and new strings, with its counts", () => {
		const { head } = card(
			call("Edit", { file_path: "a.go", old_string: "one\ntwo\nthree", new_string: "one\n2\nthree\nfour" }),
			{ open: true },
		);
		expect(head).toHaveTextContent("a.go");
		expect(head).toHaveTextContent("+2");
		expect(head).toHaveTextContent("−1");
		const body = document.querySelector(".chat-tool__body")!;
		expect(body).toHaveTextContent("two");
		expect(body).toHaveTextContent("four");
	});

	it("draws a Write as a new file and caps its preview", () => {
		const content = Array.from({ length: 50 }, (_, i) => `line ${i + 1}`).join("\n");
		const { head } = card(call("Write", { file_path: "new.go", content }), { open: true });
		expect(head).toHaveTextContent("+50");
		expect(head).toHaveTextContent("new file");
		expect(screen.getByText("… 10 more lines")).toBeInTheDocument();
	});

	it.each([
		["Read", { file_path: "x.go" }, "1\tpackage x\n2\n3", "x.go", "3 lines"],
		["Grep", { pattern: "attempt", path: "internal" }, "Found 7 matches\na\nb", '"attempt" in internal', "7 matches"],
		["Glob", { pattern: "**/*.go" }, "a.go\nb.go", "**/*.go", "2 files"],
	])("reads a %s call as one compact line", (name, input, output, arg, summary) => {
		const { head } = card(call(name, input, { isError: false, content: output }));
		expect(head.closest(".chat-tool")).toHaveClass("chat-tool--compact");
		expect(head).toHaveAttribute("aria-expanded", "false");
		expect(head).toHaveTextContent(arg);
		expect(head).toHaveTextContent(summary);
	});

	it("sums up an Agent call with the tool calls its subagent made", () => {
		const { head } = card(
			call("Agent", { subagent_type: "Explore", description: "find callers" }, { isError: false, content: "3" }),
			{ subagentTools: 9 },
		);
		expect(head).toHaveTextContent('Explore · "find callers"');
		expect(head).toHaveTextContent("done · 9 tool uses");
	});

	it("shortens the worktree root out of the head and the command", () => {
		const { head } = card(call("Bash", { command: "cd /work/project && go test ./..." }), { root: "/work/project" });
		expect(head).toHaveTextContent("cd . && go test ./...");
		expect(head).not.toHaveTextContent("/work/project");
	});

	it("tells a background Bash call's progress from its task, not from its instant result", () => {
		const bash = call(
			"Bash",
			{ command: "./slow.sh", run_in_background: true },
			{ isError: false, content: "Command running in background" },
		);
		const running = card(bash, {
			task: { id: "t", toolUseId: bash.id, description: "", taskType: "local_bash", state: "running" },
		});
		expect(running.head).toHaveTextContent("running in background");
		cleanup();
		const done = card(bash, {
			task: { id: "t", toolUseId: bash.id, description: "", taskType: "local_bash", state: "done", exitCode: 0 },
		});
		expect(done.head).toHaveTextContent("done · exit 0");
	});

	it("keeps an Agent running until its task ends, then shows the task's final text", () => {
		const agent = call(
			"Agent",
			{ subagent_type: "Explore", description: "docs" },
			{ isError: false, content: "Async agent launched successfully." },
		);
		const base = { id: "t", toolUseId: agent.id, description: "", taskType: "local_agent" };
		const running = card(agent, { task: { ...base, state: "running" } });
		expect(running.head).toHaveTextContent("running");
		cleanup();
		const done = card(agent, {
			open: true,
			subagentTools: 2,
			task: { ...base, state: "done", summary: "Two short files." },
		});
		expect(done.head).toHaveTextContent("done · 2 tool uses");
		expect(screen.getByText("Two short files.")).toBeInTheDocument();
		expect(screen.queryByText(/Async agent launched/)).toBeNull();
	});

	it("shows any other tool as a generic card with its JSON input", () => {
		card(call("WebFetch", { url: "https://example.com", note: "ship it" }, { isError: false, content: "ok" }), {
			open: true,
		});
		const body = document.querySelector(".chat-tool__body")!;
		expect(body).toHaveTextContent('"note": "ship it"');
		expect(body).toHaveTextContent("Result");
	});

	it("shows a Read of a PNG as a thumbnail that opens the full image", async () => {
		const png = { type: "image", source: { type: "base64", media_type: "image/png", data: "iVBORw==" } };
		const read = call("Read", { file_path: "/work/project/docs/shot.png" }, { isError: false, content: [png] });
		const user = userEvent.setup();
		const closed = card(read, { root: "/work/project" }).head;
		expect(closed).toHaveTextContent("docs/shot.png");
		expect(closed).toHaveTextContent("image");
		expect(screen.queryByRole("img")).toBeNull();
		cleanup();

		card(read, { open: true, root: "/work/project" });
		const thumb = screen.getByRole("img", { name: "shot.png" });
		expect(thumb).toHaveAttribute("src", "data:image/png;base64,iVBORw==");
		expect(screen.queryByText("(no output)")).toBeNull();

		await user.click(screen.getByRole("button", { name: "View shot.png" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByRole("img", { name: "shot.png" })).toHaveAttribute(
			"src",
			"data:image/png;base64,iVBORw==",
		);
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("dialog")).toBeNull();
	});

	it("marks a call with no result yet as running", () => {
		const { head } = card(call("Read", { file_path: "x.go" }));
		expect(head).toHaveTextContent("running");
	});
});

const settled = (status: PermissionOutcome["status"], extra: Partial<PermissionOutcome> = {}): PermissionOutcome => ({
	requestId: "r1",
	status,
	message: "",
	updatedPermissions: [],
	...extra,
});

describe("ResolvedLine", () => {
	it.each([
		[call("Bash", { command: "ls" }, undefined, settled("allowed")), "Allowed Bash once"],
		[
			call(
				"Bash",
				{},
				undefined,
				settled("allowed", {
					updatedPermissions: [{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: "go test:*" }] }],
				}),
			),
			"Always allowed Bash with Bash(go test:*)",
		],
		[call("Bash", {}, undefined, settled("denied", { message: "not rm" })), "Denied Bash: not rm"],
		[call("Bash", {}, undefined, settled("cancelled")), "Request withdrawn before it was answered"],
		[
			call(
				"AskUserQuestion",
				{ questions: [{ question: "Colour?", header: "Colour", options: [] }] },
				{ isError: false, content: 'Your questions have been answered: "Colour?"="Red".' },
				settled("allowed"),
			),
			"Answered: Colour = Red",
		],
		[call("ExitPlanMode", {}, undefined, settled("denied", { message: "rename it" })), "Plan sent back: rename it"],
	])("says how a settled request ended", (item, text) => {
		const { container } = render(<ResolvedLine call={item} />);
		expect(container).toHaveTextContent(text);
	});

	it("says nothing for a request still pending", () => {
		const { container } = render(<ResolvedLine call={call("Bash", {}, undefined, settled("pending"))} />);
		expect(container).toBeEmptyDOMElement();
	});
});

const pending = (
	kind: Pending["kind"],
	toolName: string,
	input: unknown,
	suggestions: unknown[] = [],
	defaultToNo = false,
): Pending => ({
	requestId: "r1",
	toolName,
	toolUseId: "t1",
	kind,
	input,
	suggestions,
	defaultToNo,
});

const GO_TEST_RULE = {
	type: "addRules",
	rules: [{ toolName: "Bash", ruleContent: "go test:*" }],
	behavior: "allow",
	destination: "localSettings",
};

function renderPending(
	request: Pending,
	{ state, disabled = false }: { state?: RequestState; disabled?: boolean } = {},
) {
	const onAnswer = vi.fn();
	const utils = render(
		<PendingRequestCard disabled={disabled} onAnswer={onAnswer} request={request} state={state} theme="dark" />,
	);
	return { ...utils, onAnswer };
}

const bash = (suggestions: unknown[] = [GO_TEST_RULE], defaultToNo = false) =>
	pending(
		"permission",
		"Bash",
		{ command: "go test -race ./...", description: "Run the suite" },
		suggestions,
		defaultToNo,
	);

describe("PermissionCard", () => {
	it("shows the command, why, and the rule Always allow adds", () => {
		renderPending(bash());
		expect(screen.getByText("Permission needed · Bash")).toBeInTheDocument();
		expect(screen.getByText("go test -race ./...")).toBeInTheDocument();
		expect(screen.getByText("Run the suite")).toBeInTheDocument();
		expect(screen.getByTestId("chat-always-rule")).toHaveTextContent(
			"Always adds Bash(go test:*) to .claude/settings.local.json",
		);
	});

	it("reads every kind of suggestion into the rule line", () => {
		renderPending(
			bash([
				{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: "rm hello.txt" }], destination: "localSettings" },
				{ type: "addDirectories", directories: ["/work/project"], destination: "session" },
				{ type: "setMode", mode: "acceptEdits", destination: "session" },
			]),
		);
		expect(screen.getByTestId("chat-always-rule")).toHaveTextContent(
			"Always adds Bash(rm hello.txt) to .claude/settings.local.json; adds the directory /work/project for this session; switches to acceptEdits mode for this session",
		);
	});

	it("answers Allow once, Always allow and Deny with the matching bodies", async () => {
		const { onAnswer } = renderPending(bash());
		await userEvent.click(screen.getByRole("button", { name: /Allow once/ }));
		await userEvent.click(screen.getByRole("button", { name: /Always allow/ }));
		await userEvent.type(screen.getByRole("textbox", { name: "Reason for denying" }), "use -short");
		await userEvent.click(screen.getByRole("button", { name: /Deny/ }));
		expect(onAnswer.mock.calls).toEqual([
			[{ requestId: "r1", behavior: "allow" }],
			[{ requestId: "r1", behavior: "allow", updatedPermissions: [GO_TEST_RULE] }],
			[{ requestId: "r1", behavior: "deny", message: "use -short" }],
		]);
	});

	it("offers no Always allow when the request suggests nothing", () => {
		renderPending(bash([]));
		expect(screen.queryByRole("button", { name: /Always allow/ })).toBeNull();
		expect(screen.queryByTestId("chat-always-rule")).toBeNull();
	});

	it("takes Enter, Cmd+Enter and Esc only while the card has focus", async () => {
		const { onAnswer } = renderPending(bash());
		await userEvent.keyboard("{Enter}{Escape}");
		expect(onAnswer).not.toHaveBeenCalled();

		const card = screen.getByRole("group", { name: "Permission needed: Bash" });
		card.focus();
		await userEvent.keyboard("{Enter}");
		await userEvent.keyboard("{Meta>}{Enter}{/Meta}");
		await userEvent.keyboard("{Escape}");
		expect(onAnswer.mock.calls).toEqual([
			[{ requestId: "r1", behavior: "allow" }],
			[{ requestId: "r1", behavior: "allow", updatedPermissions: [GO_TEST_RULE] }],
			[{ requestId: "r1", behavior: "deny", message: "" }],
		]);
	});

	it("honours default_to_no: nothing preselects Allow and Enter does not allow", async () => {
		const { onAnswer } = renderPending(bash([GO_TEST_RULE], true));
		const card = screen.getByRole("group", { name: "Permission needed: Bash" });
		expect(card).not.toContainElement(document.activeElement as HTMLElement);
		expect(screen.getByRole("button", { name: /Allow once/ })).not.toHaveTextContent("↵");
		card.focus();
		await userEvent.keyboard("{Enter}");
		await userEvent.keyboard("{Meta>}{Enter}{/Meta}");
		expect(onAnswer).not.toHaveBeenCalled();
		await userEvent.keyboard("{Escape}");
		expect(onAnswer).toHaveBeenCalledWith({ requestId: "r1", behavior: "deny", message: "" });
	});

	it("never focuses Allow on its own, even without default_to_no", () => {
		renderPending(bash());
		expect(document.activeElement).toBe(document.body);
	});

	it("holds Sending… with every input locked while the answer is in flight", async () => {
		const { onAnswer } = renderPending(bash(), { state: { phase: "sending" } });
		expect(screen.getByRole("status")).toHaveTextContent("Sending…");
		for (const name of [/Allow once/, /Always allow/, /Deny/]) {
			expect(screen.getByRole("button", { name })).toBeDisabled();
		}
		screen.getByRole("group", { name: "Permission needed: Bash" }).focus();
		await userEvent.keyboard("{Enter}{Escape}");
		expect(onAnswer).not.toHaveBeenCalled();
	});

	it("shows why the host refused the answer and lets it be answered again", () => {
		renderPending(bash(), { state: { phase: "failed", message: "request r1 is not pending" } });
		expect(screen.getByRole("alert")).toHaveTextContent("request r1 is not pending");
		expect(screen.getByRole("button", { name: /Allow once/ })).toBeEnabled();
	});

	it("locks while the session host is down", () => {
		renderPending(bash(), { disabled: true });
		expect(screen.getByRole("button", { name: /Allow once/ })).toBeDisabled();
		expect(screen.getByRole("textbox", { name: "Reason for denying" })).toBeDisabled();
	});
});

const QUESTIONS = {
	questions: [
		{
			question: "Which colour?",
			header: "Colour",
			options: [
				{ label: "Red", description: "warm", preview: "#f00 swatch" },
				{ label: "Blue", description: "cool", preview: "#00f swatch" },
			],
		},
		{
			question: "Which snacks?",
			header: "Snacks",
			multiSelect: true,
			options: [
				{ label: "Chips", description: "" },
				{ label: "Fruit", description: "" },
				{ label: "Nuts", description: "" },
			],
		},
	],
};

describe("QuestionCard", () => {
	it("submits one answer per question, a multi-select one joined in option order", async () => {
		const { onAnswer } = renderPending(pending("question", "AskUserQuestion", QUESTIONS));
		const submit = screen.getByRole("button", { name: "Submit answers" });
		expect(screen.getByText("Claude is asking · 2 questions")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("radio", { name: /Blue/ }));
		expect(submit).toBeDisabled();
		expect(screen.getByRole("tab", { name: "Colour" })).toHaveClass("chat-qtab--done");

		await userEvent.click(screen.getByRole("tab", { name: "Snacks" }));
		expect(screen.getByText("Which snacks?")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("checkbox", { name: /Nuts/ }));
		await userEvent.click(screen.getByRole("checkbox", { name: /Chips/ }));
		expect(screen.getByRole("checkbox", { name: /Chips/ })).toHaveAttribute("aria-checked", "true");
		await userEvent.click(submit);

		expect(onAnswer).toHaveBeenCalledWith({
			requestId: "r1",
			behavior: "allow",
			updatedInput: { ...QUESTIONS, answers: { "Which colour?": "Blue", "Which snacks?": "Chips, Nuts" } },
		});
	});

	it("takes the Other text as the answer and drops the picked option of a single-select question", async () => {
		const { onAnswer } = renderPending(pending("question", "AskUserQuestion", { questions: [QUESTIONS.questions[0]] }));
		await userEvent.click(screen.getByRole("radio", { name: /Red/ }));
		await userEvent.type(screen.getByRole("textbox", { name: "Other answer" }), "Teal");
		expect(screen.getByRole("radio", { name: /Red/ })).toHaveAttribute("aria-checked", "false");
		await userEvent.click(screen.getByRole("button", { name: "Submit answers" }));
		expect(onAnswer.mock.calls[0][0].updatedInput.answers).toEqual({ "Which colour?": "Teal" });
	});

	it("shows the preview of the option picked, beside the options", async () => {
		renderPending(pending("question", "AskUserQuestion", QUESTIONS));
		const preview = screen.getByTestId("chat-option-preview");
		expect(preview).toHaveTextContent("#f00 swatch");
		await userEvent.click(screen.getByRole("radio", { name: /Blue/ }));
		await userEvent.unhover(screen.getByRole("radio", { name: /Blue/ }));
		expect(preview).toHaveTextContent("Preview · Blue#00f swatch");
		await userEvent.click(screen.getByRole("tab", { name: "Snacks" }));
		expect(screen.queryByTestId("chat-option-preview")).toBeNull();
	});

	it("Reply in chat instead declines with the fixed message", async () => {
		const { onAnswer } = renderPending(pending("question", "AskUserQuestion", QUESTIONS));
		await userEvent.click(screen.getByRole("button", { name: "Reply in chat instead" }));
		expect(onAnswer).toHaveBeenCalledWith({
			requestId: "r1",
			behavior: "deny",
			message: "The user will answer in chat.",
		});
	});
});

describe("PlanCard", () => {
	const plan = () =>
		pending("plan", "ExitPlanMode", { plan: "# Retry budget\n\n1. Add Budget", planFilePath: "~/p.md" });

	it("shows the plan's markdown and where it lives", () => {
		renderPending(plan());
		const card = screen.getByRole("group", { name: "Plan ready for review" });
		expect(within(card).getByText("ExitPlanMode · ~/p.md")).toBeInTheDocument();
		expect(within(card).getByText("Retry budget")).toBeInTheDocument();
	});

	it.each([
		["Approve · auto-accept edits", "acceptEdits"],
		["Approve · ask before edits", "default"],
	])("%s allows the plan, then asks for %s mode", async (name, mode) => {
		const { onAnswer } = renderPending(plan());
		await userEvent.click(screen.getByRole("button", { name }));
		expect(onAnswer).toHaveBeenCalledWith({ requestId: "r1", behavior: "allow" }, mode);
	});

	it("Keep planning… sends the feedback as a deny, and only once there is some", async () => {
		const { onAnswer } = renderPending(plan());
		await userEvent.click(screen.getByRole("button", { name: "Keep planning…" }));
		const send = screen.getByRole("button", { name: "Send feedback" });
		expect(send).toBeDisabled();
		await userEvent.type(screen.getByRole("textbox", { name: "Plan feedback" }), "Name it goodbye.txt");
		await userEvent.click(send);
		expect(onAnswer).toHaveBeenCalledWith({ requestId: "r1", behavior: "deny", message: "Name it goodbye.txt" });
	});
});
