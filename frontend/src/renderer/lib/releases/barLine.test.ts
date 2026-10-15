import { describe, expect, it } from "vitest";
import { barLine, type BarPart } from "./barLine";
import { stage, version } from "./testkit";
import type { AppVersion } from "./types";

const today = "2026-10-14";
const mr = (over: Partial<NonNullable<AppVersion["releaseMr"]>["mr"]> = {}) => ({
	mr: {
		iid: 77,
		project: 1,
		title: "Release 2.4.0",
		state: "opened",
		author: "Example Person",
		sourceBranch: "release/2.4.0",
		targetBranch: "main",
		createdOn: "2026-10-13",
		url: "https://gitlab.example.com/mr/77",
		...over,
	},
});
const text = (parts: BarPart[]) => parts.map((p) => p.text).join(" · ");

describe("barLine", () => {
	const base = version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20", {
		stages: [
			stage("freeze", "2026-10-13", "done"),
			stage("approve", "2026-10-15", "waiting"),
			stage("publish", "2026-10-20", "todo"),
		],
	});

	it("says the release MR state, the next stage not done and how far, and that no tag exists", () => {
		const parts = barLine({ ...base, releaseMr: mr() }, today);
		expect(text(parts)).toBe("!77 open · next Approval in 1d · no tag yet");
		expect(parts.map((p) => p.tone)).toEqual(["running", undefined, "passive"]);
	});

	it("puts conflicts before a failed pipeline, and a failed pipeline before a running one", () => {
		const conflicts = {
			...mr(),
			detail: { iid: 77, project: 1, conflicts: true, pipeline: { id: 1, status: "failed", url: "" } },
		};
		expect(barLine({ ...base, releaseMr: conflicts }, today)[0]).toEqual({
			id: "mr",
			text: "!77 conflicts",
			tone: "failed",
		});
		const failed = {
			...mr(),
			detail: { iid: 77, project: 1, conflicts: false, pipeline: { id: 1, status: "failed", url: "" } },
		};
		expect(barLine({ ...base, releaseMr: failed }, today)[0]).toEqual({
			id: "mr",
			text: "!77 pipeline failed",
			tone: "failed",
		});
		const running = {
			...mr(),
			detail: { iid: 77, project: 1, conflicts: false, pipeline: { id: 1, status: "running", url: "" } },
		};
		expect(barLine({ ...base, releaseMr: running }, today)[0].text).toBe("!77 pipeline running");
		expect(barLine({ ...base, releaseMr: mr({ state: "merged" }) }, today)[0]).toEqual({
			id: "mr",
			text: "!77 merged",
			tone: "done",
		});
	});

	it("says no release MR when there is none", () => {
		expect(barLine(base, today)[0]).toEqual({ id: "mr", text: "no release MR", tone: "passive" });
	});

	it("counts a stage due today, and one already late", () => {
		expect(barLine(base, "2026-10-15")[1].text).toBe("next Approval today");
		const late = { ...base, stages: [stage("approve", "2026-10-12", "late")] };
		expect(barLine(late, today)[1].text).toBe("next Approval 2d late");
	});

	it("prefers the first open stage still ahead over one already past", () => {
		const v = { ...base, stages: [stage("scan", "2026-10-13", "failed"), stage("approve", "2026-10-15", "waiting")] };
		expect(barLine(v, today)[1].text).toBe("next Approval in 1d");
	});

	it("says published Nd ago once every stage is done, and names the tag", () => {
		const v = {
			...base,
			stages: [stage("freeze", "2026-10-13", "done"), stage("publish", "2026-10-20", "done")],
			tag: { name: "2.4.0", project: 1, url: "", commitOn: "2026-10-19", newest: true, mergesAfter: [] },
		};
		const parts = barLine(v, "2026-10-28");
		expect(text(parts)).toBe("no release MR · published 8d ago · tag 2.4.0");
		expect(parts[1].tone).toBe("done");
	});

	it("ignores a stage no system records when picking the next one", () => {
		const v = {
			...base,
			stages: [stage("publish", "2026-10-20", "done"), stage("force", undefined, "not_observable")],
		};
		expect(barLine(v, "2026-10-21")[1].text).toBe("published 1d ago");
	});
});
