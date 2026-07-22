import { describe, expect, it } from "vitest";
import { applyEvent, emptySlots, resolveAt, type ActivityFrame } from "./activity-decay";

const T = Date.parse("2026-07-22T09:00:00.000Z");
const at = (offsetMs = 0) => new Date(T + offsetMs).toISOString();

function frame(over: Partial<ActivityFrame> = {}): ActivityFrame {
	return {
		sessionId: "s1",
		kind: "tool_start",
		at: at(),
		ttlMs: 20_000,
		coarseTtlMs: 600_000,
		coarse: "working",
		...over,
	};
}

describe("the decay ladder", () => {
	it("shows the detail while it is still true", () => {
		const slots = applyEvent(emptySlots(), frame({ tool: "Bash", text: "Running the test suite" }));

		expect(resolveAt(slots, T + 19_999).level).toBe("detail");
	});

	it("falls back to the coarse truth the moment the detail expires", () => {
		// The nightmare this prevents: a Proc still saying "Running the test suite"
		// two minutes after the run finished.
		const slots = applyEvent(emptySlots(), frame({ tool: "Bash", text: "Running the test suite" }));
		const shown = resolveAt(slots, T + 20_001);

		expect(shown.level).toBe("coarse");
		expect(shown.coarse).toBe("working");
	});

	it("goes silent once even the coarse truth has expired", () => {
		const slots = applyEvent(emptySlots(), frame({ coarse: "idle", coarseTtlMs: 45_000 }));

		expect(resolveAt(slots, T + 45_001).level).toBe("unknown");
	});

	it("keeps a sticky coarse until something supersedes it", () => {
		// coarseTtlMs 0 means sticky: a pending permission prompt is pending until
		// answered, however long that takes.
		const slots = applyEvent(emptySlots(), frame({ coarse: "waiting", coarseTtlMs: 0, ttlMs: 0 }));

		expect(resolveAt(slots, T + 86_400_000).level).toBe("coarse");
		expect(resolveAt(slots, T + 86_400_000).coarse).toBe("waiting");
	});

	it("lets a newer event supersede an older one immediately", () => {
		let slots = applyEvent(emptySlots(), frame({ tool: "Bash", text: "Running the test suite" }));
		slots = applyEvent(
			slots,
			frame({ kind: "tool_end", at: at(5_000), tool: "Read", target: "hooks.go", ttlMs: 8_000 }),
		);
		const shown = resolveAt(slots, T + 6_000);

		expect(shown.detail?.target).toBe("hooks.go");
		expect(shown.detail?.text).toBeUndefined();
	});

	it("ignores a frame that carries no detail rather than blanking the one it has", () => {
		// ttlMs 0 means "this event carries no detail" — not "clear the detail".
		let slots = applyEvent(emptySlots(), frame({ tool: "Bash", text: "Running the test suite" }));
		slots = applyEvent(slots, frame({ kind: "activity", at: at(1_000), ttlMs: 0, coarse: "working" }));

		expect(resolveAt(slots, T + 2_000).level).toBe("detail");
	});

	it("leaves the coarse level alone when a frame does not carry one", () => {
		// An absent `coarse` means "this event does not change the coarse level".
		let slots = applyEvent(emptySlots(), frame({ coarse: "waiting", coarseTtlMs: 0 }));
		slots = applyEvent(slots, frame({ kind: "tool_end", at: at(1_000), coarse: undefined, ttlMs: 8_000 }));

		expect(resolveAt(slots, T + 20_000).coarse).toBe("waiting");
	});

	it("says unknown when it has heard nothing at all", () => {
		expect(resolveAt(emptySlots(), T).level).toBe("unknown");
	});

	it("re-evaluates on the clock, not on events — silence is not 'unchanged'", () => {
		// The whole point of the ladder: with no further events at all, the same slots
		// must read detail, then coarse, then unknown as time passes.
		const slots = applyEvent(
			emptySlots(),
			frame({ tool: "Bash", text: "Running the test suite", ttlMs: 20_000, coarse: "idle", coarseTtlMs: 45_000 }),
		);

		expect(resolveAt(slots, T + 1_000).level).toBe("detail");
		expect(resolveAt(slots, T + 30_000).level).toBe("coarse");
		expect(resolveAt(slots, T + 60_000).level).toBe("unknown");
	});
});
