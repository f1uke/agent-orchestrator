import { describe, expect, it } from "vitest";
import { MAX_DISPLAY_NAME_LEN, clampDisplayName } from "./display-name";

describe("clampDisplayName", () => {
	it("leaves a name at or under the cap untouched", () => {
		const atCap = "x".repeat(MAX_DISPLAY_NAME_LEN);
		expect(clampDisplayName(atCap)).toBe(atCap);
		expect(clampDisplayName("short")).toBe("short");
	});

	it("counts runes, not UTF-16 code units, so Thai gets the same budget as Latin", () => {
		// Thai spends one rune per character even though each is 3 UTF-8 bytes; a
		// byte- or grapheme-based cut here would leave Thai with a fraction of the
		// budget, which is the regression this guards.
		const thai = "ก".repeat(MAX_DISPLAY_NAME_LEN + 5);
		expect([...clampDisplayName(thai)]).toHaveLength(MAX_DISPLAY_NAME_LEN);
	});

	it("cuts an over-long name down to exactly the cap", () => {
		expect([...clampDisplayName("y".repeat(100))]).toHaveLength(MAX_DISPLAY_NAME_LEN);
	});

	it("never cuts inside an astral character", () => {
		const withEmoji = "🙂".repeat(MAX_DISPLAY_NAME_LEN + 3);
		const cut = clampDisplayName(withEmoji);
		expect([...cut]).toHaveLength(MAX_DISPLAY_NAME_LEN);
		expect(cut.endsWith("🙂")).toBe(true);
	});
});
