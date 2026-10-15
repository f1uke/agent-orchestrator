import { describe, expect, it } from "vitest";
import { draftDirty, draftFrom, planInput, PlanConflictError, planProblems, targetChoices } from "./plan";
import { board, train, version } from "./testkit";
import type { LLBGroup } from "./types";

const group: LLBGroup = {
	id: "demo-wallet",
	name: "demo-wallet",
	branches: [
		{
			app: "di",
			branch: "feature/demo-wallet",
			openedOn: "2026-09-01",
			lastIn: "2026-10-01",
			state: "open",
			cards: [],
		},
		{
			app: "da",
			branch: "feature/demo-wallet",
			openedOn: "2026-09-02",
			lastIn: "2026-10-01",
			state: "open",
			cards: [],
		},
	],
	target: { kind: "none" },
};

describe("plan draft", () => {
	it("starts a group with no plan from its derived id, its branches and no target", () => {
		expect(draftFrom(group)).toEqual({
			name: "demo-wallet",
			targetKind: "unplanned",
			note: "",
			branches: [
				{ app: "di", branch: "feature/demo-wallet" },
				{ app: "da", branch: "feature/demo-wallet" },
			],
			accepted: [],
			ifVersion: 0,
		});
	});

	it("starts from the stored plan and its version", () => {
		const d = draftFrom({
			...group,
			plan: {
				id: "demo-wallet",
				name: "Wallet",
				targetKind: "train",
				targetTrain: "22-2026",
				note: "waits for QA",
				branches: [{ app: "di", branch: "feature/demo-wallet" }],
				accepted: [],
				version: 3,
				updatedAt: "",
				updatedBy: "app",
			},
		});
		expect(d).toMatchObject({ name: "Wallet", targetKind: "train", targetTrain: "22-2026", ifVersion: 3 });
		expect(d.branches).toEqual([{ app: "di", branch: "feature/demo-wallet" }]);
	});

	it("refuses what the daemon would refuse, in words the form shows", () => {
		const d = draftFrom(group);
		expect(planProblems(d)).toEqual([]);
		expect(planProblems({ ...d, name: "  " })).toEqual(["Give the plan a name."]);
		expect(planProblems({ ...d, targetKind: "train" })).toEqual(["Pick a target train."]);
		expect(planProblems({ ...d, branches: [...d.branches, d.branches[0]] })).toEqual([
			"di:feature/demo-wallet is listed twice.",
		]);
		expect(planProblems({ ...d, accepted: [{ mrRef: "di!4", reason: "" }] })).toEqual([
			"Every accepted MR needs a reason.",
		]);
	});

	it("sends a train only with a train target, and the version the edit started from", () => {
		const d = { ...draftFrom(group), targetKind: "train" as const, targetTrain: "22-2026", ifVersion: 2 };
		expect(planInput(d)).toEqual({
			name: "demo-wallet",
			targetKind: "train",
			targetTrain: "22-2026",
			note: "",
			branches: d.branches,
			accepted: [],
			ifVersion: 2,
			via: "app",
		});
		expect(planInput({ ...d, targetKind: "dropped" }).targetTrain).toBeUndefined();
	});

	it("is dirty only when a field differs from where it started", () => {
		const d = draftFrom(group);
		expect(draftDirty(group, d)).toBe(false);
		expect(draftDirty(group, { ...d, note: "x" })).toBe(true);
		expect(draftDirty(group, { ...d, branches: d.branches.slice(1) })).toBe(true);
		const edited = { ...d, targetKind: "train" as const, targetTrain: "22-2026", ifVersion: 1 };
		const saved = {
			...group,
			plan: { ...planInput(edited), id: group.id, version: 1, updatedAt: "", updatedBy: "app" },
		};
		expect(draftDirty(saved as typeof group, edited)).toBe(false);
	});

	it("names a stale write as a conflict the form can tell apart", () => {
		const e = new PlanConflictError(4);
		expect(e).toBeInstanceOf(Error);
		expect(e.current).toBe(4);
		expect(e.message).toBe("This plan changed elsewhere. Reload it to see the change; your edit is kept here.");
	});
});

describe("target choices", () => {
	const t20 = train("20-2026", "2026-09-29", "2026-10-06", "past", [
		version("20-2026", "di", "2.3.0", "2026-09-29", "2026-10-06"),
	]);
	const t21 = train("21-2026", "2026-10-13", "2026-10-20", "current", [
		version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20"),
	]);
	const t22 = train("22-2026", "2026-10-27", "2026-11-03", "next", [
		version("22-2026", "di", "2.5.0", "2026-10-27", "2026-11-03"),
	]);

	it("offers trains not yet published, then the next two by number with estimated freeze days", () => {
		expect(targetChoices(board({ today: "2026-10-14", trains: [t20, t21, t22] }))).toEqual([
			{ key: "21-2026", label: "#21", freeze: "2026-10-13", guess: false },
			{ key: "22-2026", label: "#22", freeze: "2026-10-27", guess: false },
			{ key: "23-2026", label: "#23", freeze: "2026-11-10", guess: true },
			{ key: "24-2026", label: "#24", freeze: "2026-11-24", guess: true },
		]);
	});
});
