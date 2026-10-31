import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ContextBreakdown } from "../lib/chat-context";
import type { SubscriptionUsage } from "../lib/chat-usage";
import { ContextMeter, UsageMeter } from "./ChatMeters";

const breakdown: ContextBreakdown = {
	totalTokens: 30_700,
	maxTokens: 1_000_000,
	percentage: 3,
	autoCompactThreshold: 967_000,
	categories: [
		{ id: "system_prompt", name: "System prompt", tokens: 1_500, kind: "used" },
		{ id: "system_tools", name: "System tools", tokens: 5_300, kind: "used" },
		{ id: "system_tools_deferred", name: "System tools (deferred)", tokens: 13_289, kind: "deferred" },
		{ id: "messages", name: "Messages", tokens: 10_300, kind: "used" },
		{ id: "autocompact_buffer", name: "Autocompact buffer", tokens: 33_000, kind: "buffer" },
		{ id: "free_space", name: "Free space", tokens: 936_200, kind: "free" },
	],
};

describe("ContextMeter", () => {
	it("shows the exact breakdown: percent, total, a segmented bar and one row per category", async () => {
		render(<ContextMeter breakdown={breakdown} />);
		const trigger = screen.getByRole("button", { name: /context/i });
		expect(trigger).toHaveTextContent(/^3%$/);
		await userEvent.click(trigger);
		const pop = screen.getByRole("dialog", { name: "Context window" });
		expect(
			within(pop)
				.getByText(/of context used/)
				.closest("header"),
		).toHaveTextContent(/^3% of context used30.7k \/ 1M$/);
		const rows = within(pop).getAllByRole("listitem");
		expect(rows.map((r) => r.textContent)).toEqual([
			"System prompt1.5k",
			"System tools5.3k",
			"Messages10.3k",
			"System tools (deferred)13.3k",
			"Autocompact buffer33k",
			"Free space936k",
		]);
		for (const muted of rows.slice(3)) expect(muted).toHaveAttribute("data-muted");
		for (const used of rows.slice(0, 3)) expect(used).not.toHaveAttribute("data-muted");
		const segments = pop.querySelectorAll("[data-segment]");
		expect([...segments].map((s) => s.getAttribute("data-segment"))).toEqual([
			"system_prompt",
			"system_tools",
			"messages",
		]);
		expect(pop).not.toHaveTextContent("~");
	});

	it("falls back to the last response's size, marked estimated", async () => {
		render(<ContextMeter fallback={{ tokens: 124_000, window: 200_000 }} />);
		const trigger = screen.getByRole("button", { name: /context/i });
		expect(trigger).toHaveTextContent(/^~62%$/);
		await userEvent.click(trigger);
		const pop = screen.getByRole("dialog", { name: "Context window" });
		expect(pop).toHaveTextContent("~124k / 200k");
		expect(pop).toHaveTextContent("estimated from the last response");
		expect(within(pop).queryAllByRole("listitem")).toEqual([]);
	});

	it("shows only the token count when the window is unknown, and nothing with no data", () => {
		const { rerender, container } = render(<ContextMeter fallback={{ tokens: 12_000 }} />);
		expect(screen.getByRole("button", { name: /context/i })).toHaveTextContent(/^~12k$/);
		rerender(<ContextMeter />);
		expect(container).toBeEmptyDOMElement();
	});
});

describe("UsageMeter", () => {
	const NOW = Date.parse("2026-10-31T05:00:00.000Z");
	const usage: SubscriptionUsage = {
		fiveHour: { utilization: 0.01, resetsAt: "2026-10-31T06:40:00Z", status: "allowed" },
		sevenDay: { utilization: 0.364, resetsAt: "2026-11-03T08:00:00Z", status: "allowed_warning" },
		updatedAt: "2026-10-31T04:57:00Z",
	};

	beforeEach(() => {
		vi.useFakeTimers({ toFake: ["Date"] });
		vi.setSystemTime(NOW);
	});
	afterEach(() => vi.useRealTimers());

	it("shows both windows' percent, and opens to local reset times, status and freshness", async () => {
		render(<UsageMeter usage={usage} />);
		const trigger = screen.getByRole("button", { name: /subscription usage/i });
		expect(trigger).toHaveTextContent(/^5h 1% · week 36%$/);
		await userEvent.click(trigger);
		const pop = screen.getByRole("dialog", { name: "Subscription usage" });
		const local = (iso: string) =>
			new Date(iso).toLocaleString(undefined, { weekday: "short", hour: "2-digit", minute: "2-digit" });
		expect(pop).toHaveTextContent(`resets ${local("2026-10-31T06:40:00Z")}`);
		expect(pop).toHaveTextContent(`resets ${local("2026-11-03T08:00:00Z")}`);
		expect(within(pop).getByText("Near the limit")).toBeInTheDocument();
		expect(pop).toHaveTextContent("updated 3 min ago");
	});

	it("renders nothing with no usage", () => {
		const { container } = render(<UsageMeter />);
		expect(container).toBeEmptyDOMElement();
	});
});
