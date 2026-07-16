import { describe, it, expect } from "vitest";
import { effectiveNextRunAt, computeFraction, formatNextIn, formatInterval } from "./loop-format";

describe("effectiveNextRunAt", () => {
	it("keeps a future next-run unchanged", () => {
		expect(effectiveNextRunAt(200_000, 30_000, 190_000)).toBe(200_000);
	});
	it("rolls next-run forward once elapsed", () => {
		// next was at t=100s, interval 30s -> at now=145s next effective is 160s
		expect(effectiveNextRunAt(100_000, 30_000, 145_000)).toBe(160_000);
	});
	it("rolls exactly one interval when now == nextRun", () => {
		expect(effectiveNextRunAt(100_000, 30_000, 100_000)).toBe(130_000);
	});
	it("returns nextRun unchanged for a disabled interval", () => {
		expect(effectiveNextRunAt(100_000, 0, 200_000)).toBe(100_000);
	});
});

describe("computeFraction", () => {
	it("is 0 at cycle start and ~1 just before fire", () => {
		// cycle start = next - interval = 170s
		expect(computeFraction(200_000, 30_000, 170_000)).toBeCloseTo(0, 5);
		expect(computeFraction(200_000, 30_000, 199_999)).toBeCloseTo(1, 2);
	});
	it("rolls over to a fresh cycle exactly at fire", () => {
		// at the fire instant the ring rolls to the next cycle and reads empty
		expect(computeFraction(200_000, 30_000, 200_000)).toBeCloseTo(0, 5);
	});
	it("is 0.5 mid-cycle", () => {
		expect(computeFraction(200_000, 30_000, 185_000)).toBeCloseTo(0.5, 5);
	});
	it("stays in [0,1] after rollover", () => {
		const f = computeFraction(100_000, 30_000, 145_000);
		expect(f).toBeGreaterThanOrEqual(0);
		expect(f).toBeLessThanOrEqual(1);
	});
});

describe("formatNextIn", () => {
	it("shows <1s when due", () => {
		expect(formatNextIn(0)).toBe("<1s");
		expect(formatNextIn(500)).toBe("<1s");
	});
	it("shows m:ss under an hour", () => {
		expect(formatNextIn(9_000)).toBe("0:09");
		expect(formatNextIn(65_000)).toBe("1:05");
	});
	it("shows Xh Ym past an hour", () => {
		expect(formatNextIn(3_600_000)).toBe("1h 0m");
		expect(formatNextIn(3_900_000)).toBe("1h 5m");
	});
});

describe("formatInterval", () => {
	it("labels seconds, minutes, hours", () => {
		expect(formatInterval(30_000)).toBe("every 30s");
		expect(formatInterval(60_000)).toBe("every 1m");
		expect(formatInterval(300_000)).toBe("every 5m");
		expect(formatInterval(21_600_000)).toBe("every 6h");
	});
	it("labels a disabled loop as paused", () => {
		expect(formatInterval(0)).toBe("paused");
	});
});
