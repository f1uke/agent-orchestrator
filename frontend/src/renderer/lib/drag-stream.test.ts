import { describe, expect, it, vi } from "vitest";
import { DragStream, MAX_PENDING_MOVES, type DragPoint, type DragStep } from "./drag-stream";

/** A sender whose requests finish only when the test says so. */
function controllable() {
	const sent: { step: DragStep; point: DragPoint }[] = [];
	const waiting: (() => void)[] = [];
	const send = vi.fn(async (step: DragStep, point: DragPoint) => {
		sent.push({ step, point });
		await new Promise<void>((resolve) => waiting.push(resolve));
	});
	return {
		sent,
		send,
		/** Let the oldest in-flight request finish. */
		async settle(times = 1) {
			for (let i = 0; i < times; i++) {
				waiting.shift()?.();
				await Promise.resolve();
				await Promise.resolve();
			}
		},
		inFlight: () => waiting.length,
	};
}

const steps = (sent: { step: DragStep }[]) => sent.map((s) => s.step);

describe("DragStream", () => {
	it("sends one request at a time, so moves cannot arrive shuffled", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.9 });
		drag.move({ x: 0.5, y: 0.8 });
		drag.move({ x: 0.5, y: 0.7 });
		expect(t.sent).toHaveLength(1);
		expect(t.sent[0].step).toBe("drag-begin");

		await t.settle();
		expect(t.sent).toHaveLength(2);
	});

	// The rule the frame stream already uses in the other direction: while one
	// request is in flight the newest position is the only one worth having.
	// ⚠ This case USED to assert the opposite - that only the newest position
	// survived while a request was in flight - and that assertion described the
	// bug. Dropping the motion is fine at 1 ms and destroys the drag at 200 ms,
	// because a slow `drag-begin` outlives the whole gesture: every move
	// collapses into one slot, the end takes that slot, and the device gets a
	// touch-down and a touch-up with nothing in between. Measured on a device
	// with a human driving: 11 of 26 drags arrived as exactly two requests.
	//
	// The motion is queued now. Late motion is a worse drag; no motion is not a
	// drag at all.
	it("queues the positions it could not send yet, in order", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.9 });
		await t.settle();
		drag.move({ x: 0.5, y: 0.8 });
		drag.move({ x: 0.5, y: 0.7 });
		drag.move({ x: 0.5, y: 0.6 });
		await t.settle(3);

		expect(steps(t.sent)).toEqual(["drag-begin", "drag-move", "drag-move", "drag-move"]);
		expect(t.sent.slice(1).map((s) => s.point.y)).toEqual([0.8, 0.7, 0.6]);
	});

	// The queue is bounded, so a path that stays slow cannot put the touch
	// further and further behind the finger - the failure the single slot was
	// there to prevent. Past the cap it degrades to exactly the old behaviour.
	it("bounds how much unsent motion can pile up", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 1 });
		// Nothing is settled, so everything after the begin queues.
		for (let i = 0; i < MAX_PENDING_MOVES + 20; i += 1) {
			drag.move({ x: 0.5, y: 1 - i / 1000 });
		}
		await t.settle(MAX_PENDING_MOVES + 30);

		const moves = t.sent.filter((s) => s.step === "drag-move");
		expect(moves.length).toBeLessThanOrEqual(MAX_PENDING_MOVES);
		// And the finger's final position is never the one dropped.
		expect(moves[moves.length - 1].point.y).toBeCloseTo(1 - (MAX_PENDING_MOVES + 19) / 1000, 5);
	});

	// The touch going down and coming up are the span the daemon holds the
	// device for, so neither may ever be dropped as stale - and the motion
	// between them is delivered rather than discarded when the end catches up
	// with it. (This case used to expect the move to vanish here.)
	it("never drops the begin or the end, and keeps the motion between them", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.9 });
		drag.move({ x: 0.5, y: 0.5 });
		drag.end({ x: 0.5, y: 0.4 });
		await t.settle(4);

		expect(steps(t.sent)).toEqual(["drag-begin", "drag-move", "drag-end"]);
		expect(t.sent[1].point.y).toBeCloseTo(0.5, 5);
		expect(t.sent[2].point.y).toBeCloseTo(0.4, 5);
		expect(drag.isDragging).toBe(false);
	});

	it("ignores a move or an end when no touch is down", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.move({ x: 0.5, y: 0.5 });
		drag.end({ x: 0.5, y: 0.5 });
		await t.settle();

		expect(t.sent).toHaveLength(0);
		expect(drag.isDragging).toBe(false);
	});

	// One failed step ends the drag here, because the daemon lifts the finger on
	// the same failure - and moves for a touch that is no longer down are moves
	// with no begin behind them.
	it("stops the drag and reports it when a step fails", async () => {
		const failure = new Error("the device is busy");
		const send = vi.fn(async (step: DragStep) => {
			if (step === "drag-move") throw failure;
		});
		const onError = vi.fn();
		const drag = new DragStream(send, onError);

		drag.begin({ x: 0.5, y: 0.9 });
		await Promise.resolve();
		await Promise.resolve();
		drag.move({ x: 0.5, y: 0.5 });
		await Promise.resolve();
		await Promise.resolve();
		await Promise.resolve();

		expect(onError).toHaveBeenCalledWith(failure);
		expect(drag.isDragging).toBe(false);

		// And nothing more is sent for a drag that is over.
		const before = send.mock.calls.length;
		drag.move({ x: 0.5, y: 0.4 });
		drag.end({ x: 0.5, y: 0.4 });
		await Promise.resolve();
		expect(send.mock.calls).toHaveLength(before);
	});

	// The bug this answers: a pointer the browser took back leaves this side
	// believing a finger is still down, and every drag after it was silently
	// ignored - "sometimes I have to do it twice".
	it("closes a touch whose end never arrived rather than ignoring the next press", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.9 });
		await t.settle();
		drag.move({ x: 0.5, y: 0.6 });
		await t.settle();
		// The pointer-up never happens. The next press must still start a drag.
		drag.begin({ x: 0.2, y: 0.2 });
		await t.settle(3);

		expect(steps(t.sent)).toEqual(["drag-begin", "drag-move", "drag-end", "drag-begin"]);
		// The abandoned touch is closed where it actually got to, not where the
		// new press landed.
		expect(t.sent[2].point.y).toBeCloseTo(0.6, 5);
		expect(t.sent[3].point.y).toBeCloseTo(0.2, 5);
		expect(drag.isDragging).toBe(true);
	});
});

// ⚠ Found by recording a real drag in the Device tab and reading the flow it
// produced: every swipe came out ending at 50%,50% whatever the human did.
//
// A release ends a drag TWICE - `pointerup` with the real position, then
// `lostpointercapture`, which the pane must also treat as an end because a
// capture the OS takes back mid-drag would leave a finger down forever. The
// second one carries only a fallback position, and it used to win, because
// `active` stayed true until the queued end had actually been sent.
describe("a drag ended twice keeps the position it really ended at", () => {
	it("ignores a second end, whatever it claims", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.8 });
		await t.settle();
		// ⚠ The move is left IN FLIGHT on purpose. That is the real shape of a
		// release: a request is always outstanding when the finger comes up, so
		// the end sits queued instead of being dispatched at once - which is the
		// only window in which a second end can overwrite the first. Settling
		// here made this test pass against the bug it exists to catch.
		drag.move({ x: 0.5, y: 0.5 });
		// The real release, then the fallback the browser's own event triggers.
		drag.end({ x: 0.5, y: 0.35 });
		drag.end({ x: 0.5, y: 0.5 });
		await t.settle(3);

		const ended = t.sent.filter((s) => s.step === "drag-end");
		expect(ended).toHaveLength(1);
		expect(ended[0].point).toEqual({ x: 0.5, y: 0.35 });
	});

	it("still lets a fresh drag start afterwards", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.8 });
		drag.end({ x: 0.5, y: 0.4 });
		drag.end({ x: 0.5, y: 0.5 });
		await t.settle(3);

		drag.begin({ x: 0.2, y: 0.2 });
		drag.end({ x: 0.2, y: 0.6 });
		await t.settle(3);

		expect(t.sent.filter((s) => s.step === "drag-end").map((s) => s.point)).toEqual([
			{ x: 0.5, y: 0.4 },
			{ x: 0.2, y: 0.6 },
		]);
	});
});
