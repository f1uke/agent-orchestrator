import { expect, test } from "@playwright/test";

// THROWAWAY: proves the aggregate glyph-legibility job goes red. Reverted before merge.
test("deliberately fails", async ({ page }) => {
	await page.goto("/");
	expect(1).toBe(2);
});
