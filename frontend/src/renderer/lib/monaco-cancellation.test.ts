import { describe, expect, it } from "vitest";
import { isMonacoCancellation } from "./monaco-cancellation";

describe("isMonacoCancellation", () => {
	it("matches Monaco's CancellationError shape exactly", () => {
		const err = new Error("Canceled");
		err.name = "Canceled";
		expect(isMonacoCancellation(err)).toBe(true);
	});

	it("does not swallow anything else", () => {
		expect(isMonacoCancellation(new Error("Canceled"))).toBe(false);
		const other = new Error("Canceled by the user");
		other.name = "Canceled";
		expect(isMonacoCancellation(other)).toBe(false);
		expect(isMonacoCancellation("Canceled")).toBe(false);
		expect(isMonacoCancellation(undefined)).toBe(false);
	});
});
