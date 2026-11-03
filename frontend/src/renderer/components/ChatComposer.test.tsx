import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ChatActions } from "../hooks/useChatActions";
import type { ChatResult, UploadResult } from "../lib/chat-api";
import type { ChatCommand, ChatModel } from "../lib/chat-fold";
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

function renderComposer(over: Partial<ChatActions> = {}, props: { suggestion?: string; busy?: boolean } = {}) {
	const actions = {
		send: vi.fn<ChatActions["send"]>(async () => OK),
		answer: vi.fn<ChatActions["answer"]>(async () => OK),
		interrupt: vi.fn<ChatActions["interrupt"]>(async () => OK),
		setMode: vi.fn<ChatActions["setMode"]>(async () => OK),
		setModel: vi.fn<ChatActions["setModel"]>(async () => OK),
		stopTask: vi.fn<ChatActions["stopTask"]>(async () => ({ ok: true })),
		taskOutput: vi.fn<ChatActions["taskOutput"]>(async () => null),
		upload: vi.fn<ChatActions["upload"]>(async (file) => saved(file)),
		hostDown: false,
		...over,
	};
	const onToast = vi.fn();
	const parentKeys = vi.fn((e: React.KeyboardEvent) => e.defaultPrevented);
	const tree = (prefill?: Prefill, suggestion = props.suggestion ?? "") => (
		<div onKeyDown={parentKeys}>
			<ChatComposer
				actions={actions}
				busy={props.busy ?? false}
				suggestion={suggestion}
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
		suggest: (next: string) => rerender(tree(undefined, next)),
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
