import { describe, expect, it } from "vitest";
import type { Pet } from "./behaviour";
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

describe("which Proc paints in front of which", () => {
	it("brings a staged conversation in front of the crowd around it", () => {
		expect(stackOrder(meeting)).toBeGreaterThan(stackOrder(pet()));
	});

	it("puts the Proc in your hand above everything", () => {
		expect(stackOrder(held)).toBeGreaterThan(stackOrder(meeting));
	});

	it("never returns zero, so a Proc is always above the transparent page itself", () => {
		expect(stackOrder(pet())).toBeGreaterThan(0);
	});
});
