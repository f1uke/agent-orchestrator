import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ChatActions } from "../hooks/useChatActions";
import type { ChatResult, UploadResult } from "../lib/chat-api";
import { type ChatCommand, type ChatModel, type RemoteControl, REMOTE_CONTROL_OFF } from "../lib/chat-fold";
import { stubImageDecoder } from "../test/chat-attach-stub";
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

const saved = (file: File): UploadResult => ({
	ok: true,
	file: { path: `/data/attachments/ab12/${file.name}`, name: file.name, size: file.size },
});

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

function renderComposer(
	over: Partial<ChatActions> = {},
	props: {
		suggestion?: string;
		busy?: boolean;
		bashBusy?: boolean;
		remoteControl?: RemoteControl;
		chrome?: boolean;
		sessionId?: string;
	} = {},
) {
	const actions = {
		send: vi.fn<ChatActions["send"]>(async () => OK),
		answer: vi.fn<ChatActions["answer"]>(async () => OK),
		interrupt: vi.fn<ChatActions["interrupt"]>(async () => OK),
		setMode: vi.fn<ChatActions["setMode"]>(async () => OK),
		setModel: vi.fn<ChatActions["setModel"]>(async () => OK),
		setRemoteControl: vi.fn<ChatActions["setRemoteControl"]>(async () => OK),
		setChrome: vi.fn<ChatActions["setChrome"]>(async () => OK),
		runBash: vi.fn<ChatActions["runBash"]>(async () => ({ ok: true, id: "b1" })),
		stopBash: vi.fn<ChatActions["stopBash"]>(async () => OK),
		stopTask: vi.fn<ChatActions["stopTask"]>(async () => ({ ok: true })),
		taskOutput: vi.fn<ChatActions["taskOutput"]>(async () => null),
		rewind: vi.fn<ChatActions["rewind"]>(async () => ({ ok: true })),
		rewindPreview: vi.fn<ChatActions["rewindPreview"]>(async () => null),
		upload: vi.fn<ChatActions["upload"]>(async (file) => saved(file)),
		hostDown: false,
		...over,
	};
	const live = { ...props };
	const onToast = vi.fn();
	const parentKeys = vi.fn((e: React.KeyboardEvent) => e.defaultPrevented);
	const tree = (prefill?: Prefill, suggestion = live.suggestion ?? "") => (
		<div onKeyDown={parentKeys}>
			<ChatComposer
				actions={actions}
				bashBusy={live.bashBusy ?? false}
				busy={live.busy ?? false}
				chrome={live.chrome ?? false}
				suggestion={suggestion}
				commands={COMMANDS}
				model="claude-opus-5-5"
				models={MODELS}
				remoteControl={live.remoteControl ?? REMOTE_CONTROL_OFF}
				onToast={onToast}
				permissionMode="default"
				prefill={prefill}
				queued={[]}
				sessionId={live.sessionId ?? "chat-1"}
			/>
		</div>
	);
	const { rerender, unmount } = render(tree());
	const input = screen.getByRole("textbox", { name: /^(Message Claude|Shell command)$/ });
	return {
		actions,
		onToast,
		parentKeys,
		input,
		unmount,
		user: userEvent.setup(),
		prefill: (next: Prefill) => rerender(tree(next)),
		suggest: (next: string) => rerender(tree(undefined, next)),
		rerenderWith: (next: typeof props) => {
			Object.assign(live, next);
			rerender(tree());
		},
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

	it("turns Remote Control on from the menu, and off again while it is on", async () => {
		const off = renderComposer();
		await off.user.click(screen.getByRole("button", { name: /Model and mode/ }));
		const toggle = await screen.findByRole("menuitemcheckbox", { name: "Remote Control" });
		expect(toggle).toHaveAttribute("aria-checked", "false");
		await off.user.click(toggle);
		await act(async () => undefined);
		expect(off.actions.setRemoteControl).toHaveBeenCalledWith(true);
	});

	it.each(["connecting", "connected", "reconnecting"] as const)(
		"offers to turn Remote Control off while %s",
		async (state) => {
			const on = renderComposer({}, { remoteControl: { state, url: "", detail: "" } });
			await on.user.click(screen.getByRole("button", { name: /Model and mode/ }));
			const toggle = await screen.findByRole("menuitemcheckbox", { name: "Remote Control" });
			expect(toggle).toHaveAttribute("aria-checked", "true");
			await on.user.click(toggle);
			await act(async () => undefined);
			expect(on.actions.setRemoteControl).toHaveBeenCalledWith(false);
		},
	);

	it("offers a retry after Remote Control failed, and shows claude's refusal", async () => {
		const refused = renderComposer(
			{
				setRemoteControl: vi.fn(
					async () => ({ ok: false, error: { kind: "rejected", message: "Not available for this account" } }) as const,
				),
			},
			{ remoteControl: { state: "failed", url: "", detail: "earlier failure" } },
		);
		await refused.user.click(screen.getByRole("button", { name: /Model and mode/ }));
		const toggle = await screen.findByRole("menuitemcheckbox", { name: "Remote Control" });
		expect(toggle).toHaveAttribute("aria-checked", "false");
		await refused.user.click(toggle);
		await act(async () => undefined);
		expect(refused.actions.setRemoteControl).toHaveBeenCalledWith(true);
		expect(refused.onToast).toHaveBeenCalledWith("Not available for this account");
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

describe("ChatComposer Claude in Chrome", () => {
	const chromeItem = () => screen.findByRole("menuitemcheckbox", { name: /Claude in Chrome/ });

	it("turns Chrome on from the model menu", async () => {
		const { user, actions } = renderComposer();
		await user.click(screen.getByRole("button", { name: /Model and mode/ }));
		const item = await chromeItem();
		expect(item).toHaveAttribute("aria-checked", "false");
		await user.click(item);
		await act(async () => undefined);
		expect(actions.setChrome).toHaveBeenCalledWith(true);
	});

	it("turns Chrome off when it is on", async () => {
		const { user, actions } = renderComposer({}, { chrome: true });
		await user.click(screen.getByRole("button", { name: /Model and mode/ }));
		const item = await chromeItem();
		expect(item).toHaveAttribute("aria-checked", "true");
		await user.click(item);
		await act(async () => undefined);
		expect(actions.setChrome).toHaveBeenCalledWith(false);
	});

	it("cannot be switched while a turn runs, since claude restarts", async () => {
		const { user, actions } = renderComposer({}, { busy: true });
		await user.click(screen.getByRole("button", { name: /Model and mode/ }));
		const item = await chromeItem();
		expect(item).toHaveAttribute("aria-disabled", "true");
		await user.click(item);
		expect(actions.setChrome).not.toHaveBeenCalled();
	});

	it("shows the host's refusal", async () => {
		const { user, onToast } = renderComposer({
			setChrome: vi.fn(async () => ({ ok: false, error: { kind: "rejected", message: "claude is busy" } }) as const),
		});
		await user.click(screen.getByRole("button", { name: /Model and mode/ }));
		await user.click(await chromeItem());
		await act(async () => undefined);
		expect(onToast).toHaveBeenCalledWith("claude is busy");
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

describe("ChatComposer draft", () => {
	it("brings back unsent text after the composer is unmounted and mounted again", async () => {
		const { input, user, unmount, actions } = renderComposer();
		await user.type(input, "Half a thought");
		unmount();
		renderComposer();
		expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("Half a thought");
		expect(actions.send).not.toHaveBeenCalled();
	});

	it("keeps a draft per session", async () => {
		const first = renderComposer({}, { sessionId: "chat-1" });
		await first.user.type(first.input, "For the first session");
		first.unmount();
		const second = renderComposer({}, { sessionId: "chat-2" });
		expect(second.input).toHaveValue("");
		await second.user.type(second.input, "For the second");
		second.unmount();
		const back = renderComposer({}, { sessionId: "chat-1" });
		expect(back.input).toHaveValue("For the first session");
		back.unmount();
		renderComposer({}, { sessionId: "chat-2" });
		expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("For the second");
	});

	it("forgets the draft once the message is sent", async () => {
		const { input, user, unmount, actions } = renderComposer();
		await user.type(input, "Ship it{Enter}");
		await waitFor(() => expect(actions.send).toHaveBeenCalledWith("Ship it", []));
		await waitFor(() => expect(input).toHaveValue(""));
		unmount();
		renderComposer();
		expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("");
	});

	it("keeps the draft when sending fails", async () => {
		const { input, user, unmount } = renderComposer({
			send: vi.fn<ChatActions["send"]>(async () => ({ ok: false, error: { kind: "failed", message: "nope" } })),
		});
		await user.type(input, "Try again later{Enter}");
		unmount();
		renderComposer();
		expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("Try again later");
	});

	it("forgets the draft when the box is cleared", async () => {
		const { input, user, unmount } = renderComposer();
		await user.type(input, "Never mind");
		await user.clear(input);
		unmount();
		renderComposer();
		expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("");
	});

	it("brings a shell command back in shell mode", async () => {
		const { input, user, unmount } = renderComposer();
		await user.type(input, "!git status");
		unmount();
		expect(renderComposer().input).toHaveAccessibleName("Shell command");
		expect(screen.getByRole("textbox", { name: "Shell command" })).toHaveValue("git status");
	});

	it("does not open the slash menu for a restored draft until it is edited", async () => {
		const { input, user, unmount } = renderComposer();
		await user.type(input, "/rev");
		expect(screen.getByRole("listbox")).toBeInTheDocument();
		unmount();
		const again = renderComposer();
		expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
		await again.user.type(again.input, "i");
		expect(screen.getByRole("listbox")).toBeInTheDocument();
	});
});

const SUGGESTION = "what happens if I forget to cancel it?";
const ghost = () => screen.queryByTestId("chat-suggestion");

describe("ChatComposer prompt suggestion", () => {
	it("shows the suggestion as ghost text in an empty box and says how to use it", () => {
		const { input } = renderComposer({}, { suggestion: SUGGESTION });
		expect(ghost()).toHaveTextContent(SUGGESTION);
		expect(input).toHaveValue("");
		expect(input).toHaveAccessibleDescription(`Suggestion: ${SUGGESTION}, press Tab to use`);
		expect(input).not.toHaveAttribute("placeholder", expect.stringContaining("Message Claude"));
	});

	it("shows nothing without a suggestion, and keeps the placeholder", () => {
		const { input } = renderComposer();
		expect(ghost()).toBeNull();
		expect(input).toHaveAttribute("placeholder", expect.stringContaining("Message Claude"));
		expect(input).not.toHaveAccessibleDescription();
	});

	it("is not shown while the session works", () => {
		renderComposer({}, { suggestion: SUGGESTION, busy: true });
		expect(ghost()).toBeNull();
	});

	it("is not shown while the host is down", () => {
		renderComposer({ hostDown: true }, { suggestion: SUGGESTION });
		expect(ghost()).toBeNull();
	});

	it("fills the box on Tab without sending, with the caret at the end and focus kept", async () => {
		const { input, user, actions } = renderComposer({}, { suggestion: SUGGESTION });
		input.focus();
		await user.tab();
		expect(input).toHaveValue(SUGGESTION);
		expect(input).toHaveFocus();
		expect((input as HTMLTextAreaElement).selectionStart).toBe(SUGGESTION.length);
		expect(ghost()).toBeNull();
		expect(actions.send).not.toHaveBeenCalled();
	});

	it("lets the person edit the accepted text and send it", async () => {
		const { input, user, actions } = renderComposer({}, { suggestion: SUGGESTION });
		input.focus();
		await user.tab();
		await user.type(input, " now");
		await user.keyboard("{Enter}");
		expect(actions.send).toHaveBeenCalledWith(`${SUGGESTION} now`, []);
	});

	it("hides as soon as anything is typed, and leaves Tab to move focus", async () => {
		const { input, user } = renderComposer({}, { suggestion: SUGGESTION });
		await user.type(input, "x");
		expect(ghost()).toBeNull();
		await user.tab();
		expect(input).toHaveValue("x");
		expect(input).not.toHaveFocus();
	});

	it("comes back when the box is emptied again", async () => {
		const { input, user } = renderComposer({}, { suggestion: SUGGESTION });
		await user.type(input, "x");
		await user.clear(input);
		expect(ghost()).toHaveTextContent(SUGGESTION);
	});

	it("is dismissed by Escape for that suggestion, and a new suggestion shows again", async () => {
		const { input, user, suggest, parentKeys } = renderComposer({}, { suggestion: SUGGESTION });
		input.focus();
		await user.keyboard("{Escape}");
		expect(ghost()).toBeNull();
		expect(parentKeys).toHaveLastReturnedWith(true);
		await user.tab();
		expect(input).toHaveValue("");
		suggest("");
		suggest(SUGGESTION);
		expect(ghost()).toHaveTextContent(SUGGESTION);
	});

	it("sends nothing on Enter in an empty box", async () => {
		const { input, user, actions } = renderComposer({}, { suggestion: SUGGESTION });
		input.focus();
		await user.keyboard("{Enter}");
		expect(actions.send).not.toHaveBeenCalled();
		expect(input).toHaveValue("");
		expect(ghost()).toHaveTextContent(SUGGESTION);
	});

	it("leaves Shift+Tab alone", async () => {
		const { input, user } = renderComposer({}, { suggestion: SUGGESTION });
		input.focus();
		await user.tab({ shift: true });
		expect(input).toHaveValue("");
	});

	it("does not take Tab from an open slash menu", async () => {
		const { input, user } = renderComposer({}, { suggestion: SUGGESTION });
		await user.type(input, "/co");
		await user.keyboard("{Tab}");
		expect(input).toHaveValue("/compact ");
	});

	it("does not take Tab from an open @ menu", async () => {
		const { input, user } = renderComposer({}, { suggestion: SUGGESTION });
		await user.type(input, "@sen");
		await user.keyboard("{Tab}");
		expect(input).toHaveValue("@internal/webhook/sender.go ");
	});
});

const MIB = 1024 * 1024;
const pdf = (name = "spec.pdf") => new File(["%PDF"], name, { type: "application/pdf" });
const png = (name = "sketch.png", bytes = 4) => new File([new Uint8Array(bytes).fill(7)], name, { type: "image/png" });
const chipFor = (name: string) => screen.getByRole("group", { name: `Attachment ${name}` });
const chips = () => screen.queryAllByTestId("chat-attachment");
const fileInput = () => screen.getByTestId("chat-file-input");
const sendButton = () => screen.getByRole("button", { name: "Send" });
const drop = (files: File[]) =>
	fireEvent.drop(document.querySelector(".chat-composer")!, { dataTransfer: { files, types: ["Files"] } });

const failed = (message = "Request failed (HTTP 500)"): UploadResult => ({
	ok: false,
	error: { kind: "failed", message },
});

afterEach(() => {
	window.localStorage.clear();
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe("ChatComposer attachments", () => {
	it("says files can be attached", () => {
		const { input } = renderComposer();
		expect(input).toHaveAttribute(
			"placeholder",
			"Message Claude. / for commands and skills, @ for files, paste, drop or attach files.",
		);
	});

	it("opens the file picker from the Attach button", async () => {
		const { user } = renderComposer();
		const click = vi.spyOn(HTMLInputElement.prototype, "click").mockImplementation(() => undefined);
		await user.click(screen.getByRole("button", { name: "Attach files" }));
		expect(click).toHaveBeenCalledTimes(1);
		expect(fileInput()).toHaveAttribute("multiple");
	});

	it("shows a PDF picked through the input as a chip, uploads it once and ends the sent text with its path", async () => {
		const { input, user, actions } = renderComposer();
		const file = pdf();
		await user.upload(fileInput(), file);

		await waitFor(() => expect(chipFor("spec.pdf")).toHaveAttribute("data-state", "ready"));
		expect(chipFor("spec.pdf")).toHaveTextContent("PDF");
		expect(chipFor("spec.pdf")).toHaveTextContent("4 B");
		expect(actions.upload).toHaveBeenCalledTimes(1);
		expect(actions.upload).toHaveBeenCalledWith(file);

		await user.type(input, "Summarise this");
		await user.click(sendButton());
		expect(actions.send).toHaveBeenCalledWith("Summarise this\nAttached: /data/attachments/ab12/spec.pdf", []);
		await waitFor(() => expect(chips()).toHaveLength(0));
		expect(actions.upload).toHaveBeenCalledTimes(1);
	});

	it("takes a PDF dropped on the composer the same way", async () => {
		const { input, user, actions } = renderComposer();
		drop([pdf("dropped.pdf")]);
		await waitFor(() => expect(chipFor("dropped.pdf")).toHaveAttribute("data-state", "ready"));
		expect(actions.upload).toHaveBeenCalledTimes(1);
		await user.type(input, "Look");
		await user.click(sendButton());
		expect(actions.send).toHaveBeenCalledWith("Look\nAttached: /data/attachments/ab12/dropped.pdf", []);
	});

	it("takes a file pasted into the box", async () => {
		const { input, actions } = renderComposer();
		fireEvent.paste(input, { clipboardData: { files: [pdf("pasted.pdf")] } });
		await waitFor(() => expect(chipFor("pasted.pdf")).toHaveAttribute("data-state", "ready"));
		expect(actions.upload).toHaveBeenCalledTimes(1);
	});

	it("shows a spinner state and holds Send while an upload runs", async () => {
		let finish: (r: UploadResult) => void = () => undefined;
		const { input, user, actions } = renderComposer({
			upload: vi.fn<ChatActions["upload"]>(() => new Promise((resolve) => (finish = resolve))),
		});
		await user.upload(fileInput(), pdf("slow.pdf"));
		await user.type(input, "wait for it");
		expect(chipFor("slow.pdf")).toHaveAttribute("data-state", "uploading");
		expect(sendButton()).toBeDisabled();
		await user.keyboard("{Enter}");
		expect(actions.send).not.toHaveBeenCalled();

		await act(async () => finish(saved(pdf("slow.pdf"))));
		expect(chipFor("slow.pdf")).toHaveAttribute("data-state", "ready");
		expect(sendButton()).toBeEnabled();
	});

	it("shows an error chip when an upload fails and holds Send until Retry succeeds", async () => {
		const upload = vi
			.fn<ChatActions["upload"]>()
			.mockResolvedValueOnce(failed("File is too large"))
			.mockImplementation(async (file) => saved(file));
		const { input, user, actions } = renderComposer({ upload });
		await user.upload(fileInput(), pdf("big.pdf"));
		await waitFor(() => expect(chipFor("big.pdf")).toHaveAttribute("data-state", "failed"));
		expect(chipFor("big.pdf")).toHaveTextContent("File is too large");
		await user.type(input, "see attached");
		expect(sendButton()).toBeDisabled();
		await user.keyboard("{Enter}");
		expect(actions.send).not.toHaveBeenCalled();

		await user.click(screen.getByRole("button", { name: "Retry big.pdf" }));
		await waitFor(() => expect(chipFor("big.pdf")).toHaveAttribute("data-state", "ready"));
		expect(upload).toHaveBeenCalledTimes(2);
		expect(sendButton()).toBeEnabled();
		await user.click(sendButton());
		expect(actions.send).toHaveBeenCalledWith("see attached\nAttached: /data/attachments/ab12/big.pdf", []);
	});

	it("lets Send through again once a failed upload is removed", async () => {
		const { input, user, actions } = renderComposer({ upload: vi.fn<ChatActions["upload"]>(async () => failed()) });
		await user.upload(fileInput(), pdf("bad.pdf"));
		await waitFor(() => expect(chipFor("bad.pdf")).toHaveAttribute("data-state", "failed"));
		await user.type(input, "never mind");
		expect(sendButton()).toBeDisabled();

		await user.click(screen.getByRole("button", { name: "Remove bad.pdf" }));
		expect(chips()).toHaveLength(0);
		expect(sendButton()).toBeEnabled();
		await user.click(sendButton());
		expect(actions.send).toHaveBeenCalledWith("never mind", []);
	});

	it("sends a message with only a file, no text", async () => {
		const { user, actions } = renderComposer();
		expect(sendButton()).toBeDisabled();
		await user.upload(fileInput(), pdf("only.pdf"));
		await waitFor(() => expect(sendButton()).toBeEnabled());
		await user.click(sendButton());
		expect(actions.send).toHaveBeenCalledWith("Attached: /data/attachments/ab12/only.pdf", []);
	});

	it("sends a message with only an image, no text, and does not upload the image", async () => {
		const { user, actions } = renderComposer();
		await user.upload(fileInput(), png("only.png"));
		expect(await screen.findByRole("img", { name: "only.png" })).toBeVisible();
		await user.click(sendButton());
		expect(actions.send).toHaveBeenCalledWith("", [{ mediaType: "image/png", base64: "BwcHBw==" }]);
		expect(actions.upload).not.toHaveBeenCalled();
	});

	it("sends images and files in one message", async () => {
		const { input, user, actions } = renderComposer();
		await user.upload(fileInput(), [png("a.png"), pdf("b.pdf")]);
		await waitFor(() => expect(chipFor("b.pdf")).toHaveAttribute("data-state", "ready"));
		await user.type(input, "both");
		await user.click(sendButton());
		expect(actions.send).toHaveBeenCalledWith("both\nAttached: /data/attachments/ab12/b.pdf", [
			{ mediaType: "image/png", base64: "BwcHBw==" },
		]);
	});

	it("treats an image type the daemon does not take as a file", async () => {
		const { user, actions } = renderComposer();
		const svg = new File(["<svg/>"], "logo.svg", { type: "image/svg+xml" });
		await user.upload(fileInput(), svg);
		await waitFor(() => expect(chipFor("logo.svg")).toHaveAttribute("data-state", "ready"));
		expect(chipFor("logo.svg")).toHaveTextContent("SVG");
		expect(actions.upload).toHaveBeenCalledWith(svg);
	});

	it("downsamples a large image before it is sent", async () => {
		const seen = stubImageDecoder(4000, 3000);
		const { user, actions } = renderComposer();
		await user.upload(fileInput(), png("huge.png", 6 * MIB));
		expect(await screen.findByRole("img", { name: "huge.png" })).toBeVisible();
		expect(seen.drawn).toEqual([[0, 0, 2000, 1500]]);
		await user.click(sendButton());
		expect(actions.send).toHaveBeenCalledWith("", [{ mediaType: "image/png", base64: "AQID" }]);
	});

	it("refuses an image that is over 5 MiB after resizing, with a toast", async () => {
		stubImageDecoder(1000, 1000);
		const { user, onToast, actions } = renderComposer();
		await user.upload(fileInput(), png("raw.png", 5 * MIB + 1));
		await waitFor(() => expect(onToast).toHaveBeenCalledTimes(1));
		expect(onToast.mock.calls[0][0]).toMatch(/raw\.png.*5 MB/);
		expect(chips()).toHaveLength(0);
		expect(actions.send).not.toHaveBeenCalled();
	});

	it("refuses the 21st image of a message with a toast", async () => {
		stubImageDecoder(100, 100);
		const { user, onToast } = renderComposer();
		await user.upload(
			fileInput(),
			Array.from({ length: 21 }, (_, i) => png(`i${i}.png`)),
		);
		await waitFor(() => expect(chips()).toHaveLength(20));
		expect(onToast).toHaveBeenCalledTimes(1);
		expect(onToast.mock.calls[0][0]).toMatch(/at most 20 images/i);
		expect(screen.queryByRole("img", { name: "i20.png" })).toBeNull();

		await user.upload(fileInput(), png("late.png"));
		await waitFor(() => expect(onToast).toHaveBeenCalledTimes(2));
		expect(chips()).toHaveLength(20);
	});
});

describe("ChatComposer bash mode", () => {
	const shell = () => screen.getByRole("textbox", { name: "Shell command" });
	const mode = () => screen.queryByTestId("chat-bash-mode");

	it("enters on a leading ! in an empty box, consumes it, and shows the mode", async () => {
		const { input, user } = renderComposer();
		expect(mode()).toBeNull();
		await user.type(input, "!");
		expect(mode()).toHaveTextContent("! bash");
		expect(mode()).toHaveTextContent("Runs in the worktree as you. Claude sees the output next message.");
		expect(shell()).toHaveValue("");
		await user.type(shell(), "git status");
		expect(shell()).toHaveValue("git status");
	});

	it("enters on a paste that starts with ! and keeps the rest as the command", () => {
		const { input } = renderComposer();
		fireEvent.change(input, { target: { value: "!git log --oneline" } });
		expect(mode()).not.toBeNull();
		expect(shell()).toHaveValue("git log --oneline");
	});

	it.each([
		["a ! after other text", "echo !"],
		["a space then !", " !ls"],
	])("stays a prompt for %s", async (_name, typed) => {
		const { input, user } = renderComposer();
		await user.type(input, typed);
		expect(mode()).toBeNull();
		expect(input).toHaveValue(typed);
	});

	it("does not enter once a file is attached", async () => {
		const { input, user } = renderComposer();
		await user.upload(screen.getByTestId("chat-file-input"), new File(["x"], "a.txt", { type: "text/plain" }));
		await screen.findByText("a.txt");
		await user.type(input, "!ls");
		expect(mode()).toBeNull();
		expect(input).toHaveValue("!ls");
	});

	it("runs the command on Enter, trims it, clears the box and leaves the mode", async () => {
		const { input, user, actions } = renderComposer();
		await user.type(input, "!  git status ");
		await user.keyboard("{Enter}");
		expect(actions.runBash).toHaveBeenCalledExactlyOnceWith("git status");
		expect(actions.send).not.toHaveBeenCalled();
		await waitFor(() => expect(mode()).toBeNull());
		expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("");
	});

	it("runs from the button too, labelled Run command", async () => {
		const { input, user, actions } = renderComposer();
		await user.type(input, "!ls");
		expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
		await user.click(screen.getByRole("button", { name: "Run command" }));
		expect(actions.runBash).toHaveBeenCalledExactlyOnceWith("ls");
	});

	it("keeps a newline on Shift+Enter instead of running", async () => {
		const { input, user, actions } = renderComposer();
		await user.type(input, "!echo a");
		await user.keyboard("{Shift>}{Enter}{/Shift}");
		expect(actions.runBash).not.toHaveBeenCalled();
		expect(shell()).toHaveValue("echo a\n");
	});

	it("does nothing on Enter for a blank command", async () => {
		const { input, user, actions } = renderComposer();
		await user.type(input, "!");
		await user.keyboard("{Enter}");
		await user.type(shell(), "   {Enter}");
		expect(actions.runBash).not.toHaveBeenCalled();
		expect(screen.getByRole("button", { name: "Run command" })).toBeDisabled();
	});

	it("leaves on Backspace in an empty box, and not while there is text", async () => {
		const { input, user } = renderComposer();
		await user.type(input, "!ab");
		await user.keyboard("{Backspace}{Backspace}");
		expect(mode()).not.toBeNull();
		await user.keyboard("{Backspace}");
		expect(mode()).toBeNull();
		expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("");
	});

	it("leaves on Esc, drops the text, and does not let the pane read that Esc as an interrupt", async () => {
		const { input, user, parentKeys } = renderComposer();
		await user.type(input, "!ls");
		parentKeys.mockClear();
		await user.keyboard("{Escape}");
		expect(mode()).toBeNull();
		expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("");
		expect(parentKeys).toHaveReturnedWith(true);
	});

	it("does not open the slash or mention menus, and disables their buttons", async () => {
		const { input, user } = renderComposer();
		await user.type(input, "!");
		await user.type(shell(), "/co @RE");
		expect(screen.queryByRole("listbox")).toBeNull();
		expect(screen.getByRole("button", { name: "Attach files" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Commands and skills" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Mention a file" })).toBeDisabled();
	});

	it("keeps the text and mode and toasts the reason when the host refuses", async () => {
		const runBash = vi.fn<ChatActions["runBash"]>(async () => ({
			ok: false,
			error: { kind: "rejected", message: "claude is busy" },
		}));
		const { input, user, onToast } = renderComposer({ runBash });
		await user.type(input, "!ls");
		await user.keyboard("{Enter}");
		await waitFor(() => expect(onToast).toHaveBeenCalledWith("claude is busy"));
		expect(shell()).toHaveValue("ls");
	});

	describe("while a turn runs", () => {
		it("still enters the mode but says why Enter and the button do nothing", async () => {
			const { input, user, actions } = renderComposer({}, { busy: true });
			await user.type(input, "!ls");
			expect(mode()).toHaveTextContent("Claude is working. Wait for the turn to end to run a command.");
			expect(screen.getByRole("button", { name: "Run command" })).toBeDisabled();
			await user.keyboard("{Enter}");
			expect(actions.runBash).not.toHaveBeenCalled();
			expect(actions.send).not.toHaveBeenCalled();
			expect(shell()).toHaveValue("ls");
		});

		it("runs once the turn ends", async () => {
			const { input, user, actions, rerenderWith } = renderComposer({}, { busy: true });
			await user.type(input, "!ls");
			rerenderWith({ busy: false });
			await user.type(shell(), "{Enter}");
			expect(actions.runBash).toHaveBeenCalledExactlyOnceWith("ls");
		});
	});

	describe("while a run is busy", () => {
		it("refuses a second command with the reason", async () => {
			const { input, user, actions } = renderComposer({}, { bashBusy: true });
			await user.type(input, "!ls");
			expect(mode()).toHaveTextContent("A command is still running or being passed to Claude.");
			await user.keyboard("{Enter}");
			expect(actions.runBash).not.toHaveBeenCalled();
		});

		it("disables the normal Send and does not send on Enter", async () => {
			const { input, user, actions } = renderComposer({}, { bashBusy: true });
			await user.type(input, "hello");
			const send = screen.getByRole("button", { name: "Send" });
			expect(send).toBeDisabled();
			expect(send).toHaveAttribute(
				"title",
				"A command is still running or being passed to Claude. Wait for it to finish.",
			);
			await user.keyboard("{Enter}");
			expect(actions.send).not.toHaveBeenCalled();
		});
	});

	describe("never runs a command by itself", () => {
		it("a prefill that starts with ! fills the box as a prompt and sends nothing", async () => {
			const { prefill, input, user, actions } = renderComposer();
			prefill({ text: "!rm -rf build", seq: 1 });
			expect(mode()).toBeNull();
			expect(input).toHaveValue("!rm -rf build");
			expect(actions.runBash).not.toHaveBeenCalled();
			await user.click(input);
			await user.keyboard("{Enter}");
			expect(actions.runBash).not.toHaveBeenCalled();
			expect(actions.send).toHaveBeenCalledWith("!rm -rf build", []);
		});

		it("a prefill replaces a half-typed command and leaves bash mode", async () => {
			const { prefill, input, user } = renderComposer();
			await user.type(input, "!echo hi");
			prefill({ text: "Run this command:", seq: 1 });
			expect(mode()).toBeNull();
			expect(screen.getByRole("textbox", { name: "Message Claude" })).toHaveValue("Run this command:");
		});

		it("a suggestion on Tab fills a prompt and does not enter the mode", async () => {
			const { input, user, actions } = renderComposer({}, { suggestion: "!rm -rf build" });
			await user.click(input);
			await user.keyboard("{Tab}");
			expect(input).toHaveValue("!rm -rf build");
			expect(mode()).toBeNull();
			expect(actions.runBash).not.toHaveBeenCalled();
		});
	});
});
