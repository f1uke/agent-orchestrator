import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TaskOutput } from "../lib/chat-api";
import { TaskOutputProvider, TaskOutputView } from "./ChatTaskOutput";

const output = (text: string, extra: Partial<TaskOutput> = {}): TaskOutput => ({
	text,
	size: text.length,
	truncated: false,
	modifiedAt: "2026-10-30T10:56:00Z",
	...extra,
});

function show(read: (taskId: string) => Promise<TaskOutput | null>, running: boolean) {
	const view = (isRunning: boolean) => (
		<TaskOutputProvider read={read}>
			<TaskOutputView running={isRunning} taskId="b7x" />
		</TaskOutputProvider>
	);
	const utils = render(view(running));
	return { ...utils, setRunning: (next: boolean) => utils.rerender(view(next)) };
}

const flush = () => act(async () => undefined);
const tick = (ms: number) => act(async () => vi.advanceTimersByTime(ms));

beforeEach(() => {
	vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "setTimeout", "clearTimeout"] });
});

afterEach(() => {
	cleanup();
	vi.useRealTimers();
});

describe("TaskOutputView", () => {
	it("shows the tail of the output in the terminal style", async () => {
		const read = vi.fn(async () => output("=== RUN TestSoak\n--- PASS: TestSoak\n"));
		show(read, true);
		await flush();
		expect(read).toHaveBeenCalledWith("b7x");
		const term = document.querySelector(".chat-term[data-task-output]");
		expect(term).toHaveTextContent("=== RUN TestSoak --- PASS: TestSoak");
	});

	it("polls every 2s while the task runs", async () => {
		const read = vi.fn(async () => output("x"));
		show(read, true);
		await flush();
		expect(read).toHaveBeenCalledTimes(1);
		await tick(2000);
		expect(read).toHaveBeenCalledTimes(2);
		await tick(4000);
		expect(read).toHaveBeenCalledTimes(4);
	});

	it("reads once more when the task ends, then stops", async () => {
		let n = 0;
		const read = vi.fn(async () => output(`line ${++n}`));
		const { setRunning } = show(read, true);
		await flush();
		setRunning(false);
		await flush();
		expect(read).toHaveBeenCalledTimes(2);
		expect(document.querySelector("[data-task-output]")).toHaveTextContent("line 2");
		await tick(10_000);
		expect(read).toHaveBeenCalledTimes(2);
	});

	it("reads once and does not poll a task that has already ended", async () => {
		const read = vi.fn(async () => output("done"));
		show(read, false);
		await flush();
		await tick(10_000);
		expect(read).toHaveBeenCalledTimes(1);
	});

	it("stops polling once it is closed", async () => {
		const read = vi.fn(async () => output("x"));
		const { unmount } = show(read, true);
		await flush();
		unmount();
		await tick(10_000);
		expect(read).toHaveBeenCalledTimes(1);
	});

	it("says No output yet for an empty file", async () => {
		show(async () => output(""), true);
		await flush();
		expect(screen.getByText("No output yet")).toBeInTheDocument();
	});

	it("says Output is not available when the daemon has none", async () => {
		show(async () => null, true);
		await flush();
		expect(screen.getByText("Output is not available")).toBeInTheDocument();
	});

	it("says when only the end of a long output is shown", async () => {
		show(async () => output("tail", { size: 200 * 1024, truncated: true }), false);
		await flush();
		expect(screen.getByText("Showing the last 64 KB of 200 KB")).toBeInTheDocument();
	});

	it("follows new output at the bottom until the user scrolls up", async () => {
		let text = "a\n";
		const read = vi.fn(async () => output(text));
		show(read, true);
		await flush();
		const term = document.querySelector<HTMLElement>("[data-task-output]")!;
		let height = 500;
		Object.defineProperty(term, "scrollHeight", { configurable: true, get: () => height });
		Object.defineProperty(term, "clientHeight", { configurable: true, get: () => 200 });

		text = "a\nb\n";
		await tick(2000);
		expect(term.scrollTop).toBe(500);

		term.scrollTop = 100;
		fireEvent.scroll(term);
		height = 600;
		text = "a\nb\nc\n";
		await tick(2000);
		expect(term.scrollTop).toBe(100);

		term.scrollTop = 400;
		fireEvent.scroll(term);
		height = 700;
		text = "a\nb\nc\nd\n";
		await tick(2000);
		expect(term.scrollTop).toBe(700);
	});
});
