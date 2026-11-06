import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { BashItem } from "../lib/chat-fold";
import { ChatBashRow, StopBashProvider } from "./ChatBashRow";

const item = (over: Partial<BashItem> = {}): BashItem => ({
	kind: "bash",
	id: "b1",
	command: "git status",
	cwd: "/w",
	chunks: [{ stream: "stdout", text: "clean\n" }],
	state: "done",
	code: 0,
	signal: null,
	truncated: false,
	ts: "",
	...over,
});

const badge = () => screen.getByTestId("chat-bash-badge").textContent;

describe("ChatBashRow", () => {
	it("shows the command and output with stderr apart from stdout", () => {
		render(
			<ChatBashRow
				item={item({
					chunks: [
						{ stream: "stdout", text: "out\n" },
						{ stream: "stderr", text: "err\n" },
					],
				})}
			/>,
		);
		expect(screen.getByText("git status")).toBeInTheDocument();
		const output = screen.getByLabelText("Command output");
		expect(output.querySelector('[data-stream="stdout"]')).toHaveTextContent("out");
		const err = output.querySelector('[data-stream="stderr"]')!;
		expect(err).toHaveTextContent("err");
		expect(err).toHaveClass("chat-bash__err");
		expect(output.querySelector('[data-stream="stdout"]')).not.toHaveClass("chat-bash__err");
	});

	it.each([
		["exit 0", item({ code: 0 })],
		["exit 2", item({ code: 2 })],
		["SIGSEGV", item({ code: null, signal: "SIGSEGV" })],
		["stopped", item({ state: "stopped", code: null })],
		["running", item({ state: "running", code: null })],
	])("badges the run as %s", (label, run) => {
		render(<ChatBashRow item={run} />);
		expect(badge()).toBe(label);
	});

	it("offers Stop only while running, calls it with the run id, and disables it after one click", async () => {
		const stop = vi.fn();
		const user = userEvent.setup();
		const { rerender } = render(
			<StopBashProvider stop={stop}>
				<ChatBashRow item={item({ state: "running", code: null })} />
			</StopBashProvider>,
		);
		const button = screen.getByRole("button", { name: "Stop git status" });
		await user.click(button);
		await user.click(button);
		expect(stop).toHaveBeenCalledExactlyOnceWith("b1");
		expect(button).toBeDisabled();
		rerender(
			<StopBashProvider stop={stop}>
				<ChatBashRow item={item({ state: "done" })} />
			</StopBashProvider>,
		);
		expect(screen.queryByRole("button", { name: /^Stop/ })).toBeNull();
	});

	it("says there was no output for a finished run with none, and nothing while it runs", () => {
		const { rerender } = render(<ChatBashRow item={item({ chunks: [] })} />);
		expect(screen.getByText("No output")).toBeInTheDocument();
		rerender(<ChatBashRow item={item({ chunks: [], state: "running", code: null })} />);
		expect(screen.queryByText("No output")).toBeNull();
	});

	it("caps a long output and expands and collapses it", async () => {
		const user = userEvent.setup();
		const text = Array.from({ length: 40 }, (_, i) => `line ${i}\n`).join("");
		render(<ChatBashRow item={item({ chunks: [{ stream: "stdout", text }] })} />);
		const output = screen.getByLabelText("Command output");
		expect(output).toHaveAttribute("data-capped");
		await user.click(screen.getByRole("button", { name: "Show all 40 lines" }));
		expect(output).not.toHaveAttribute("data-capped");
		await user.click(screen.getByRole("button", { name: "Show less" }));
		expect(output).toHaveAttribute("data-capped");
	});

	it("does not offer to expand a short output", () => {
		render(<ChatBashRow item={item()} />);
		expect(screen.queryByRole("button", { name: /Show all/ })).toBeNull();
	});

	it("notes that the output was cut off", () => {
		const { rerender } = render(<ChatBashRow item={item({ truncated: true })} />);
		expect(screen.getByText("Output was cut off at 1 MiB.")).toBeInTheDocument();
		rerender(<ChatBashRow item={item()} />);
		expect(screen.queryByText(/cut off/)).toBeNull();
	});
});
