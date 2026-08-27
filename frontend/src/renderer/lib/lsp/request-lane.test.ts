import { afterEach, describe, expect, test, vi } from "vitest";
import { forgetLane, laneBusy, resetLanes, runInLane, withTimeout } from "./request-lane";

/**
 * The one slot per document that completion, hover and references share.
 *
 * 🗝 The design decision under test is "wait, never cancel" — measured, not
 * assumed. Per-keystroke plus `$/cancelRequest` took 2 172–2 422 ms to the last
 * answer on a cold Swift file; serialising with no cancel took 2 ms, because
 * each cancellation throws away the in-progress type-check the next request has
 * to redo. Hover was measured separately for this slice and agrees.
 */

const A = "ao-file:///s/a.swift";
const B = "ao-file:///s/b.swift";

afterEach(() => resetLanes());

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("one request on the wire", () => {
	test("a second call for the same document waits rather than going out", async () => {
		const first = deferred<string>();
		const sent: string[] = [];
		const a = runInLane(
			A,
			() => false,
			() => {
				sent.push("first");
				return first.promise;
			},
		);
		const b = runInLane(
			A,
			() => false,
			() => {
				sent.push("second");
				return Promise.resolve("second answer");
			},
		);
		await tick();
		expect(sent).toEqual(["first"]);
		first.resolve("first answer");
		await expect(a).resolves.toEqual({ ok: true, value: "first answer" });
		await expect(b).resolves.toEqual({ ok: true, value: "second answer" });
		expect(sent).toEqual(["first", "second"]);
	});

	// A different file is a different type-check; making them queue would be a
	// self-inflicted serialisation across panes.
	test("a DIFFERENT document is not made to wait", async () => {
		const first = deferred<string>();
		const sent: string[] = [];
		void runInLane(
			A,
			() => false,
			() => {
				sent.push("a");
				return first.promise;
			},
		);
		await runInLane(
			B,
			() => false,
			() => {
				sent.push("b");
				return Promise.resolve("b");
			},
		);
		expect(sent).toEqual(["a", "b"]);
		first.resolve("done");
	});

	// 🗝 Dropped BEFORE the wire, not after. An answer for a prefix or a pointer
	// position that is no longer on screen must never reach a widget.
	test("a call superseded while queued never sends its request", async () => {
		const first = deferred<string>();
		const sent: string[] = [];
		const a = runInLane(
			A,
			() => false,
			() => {
				sent.push("first");
				return first.promise;
			},
		);
		let stale = false;
		const b = runInLane(
			A,
			() => stale,
			() => {
				sent.push("second");
				return Promise.resolve("second");
			},
		);
		await tick();
		stale = true;
		first.resolve("first answer");
		await a;
		await expect(b).resolves.toEqual({ ok: false, stale: true });
		expect(sent).toEqual(["first"]);
	});

	test("a call that is already stale never sends, even with the wire free", async () => {
		const sent: string[] = [];
		const outcome = await runInLane(
			A,
			() => true,
			() => {
				sent.push("nope");
				return Promise.resolve("x");
			},
		);
		expect(outcome).toEqual({ ok: false, stale: true });
		expect(sent).toEqual([]);
	});
});

describe("releasing the slot", () => {
	// 🗝 The strongest failure this module can produce: leave a settled promise in
	// the slot and every later request for that document waits forever. #258
	// found it by removing the release and watching the event loop spin.
	test("the slot is free again once the request settles", async () => {
		const first = deferred<string>();
		const a = runInLane(
			A,
			() => false,
			() => first.promise,
		);
		await tick();
		expect(laneBusy(A)).toBe(true);
		first.resolve("done");
		await a;
		expect(laneBusy(A)).toBe(false);
	});

	test("a REJECTION releases it too, and the next caller still goes out", async () => {
		const first = deferred<string>();
		const a = runInLane(
			A,
			() => false,
			() => first.promise,
		);
		await tick();
		first.reject(new Error("boom"));
		await expect(a).rejects.toThrow("boom");
		expect(laneBusy(A)).toBe(false);
		await expect(
			runInLane(
				A,
				() => false,
				() => Promise.resolve("next"),
			),
		).resolves.toEqual({
			ok: true,
			value: "next",
		});
	});

	// The waiter must not be taken down by somebody else's failure; its own
	// caller reports that. All it wanted was the slot.
	test("a waiter survives the failure of the request it was queued behind", async () => {
		const first = deferred<string>();
		const a = runInLane(
			A,
			() => false,
			() => first.promise,
		);
		const b = runInLane(
			A,
			() => false,
			() => Promise.resolve("mine"),
		);
		await tick();
		first.reject(new Error("boom"));
		await expect(a).rejects.toThrow("boom");
		await expect(b).resolves.toEqual({ ok: true, value: "mine" });
	});

	test("a document that is forgotten leaves no entry behind", async () => {
		await runInLane(
			A,
			() => false,
			() => Promise.resolve("x"),
		);
		forgetLane(A);
		expect(laneBusy(A)).toBe(false);
	});
});

describe("the ceiling", () => {
	// Chosen well above the slowest thing measured (a cold Swift hover at
	// 1 919 ms) and well below forever, because the failure this whole area is
	// written against is a server that answers nothing at all.
	test("a request that never answers rejects after eight seconds", async () => {
		vi.useFakeTimers();
		try {
			const pending = withTimeout(() => new Promise<string>(() => undefined));
			const settled = pending.catch((err: Error) => err.message);
			await vi.advanceTimersByTimeAsync(7_999);
			await vi.advanceTimersByTimeAsync(2);
			await expect(settled).resolves.toBe("the language server did not answer");
		} finally {
			vi.useRealTimers();
		}
	});

	test("an answer that arrives in time is not affected", async () => {
		await expect(withTimeout(() => Promise.resolve("fast"))).resolves.toBe("fast");
	});
});
