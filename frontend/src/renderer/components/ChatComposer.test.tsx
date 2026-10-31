import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ChatActions } from "../hooks/useChatActions";
import type { ChatResult } from "../lib/chat-api";
import type { ChatCommand, ChatModel } from "../lib/chat-fold";
import { ChatComposer, type Prefill } from "./ChatComposer";

const files = vi.hoisted(() => ({
	hook: vi.fn((_sessionId?: string, _enabled?: boolean) => ({
		isPending: false,
		data: {
			available: true,
			truncated: false,
			paths: ["internal/webhook/sender.go", "internal/webhook/budget.go", "docs/webhooks/retry-policy.md", "README.md"],
		},
	})),
}));

vi.mock("../hooks/useWorkspaceFiles", () => ({ useWorkspaceFiles: files.hook }));

const OK: ChatResult = { ok: true };

const COMMANDS: ChatCommand[] = [
	{ name: "compact", description: "Summarise the conversation", argumentHint: "<instructions>", builtin: true },
	{ name: "review", description: "Review the branch diff", argumentHint: "", builtin: true },
	{ name: "tdd", description: "Write the failing test first", argumentHint: "", builtin: false },
];

const model = (value: string, resolvedModel: string, displayName: string, description = ""): ChatModel => ({
	value,
	resolvedModel,
	displayName,
	description,
	supportsEffort: true,
	supportedEffortLevels: ["low", "medium", "high", "xhigh", "max"],
});

const MODELS: ChatModel[] = [
	model("default", "claude-opus-5-5", "Default (recommended)"),
	model("opus", "claude-opus-5-5", "Opus 5.5", "For complex work and everyday tasks"),
	model("sonnet", "claude-sonnet-5-5", "Sonnet 5.5", "Most efficient for simpler tasks"),
	model("claude-opus-4-8", "claude-opus-4-8", "Opus 4.8"),
];

function renderComposer(over: Partial<ChatActions> = {}) {
	const actions = {
		send: vi.fn<ChatActions["send"]>(async () => OK),
		answer: vi.fn<ChatActions["answer"]>(async () => OK),
		interrupt: vi.fn<ChatActions["interrupt"]>(async () => OK),
		setMode: vi.fn<ChatActions["setMode"]>(async () => OK),
		setModel: vi.fn<ChatActions["setModel"]>(async () => OK),
		taskOutput: vi.fn<ChatActions["taskOutput"]>(async () => null),
		hostDown: false,
		...over,
	};
	const onToast = vi.fn();
	const parentKeys = vi.fn((e: React.KeyboardEvent) => e.defaultPrevented);
	const tree = (prefill?: Prefill) => (
		<div onKeyDown={parentKeys}>
			<ChatComposer
				actions={actions}
				busy={false}
				commands={COMMANDS}
				model="claude-opus-5-5"
				models={MODELS}
				onToast={onToast}
				permissionMode="default"
				prefill={prefill}
				queued={[]}
				sessionId="chat-1"
			/>
		</div>
	);
	const { rerender } = render(tree());
	const input = screen.getByRole("textbox", { name: "Message Claude" });
	return {
		actions,
		onToast,
		parentKeys,
		input,
		user: userEvent.setup(),
		prefill: (next: Prefill) => rerender(tree(next)),
	};
}

const options = () => screen.getAllByRole("option").map((o) => o.textContent);
const selected = () => screen.getByRole("option", { selected: true }).textContent;

describe("ChatComposer slash menu", () => {
	it("opens on a leading slash, groups commands and skills, and filters as you type", async () => {
		const { input, user } = renderComposer();
		await user.type(input, "/");
		const menu = screen.getByRole("listbox", { name: "Commands and skills" });
		expect(within(menu).getByRole("group", { name: "Commands" })).toHaveTextContent("/compact");
		expect(within(menu).getByRole("group", { name: "Skills" })).toHaveTextContent("/tdd");
		expect(options()).toEqual([
			"/compactSummarise the conversation",
			"/reviewReview the branch diff",
			"/tddWrite the failing test first",
		]);

		await user.type(input, "td");
		expect(options()).toEqual(["/tddWrite the failing test first"]);
	});

	it("moves with the arrows, inserts the command on Enter without sending, and shows its argument hint", async () => {
		const { input, user, actions } = renderComposer();
		await user.type(input, "/");
		expect(selected()).toContain("/compact");
		await user.keyboard("{ArrowDown}{ArrowDown}{ArrowDown}");
		expect(selected()).toContain("/compact");
		expect(input).toHaveAttribute("aria-activedescendant", screen.getByRole("option", { selected: true }).id);
		await user.keyboard("{ArrowUp}{Enter}");
		expect(input).toHaveValue("/tdd ");
		expect(screen.queryByRole("listbox")).toBeNull();
		expect(actions.send).not.toHaveBeenCalled();

		await user.clear(input);
		await user.type(input, "/co{Enter}");
		expect(input).toHaveValue("/compact ");
		expect(screen.getByTestId("chat-arg-hint")).toHaveTextContent("<instructions>");
		await user.type(input, "x");
		expect(screen.queryByTestId("chat-arg-hint")).toBeNull();
	});

	it("closes on Escape without letting the pane interrupt, and sends an unknown command as text", async () => {
		const { input, user, actions, parentKeys } = renderComposer();
		await user.type(input, "/zz");
		expect(screen.getByRole("listbox")).toHaveTextContent("No command matches");
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("listbox")).toBeNull();
		expect(parentKeys).toHaveLastReturnedWith(true);
		await user.keyboard("{Enter}");
		expect(actions.send).toHaveBeenCalledWith("/zz", []);
	});

	it("opens nothing for a slash after the start of the message", async () => {
		const { input, user } = renderComposer();
		await user.type(input, "use /tdd");
		expect(screen.queryByRole("listbox")).toBeNull();
	});
});

describe("ChatComposer @ menu", () => {
	it("loads the file index only once a mention starts", async () => {
		files.hook.mockClear();
		const { input, user } = renderComposer();
		expect(files.hook).toHaveBeenLastCalledWith("chat-1", false);
		await user.type(input, "@");
		expect(files.hook).toHaveBeenLastCalledWith("chat-1", true);
		expect(screen.getByRole("listbox", { name: "Files" })).toHaveTextContent("Type to search files");
	});

	it("ranks files fuzzily as you type and inserts the picked path in place", async () => {
		const { input, user, actions } = renderComposer();
		await user.type(input, "Also cover @sendr");
		expect(options()[0]).toBe("internal/webhook/sender.go");
		await user.keyboard("{Enter}");
		expect(input).toHaveValue("Also cover @internal/webhook/sender.go ");
		expect(actions.send).not.toHaveBeenCalled();

		await user.type(input, "and @budget");
		await user.click(screen.getByRole("option", { name: /budget\.go/ }));
		expect(input).toHaveValue("Also cover @internal/webhook/sender.go and @internal/webhook/budget.go ");
		expect(input).toHaveFocus();
	});

	it("opens from the @ button at the caret", async () => {
		const { input, user } = renderComposer();
		await user.type(input, "see");
		await user.click(screen.getByRole("button", { name: "Mention a file" }));
		expect(input).toHaveValue("see @");
		await user.keyboard("readme{Enter}");
		expect(input).toHaveValue("see @README.md ");
	});
});

describe("ChatComposer model picker", () => {
	it("names the model and mode, marks the model in use, and folds the default alias into it", async () => {
		const { user } = renderComposer();
		await user.click(screen.getByRole("button", { name: "Model and mode: Opus 5.5 · Ask before edits" }));
		const opus = await screen.findByRole("menuitemradio", { name: /Opus 5\.5/ });
		expect(opus).toHaveAttribute("aria-checked", "true");
		expect(opus).toHaveTextContent("default");
		expect(screen.getByRole("menuitemradio", { name: /Sonnet 5\.5/ })).toHaveAttribute("aria-checked", "false");
		expect(screen.queryByRole("menuitemradio", { name: /Default \(recommended\)/ })).toBeNull();
		expect(screen.getByTestId("chat-effort-hint")).toHaveTextContent("low · medium · high · xhigh · max");
		expect(screen.getByRole("menuitemradio", { name: "Ask before edits" })).toHaveAttribute("aria-checked", "true");
	});

	it("sends the picked model's value", async () => {
		const { user, actions } = renderComposer();
		await user.click(screen.getByRole("button", { name: /Model and mode/ }));
		await user.click(await screen.findByRole("menuitemradio", { name: /Sonnet 5\.5/ }));
		await act(async () => undefined);
		expect(actions.setModel).toHaveBeenCalledWith("sonnet");
	});

	it("offers pinned versions in a submenu", async () => {
		const { user, actions } = renderComposer();
		await user.click(screen.getByRole("button", { name: /Model and mode/ }));
		(await screen.findByRole("menuitem", { name: "Other versions" })).focus();
		await user.keyboard("{ArrowRight}");
		expect(await screen.findByRole("menuitemradio", { name: /Opus 4\.8/ })).toHaveFocus();
		await user.keyboard("{Enter}");
		await act(async () => undefined);
		expect(actions.setModel).toHaveBeenCalledWith("claude-opus-4-8");
	});

	it("shows claude's refusal", async () => {
		const { user, onToast } = renderComposer({
			setModel: vi.fn(
				async () => ({ ok: false, error: { kind: "rejected", message: "Model 'x' not found" } }) as const,
			),
		});
		await user.click(screen.getByRole("button", { name: /Model and mode/ }));
		await user.click(await screen.findByRole("menuitemradio", { name: /Sonnet 5\.5/ }));
		await act(async () => undefined);
		expect(onToast).toHaveBeenCalledWith("Model 'x' not found");
	});
});

describe("ChatComposer prefill", () => {
	it("fills an empty box, puts the caret at the end and focuses it, without sending", () => {
		const { input, prefill, actions } = renderComposer();
		prefill({ text: "Explain the curve", seq: 1 });
		expect(input).toHaveValue("Explain the curve");
		expect(input).toHaveFocus();
		expect((input as HTMLTextAreaElement).selectionStart).toBe("Explain the curve".length);
		expect(actions.send).not.toHaveBeenCalled();
	});

	it("adds to a draft after a blank line and keeps the draft", async () => {
		const { input, prefill, user, actions } = renderComposer();
		await user.type(input, "Check the budget first");
		prefill({ text: "Explain the curve", seq: 1 });
		expect(input).toHaveValue("Check the budget first\n\nExplain the curve");
		expect((input as HTMLTextAreaElement).selectionStart).toBe((input as HTMLTextAreaElement).value.length);
		expect(actions.send).not.toHaveBeenCalled();
	});

	it("acts once per request: the same seq on a later render does not refill the box", async () => {
		const { input, prefill, user } = renderComposer();
		prefill({ text: "Explain", seq: 1 });
		await user.clear(input);
		prefill({ text: "Explain", seq: 1 });
		expect(input).toHaveValue("");
		prefill({ text: "Explain", seq: 2 });
		expect(input).toHaveValue("Explain");
	});
});
