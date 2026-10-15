import { describe, expect, it } from "vitest";
import { addDays, dayDiff, dayLabel, shortDay, weekday } from "./day";

describe("civil days", () => {
	it("counts days across a month end and a DST change without drift", () => {
		expect(dayDiff("2026-10-30", "2026-11-02")).toBe(3);
		expect(dayDiff("2026-03-28", "2026-03-30")).toBe(2);
		expect(dayDiff("2026-11-02", "2026-10-30")).toBe(-3);
	});

	it("adds days across a year end", () => {
		expect(addDays("2026-12-30", 3)).toBe("2027-01-02");
		expect(addDays("2026-03-01", -1)).toBe("2026-02-28");
	});

	it("names the weekday of the civil date, not of a UTC instant", () => {
		expect(weekday("2026-10-12")).toBe(1);
		expect(weekday("2026-10-18")).toBe(0);
	});

	it("formats dates the way the page shows them", () => {
		expect(dayLabel("2026-10-13")).toBe("Tue 13 Oct");
		expect(shortDay("2026-10-13")).toBe("13 Oct");
	});
});
