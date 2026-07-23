import { describe, expect, it } from "vitest";
import { RALLY_MAX_DEPTH, type Pet } from "./behaviour";
import { stackOrder } from "./stacking";

function pet(over: Partial<Pet> = {}): Pet {
	return {
		id: "demo-app-1",
		status: "pr_open",
		name: "login rate limit",
		project: "demo-app",
		kind: "worker",
		x: 100,
		y: 0,
		facing: "front",
		motion: { kind: "standing" },
		restUntil: 0,
		...over,
	};
}

const meeting = pet({ meeting: { withId: "b", homeX: 100, line: "hi", phase: "greeting", until: 0 } });
const held = pet({ motion: { kind: "held", grabbedAt: 0 } });
const rallying = pet({ rally: { leaderId: "lead", phase: "gathered", startedAt: 0, until: 0, depth: 0 } });
const posedAt = (depth: number) =>
	pet({ rally: { leaderId: "lead", phase: "gathered", startedAt: 0, until: 0, depth } });

describe("which Proc paints in front of which", () => {
	it("brings a staged conversation in front of the crowd around it", () => {
		expect(stackOrder(meeting)).toBeGreaterThan(stackOrder(pet()));
	});

	it("lifts a roll-call over the band it is standing in, and under a staged conversation", () => {
		expect(stackOrder(rallying)).toBeGreaterThan(stackOrder(pet()));
		expect(stackOrder(rallying)).toBeLessThan(stackOrder(meeting));
	});

	it("puts the Proc in your hand above everything", () => {
		expect(stackOrder(held)).toBeGreaterThan(stackOrder(meeting));
	});

	it("layers the team photo front to back by where each Proc stands in the row", () => {
		// The photo's bodies overlap on purpose, so "which one is in front" has to be
		// decided rather than left to document order.
		expect(stackOrder(posedAt(3))).toBeGreaterThan(stackOrder(posedAt(2)));
		expect(stackOrder(posedAt(0))).toBeGreaterThan(stackOrder(pet()));
	});

	it("keeps the deepest photo still under a staged conversation", () => {
		// The depth ladder is bounded by the overlay's cast cap, so it can never climb
		// out of its own layer and paint over a meeting or a dragged Proc.
		expect(stackOrder(posedAt(RALLY_MAX_DEPTH - 1))).toBeLessThan(stackOrder(meeting));
		expect(stackOrder(posedAt(RALLY_MAX_DEPTH - 1))).toBeLessThan(stackOrder(held));
	});

	it("never returns zero, so a Proc is always above the transparent page itself", () => {
		expect(stackOrder(pet())).toBeGreaterThan(0);
	});
});
