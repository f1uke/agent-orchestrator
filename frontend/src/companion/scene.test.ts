import { describe, expect, it } from "vitest";
import type { SessionStatus } from "../renderer/types/workspace";
import { ALL_COMPANION_STATUSES, groundFor, isAnchored } from "./scene";

describe("groundFor", () => {
	it("puts the working states at a desk", () => {
		expect(groundFor("working")).toBe("desk");
		expect(groundFor("ci_failed")).toBe("desk");
	});

	it("puts idle in a bed and todo in a crate", () => {
		expect(groundFor("idle")).toBe("bed");
		expect(groundFor("todo")).toBe("crate");
	});

	it("gives no_signal no ground, because we must not depict work we cannot see", () => {
		expect(groundFor("no_signal")).toBe("none");
	});

	it("gives every other status no ground", () => {
		const grounded: SessionStatus[] = ["working", "ci_failed", "idle", "todo"];
		for (const status of ALL_COMPANION_STATUSES) {
			if (grounded.includes(status)) continue;
			expect(groundFor(status)).toBe("none");
		}
	});
});

describe("isAnchored", () => {
	it("is derived from the ground, not from a second table", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			expect(isAnchored(status)).toBe(groundFor(status) !== "none");
		}
	});
});
