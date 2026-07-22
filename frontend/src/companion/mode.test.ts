import { describe, expect, it } from "vitest";
import { isAnchored, ALL_COMPANION_STATUSES } from "./scene";
import { modeFor } from "./mode";

describe("modeFor", () => {
	it("lets everything walk except the two states that ARE somewhere", () => {
		// The human's call (2026-07-23). `no_signal`, `merged`, `terminated` and
		// `unknown` used to be held still on the argument that motion asserts liveness
		// we do not have — but the truthfulness the feed guarantees is in what a Proc
		// SAYS, and that is unchanged: a quiet session's Proc still shows the quiet
		// scene, an unplugged cord, and no bubble at all.
		for (const status of ["no_signal", "merged", "terminated", "unknown", "pr_open", "working", "ci_failed"] as const) {
			expect(modeFor(status), status).toBe("amble");
		}
	});

	it("keeps a Proc that is at a PLACE off its feet, and only those", () => {
		expect(modeFor("idle")).toBe("anchor");
		expect(modeFor("todo")).toBe("anchor");
		for (const status of ALL_COMPANION_STATUSES) {
			if (status === "idle" || status === "todo") continue;
			expect(modeFor(status), status).not.toBe("anchor");
		}
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
