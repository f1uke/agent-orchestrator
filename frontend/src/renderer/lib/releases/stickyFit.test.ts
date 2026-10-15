import { describe, expect, it } from "vitest";
import { BAR_DROP_ORDER, fitParts, labelHidden } from "./stickyFit";

const widths: Record<string, number> = { tt: 30, mr: 60, next: 80, tag: 50, dates: 70 };

function needAfterHiding(hidden: ReadonlySet<string>): number {
	const line1 = 40 + (hidden.has("tt") ? 0 : widths.tt) + (hidden.has("dates") ? 0 : widths.dates);
	const line2 = ["mr", "next", "tag"].reduce((w, k) => w + (hidden.has(k) ? 0 : widths[k]), 0);
	return Math.max(line1, line2) + 20;
}

describe("fitParts", () => {
	const hiddenAt = (avail: number) => [...fitParts(BAR_DROP_ORDER, avail, needAfterHiding)];

	it("hides nothing when the bar is wide enough", () => {
		expect(hiddenAt(400)).toEqual([]);
	});

	it("drops line 2 from the right (tag, then next), then the dates, the release MR, and the #N tag last", () => {
		expect(hiddenAt(210)).toEqual([]);
		expect(hiddenAt(205)).toEqual(["tag"]);
		expect(hiddenAt(155)).toEqual(["tag", "next", "dates"]);
		expect(hiddenAt(10)).toEqual(["tag", "next", "dates", "mr", "tt"]);
	});

	it("measures again only until the rest fits", () => {
		let calls = 0;
		fitParts(BAR_DROP_ORDER, 205, (h) => {
			calls++;
			return needAfterHiding(h);
		});
		expect(calls).toBe(2);
	});
});

describe("labelHidden", () => {
	const edge = 280;
	it("hides a sticky label whose box slid under the app column with too little left to show it", () => {
		expect(labelHidden({ boxLeft: 100, boxRight: 300, labelWidth: 40, edge })).toBe(true);
	});
	it("keeps it when enough of the box is still visible", () => {
		expect(labelHidden({ boxLeft: 100, boxRight: 400, labelWidth: 40, edge })).toBe(false);
	});
	it("keeps it when the box starts right of the column", () => {
		expect(labelHidden({ boxLeft: 300, boxRight: 310, labelWidth: 40, edge })).toBe(false);
	});
});
