import { describe, expect, it } from "vitest";
import { isAnchored, ALL_COMPANION_STATUSES } from "./scene";
import { modeFor } from "./mode";

describe("modeFor", () => {
	it("stands a Proc still when we have nothing live to show", () => {
		expect(modeFor("no_signal")).toBe("still");
		expect(modeFor("merged")).toBe("still");
		expect(modeFor("terminated")).toBe("still");
		expect(modeFor("unknown")).toBe("still");
	});

	it("summons a Proc that is waiting on the human", () => {
		expect(modeFor("needs_input")).toBe("summon");
	});

	it("anchors a Proc exactly when its scene has a ground", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			expect(modeFor(status) === "anchor").toBe(isAnchored(status));
		}
	});

	it("ambles the states that are neither anchored, summoned nor still", () => {
		expect(modeFor("pr_open")).toBe("amble");
		expect(modeFor("draft")).toBe("amble");
		expect(modeFor("review_pending")).toBe("amble");
		expect(modeFor("changes_requested")).toBe("amble");
		expect(modeFor("approved")).toBe("amble");
		expect(modeFor("mergeable")).toBe("amble");
	});

	it("covers every status", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			expect(["anchor", "amble", "summon", "still"]).toContain(modeFor(status));
		}
	});
});
