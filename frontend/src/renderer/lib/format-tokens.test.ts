import { describe, expect, it } from "vitest";
import type { TokenUsage } from "../types/workspace";
import { formatCompactTokens, tokenUsageTitle } from "./format-tokens";

describe("formatCompactTokens", () => {
	it("formats across magnitudes", () => {
		expect(formatCompactTokens(0)).toBe("0");
		expect(formatCompactTokens(942)).toBe("942");
		expect(formatCompactTokens(1234)).toBe("1.2K");
		expect(formatCompactTokens(23_506_652)).toBe("23.5M");
		expect(formatCompactTokens(156_346_801)).toBe("156M");
		expect(formatCompactTokens(1_200_000_000)).toBe("1.2B");
	});

	it("guards non-finite / negative", () => {
		expect(formatCompactTokens(Number.NaN)).toBe("0");
		expect(formatCompactTokens(-5)).toBe("0");
	});
});

describe("tokenUsageTitle", () => {
	const usage: TokenUsage = {
		input: 82010,
		cacheCreation: 2525549,
		cacheRead: 152740511,
		output: 998731,
		turns: 602,
		rawTotal: 156346801,
		costWeighted: 23506652,
		runaway: false,
		updatedAt: "2026-07-13T00:00:00Z",
	};

	it("spells out the breakdown", () => {
		const title = tokenUsageTitle(usage);
		expect(title).toContain("156,346,801 tokens raw");
		expect(title).toContain("602 turns");
		expect(title).toContain("cache-read 152,740,511");
		expect(title).not.toContain("runaway");
	});

	it("adds a runaway note when flagged", () => {
		expect(tokenUsageTitle({ ...usage, runaway: true })).toContain("runaway");
	});
});
