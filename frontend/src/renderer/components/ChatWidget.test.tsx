import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ToolCallItem } from "../lib/chat-fold";
import { ChatWidget } from "./ChatWidget";

const call = (over: Partial<ToolCallItem> = {}): ToolCallItem => ({
	kind: "tool_call",
	id: "w1",
	name: "mcp__ao__show_widget",
	parentToolUseId: "",
	input: { title: "calc", widget_code: "<p>hi</p>", loading_messages: ["Building the calculator…"] },
	result: { isError: false, content: "Shown to the user." },
	startedTs: "",
	...over,
});

const frameOf = () => screen.getByTitle("calc") as HTMLIFrameElement;

function post(data: unknown, source: MessageEventSource | null) {
	act(() => {
		window.dispatchEvent(new MessageEvent("message", { data, source }));
	});
}

describe("ChatWidget", () => {
	it("draws the visual in a frame whose sandbox is exactly allow-scripts", () => {
		render(<ChatWidget call={call()} onPrompt={vi.fn()} theme="dark" />);
		const frame = frameOf();
		expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
		expect(frame.getAttribute("referrerpolicy")).toBe("no-referrer");
		expect(frame.getAttribute("src")).toBe("/widget-frame.html");
		expect(frame.hasAttribute("srcdoc")).toBe(false);
		expect(screen.getByText("Made a visual")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Expand visual" })).toBeInTheDocument();
	});

	it("hands a prompt from its own frame to onPrompt", () => {
		const onPrompt = vi.fn();
		render(<ChatWidget call={call()} onPrompt={onPrompt} theme="dark" />);
		post({ type: "ao-widget/prompt", text: "  Explain the curve  " }, frameOf().contentWindow);
		expect(onPrompt).toHaveBeenCalledExactlyOnceWith("Explain the curve");
	});

	it("ignores a prompt from any other window, including the page itself and a message with no source", () => {
		const onPrompt = vi.fn();
		render(<ChatWidget call={call()} onPrompt={onPrompt} theme="dark" />);
		const other = document.createElement("iframe");
		document.body.append(other);
		post({ type: "ao-widget/prompt", text: "from elsewhere" }, other.contentWindow);
		post({ type: "ao-widget/prompt", text: "from the page" }, window);
		post({ type: "ao-widget/prompt", text: "from nowhere" }, null);
		other.remove();
		expect(onPrompt).not.toHaveBeenCalled();
	});

	it("ignores a message from its frame that is not a prompt or a size", () => {
		const onPrompt = vi.fn();
		render(<ChatWidget call={call()} onPrompt={onPrompt} theme="dark" />);
		post({ type: "ao-widget/theme", mode: "light" }, frameOf().contentWindow);
		post("ao-widget/prompt", frameOf().contentWindow);
		post({ type: "ao-widget/prompt", text: "" }, frameOf().contentWindow);
		expect(onPrompt).not.toHaveBeenCalled();
	});

	it("follows the height its frame reports, within bounds, and shows the loading line until then", () => {
		render(<ChatWidget call={call({ id: "w2" })} onPrompt={vi.fn()} theme="dark" />);
		expect(screen.getByRole("status")).toHaveTextContent("Building the calculator…");
		expect(frameOf().style.height).toBe("160px");
		post({ type: "ao-widget/size", height: 412 }, frameOf().contentWindow);
		expect(frameOf().style.height).toBe("412px");
		expect(screen.queryByRole("status")).toBeNull();
		post({ type: "ao-widget/size", height: 99_999 }, frameOf().contentWindow);
		expect(frameOf().style.height).toBe("4000px");
	});

	it("says it is making the visual while the call has no result", () => {
		render(<ChatWidget call={call({ result: undefined })} onPrompt={vi.fn()} theme="dark" />);
		expect(screen.getByText("Making a visual")).toBeInTheDocument();
		expect(screen.queryByText("Made a visual")).toBeNull();
	});

	it("opens a second frame of the same visual in a sheet, with the same sandbox and the same bridge", () => {
		const onPrompt = vi.fn();
		render(<ChatWidget call={call()} onPrompt={onPrompt} theme="dark" />);
		fireEvent.click(screen.getByRole("button", { name: "Expand visual" }));
		const frames = screen.getAllByTitle(/^calc/) as HTMLIFrameElement[];
		expect(frames).toHaveLength(2);
		const expanded = frames.find((f) => f.title === "calc (expanded)")!;
		expect(expanded.getAttribute("sandbox")).toBe("allow-scripts");
		post({ type: "ao-widget/prompt", text: "from the sheet" }, expanded.contentWindow);
		expect(onPrompt).toHaveBeenCalledExactlyOnceWith("from the sheet");
	});

	it("draws nothing for a call with no code", () => {
		const { container } = render(
			<ChatWidget call={call({ input: { title: "x", widget_code: "" } })} onPrompt={vi.fn()} theme="dark" />,
		);
		expect(container).toBeEmptyDOMElement();
	});
});
