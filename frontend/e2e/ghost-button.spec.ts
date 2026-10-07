import { expect, test, type Locator, type Page } from "@playwright/test";

// A ghost button's hover must show on whatever it sits on. Its hover was once
// the card colour itself, so on a card, in a dialog, and on the light page it
// painted nothing at all.

type Rgba = [number, number, number, number];

const parse = (css: string): Rgba => {
	const [r, g, b, a = 1] = css.match(/[\d.]+/g)!.map(Number);
	return [r, g, b, a];
};

/** How far the colour under the pointer moves on hover: the button's wash laid over the first opaque colour behind it. */
async function hoverShift(button: Locator): Promise<number> {
	const [wash, behind] = await button.evaluate((el) => {
		let behind = "rgb(0, 0, 0)";
		for (let n = el.parentElement; n; n = n.parentElement) {
			const c = getComputedStyle(n).backgroundColor;
			if (c !== "rgba(0, 0, 0, 0)") {
				behind = c;
				break;
			}
		}
		return [getComputedStyle(el).backgroundColor, behind];
	});
	const [r, g, b, a] = parse(wash);
	const base = parse(behind);
	return [r, g, b].reduce((sum, c, i) => sum + Math.abs(c * a + base[i] * (1 - a) - base[i]), 0);
}

async function expectVisibleHover(page: Page, button: Locator) {
	await page.mouse.move(0, 0);
	await expect.poll(() => hoverShift(button)).toBe(0);
	await button.hover();
	await expect.poll(() => hoverShift(button)).toBeGreaterThan(12);
}

for (const theme of ["dark", "light"]) {
	test(`a ghost button shows its hover on the page, on a card and in a dialog (${theme})`, async ({ page }) => {
		await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");
		const inspector = page.locator("#inspector");
		await inspector.getByRole("tab", { name: "Testiny" }).click();
		await expect(inspector.getByRole("article").first()).toBeVisible();
		// After the shell has mounted, or its own theme effect paints over this one.
		await page.evaluate((t) => {
			document.documentElement.dataset.theme = t;
		}, theme);
		await expect(page.locator("body")).toHaveCSS(
			"background-color",
			theme === "dark" ? "rgb(10, 11, 13)" : "rgb(252, 252, 252)",
		);

		await expectVisibleHover(page, inspector.getByRole("button", { name: "Refresh" }));
		await expectVisibleHover(
			page,
			inspector.getByRole("article").first().getByRole("button", { name: "Reveal in Finder" }),
		);

		await page.getByRole("button", { name: "Kill session" }).first().click();
		await expectVisibleHover(page, page.getByRole("dialog").getByRole("button", { name: "Cancel" }));
	});
}
