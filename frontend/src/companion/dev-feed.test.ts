import { describe, expect, it, vi } from "vitest";
import type { ActivityFrame } from "./activity-decay";
import { createManualFeed } from "./dev-feed";

const AT = "2026-07-22T12:00:00.000Z";
const T0 = Date.parse(AT);

function frame(overrides: Partial<ActivityFrame> = {}): ActivityFrame {
	return {
		sessionId: "demo-app-10",
		kind: "tool_start",
		at: AT,
		tool: "Bash",
		text: "Running the test suite",
		ttlMs: 20_000,
		coarse: "working",
		coarseTtlMs: 600_000,
		...overrides,
	} as ActivityFrame;
}

describe("the playground's hand-driven feed", () => {
	it("pushes the whole roster to a new subscriber immediately, like the real one", () => {
		const feed = createManualFeed([{ sessionId: "demo-app-10", status: "working" }]);
		const seen = vi.fn();

		feed.subscribe(seen);

		expect(seen).toHaveBeenCalledWith([{ sessionId: "demo-app-10", status: "working" }]);
	});

	it("replaces the roster wholesale, so removing a session removes its Proc", () => {
		const feed = createManualFeed([{ sessionId: "a", status: "working" }]);
		const seen = vi.fn();
		feed.subscribe(seen);

		feed.setRoster([{ sessionId: "b", status: "idle" }]);

		expect(seen).toHaveBeenLastCalledWith([{ sessionId: "b", status: "idle" }]);
	});

	it("runs a pressed frame through the REAL decay ladder, not a shortcut", () => {
		vi.useFakeTimers();
		vi.setSystemTime(T0 + 1_000);
		const feed = createManualFeed();
		feed.push(frame());

		expect(feed.bubbleFor("demo-app-10")?.text).toBe("Running the test suite");

		// Past the detail's TTL: the coarse truth, and only that.
		vi.setSystemTime(T0 + 25_000);
		expect(feed.bubbleFor("demo-app-10")).toEqual({ text: "Working…", tone: "normal", decay: "settled" });

		// Past the coarse TTL too: silence, which is what the panel must be able to show.
		vi.setSystemTime(T0 + 700_000);
		expect(feed.bubbleFor("demo-app-10")).toBeNull();
		vi.useRealTimers();
	});

	it("says nothing at all for a session that has never spoken", () => {
		expect(createManualFeed().bubbleFor("demo-app-10")).toBeNull();
	});

	it("hushes a session on demand, so silence can be looked at without waiting out a TTL", () => {
		const feed = createManualFeed();
		feed.push(frame());
		feed.hush("demo-app-10");

		expect(feed.bubbleFor("demo-app-10")).toBeNull();
	});
});
