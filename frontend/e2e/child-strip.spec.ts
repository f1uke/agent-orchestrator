import { expect, test } from "@playwright/test";

// The subagent strip is one pip per state so it stays on ONE line inside the
// card at every board width; its first design drew a chip per subagent and
// stacked four lines on the board's ~190px column. The demo board's working card
// carries one subagent in each of four states. Painted boxes at a real width are
// what this checks, which jsdom does not lay out.
for (const width of [960, 1280, 1440, 1800]) {
	test(`the subagent strip is one line inside its card at ${width}px`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		await page.goto("/");
		const strip = page.locator("[data-child-strip]").first();
		await expect(strip).toBeVisible();
		const report = await strip.evaluate((el) => {
			const card = (el.parentElement as HTMLElement).getBoundingClientRect();
			const pips = [...el.querySelectorAll("[data-child-pip]")].map((p) => p.getBoundingClientRect());
			const escapes = [...el.querySelectorAll("*")].filter((c) => {
				const b = c.getBoundingClientRect();
				return b.width > 0 && (b.right > card.right + 0.5 || b.left < card.left - 0.5);
			}).length;
			const rows = new Set(pips.map((b) => Math.round(b.top))).size;
			return { pips: pips.length, rows, escapes };
		});
		expect(report).toEqual({ pips: 4, rows: 1, escapes: 0 });
	});
}
