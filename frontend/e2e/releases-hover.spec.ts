import { expect, test, type Page } from "@playwright/test";
import { NARROW, settle, open } from "./releases-kit";

const DW = 36;
const line = (page: Page) => page.locator(".rel-bg .hovercol");
const chip = (page: Page, col: number) => page.locator(`.rel-day[data-col="${col}"]`);

async function column(page: Page, col: number) {
	const c = (await chip(page, col).boundingBox())!;
	return { x: c.x + c.width / 2, centre: c.x + c.width / 2 };
}

test.describe("releases hover guide line", () => {
	test.use({ viewport: NARROW });

	test("follows the pointer, snaps to the day column and lights that day's chip", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards");
		await expect(line(page)).toBeHidden();
		const todayCol = await page.locator(".rel-day.td").getAttribute("data-col");
		const col = Number(todayCol) + 5;
		const at = await column(page, col);
		await page.mouse.move(at.x - 7, 400);
		await expect(line(page)).toBeVisible();
		const box = (await line(page).boundingBox())!;
		expect(Math.abs(box.x + box.width / 2 - at.centre)).toBeLessThanOrEqual(1);
		await expect(chip(page, col)).toHaveClass(/\bhv\b/);
		await expect(page.locator(".rel-day.hv")).toHaveCount(1);

		const canvas = (await page.locator(".rel-canvas").boundingBox())!;
		const tall = await page.locator(".rel-canvas").evaluate((el) => el.scrollHeight);
		expect(box.height, "the line runs the full height through the header and every row").toBeGreaterThanOrEqual(
			Math.min(tall, canvas.height) - 2,
		);

		await page.mouse.move(at.x + DW - 8, 400);
		await expect(chip(page, col + 1)).toHaveClass(/\bhv\b/);
		await expect(chip(page, col)).not.toHaveClass(/\bhv\b/);
	});

	test("the chip is faded grey, not the accent the today chip wears", async ({ page }) => {
		await open(page);
		const todayCol = Number(await page.locator(".rel-day.td").getAttribute("data-col"));
		const at = await column(page, todayCol + 3);
		await page.mouse.move(at.x, 400);
		await expect(chip(page, todayCol + 3)).toHaveClass(/\bhv\b/);
		const bg = (el: string) => page.locator(el).evaluate((n) => getComputedStyle(n).backgroundColor);
		const hover = await bg(`.rel-day[data-col="${todayCol + 3}"] span`);
		const today = await bg(".rel-day.td span");
		expect(hover).not.toBe(today);
		const [r, g, b] = (hover.match(/[\d.]+/g) ?? []).map(Number);
		expect(Math.max(r, g, b) - Math.min(r, g, b), "grey: red, green and blue sit together").toBeLessThan(40);
	});

	test("leaving the timeline hides the line and the chip", async ({ page }) => {
		await open(page);
		const todayCol = Number(await page.locator(".rel-day.td").getAttribute("data-col"));
		const at = await column(page, todayCol + 2);
		await page.mouse.move(at.x, 400);
		await expect(line(page)).toBeVisible();
		await page.mouse.move(at.x, 4);
		await expect(line(page)).toBeHidden();
		await expect(page.locator(".rel-day.hv")).toHaveCount(0);
	});

	test("over the sticky label column there is no line", async ({ page }) => {
		await open(page);
		await page.mouse.move(120, 400);
		await expect(line(page)).toBeHidden();
	});

	test("a card under the line still opens on click, and a stage hover is unaffected", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards");
		const row = page.getByTestId("sprint-row-DEMO");
		await row.locator(".spsrc .csum").click();
		await row.locator(".dsum").first().click();
		await settle(page);
		const crow = row.locator(".crow[data-key]").first();
		const b = (await crow.boundingBox())!;
		await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
		await expect(line(page)).toBeVisible();
		const key = await crow.getAttribute("data-key");
		const hit = await page.evaluate(
			([x, y]) => (document.elementFromPoint(x, y) as HTMLElement | null)?.closest(".hovercol") !== null,
			[b.x + b.width / 2, b.y + b.height / 2],
		);
		expect(hit, "hit tests pass through the line").toBe(false);
		await crow.click();
		await expect(page).toHaveURL(new RegExp(`sheet=card(%3A|:)${key}`));
	});

	test("on today the today line and chip stay on top", async ({ page }) => {
		await open(page);
		const todayCol = Number(await page.locator(".rel-day.td").getAttribute("data-col"));
		const at = await column(page, todayCol);
		await page.mouse.move(at.x, 400);
		await expect(line(page)).toBeVisible();
		const today = page.locator(".rel-bg .today");
		await expect(today).toBeVisible();
		const [h, t] = await Promise.all([line(page).boundingBox(), today.boundingBox()]);
		expect(Math.abs(h!.x - t!.x)).toBeLessThanOrEqual(1);
		const order = await page.evaluate(() => {
			const kids = [...document.querySelectorAll(".rel-bg > *")];
			return [
				kids.findIndex((k) => k.classList.contains("hovercol")),
				kids.findIndex((k) => k.classList.contains("today")),
			];
		});
		expect(order[0], "the today line is drawn after, so above, the hover line").toBeLessThan(order[1]);
		const bg = await page.locator(".rel-day.td span").evaluate((n) => getComputedStyle(n).backgroundColor);
		const accent = await page.evaluate(() => {
			const probe = document.createElement("i");
			probe.style.background = "var(--accent)";
			document.body.append(probe);
			const c = getComputedStyle(probe).backgroundColor;
			probe.remove();
			return c;
		});
		expect(bg, "today's chip keeps the accent while the pointer is on it").toBe(accent);
	});

	test("a pan still moves the timeline and the line follows the scroll", async ({ page }) => {
		await open(page);
		const sc = page.getByTestId("release-timeline");
		const before = await sc.evaluate((el) => el.scrollLeft);
		const box = (await sc.boundingBox())!;
		await page.mouse.move(box.x + 700, 420);
		await page.mouse.down();
		await page.mouse.move(box.x + 500, 420, { steps: 6 });
		await page.mouse.up();
		await expect.poll(() => sc.evaluate((el) => el.scrollLeft)).toBeGreaterThan(before + 100);
		await page.mouse.move(box.x + 760, 420);
		await expect(line(page)).toBeVisible();
		const lb = (await line(page).boundingBox())!;
		const col = await page.locator(".rel-day.hv").getAttribute("data-col");
		const cb = (await chip(page, Number(col)).boundingBox())!;
		expect(Math.abs(lb.x + lb.width / 2 - (cb.x + cb.width / 2))).toBeLessThanOrEqual(1);
	});
});
