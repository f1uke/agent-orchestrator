import { describe, expect, it, vi } from "vitest";
import { DragStream, type DragPoint, type DragStep } from "./drag-stream";

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
	it("keeps only the newest position while a request is in flight", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.9 });
		await t.settle();
		drag.move({ x: 0.5, y: 0.8 });
		drag.move({ x: 0.5, y: 0.7 });
		drag.move({ x: 0.5, y: 0.6 });
		await t.settle();

		expect(steps(t.sent)).toEqual(["drag-begin", "drag-move", "drag-move"]);
		// The two intermediate positions were dropped, not queued.
		expect(t.sent[1].point.y).toBeCloseTo(0.8, 5);
		expect(t.sent[2].point.y).toBeCloseTo(0.6, 5);
	});

	// The touch going down and coming up are the span the daemon holds the
	// device for, so neither may ever be dropped as stale.
	it("never drops the begin or the end", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.9 });
		drag.move({ x: 0.5, y: 0.5 });
		drag.end({ x: 0.5, y: 0.4 });
		await t.settle(3);

		expect(steps(t.sent)).toEqual(["drag-begin", "drag-end"]);
		expect(t.sent[1].point.y).toBeCloseTo(0.4, 5);
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

	it("does not start a second touch while one is already down", async () => {
		const t = controllable();
		const drag = new DragStream(t.send);

		drag.begin({ x: 0.5, y: 0.9 });
		drag.begin({ x: 0.1, y: 0.1 });
		await t.settle(2);

		expect(steps(t.sent)).toEqual(["drag-begin"]);
	});
});
