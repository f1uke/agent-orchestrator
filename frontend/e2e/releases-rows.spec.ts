import { expect, test, type Locator, type Page } from "@playwright/test";
import { NARROW, WIDE, labelOverlaps, open, settle } from "./releases-kit";

const LAYERS = "?layers=sprint,llb,cards";

type Box = { x: number; y: number; width: number; height: number };
const box = async (l: Locator): Promise<Box> => (await l.boundingBox())!;
const right = (b: Box) => b.x + b.width;

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

async function chipsInside(row: Locator) {
	const cell = await box(row.locator(".t"));
	const tt = await box(row.locator(".tt"));
	const chips = row.locator(".tc > .ob");
	expect(await chips.count(), "the row carries chips").toBeGreaterThan(0);
	for (const c of await chips.all()) {
		const b = await box(c);
		expect(b.x, "chip starts after the visible title").toBeGreaterThanOrEqual(right(tt) - 0.5);
		expect(Math.abs(b.y + b.height / 2 - (tt.y + tt.height / 2)), "chip sits on the title's line").toBeLessThan(2);
		expect(right(b), "chip ends inside the title cell").toBeLessThanOrEqual(right(cell) + 0.5);
		expect(await c.evaluate((el) => el.scrollWidth <= el.clientWidth + 1), "chip text not cut").toBe(true);
	}
}

async function chipsVisible(row: Locator) {
	const chips = row.locator(".tc > .ob");
	expect(await chips.count(), "the row carries warning chips").toBeGreaterThan(0);
	for (const c of await chips.all()) {
		const b = await box(c);
		const hit = await c.evaluate(
			(el, pts) => pts.every(([x, y]) => el.contains(document.elementFromPoint(x, y))),
			[
				[b.x + 2, b.y + b.height / 2],
				[right(b) - 2, b.y + b.height / 2],
			],
		);
		expect(hit, `chip "${await c.textContent()}" is whole and on top`).toBe(true);
		expect(await c.evaluate((el) => el.scrollWidth <= el.clientWidth + 1), "chip text not cut").toBe(true);
	}
}

const visibleCount = (l: Locator) =>
	l.evaluateAll((els) => els.filter((e) => e.checkVisibility({ visibilityProperty: true })).length);

async function onTop(l: Locator) {
	const b = await box(l);
	return l.evaluate(
		(el, [x, y]) => el.contains(document.elementFromPoint(x, y)),
		[b.x + b.width / 2, b.y + b.height / 2],
	);
}

const REST_PARTS = [".tt", ".k", "> .spill", "[data-testid='mr-glyph']"];
const OPEN_PARTS = [".tt", ".k", ".tail .spill", ".tail .mrd"];

async function partsOnTop(row: Locator, open = false) {
	for (const sel of open ? OPEN_PARTS : REST_PARTS) {
		const parts = row.locator(sel);
		for (const p of await parts.all()) expect(await onTop(p), `${sel} is visible and hit-testable`).toBe(true);
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
			await chipsVisible(row);
			const mrs = await box(row.locator("> .spill"));
			expect(right(mrs), "Jira status column stays in the row").toBeLessThanOrEqual(right(await box(row)) + 0.5);
		});

		test(`sprint block ${block}: hover opens the whole row on one line past the block edge, leave puts it back, nothing else moves`, async ({
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
			await expect.poll(async () => (await box(cell)).width).toBeGreaterThan(restCell.width + 20);
			await settle(page);
			const wide = await box(cell);
			expect(wide.height, "the open title stays on one line, inside the 24px row").toBeLessThanOrEqual(24);
			expect(await truncated(row.locator(".tt")), "no ellipsis while open").toBe(false);
			expect((await box(row.locator(".tt"))).height, "the title does not wrap").toBeLessThan(20);
			const hovered = await box(row);
			expect([hovered.x, hovered.y, hovered.height], "the row keeps its place and height").toEqual([
				rest.x,
				rest.y,
				rest.height,
			]);
			await chipsInside(row);
			await chipsVisible(row);
			for (const col of [".k", ".tail .spill"]) {
				const c = await box(row.locator(col).first());
				expect(
					Math.abs(c.y + c.height / 2 - (rest.y + rest.height / 2)),
					`${col} stays on the row's line`,
				).toBeLessThan(2);
			}
			if (nextRest) expect(await box(next), "no neighbour moves").toEqual(nextRest);
			await partsOnTop(row, true);
			await page.mouse.move(5, 5);
			await settle(page);
			expect(await box(cell)).toEqual(restCell);
		});

		test(`sprint block ${block}: keyboard focus opens the row, blur closes it`, async ({ page }) => {
			await open(page, LAYERS, "dense-lists");
			await unfoldSprint(page);
			const row = rowOf(page, block, key);
			const rest = (await box(row.locator(".t"))).width;
			await row.focus();
			await expect(row).toBeFocused();
			await page.keyboard.press("Shift+Tab");
			await page.keyboard.press("Tab");
			await expect(row).toBeFocused();
			await expect.poll(async () => (await box(row.locator(".t"))).width).toBeGreaterThan(rest + 20);
			await row.blur();
			await expect.poll(async () => (await box(row.locator(".t"))).width).toBeCloseTo(rest, 0);
		});
	}

	test("a long title with more warning chips than the cell holds folds the rest into +N, keeps its MR pill clickable at rest, and shows every chip on hover", async ({
		page,
	}) => {
		await open(page, LAYERS, "dense-lists");
		await unfoldSprint(page);
		const row = rowOf(page, "carried", "DEMO-363");
		const shown = row.locator(".tc > .ob:not(.more)");
		const more = row.getByTestId("more-chips");
		await expect(shown.first()).toHaveText(/not on the board/);
		const n = await shown.count();
		expect(n, "at least the first chip shows").toBeGreaterThanOrEqual(1);
		expect(n, "some chips fold away").toBeLessThan(4);
		await expect(more).toHaveText(`+${4 - n}`);
		const tt = await box(row.locator(".tt"));
		expect(tt.width, "the title keeps the minimum").toBeGreaterThanOrEqual(40);
		expect(right(await box(row.locator(".tc"))), "the chips end before the MRs column").toBeLessThanOrEqual(
			(await box(row.locator("> .mrs"))).x + 0.5,
		);
		await chipsVisible(row);
		await partsOnTop(row);
		await row.hover();
		await settle(page);
		await expect(more).toBeHidden();
		await expect(row.locator(".tc .ob:not(.more)")).toHaveCount(4);
		expect(await truncated(row.locator(".tt")), "no ellipsis while open").toBe(false);
		expect((await box(row.locator(".t"))).height, "one line").toBeLessThanOrEqual(24);
		for (const c of await row.locator(".tc .ob:not(.more)").all())
			expect(await onTop(c), `chip "${await c.textContent()}" shows on hover`).toBe(true);
		await partsOnTop(row, true);
		await page.mouse.move(5, 5);
		await settle(page);
		await expect(more).toBeVisible();
	});

	for (const key of ["DEMO-361", "DEMO-363"])
		test(`${key}: moving the pointer onto the MR button at rest and clicking it opens the MR, not the card`, async ({
			page,
			context,
		}) => {
			await context.route("**/merge_requests/**", (r) => r.fulfill({ body: "mr" }));
			await open(page, LAYERS, "dense-lists");
			await unfoldSprint(page);
			const row = rowOf(page, "carried", key);
			const b = await box(row.getByTestId("mr-glyph"));
			await page.mouse.move(b.x - 300, b.y - 120);
			await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 8 });
			await settle(page);
			const popup = page.waitForEvent("popup", { timeout: 3_000 });
			await page.mouse.down();
			await page.mouse.up();
			expect((await popup).url()).toContain("/merge_requests/");
			await expect(page.locator(".rel-sheet")).toHaveCount(0);
		});

	test("hovering a row's key or avatar does not open the line", async ({ page }) => {
		await open(page, LAYERS, "dense-lists");
		await unfoldSprint(page);
		const row = rowOf(page, "carried", "DEMO-361");
		const rest = await box(row.locator(".t"));
		await row.locator(".k").hover();
		await settle(page);
		expect(await box(row.locator(".t"))).toEqual(rest);
		expect(await truncated(row.locator(".tt"))).toBe(true);
	});

	test("a hovered long-title row shows its title, every chip, its MR buttons and Jira status, and its MR button clicks through", async ({
		page,
		context,
	}) => {
		await context.route("**/merge_requests/**", (r) => r.fulfill({ body: "mr" }));
		await open(page, LAYERS, "dense-lists");
		await unfoldSprint(page);
		const row = rowOf(page, "carried", "DEMO-361");
		const mr = row.getByTestId("mr-glyph");
		await expect(mr).toHaveCount(1);
		await expect(row.locator("> .spill")).toHaveText("Done");
		await chipsVisible(row);
		await partsOnTop(row);
		const rest = await box(row);
		await row.locator(".tt").hover();
		await settle(page);
		expect(await truncated(row.locator(".tt")), "the whole title shows").toBe(false);
		const line = await box(row.locator(".t"));
		expect(line.height, "one line").toBeLessThanOrEqual(24);
		expect(right(line), "the open line runs past the block edge").toBeGreaterThan(right(rest) + 20);
		const tt = await box(row.locator(".tt"));
		const copy = row.locator(".tail .mrd");
		expect((await box(copy)).x, "the MR buttons follow the whole title").toBeGreaterThan(right(tt));
		expect(await copy.getAttribute("href")).toBe(await mr.getAttribute("href"));
		await expect(row.locator(".tail .spill")).toHaveText("Done");
		await chipsVisible(row);
		await partsOnTop(row, true);
		const b = await box(copy);
		await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 4 });
		const popup = page.waitForEvent("popup");
		await page.mouse.down();
		await page.mouse.up();
		expect((await popup).url()).toContain("/merge_requests/431");
		await expect(page.locator(".rel-sheet"), "the MR click does not open the card sheet").toHaveCount(0);
	});

	test("hovering a short-title row moves none of its columns", async ({ page }) => {
		await open(page, LAYERS, "dense-lists");
		await unfoldSprint(page);
		const rows = sprintRow(page).locator(".crow:not(.sph)");
		let row: Locator | null = null;
		for (const r of await rows.all()) if (!(await truncated(r.locator(".tt")))) row = r;
		expect(row, "a row whose title fits").not.toBeNull();
		const parts = [".k", ".tt", "> .mrs", "> .spill"].map((s) => row!.locator(s));
		const before = await Promise.all(parts.map(box));
		const rest = await box(row!);
		await row!.hover();
		await settle(page);
		expect(await Promise.all(parts.map(box)), "no column moves").toEqual(before);
		expect(await box(row!)).toEqual(rest);
	});

	test("every row that fits at rest never opens, shows its status once and no MR copy; every row that does not fit opens", async ({
		page,
	}) => {
		await open(page, LAYERS, "dense-lists");
		await unfoldSprint(page);
		const rows = await sprintRow(page).locator(".crow:not(.sph)").all();
		let fits = 0;
		let opens = 0;
		for (const row of rows) {
			const key = await row.getAttribute("data-key");
			const hidden = (await truncated(row.locator(".tt"))) || (await row.getByTestId("more-chips").count()) > 0;
			const rest = await box(row.locator(".t"));
			await row.locator(".tt").hover();
			await settle(page);
			const shown = await visibleCount(row.locator(".spill"));
			expect(shown, `${key} shows its status once`).toBe(1);
			if (hidden) {
				opens++;
				expect((await box(row.locator(".t"))).width, `${key} opens`).toBeGreaterThan(rest.width + 20);
				await partsOnTop(row, true);
				expect(await visibleCount(row.locator(".mrs")), `${key} shows its MR pills once`).toBe(1);
			} else {
				fits++;
				expect(await box(row.locator(".t")), `${key} stays at rest`).toEqual(rest);
				await expect(row.locator(".tail"), `${key} carries no copy`).toHaveCount(0);
			}
			await page.mouse.move(5, 5);
			await settle(page);
		}
		expect(fits, "rows that fit").toBeGreaterThan(2);
		expect(opens, "rows that do not fit").toBeGreaterThan(2);
	});

	test("the open line is not clipped by its block and no block moves", async ({ page }) => {
		await open(page, LAYERS, "dense-lists");
		await unfoldSprint(page);
		const blocks = sprintRow(page).getByTestId("sprint-dest-block");
		const tops = async () => Promise.all((await blocks.all()).map(async (b) => (await box(b)).y));
		const before = await tops();
		const row = rowOf(page, "web", "DEMO-362");
		await row.hover();
		await settle(page);
		expect(await tops(), "no block moves").toEqual(before);
		const line = await box(row.locator(".t"));
		const at = await page.evaluate(
			([x, y]) => document.elementFromPoint(x, y)?.closest(".crow")?.getAttribute("data-key"),
			[right(line) - 3, line.y + line.height / 2],
		);
		expect(at, "the right end of the open line is on top").toBe("DEMO-362");
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
			await chipsInside(row);
			const below = row.locator("xpath=following-sibling::*[contains(@class,'crow')][1]");
			const belowRest = await box(below);
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
			expect(wide.height, "the row stays on one line").toBeCloseTo(rest.height, 0);
			expect(await box(below), "the row below does not move").toEqual(belowRest);
			await chipsInside(row);
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

	test("an opened sprint row is opaque in the light theme too", async ({ page }) => {
		await page.addInitScript(() => localStorage.setItem("theme", "light"));
		await open(page, LAYERS, "dense-lists");
		await page.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
		await unfoldSprint(page);
		const row = rowOf(page, "carried", "DEMO-361");
		await row.hover();
		await settle(page);
		const bg = await row.evaluate((el) => getComputedStyle(el).backgroundColor);
		const [r, g, b] = (bg.match(/[\d.]+/g) ?? []).map(Number);
		expect(Math.min(r, g, b), "a light surface").toBeGreaterThan(200);
		expect(bg).not.toMatch(/, 0\)/);
	});
});
