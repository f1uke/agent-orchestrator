import { describe, expect, it } from "vitest";
import type { SessionStatus } from "../renderer/types/workspace";
import { ALL_COMPANION_STATUSES, groundFor, isAnchored, sceneAnimates, sceneFor } from "./scene";
import { modeFor } from "./mode";

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

describe("sceneFor", () => {
	it("gives every status a scene", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			expect(sceneFor(status), status).toBeDefined();
		}
	});

	it("keeps the ground slot exactly as groundFor decides it", () => {
		// The anchoring rule reads groundFor; if the scene could disagree, a working
		// Proc could be drawn at a desk and still wander off it.
		for (const status of ALL_COMPANION_STATUSES) {
			expect(sceneFor(status).ground, status).toBe(groundFor(status));
		}
	});

	it("shows each state the design named by its prop", () => {
		expect(sceneFor("idle")).toMatchObject({ ground: "bed", emit: "zzz" });
		expect(sceneFor("working")).toMatchObject({ ground: "desk", cord: "streaming" });
		expect(sceneFor("todo")).toMatchObject({ ground: "crate" });
		expect(sceneFor("ci_failed")).toMatchObject({ emit: "sparks", cord: "sparking" });
		expect(sceneFor("merged")).toMatchObject({ emit: "confetti", cord: "unplugged" });
		expect(sceneFor("terminated")).toMatchObject({ cord: "unplugged" });
		expect(sceneFor("needs_input")).toMatchObject({ held: "sign-question", cord: "tugging" });
	});

	it("gives no two statuses the same scene, so every state reads as itself", () => {
		// This is the human's actual complaint: pets that do not show their state.
		// Two states drawn identically are two states the overlay cannot tell you.
		const seen = new Map<string, SessionStatus>();
		for (const status of ALL_COMPANION_STATUSES) {
			const scene = sceneFor(status);
			const key = `${scene.ground}/${scene.held}/${scene.emit}/${scene.cord}`;
			expect(seen.get(key), `${status} looks identical to ${seen.get(key)}`).toBeUndefined();
			seen.set(key, status);
		}
	});

	it("unplugs the cord exactly when there is no live session behind it", () => {
		// The cord is the LINK. A Proc still plugged in for a session that merged,
		// died or went silent is claiming a connection we do not have.
		for (const status of ALL_COMPANION_STATUSES) {
			const unplugged = sceneFor(status).cord === "unplugged";
			expect(unplugged, status).toBe(status === "merged" || status === "terminated" || status === "no_signal");
		}
	});

	it("never gives a walking state a cord that is plugged into the floor", () => {
		// A Proc that ambles cannot be plugged into a ground prop it is walking away
		// from, so "plugged in" and "can walk" must not both be true.
		for (const status of ALL_COMPANION_STATUSES) {
			if (sceneFor(status).cord !== "unplugged" && !isAnchored(status)) {
				expect(modeFor(status), status).not.toBe("anchor");
			}
		}
	});

	it("keeps held props off the states that already have a ground telling the story", () => {
		// Cord right, props left, one message each. A crate AND a page is two props
		// competing to say the same thing.
		expect(sceneFor("todo").held).toBe("none");
		expect(sceneFor("idle").held).toBe("none");
	});
});

describe("sceneAnimates", () => {
	it("is true only for the states whose scene actually moves", () => {
		expect(sceneAnimates("idle")).toBe(true);
		expect(sceneAnimates("working")).toBe(true);
		expect(sceneAnimates("ci_failed")).toBe(true);
		expect(sceneAnimates("merged")).toBe(true);
		expect(sceneAnimates("needs_input")).toBe(true);
	});

	it("leaves a quiet state genuinely quiet", () => {
		// no_signal must not twitch: motion would assert liveness we do not have.
		expect(sceneAnimates("no_signal")).toBe(false);
		expect(sceneAnimates("terminated")).toBe(false);
		expect(sceneAnimates("unknown")).toBe(false);
		expect(sceneAnimates("pr_open")).toBe(false);
	});
});
