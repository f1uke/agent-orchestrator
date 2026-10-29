import { describe, expect, it } from "vitest";
import { dueChip } from "./dueChip";
import type { AlertDue } from "./types";

const due = (workDays: number, over: Partial<AlertDue> = {}): AlertDue => ({
	on: "2026-11-03",
	event: "publish",
	workDays,
	...over,
});

describe("dueChip", () => {
	it.each([
		[-2, "2 days late", "failed"],
		[-1, "1 day late", "failed"],
		[0, "Today", "failed"],
		[1, "1 day left", "failed"],
		[2, "2 days left", "waiting"],
		[3, "3 days left", "waiting"],
		[4, "4 days left", "unknown"],
		[8, "8 days left", "unknown"],
	])("reads %i working days as %s in tone %s", (workDays, text, tone) => {
		expect(dueChip(due(workDays))).toEqual({ text, tone });
	});

	it("says Missed in red whatever the count", () => {
		expect(dueChip(due(-3, { missed: true }))).toEqual({ text: "Missed", tone: "failed" });
		expect(dueChip(due(5, { missed: true }))).toEqual({ text: "Missed", tone: "failed" });
	});
});
