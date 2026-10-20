import { expect, test } from "@playwright/test";
import { NARROW, WIDE, layer, open, settle } from "./releases-kit";

const LAYERS = "?layers=sprint,llb,cards";

test.describe("releases next sprint", () => {
	test.use({ viewport: NARROW });

	test("the next sprint is a dimmed row of its own beside the active one, and PILOT has none", async ({ page }) => {
		await open(page, LAYERS);
		const active = page.getByTestId("sprint-row-DEMO");
		const next = page.getByTestId("sprint-row-DEMO-next");
		await expect(page.locator("[data-board]")).toHaveCount(3);
		await expect(page.getByTestId("sprint-row-PILOT-next")).toHaveCount(0);
		await expect(next.locator(".rel-lab .spn")).toContainText(/DEMO 21\s*\d+ \w+ → \d+ \w+/);
		await expect(next.getByTestId("sprint-scope")).toHaveText("3 new · 1 carried, done");
		await expect(next.getByTestId("sprint-dests")).toHaveText(/#21 1\s*no MR 2/);
		await expect(active.getByTestId("sprint-scope")).toHaveText("4 new · 2 carried with MRs · 2 carried, done");

		const opacity = (row: typeof next) =>
			row.evaluate((el) => ({
				lab: getComputedStyle(el.querySelector(".rel-lab") as Element).opacity,
				trk: getComputedStyle(el.querySelector(".rel-trk") as Element).opacity,
			}));
		expect(await opacity(active)).toEqual({ lab: "1", trk: "1" });
		const dim = await opacity(next);
		expect(Number(dim.lab)).toBeLessThan(0.7);
		expect(Number(dim.trk)).toBeLessThan(0.7);
		await expect(next).toHaveAttribute("data-next", "true");
	});

	test("it sits on the timeline at its own dates, right where the active sprint ends", async ({ page }) => {
		await open(page, LAYERS);
		const band = async (id: string) => (await page.getByTestId(id).boundingBox())!;
		const a = await band("sprint-band");
		const n = await band("sprint-band-next");
		const day = 36;
		expect(n.x, "the next band starts where the active one ends").toBeCloseTo(a.x + a.width, 0);
		expect(Math.round(a.width / day)).toBe(14);
		expect(Math.round(n.width / day)).toBe(14);
		await expect(page.getByTestId("sprint-band-next")).toContainText("Sprint 21 · next");

		const activeCsum = (await page.getByTestId("sprint-row-DEMO").locator(".spsrc").boundingBox())!;
		const nextCsum = (await page.getByTestId("sprint-row-DEMO-next").locator(".spsrc").boundingBox())!;
		expect(nextCsum.x - activeCsum.x, "the card group starts a sprint later").toBeCloseTo(14 * day, 0);
	});

	test("it unfolds on its own, with the same destinations and cards as an active row", async ({ page }) => {
		await open(page, LAYERS);
		const active = page.getByTestId("sprint-row-DEMO");
		const next = page.getByTestId("sprint-row-DEMO-next");
		await next.locator(".spsrc .csum").click();
		await expect(next.getByTestId("sprint-dest-block")).toHaveCount(3);
		await expect(active.getByTestId("sprint-dest-block")).toHaveCount(0);
		await expect(next.locator(".dsum").first()).toContainText("→ #21");
		await next.locator(".dsum").first().click();
		await expect(next.locator('.crow[data-key="DEMO-351"]')).toBeVisible();
		await expect(next.locator('.crow[data-key="DEMO-351"]')).not.toHaveCSS("opacity", "0");
		await settle(page);
	});

	test("hiding the board's sprint row hides both, and Show all brings both back", async ({ page }) => {
		await open(page, LAYERS);
		const active = page.getByTestId("sprint-row-DEMO");
		await active.hover();
		await active.getByRole("button", { name: "Hide DEMO sprint" }).click();
		await expect(active).toHaveCount(0);
		await expect(page.getByTestId("sprint-row-DEMO-next")).toHaveCount(0);
		await expect(page.getByTestId("sprint-row-PILOT")).toBeVisible();
		await expect(page.getByTestId("hidden-boards")).toHaveText("1 hidden");
		await page.getByTestId("hidden-boards").click();
		await expect(page.getByRole("menuitemcheckbox")).toHaveCount(2);
		await page.getByRole("menuitem", { name: "Show all" }).click();
		await expect(page.getByTestId("sprint-row-DEMO")).toBeVisible();
		await expect(page.getByTestId("sprint-row-DEMO-next")).toBeVisible();
	});

	test("it raises no alert", async ({ page }) => {
		await open(page, "?sheet=attention");
		const hits = await page
			.locator(".rel-sheet .al[data-hit]")
			.evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.hit ?? ""));
		expect(hits.length).toBeGreaterThan(0);
		expect(hits.filter((h) => /DEMO-35\d/.test(h))).toEqual([]);
	});

	test("Sources names Jira on the next sprint the way it does on the active one", async ({ page }) => {
		await open(page, `${LAYERS},src`);
		const tag = (id: string) => page.getByTestId(id).locator(".rel-lab .spn").getByTestId("source-tag");
		await expect(tag("sprint-row-DEMO")).toHaveText("Jira");
		await expect(tag("sprint-row-DEMO-next")).toHaveText("Jira");
		await tag("sprint-row-DEMO-next").click();
		await expect(page.getByTestId("source-card")).toContainText('next sprint "DEMO 21"');
	});

	test("its sheet says next, and a card of it opens the next sprint's sheet", async ({ page }) => {
		await open(page, `${LAYERS}&sheet=card:DEMO-351`);
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await sheet.getByRole("button", { name: "DEMO 21" }).click();
		await expect(page).toHaveURL(/sheet=sprint%3ADEMO%3Anext|sheet=sprint:DEMO:next/);
		await expect(sheet).toContainText("next");
		await expect(sheet.getByTestId("sprint-sheet-cards")).toContainText("13 on the board");
	});

	test("the Sprint layer off hides both rows", async ({ page }) => {
		await open(page, LAYERS);
		await layer(page, "Sprint cards").click();
		await expect(page.getByTestId("sprint-row-DEMO")).toHaveCount(0);
		await expect(page.getByTestId("sprint-row-DEMO-next")).toHaveCount(0);
	});
});

test.describe("releases next sprint wide", () => {
	test.use({ viewport: WIDE });

	test("both rows fit at 2560x1440 and the next one stays dimmed", async ({ page }) => {
		await open(page, LAYERS);
		const next = page.getByTestId("sprint-row-DEMO-next");
		await expect(next).toBeVisible();
		const box = (await next.boundingBox())!;
		expect(box.x + box.width).toBeLessThanOrEqual(WIDE.width);
	});
});
