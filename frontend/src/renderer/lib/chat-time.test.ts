import { describe, expect, it } from "vitest";
import { formatDuration } from "./chat-time";

describe("formatDuration", () => {
	it.each([
		[0, "0s"],
		[999, "0s"],
		[45_000, "45s"],
		[59_000, "59s"],
		[59_999, "59s"],
		[60_000, "1m 0s"],
		[69_000, "1m 9s"],
		[134_000, "2m 14s"],
		[3_599_000, "59m 59s"],
		[3_600_000, "1h 0m"],
		[3_900_000, "1h 5m"],
		[-5_000, "0s"],
	])("%i ms reads %s", (ms, want) => {
		expect(formatDuration(ms)).toBe(want);
	});
});
