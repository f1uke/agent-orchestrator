import { expect, test } from "@playwright/test";

const ROUTES = ["/", "/projects/ao-demo", "/projects/ao-demo/sessions/demo-working"];

test.describe("an idle window runs no endless animation", () => {
	test.use({ viewport: { width: 1920, height: 1080 } });

	for (const route of ROUTES) {
		test(`#${route} with working sessions on screen`, async ({ page }) => {
			await page.goto(`/#${route}`);
			await expect(page.locator("[data-session-glyph]").first()).toBeVisible({ timeout: 15_000 });
			const endless = await page.evaluate(() =>
				document
					.getAnimations()
					.filter((a) => a.effect?.getComputedTiming().iterations === Infinity)
					.map((a) => `${(a as CSSAnimation).animationName} on ${(a.effect as KeyframeEffect).target?.outerHTML.slice(0, 80)}`),
			);
			expect(endless).toEqual([]);
		});
	}
});
