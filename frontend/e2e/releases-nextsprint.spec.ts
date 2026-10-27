import { expect, test, type Locator, type Page } from "@playwright/test";
import { NARROW, WIDE, labelOverlaps, layer, open, settle } from "./releases-kit";

const LAYERS = "?layers=sprint,llb,cards";
const DAY = 36;

const bar = (row: Locator, which: "active" | "next") => row.locator(`.spsrc[data-sprint='${which}']`);
const blocks = (row: Locator, which: "active" | "next") =>
	row.locator(`[data-testid=sprint-dest-block][data-sprint='${which}']`);

test.describe("releases sprint bars cover their last day", () => {
	test.use({ viewport: NARROW });

	const dayAt = (page: Page, x: number, side: "left" | "right") =>
		page.evaluate(
			([px, edge]) => {
				for (const c of document.querySelectorAll<HTMLElement>(".rel-day")) {
					const r = c.getBoundingClientRect();
					if (Math.abs((edge === "left" ? r.left : r.right) - px) <= 0.5)
						return c.querySelector("span")?.textContent ?? null;
				}
				return null;
			},
			[x, side] as const,
		);
	const days = async (label: Locator) => {
		const m = (await label.innerText()).match(/(\d+) \w+\s*→\s*(\d+) \w+/);
		return { first: m![1], last: m![2] };
	};

	test("the Sprint band and the row's bar end at the end of the end day's column", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		const { first, last } = await days(demo.locator(".rel-lab .spn small"));
		const band = (await page.getByTestId("sprint-band").boundingBox())!;
		expect(await dayAt(page, band.x, "left"), "the band starts on its first day").toBe(first);
		expect(await dayAt(page, band.x + band.width, "right"), "the band ends with its last day").toBe(last);

		const a = (await bar(demo, "active").boundingBox())!;
		expect(await dayAt(page, a.x - 2, "left"), "the bar starts on its first day, less its inset").toBe(first);
		expect(await dayAt(page, a.x + a.width + 2, "right"), "the bar ends with its last day, less its inset").toBe(last);
	});

	test("the next bar starts the day after the active sprint's last day", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		const active = await days(demo.locator(".rel-lab .spn small"));
		const nextLabel = await days(bar(demo, "next").locator(".passive"));
		const n = (await page.getByTestId("sprint-band-next").boundingBox())!;
		expect(await dayAt(page, n.x, "left")).toBe(nextLabel.first);
		const a = (await page.getByTestId("sprint-band").boundingBox())!;
		expect(n.x, "no gap and no overlap between the two bands").toBeCloseTo(a.x + a.width, 0);
		expect(Number(nextLabel.first)).not.toBe(Number(active.last));
	});
});

test.describe("releases next sprint on its board's row", () => {
	test.use({ viewport: NARROW });

	test("a board is one row with two bars, the next one dimmed, and PILOT's next one is estimated", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		await expect(page.locator("[data-board]")).toHaveCount(2);
		await expect(page.getByTestId("sprint-row-DEMO-next")).toHaveCount(0);
		await expect(demo.getByTestId("sprint-bar")).toHaveCount(2);
		await expect(page.getByTestId("sprint-row-PILOT").getByTestId("sprint-bar")).toHaveCount(2);
		await expect(bar(demo, "next")).toContainText("DEMO 21");
		await expect(bar(demo, "next")).toContainText(/\d+ \w+ → \d+ \w+/);
		await expect(bar(demo, "next").locator(".sl > b")).toHaveText("4 cards");

		const opacity = (l: Locator) => l.evaluate((el) => Number(getComputedStyle(el).opacity));
		expect(await opacity(bar(demo, "active"))).toBe(1);
		expect(await opacity(bar(demo, "next"))).toBeLessThan(0.7);
	});

	test("the label column keeps the active sprint's label and has no block for the next one", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		await expect(demo.locator(".rel-lab .spn")).toContainText(/DEMO 20\s*\d+ \w+ → \d+ \w+/);
		await expect(demo.locator(".rel-lab")).not.toContainText("DEMO 21");
		await expect(demo.getByTestId("sprint-scope")).toHaveText("4 new · 2 carried with MRs · 2 carried, done");
		await expect(demo.getByTestId("sprint-dests")).not.toContainText("#21 1 no MR 2");
	});

	test("the next bar sits on the active bar's lane, a sprint to its right, and never overlaps it", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		const a = (await bar(demo, "active").boundingBox())!;
		const n = (await bar(demo, "next").boundingBox())!;
		expect(n.y, "same lane").toBeCloseTo(a.y, 0);
		expect(n.x - a.x, "starts the day after the active sprint ends").toBeCloseTo(15 * DAY, 0);
		expect(n.x).toBeGreaterThanOrEqual(a.x + a.width);
		const bands = async (id: string) => (await page.getByTestId(id).boundingBox())!;
		const band = await bands("sprint-band-next");
		expect(band.x, "the Sprint header band lines up with the bar").toBeCloseTo(n.x - 2, 0);
	});

	test("its sticky label stays opaque so a panned track never shows through the label column", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		await bar(demo, "next").locator(".csum").click();
		await page.getByTestId("release-timeline").evaluate((el) => (el.scrollLeft += 900));
		await settle(page);
		expect(await demo.locator(".rel-lab").evaluate((el) => getComputedStyle(el).opacity)).toBe("1");
		expect(await demo.locator(".rel-lab").evaluate((el) => getComputedStyle(el).backgroundColor)).not.toBe(
			"rgba(0, 0, 0, 0)",
		);
	});

	test("each bar unfolds on its own, under its own x-range, and the row grows to fit", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		const height = async () => (await demo.boundingBox())!.height;
		const closed = await height();

		await bar(demo, "next").locator(".csum").click();
		await expect(blocks(demo, "next")).toHaveCount(3);
		await expect(blocks(demo, "active")).toHaveCount(0);
		await expect(bar(demo, "next").locator(".csum")).toHaveAttribute("aria-expanded", "true");
		await expect(bar(demo, "active").locator(".csum")).toHaveAttribute("aria-expanded", "false");
		await settle(page);
		const nextBar = (await bar(demo, "next").boundingBox())!;
		for (const b of await blocks(demo, "next").all())
			expect((await b.boundingBox())!.x, "under the next bar").toBeGreaterThanOrEqual(nextBar.x);
		const grown = await height();
		expect(grown).toBeGreaterThan(closed);

		await bar(demo, "active").locator(".csum").click();
		await expect(blocks(demo, "active")).not.toHaveCount(0);
		await expect(blocks(demo, "next")).toHaveCount(3);
		await settle(page);
		const activeBar = (await bar(demo, "active").boundingBox())!;
		for (const b of await blocks(demo, "active").all())
			expect((await b.boundingBox())!.x, "under the active bar").toBeGreaterThanOrEqual(activeBar.x);

		await bar(demo, "next").locator(".csum").click();
		await expect(blocks(demo, "next")).toHaveCount(0);
		await expect(blocks(demo, "active")).not.toHaveCount(0);
	});

	test("an unfolded next block opens its cards, and a card of it opens its sheet", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		await bar(demo, "next").locator(".csum").click();
		await blocks(demo, "next").first().locator(".dsum").click();
		const crow = demo.locator(".rel-cg.dest.next .crow[data-key]").first();
		await expect(crow).toBeVisible();
		await expect(crow).not.toHaveCSS("opacity", "0");
		await settle(page);
		await expect(demo.locator(".rel-cg.dest.next").first()).toHaveCSS("filter", /opacity\(0\.55\)/);
	});

	test("hiding the board hides both bars, counts the board once, and Show all brings them back", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		await demo.hover();
		await demo.getByRole("button", { name: "Hide DEMO sprint" }).click();
		await expect(demo).toHaveCount(0);
		await expect(page.getByTestId("sprint-bar")).toHaveCount(2);
		await expect(page.getByTestId("sprint-row-PILOT")).toBeVisible();
		await expect(page.getByTestId("hidden-boards")).toHaveText("1 hidden");
		await page.getByTestId("hidden-boards").click();
		await expect(page.getByRole("menuitemcheckbox")).toHaveCount(2);
		await page.getByRole("menuitem", { name: "Show all" }).click();
		await expect(page.getByTestId("sprint-row-DEMO").getByTestId("sprint-bar")).toHaveCount(2);
	});

	test("it raises no alert", async ({ page }) => {
		await open(page, "?sheet=attention");
		const hits = await page
			.locator(".rel-sheet .al[data-hit]")
			.evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.hit ?? ""));
		expect(hits.length).toBeGreaterThan(0);
		expect(hits.filter((h) => /DEMO-35\d/.test(h))).toEqual([]);
	});

	test("Sources names Jira on the next bar the way it does on the board label", async ({ page }) => {
		await open(page, `${LAYERS},src`);
		const demo = page.getByTestId("sprint-row-DEMO");
		await expect(demo.locator(".rel-lab .spn").getByTestId("source-tag")).toHaveText("Jira");
		const tag = bar(demo, "next").getByTestId("source-tag");
		await expect(tag).toHaveText("Jira");
		await tag.click();
		await expect(page.getByTestId("source-card")).toContainText('next sprint "DEMO 21"');
	});

	test("the next bar's sprint link opens the next sprint's sheet, and a card of it says next", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		await bar(demo, "next").locator(".lbn").click();
		await expect(page).toHaveURL(/sheet=sprint%3ADEMO%3Anext|sheet=sprint:DEMO:next/);
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet).toContainText("next");
		await expect(sheet.getByTestId("sprint-sheet-cards")).toContainText("4 on the board");
	});

	test("a card of the next sprint opens its sheet from a card link", async ({ page }) => {
		await open(page, `${LAYERS}&sheet=card:DEMO-351`);
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await sheet.getByRole("button", { name: "DEMO 21" }).click();
		await expect(page).toHaveURL(/sheet=sprint%3ADEMO%3Anext|sheet=sprint:DEMO:next/);
	});

	test("the Sprint layer off hides the row and both bars", async ({ page }) => {
		await open(page, LAYERS);
		await layer(page, "Sprint cards").click();
		await expect(page.getByTestId("sprint-row-DEMO")).toHaveCount(0);
		await expect(page.getByTestId("sprint-bar")).toHaveCount(0);
	});

	test("Only me keeps the next bar and counts only my cards on it", async ({ page }) => {
		await open(page, `${LAYERS}&person=ada`);
		const demo = page.getByTestId("sprint-row-DEMO");
		await expect(demo.getByTestId("sprint-bar")).toHaveCount(2);
		await expect(bar(demo, "next")).toContainText("of mine");
	});
});

for (const vp of [NARROW, WIDE]) {
	test.describe(`releases next sprint at ${vp.width}x${vp.height}`, () => {
		test.use({ viewport: vp });

		test("both bars sit inside the track and no labels overlap with both unfolded", async ({ page }) => {
			await open(page, LAYERS);
			const demo = page.getByTestId("sprint-row-DEMO");
			await bar(demo, "active").locator(".csum").click();
			await bar(demo, "next").locator(".csum").click();
			await settle(page);
			for (const d of await demo.locator(".rel-cg.dest .dsum").all()) await d.click();
			await settle(page);
			const track = (await demo.locator(".rel-trk").boundingBox())!;
			for (const which of ["active", "next"] as const) {
				const b = (await bar(demo, which).boundingBox())!;
				expect(b.x, `${which} bar starts inside the track`).toBeGreaterThanOrEqual(track.x);
				expect(b.x + b.width, `${which} bar ends inside the track`).toBeLessThanOrEqual(track.x + track.width);
			}
			const { n, overlaps } = await labelOverlaps(page);
			expect(n).toBeGreaterThan(10);
			expect(overlaps).toEqual([]);
		});

		test("an unfolded next block never lands on an unfolded active block", async ({ page }) => {
			await open(page, LAYERS);
			const demo = page.getByTestId("sprint-row-DEMO");
			await bar(demo, "active").locator(".csum").click();
			await bar(demo, "next").locator(".csum").click();
			await settle(page);
			const rects = async (which: "active" | "next") =>
				Promise.all((await blocks(demo, which).all()).map((b) => b.boundingBox()));
			const [as, ns] = [await rects("active"), await rects("next")];
			for (const a of as)
				for (const n of ns) {
					const ix = Math.min(a!.x + a!.width, n!.x + n!.width) - Math.max(a!.x, n!.x);
					const iy = Math.min(a!.y + a!.height, n!.y + n!.height) - Math.max(a!.y, n!.y);
					expect(ix > 1 && iy > 1, "blocks of the two bars overlap").toBe(false);
				}
		});
	});
}
