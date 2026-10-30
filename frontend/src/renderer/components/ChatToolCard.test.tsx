import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Pending, PermissionOutcome, ToolCallItem } from "../lib/chat-fold";
import { PendingRequestCard, READ_ONLY_HINT, ResolvedLine, ToolCard } from "./ChatToolCard";
import { TooltipProvider } from "./ui/tooltip";

const call = (name: string, input: unknown, result?: ToolCallItem["result"], permission?: PermissionOutcome) =>
	({ kind: "tool_call", id: `t-${name}`, name, parentToolUseId: "", input, result, permission }) satisfies ToolCallItem;

function card(item: ToolCallItem, { open, subagentTools = 0 }: { open?: boolean; subagentTools?: number } = {}) {
	const onToggle = vi.fn();
	render(<ToolCard call={item} onToggle={onToggle} open={open} subagentTools={subagentTools} theme="dark" />);
	return { head: screen.getByRole("button"), onToggle };
}

describe("ToolCard", () => {
	it("shows a failed Bash call open, with its exit code, command and output", () => {
		const { head } = card(
			call("Bash", { command: "go test ./..." }, { isError: true, content: "Exit code 1\n--- FAIL: TestX\nok  \tpkg" }),
		);
		expect(head).toHaveAttribute("aria-expanded", "true");
		expect(head).toHaveTextContent("go test ./...");
		expect(head).toHaveTextContent("exit 1");
		expect(screen.getByText("$ go test ./...")).toBeInTheDocument();
		expect(screen.getByText(/--- FAIL: TestX/)).toHaveClass("chat-term__line--fail");
		expect(screen.queryByText(/Exit code 1/)).toBeNull();
	});

	it("keeps a passing Bash call shut until it is toggled", async () => {
		const { head, onToggle } = card(call("Bash", { command: "ls" }, { isError: false, content: "a\nb" }));
		expect(head).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("$ ls")).toBeNull();
		await userEvent.click(head);
		expect(onToggle).toHaveBeenCalledWith(true);
	});

	it("draws an Edit as a diff of old and new strings, open, with its counts", () => {
		const { head } = card(
			call("Edit", { file_path: "a.go", old_string: "one\ntwo\nthree", new_string: "one\n2\nthree\nfour" }),
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

	it("shows any other tool as a generic card with its JSON input", () => {
		card(call("TodoWrite", { todos: [{ content: "ship it" }] }, { isError: false, content: "ok" }), { open: true });
		const body = document.querySelector(".chat-tool__body")!;
		expect(body).toHaveTextContent('"content": "ship it"');
		expect(body).toHaveTextContent("Result");
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

const pending = (kind: Pending["kind"], toolName: string, input: unknown, suggestions: unknown[] = []): Pending => ({
	requestId: "r1",
	toolName,
	toolUseId: "t1",
	kind,
	input,
	suggestions,
	defaultToNo: false,
});

function renderPending(request: Pending) {
	render(
		<TooltipProvider delayDuration={0}>
			<PendingRequestCard request={request} theme="dark" />
		</TooltipProvider>,
	);
}

describe("PendingRequestCard", () => {
	it("shows a permission waiting for an answer, with every answer button disabled and explained", async () => {
		renderPending(
			pending("permission", "Bash", { command: "go test -race ./...", description: "Run the suite" }, [
				{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: "go test:*" }] },
			]),
		);
		expect(screen.getByText("Permission needed · Bash")).toBeInTheDocument();
		expect(screen.getByText("go test -race ./...")).toBeInTheDocument();
		expect(screen.getByText("Run the suite")).toBeInTheDocument();
		expect(screen.getByText("Bash(go test:*)")).toBeInTheDocument();
		for (const name of ["Allow once", "Always allow", "Deny"]) {
			expect(screen.getByRole("button", { name })).toBeDisabled();
		}
		await userEvent.hover(screen.getByTestId("chat-request-actions"));
		expect(await screen.findByRole("tooltip")).toHaveTextContent(READ_ONLY_HINT);
	});

	it("shows a question with its options, one question per tab, and a disabled submit", async () => {
		renderPending(
			pending("question", "AskUserQuestion", {
				questions: [
					{ question: "Which colour?", header: "Colour", options: [{ label: "Red", description: "warm" }] },
					{
						question: "Which snacks?",
						header: "Snacks",
						multiSelect: true,
						options: [{ label: "Nuts", description: "" }],
					},
				],
			}),
		);
		expect(screen.getByText("Claude is asking · 2 questions")).toBeInTheDocument();
		expect(screen.getByText("Which colour?")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("tab", { name: "Snacks" }));
		expect(screen.getByText("Which snacks?")).toBeInTheDocument();
		expect(screen.getByText("Nuts").closest(".chat-opt")).toHaveClass("chat-opt--multi");
		expect(screen.getByRole("button", { name: "Submit answers" })).toBeDisabled();
		expect(screen.getByRole("textbox", { name: "Other answer" })).toBeDisabled();
	});

	it("shows a plan with its markdown and disabled approvals", () => {
		renderPending(
			pending("plan", "ExitPlanMode", { plan: "# Retry budget\n\n1. Add Budget", planFilePath: "~/plans/p.md" }),
		);
		const card = screen.getByText("Plan ready for review").closest(".chat-ask") as HTMLElement;
		expect(within(card).getByText("ExitPlanMode · ~/plans/p.md")).toBeInTheDocument();
		expect(within(card).getByText("Retry budget")).toBeInTheDocument();
		for (const name of ["Approve · auto-accept edits", "Approve · ask before edits", "Keep planning…"]) {
			expect(within(card).getByRole("button", { name })).toBeDisabled();
		}
	});
});
