import { describe, expect, it, vi, afterEach } from "vitest";
import { createLiveFeed, type LiveFeedDeps } from "./live-feed";
import type { LiveSession } from "./live-roster";

afterEach(() => vi.useRealTimers());

function session(over: Partial<LiveSession> = {}): LiveSession {
	return {
		id: "demo-app-1",
		name: "add login",
		projectName: "demo-app",
		status: "working",
		isTerminated: false,
		...over,
	};
}

/** A stand-in for the SSE connection, so the lifecycle can be driven by hand. */
function fakeStream() {
	const opened: Array<{ onFrame: (f: unknown) => void; onError: () => void }> = [];
	let closed = 0;
	const deps: Pick<LiveFeedDeps, "openStream"> = {
		openStream: (onFrame, onError) => {
			opened.push({ onFrame, onError });
			return () => {
				closed += 1;
			};
		},
	};
	return { deps, opened, closed: () => closed };
}

function deps(over: Partial<LiveFeedDeps> = {}): LiveFeedDeps {
	return {
		fetchSessions: async () => [session()],
		openStream: () => () => {},
		now: () => 0,
		...over,
	};
}

describe("createLiveFeed", () => {
	it("pushes the real roster, so the pets are actual sessions", async () => {
		const seen: string[][] = [];
		const feed = createLiveFeed(deps({ fetchSessions: async () => [session({ id: "a" }), session({ id: "b" })] }));

		const stop = feed.subscribe((roster) => seen.push(roster.map((r) => r.sessionId)));
		await vi.waitFor(() => expect(seen.length).toBeGreaterThan(0));

		expect(seen[seen.length - 1]).toEqual(["a", "b"]);
		stop();
	});

	it("keeps the last good roster when a poll fails, rather than emptying the desktop", async () => {
		// A daemon restart must not make every Proc vanish and then reappear.
		let calls = 0;
		const seen: number[] = [];
		const feed = createLiveFeed(
			deps({
				fetchSessions: async () => {
					calls += 1;
					if (calls > 1) throw new Error("daemon down");
					return [session({ id: "a" })];
				},
			}),
		);

		const stop = feed.subscribe((roster) => seen.push(roster.length));
		await vi.waitFor(() => expect(seen.length).toBeGreaterThan(0));
		await feed.refreshNow();

		expect(seen[seen.length - 1]).toBe(1);
		stop();
	});

	it("opens the activity stream once and closes it when the last listener goes", () => {
		const stream = fakeStream();
		const feed = createLiveFeed(deps(stream.deps));

		const stop = feed.subscribe(() => {});
		expect(stream.opened).toHaveLength(1);

		stop();
		expect(stream.closed()).toBe(1);
	});

	it("reopens the stream when the connection drops", () => {
		vi.useFakeTimers();
		const stream = fakeStream();
		const feed = createLiveFeed(deps(stream.deps));
		const stop = feed.subscribe(() => {});

		stream.opened[0].onError();
		vi.advanceTimersByTime(10_000);

		expect(stream.opened.length).toBeGreaterThan(1);
		stop();
	});

	it("does not leak a reconnect timer after it is stopped", () => {
		vi.useFakeTimers();
		const stream = fakeStream();
		const feed = createLiveFeed(deps(stream.deps));
		const stop = feed.subscribe(() => {});

		stream.opened[0].onError();
		stop();
		vi.advanceTimersByTime(60_000);

		expect(stream.opened).toHaveLength(1);
	});

	it("turns a frame into what that session's Proc may say", () => {
		let clock = Date.parse("2026-07-22T09:00:00.000Z");
		const stream = fakeStream();
		const feed = createLiveFeed(deps({ ...stream.deps, now: () => clock }));
		const stop = feed.subscribe(() => {});

		stream.opened[0].onFrame({
			sessionId: "demo-app-1",
			kind: "tool_start",
			at: "2026-07-22T09:00:00.000Z",
			tool: "Bash",
			text: "Running the test suite",
			ttlMs: 20_000,
			coarse: "working",
			coarseTtlMs: 600_000,
		});

		expect(feed.bubbleFor("demo-app-1")?.text).toBe("Running the test suite");

		// …and it stops saying it once the claim has expired, with no further frames.
		clock += 30_000;
		expect(feed.bubbleFor("demo-app-1")?.text).toBe("Working…");
		clock += 10 * 60_000;
		expect(feed.bubbleFor("demo-app-1")).toBeNull();

		stop();
	});

	it("says nothing for a session that has never emitted a frame", () => {
		// Every hook-less harness lives here — codex and about nine others. Silence,
		// with no badge and no explanation, is the whole design.
		const feed = createLiveFeed(deps());

		expect(feed.bubbleFor("never-heard-of-it")).toBeNull();
	});

	it("ignores a malformed frame instead of taking the overlay down with it", () => {
		const stream = fakeStream();
		const feed = createLiveFeed(deps(stream.deps));
		const stop = feed.subscribe(() => {});

		expect(() => stream.opened[0].onFrame({ nonsense: true })).not.toThrow();
		expect(() => stream.opened[0].onFrame(null)).not.toThrow();
		stop();
	});
});
