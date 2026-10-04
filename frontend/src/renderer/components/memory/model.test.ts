import { describe, expect, it } from "vitest";
import { matchesOutcome, outcomeOf } from "./model";

const at = "2026-10-04T01:00:00Z";
const memory = { action: "create_memory", decidedAt: at };
const conflict = { action: "conflict", decidedAt: at };

describe("outcomeOf", () => {
	it("says a written proposal was kept", () => {
		expect(outcomeOf({ ...memory, status: "applied" })).toEqual({ kind: "kept", label: "Written", at });
	});

	it("says a rejected proposal was not kept, with the person's reason", () => {
		expect(outcomeOf({ ...memory, status: "rejected", rejectReason: "one-off" })).toEqual({
			kind: "not_kept",
			label: "Rejected",
			at,
			reason: "one-off",
		});
	});

	it("leaves out the reason the daemon fills in when none was given", () => {
		expect(outcomeOf({ ...memory, status: "rejected", rejectReason: "no reason given" })?.reason).toBeUndefined();
	});

	it("says which side of a conflict won", () => {
		expect(outcomeOf({ ...conflict, status: "applied", resolution: "words_win" })).toMatchObject({
			kind: "kept",
			label: "Kept your words",
		});
		expect(outcomeOf({ ...conflict, status: "applied", resolution: "both" })).toMatchObject({
			kind: "kept",
			label: "Kept both, scoped",
		});
		// Keeping the rule drops the lesson: the daemon settles it as rejected.
		expect(
			outcomeOf({ ...conflict, status: "rejected", resolution: "keep_rule", rejectReason: "kept the rule" }),
		).toEqual({ kind: "not_kept", label: "Kept the rule", at });
	});

	it("says a conflict rejected without picking a side was rejected, never just decided", () => {
		expect(outcomeOf({ ...conflict, status: "rejected", rejectReason: "not a rule" })).toEqual({
			kind: "not_kept",
			label: "Rejected",
			at,
			reason: "not a rule",
		});
	});

	it("says an undone proposal was undone, from its history", () => {
		const history = [
			{ kind: "approved", at: "2026-10-04T01:00:00Z" },
			{ kind: "undone", at: "2026-10-04T02:00:00Z" },
		];
		expect(outcomeOf({ ...memory, status: "pending", decidedAt: null }, history)).toEqual({
			kind: "undone",
			label: "Undone",
			at: "2026-10-04T02:00:00Z",
		});
	});

	it("has no outcome for a proposal still waiting, or one decided again since an undo", () => {
		expect(outcomeOf({ ...memory, status: "pending", decidedAt: null })).toBeNull();
		expect(
			outcomeOf({ ...memory, status: "pending", decidedAt: null }, [
				{ kind: "undone", at },
				{ kind: "snoozed", at },
			]),
		).toBeNull();
		expect(outcomeOf({ ...memory, status: "stale" })).toBeNull();
	});
});

describe("matchesOutcome", () => {
	const kept = outcomeOf({ ...memory, status: "applied" });
	const rejected = outcomeOf({ ...memory, status: "rejected" });
	const keptRule = outcomeOf({ ...conflict, status: "rejected", resolution: "keep_rule" });

	it("shows everything under All", () => {
		expect([kept, rejected, keptRule].every((o) => matchesOutcome(o, "all"))).toBe(true);
	});

	it("splits kept from not kept, a kept rule counting as the lesson not kept", () => {
		expect(matchesOutcome(kept, "kept")).toBe(true);
		expect(matchesOutcome(kept, "not_kept")).toBe(false);
		expect(matchesOutcome(rejected, "not_kept")).toBe(true);
		expect(matchesOutcome(keptRule, "not_kept")).toBe(true);
		expect(matchesOutcome(keptRule, "kept")).toBe(false);
	});
});
