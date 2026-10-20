import { expect, test, type Locator, type Page } from "@playwright/test";
import { NARROW, WIDE, labelOverlaps, open, settle } from "./releases-kit";

const LAYERS = "?layers=sprint,llb,cards";

type Box = { x: number; y: number; width: number; height: number };
const box = async (l: Locator): Promise<Box> => (await l.boundingBox())!;
const right = (b: Box) => b.x + b.width;
const bottom = (b: Box) => b.y + b.height;

const sprintRow = (page: Page) => page.getByTestId("sprint-row-DEMO");

async function unfoldSprint(page: Page) {
	const row = sprintRow(page);
	await row.locator(".spsrc[data-sprint='active'] .csum").click({ position: { x: 10, y: 10 } });
	await settle(page);
	for (const key of ["carried", "web"]) {
		await row.locator(`[data-dest="${key}"] .dsum`).click();
		await settle(page);
	}
}

const rowOf = (page: Page, block: string, key: string) =>
	sprintRow(page).locator(`[data-dest="${block}"] .crow[data-key="${key}"]`);

const truncated = (l: Locator) => l.evaluate((el) => el.scrollWidth > el.clientWidth);

async function chipsInside(row: Locator, wrapped = false) {
	const r = await box(row);
	const tt = await box(row.locator(".tt"));
	const chips = row.locator(".tc > .ob");
	expect(await chips.count(), "the row carries chips").toBeGreaterThan(0);
	for (const c of await chips.all()) {
		const b = await box(c);
		if (wrapped) expect(b.y, "chips wrap under the full title").toBeGreaterThanOrEqual(bottom(tt) - 0.5);
		else expect(b.x, "chip starts after the visible title").toBeGreaterThanOrEqual(right(tt) - 0.5);
		expect(right(b), "chip ends inside the row").toBeLessThanOrEqual(right(r) + 0.5);
		expect(await c.evaluate((el) => el.scrollWidth <= el.clientWidth + 1), "chip text not cut").toBe(true);
	}
}

test.describe("releases card rows keep their chips and open on hover", () => {
	test.use({ viewport: NARROW });

	for (const [block, key, chip] of [
		["carried", "DEMO-361", "went to"],
		["web", "DEMO-362", "web"],
	] as const) {
		test(`sprint block ${block}: a long title ellipsises and the ${chip} chip stays whole`, async ({ page }) => {
			await open(page, LAYERS, "dense-lists");
			await unfoldSprint(page);
			const row = rowOf(page, block, key);
			await expect(row.locator(".tc")).toContainText(chip);
			expect(await truncated(row.locator(".tt")), "the title carries the ellipsis").toBe(true);
			await chipsInside(row);
			const mrs = await box(row.locator(".spill"));
			expect(right(mrs), "Jira status column stays in the row").toBeLessThanOrEqual(right(await box(row)) + 0.5);
		});

		test(`sprint block ${block}: hover opens the whole title over the rows below, leave puts it back, nothing moves`, async ({
			page,
		}) => {
			await open(page, LAYERS, "dense-lists");
			await unfoldSprint(page);
			const row = rowOf(page, block, key);
			const cell = row.locator(".t");
			const next = row.locator("xpath=following-sibling::*[contains(@class,'crow')][1]");
			const rest = await box(row);
			const restCell = await box(cell);
			const nextRest = (await next.count()) ? await box(next) : null;
			await row.hover();
			await expect.poll(async () => (await box(cell)).height).toBeGreaterThan(restCell.height + 4);
			await settle(page);
			const wide = await box(cell);
			expect(await truncated(row.locator(".tt")), "no ellipsis while open").toBe(false);
			expect(await box(row), "the row itself keeps its box").toEqual(rest);
			await chipsInside(row, true);
			if (nextRest) expect(await box(next), "no neighbour moves").toEqual(nextRest);
			const top = await page.evaluate(
				([x, y]) => document.elementFromPoint(x, y)?.closest(".crow")?.getAttribute("data-key"),
				[restCell.x + 20, wide.y + wide.height - 3],
			);
			expect(top, "the last line of the open title is on top and not clipped").toBe(key);
			await page.mouse.move(5, 5);
			await settle(page);
			expect(await box(cell)).toEqual(restCell);
		});

		test(`sprint block ${block}: keyboard focus opens the row, blur closes it`, async ({ page }) => {
			await open(page, LAYERS, "dense-lists");
			await unfoldSprint(page);
			const row = rowOf(page, block, key);
			const rest = (await box(row.locator(".t"))).height;
			await row.focus();
			await expect(row).toBeFocused();
			await page.keyboard.press("Shift+Tab");
			await page.keyboard.press("Tab");
			await expect(row).toBeFocused();
			await expect.poll(async () => (await box(row.locator(".t"))).height).toBeGreaterThan(rest + 4);
			await row.blur();
			await expect.poll(async () => (await box(row.locator(".t"))).height).toBeCloseTo(rest, 0);
		});
	}

	test("the open title is not clipped by its block and no block moves", async ({ page }) => {
		await open(page, LAYERS, "dense-lists");
		await unfoldSprint(page);
		const blocks = sprintRow(page).getByTestId("sprint-dest-block");
		const tops = async () => Promise.all((await blocks.all()).map(async (b) => (await box(b)).y));
		const before = await tops();
		const row = rowOf(page, "web", "DEMO-362");
		await row.hover();
		await settle(page);
		expect(await tops(), "no block moves").toEqual(before);
		const last = await box(row.locator(".t"));
		const block = await box(sprintRow(page).locator('[data-dest="web"]'));
		expect(bottom(last), "the open title reaches below its block's own height or fits in it").toBeGreaterThan(0);
		const at = await page.evaluate(
			([x, y]) => document.elementFromPoint(x, y)?.closest(".crow")?.getAttribute("data-key"),
			[last.x + 20, bottom(last) - 3],
		);
		expect(at, "the last line of the open title is on top").toBe("DEMO-362");
		void block;
	});
});

const listOf = (page: Page, train: string) => page.getByTestId(`cards-di:${train}-2026`);

async function unfoldLists(page: Page) {
	for (const t of ["22", "23"]) {
		await listOf(page, t).locator(".csum").click();
		await settle(page);
	}
}

for (const viewport of [NARROW, WIDE]) {
	test.describe(`release lists beside a close release at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport });

		test("two lists that start 11 days apart do not overlap, folded or unfolded", async ({ page }) => {
			await open(page, LAYERS, "dense-lists");
			const folded = [await box(listOf(page, "22").locator(".csum")), await box(listOf(page, "23").locator(".csum"))];
			expect(right(folded[0]), "folded header stops before the next list").toBeLessThanOrEqual(folded[1].x - 8 + 0.5);
			await unfoldLists(page);
			const a = await box(listOf(page, "22"));
			const b = await box(listOf(page, "23"));
			expect(right(a), "unfolded list stops before the next list").toBeLessThanOrEqual(b.x - 8 + 0.5);
			for (const r of await listOf(page, "22").locator(".crow").all())
				expect(right(await box(r))).toBeLessThanOrEqual(b.x - 8 + 0.5);
			const report = await labelOverlaps(page);
			expect(report.overlaps).toEqual([]);
		});

		test("a long title keeps its chips; hover opens the row over the next list, leave closes it", async ({ page }) => {
			await open(page, LAYERS, "dense-lists");
			await unfoldLists(page);
			const row = listOf(page, "22").locator('.crow[data-key="DEMO-223"]');
			const other = listOf(page, "23");
			const rest = await box(row);
			expect(await truncated(row.locator(".tt")), "ellipsis at rest").toBe(true);
			await row.hover();
			await expect.poll(async () => (await box(row)).width).toBeGreaterThan(rest.width + 20);
			await settle(page);
			const wide = await box(row);
			expect(await truncated(row.locator(".tt")), "no ellipsis while open").toBe(false);
			expect(right(wide), "the open row reaches into the next list's area").toBeGreaterThan((await box(other)).x);
			const seen = await page.evaluate(
				([x, y]) => document.elementFromPoint(x, y)?.closest(".crow")?.getAttribute("data-key"),
				[(await box(other)).x + 12, wide.y + wide.height / 2],
			);
			expect(seen, "the open row is above the neighbour list").toBe("DEMO-223");
			const bg = await row.evaluate((el) => getComputedStyle(el).backgroundColor);
			expect(bg, "opaque background").not.toMatch(/rgba\(.*, 0\)|transparent/);
			await page.mouse.move(5, 5);
			await settle(page);
			expect((await box(row)).width).toBeCloseTo(rest.width, 0);
			expect((await box(row)).height).toBeCloseTo(rest.height, 0);
		});

		test("keyboard focus opens a list row like hover does", async ({ page }) => {
			await open(page, LAYERS, "dense-lists");
			await unfoldLists(page);
			const row = listOf(page, "22").locator('.crow[data-key="DEMO-223"]');
			const rest = await box(row);
			await row.focus();
			await page.keyboard.press("Shift+Tab");
			await page.keyboard.press("Tab");
			await expect(row).toBeFocused();
			await expect.poll(async () => (await box(row)).width).toBeGreaterThan(rest.width + 20);
			await row.blur();
			await expect.poll(async () => (await box(row)).width).toBeCloseTo(rest.width, 0);
		});

		test("the list header opens on hover and is whole", async ({ page }) => {
			await open(page, LAYERS, "dense-lists");
			const head = listOf(page, "22").locator(".csum");
			const rest = await box(head);
			const need = await head.evaluate((el) => el.scrollWidth);
			expect(need, "the header needs more room than the cap gives").toBeGreaterThan(rest.width);
			await head.hover();
			await expect.poll(async () => (await box(head)).width).toBeGreaterThan(rest.width + 5);
			expect((await box(head)).width).toBeGreaterThanOrEqual(need - 1);
			const other = await box(listOf(page, "23").locator(".csum"));
			const seen = await page.evaluate(
				([x, y]) => document.elementFromPoint(x, y)?.closest(".csum") !== null,
				[other.x + 4, other.y + other.height / 2],
			);
			expect(seen).toBe(true);
			await page.mouse.move(5, 5);
			await expect.poll(async () => (await box(head)).width).toBeCloseTo(rest.width, 0);
		});
	});
}

test.describe("hover costs no React render", () => {
	test.use({ viewport: NARROW });

	test("moving over card rows writes no attribute, class or node", async ({ page }) => {
		await open(page, LAYERS, "dense-lists");
		await unfoldLists(page);
		await settle(page);
		const rows = await listOf(page, "22").locator(".crow").all();
		await page.evaluate(() => {
			(globalThis as unknown as { __w: string[] }).__w = [];
			const mo = new MutationObserver((l) =>
				(globalThis as unknown as { __w: string[] }).__w.push(
					...l.map((x) => `${x.type} ${(x.target as HTMLElement).className} ${x.attributeName}`),
				),
			);
			for (const row of document.querySelectorAll(".rel-row"))
				mo.observe(row, { subtree: true, childList: true, attributes: true, characterData: true });
			(globalThis as unknown as { __mo: MutationObserver }).__mo = mo;
		});
		for (const r of rows) {
			await r.hover();
			await page.waitForTimeout(60);
		}
		const n = await page.evaluate(() => {
			const g = globalThis as unknown as { __w: string[]; __mo: MutationObserver };
			g.__mo.disconnect();
			return g.__w;
		});
		expect(
			n.filter((w) => !w.includes("rel-day")),
			"the day guide is the page's own hover line",
		).toEqual([]);
	});
});

test.describe("hover row in the other theme", () => {
	test.use({ viewport: NARROW, colorScheme: "light" });

	test("an opened list row is opaque in the light theme too", async ({ page }) => {
		await page.addInitScript(() => localStorage.setItem("theme", "light"));
		await open(page, LAYERS, "dense-lists");
		await page.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
		await unfoldLists(page);
		const row = listOf(page, "22").locator('.crow[data-key="DEMO-223"]');
		await row.hover();
		await settle(page);
		const bg = await row.evaluate((el) => getComputedStyle(el).backgroundColor);
		const [r, g, b] = (bg.match(/[\d.]+/g) ?? []).map(Number);
		expect(Math.min(r, g, b), "a light surface").toBeGreaterThan(200);
		expect(bg).not.toMatch(/, 0\)/);
	});
});
