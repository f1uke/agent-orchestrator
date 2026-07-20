import { describe, expect, it } from "vitest";
import { activeIndexFromTops } from "./active-section";

describe("activeIndexFromTops", () => {
	it("picks the last section whose top has passed the anchor", () => {
		// Three stacked file sections; the reader has scrolled so section 1's
		// header sits above the anchor line and section 2's is still below it.
		expect(activeIndexFromTops([-400, -20, 300], 0)).toBe(1);
	});

	it("picks the first section while the reader is still above all of them", () => {
		expect(activeIndexFromTops([40, 500, 900], 0)).toBe(0);
	});

	it("picks the last section once everything has scrolled past", () => {
		expect(activeIndexFromTops([-900, -500, -100], 0)).toBe(2);
	});

	// A section whose top lands exactly on the anchor is the one being read —
	// off-by-one here makes the tree highlight lag a whole file behind.
	it("counts a section sitting exactly on the anchor as active", () => {
		expect(activeIndexFromTops([-100, 0, 200], 0)).toBe(1);
	});

	it("honours a non-zero anchor", () => {
		// With the anchor pushed down 80px, section 2 (top 60) has passed it.
		expect(activeIndexFromTops([-200, 10, 60, 400], 80)).toBe(2);
	});

	it("reports no active section for an empty list", () => {
		expect(activeIndexFromTops([], 0)).toBe(-1);
	});
});
