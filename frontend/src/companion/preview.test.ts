import { describe, expect, it } from "vitest";
import { CAST } from "./cast";
import { ALL_COMPANION_STATUSES, sceneFor } from "./scene";
import { PREVIEW_BUBBLES, previewRoster } from "./preview";

describe("previewRoster", () => {
	it("shows every one of the fifteen states", () => {
		expect(previewRoster().map((entry) => entry.status)).toEqual(ALL_COMPANION_STATUSES);
	});

	it("shows every character across the set, so the gallery is a cast list too", () => {
		const used = new Set(previewRoster().map((entry) => entry.cast.id));

		expect(used.size).toBe(CAST.length);
	});

	it("gives each state a plain-English line, because a status id is not an explanation", () => {
		for (const entry of previewRoster()) {
			expect(entry.label.length, entry.status).toBeGreaterThan(0);
			expect(entry.label, entry.status).not.toContain("_");
		}
	});

	it("is stable, so the gallery does not reshuffle every time Settings is opened", () => {
		expect(previewRoster()).toEqual(previewRoster());
	});

	it("puts a bubble only on the states that would really have one", () => {
		// A Proc without a bubble is just a Proc — the preview must not imply that
		// every state chats, or it teaches the wrong thing about the feature.
		for (const entry of previewRoster()) {
			if (!entry.bubble) continue;
			expect(sceneFor(entry.status).cord, entry.status).not.toBe("unplugged");
		}
	});

	it("never puts a raw command in a preview bubble, exactly as the real thing must not", () => {
		for (const entry of previewRoster()) {
			if (!entry.bubble) continue;
			expect(entry.bubble.text, entry.status).not.toMatch(/[|&;]{1,2}\s*\S|\$\(|(^|\s)[~/]\S*\//);
		}
	});
});

describe("PREVIEW_BUBBLES", () => {
	it("walks a claim through its whole life, so the decay is visible and not just described", () => {
		expect(PREVIEW_BUBBLES.map((sample) => sample.decay)).toEqual(["fresh", "fading", "settled"]);
	});

	it("keeps the decay ladder unalarmed — staleness is not an emergency", () => {
		expect(PREVIEW_BUBBLES.every((sample) => sample.tone !== "alert")).toBe(true);
	});

	it("reserves the alert tone for the one state that is genuinely blocked on you", () => {
		// StatusReason splits real from inferred. An inferred quiet gets a settled
		// bubble and no alarm; only a genuine block is allowed to shout.
		const alerts = previewRoster().filter((entry) => entry.bubble?.tone === "alert");

		expect(alerts.map((entry) => entry.status)).toEqual(["needs_input"]);
	});
});
