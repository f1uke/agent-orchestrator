import { expect, test, type Locator, type Page } from "@playwright/test";
import { layer, open, settle } from "./releases-kit";

test.use({ viewport: { width: 1280, height: 800 } });

const sheet = (page: Page) => page.getByRole("dialog", { name: "Release details" });

async function show(page: Page, hit: string) {
	await settle(page);
	await page.locator(`.rel-sheet .al[data-hit="${hit}"]`).getByTestId("hit-show").click();
}

async function inOpenView(target: Locator): Promise<string> {
	return target.evaluate((el) => {
		const r = el.getBoundingClientRect();
		const sheetLeft = document.querySelector(".rel-sheet")?.getBoundingClientRect().left ?? Infinity;
		const scroller = document.querySelector(".rel-sc")!;
		const inTimeline = scroller.contains(el);
		const sc = (inTimeline ? scroller : document.querySelector(".rel")!).getBoundingClientRect();
		const left = !inTimeline || el.closest(".rel-lab") ? sc.left : sc.left + 280;
		const top = inTimeline ? document.querySelector(".rel-axis")!.getBoundingClientRect().bottom : sc.top;
		const right = Math.min(sc.right, sheetLeft);
		const misses = [
			r.left < left - 1 && `left ${r.left} < ${left}`,
			r.top < top - 1 && `top ${r.top} < ${top}`,
			Math.min(r.right, r.left + 120) > right + 1 && `right ${r.right} > ${right}`,
			r.bottom > sc.bottom + 1 && `bottom ${r.bottom} > ${sc.bottom}`,
		].filter(Boolean);
		return misses.join(", ");
	});
}

const CASES: [string, string, string][] = [
	["C5~v/21-2026/di", "the version bar", '[data-testid="bar-di-21-2026"]'],
	[
		"A6~vd/21-2026/di/freeze",
		"the freeze marker",
		'.rel-app[data-app="di"] .rel-mk[data-key="21-2026"][data-stage="freeze"]',
	],
	["E1~h/da/2.3.1", "the hotfix bar", '[data-testid="hotfix-da-2.3.1"]'],
	["F2~ht/da/2.3.1/21-2026", "the hotfix bar", '[data-testid="hotfix-da-2.3.1"]'],
	["L3~ct/21-2026/di/DEMO-109", "the parked card row", '[data-testid="cards-di:21-2026"] .crow[data-key="DEMO-109"]'],
	["G5~pf/21-2026/demo/open", "the card row", '[data-testid="cards-di:21-2026"] .crow[data-key="DEMO-103"]'],
	[
		"G2~c/DEMO-304",
		"the sprint card row",
		'[data-testid="sprint-row-DEMO"] [data-dest="none"] .crow[data-key="DEMO-304"]',
	],
	[
		"L1~mr/di/330",
		"the long-lived lane",
		'.rel-app[data-app="di"] [data-testid="llb-lane"][data-branch="feature/demo-wallet"]',
	],
	[
		"L8~mr/pi/405",
		"the MR glyph",
		'[data-testid="sprint-row-DEMO"] [data-dest="off"] .crow[data-key="DEMO-303"] [data-testid="mr-glyph"][data-app="pi"]',
	],
	["L9~mr/da/415", "the app label (fallback)", '[data-testid="label-da"]'],
	["A2~jv/7001", "the app label (fallback)", '[data-testid="label-da"]'],
	["A3~m/pi/9001", "the app label (fallback)", '[data-testid="label-pi"]'],
	["F1~tt/21-2026/22-2026", "the later train's chip (fallback)", '.rel-tchip[data-train="22-2026"]'],
];

test.describe("Show on an alert", () => {
	for (const [hit, what, selector] of CASES)
		test(`${hit} pulses ${what} in view`, async ({ page }) => {
			await open(page, "?sheet=attention", "subjects");
			await show(page, hit);
			const target = page.locator(selector);
			await expect(target).toHaveAttribute("data-pulse", "motion");
			await expect(page.locator("[data-pulse]")).toHaveCount(1);
			await settle(page);
			expect(await inOpenView(target)).toBe("");
			await expect(target).not.toHaveAttribute("data-pulse", { timeout: 3000 });
		});

	test("slides the sheet aside, keeps its place, and brings it back", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		await settle(page);
		await sheet(page)
			.locator(".sh-b")
			.evaluate((el) => (el.scrollTop = 200));
		await show(page, "C5~v/21-2026/di");
		await expect(sheet(page)).toHaveAttribute("data-aside", "true");
		await expect(page.locator(".rel-scrim")).toHaveCount(0);
		await expect
			.poll(() => sheet(page).evaluate((el) => Math.round(innerWidth - el.getBoundingClientRect().left)))
			.toBe(36 + 12);
		await page.getByTestId("bar-di-21-2026").click();
		await expect(sheet(page)).not.toHaveAttribute("data-aside");
		await expect(page).toHaveURL(/sheet=version/);

		await open(page, "?sheet=attention", "subjects");
		await settle(page);
		const lower = page.locator('.rel-sheet .al[data-hit="L8~mr/pi/405"]');
		await lower.scrollIntoViewIfNeeded();
		const at = await sheet(page)
			.locator(".sh-b")
			.evaluate((el) => el.scrollTop);
		expect(at).toBeGreaterThan(200);
		await lower.getByTestId("hit-show").click();
		await page.getByTestId("sheet-return").click();
		await expect(sheet(page)).not.toHaveAttribute("data-aside");
		await expect(page.locator(".rel-scrim")).toHaveCount(1);
		expect(
			await sheet(page)
				.locator(".sh-b")
				.evaluate((el) => el.scrollTop),
		).toBe(at);
	});

	test("a card on a hidden board turns on Sprint cards, shows the board and opens its folds", async ({ page }) => {
		await open(page, "?sheet=attention&hideBoards=PILOT", "subjects");
		await expect(layer(page, "Sprint cards")).toHaveAttribute("aria-pressed", "false");
		await expect(page.getByTestId("sprint-row-PILOT")).toHaveCount(0);
		await show(page, "G2~c/PIL-12");
		await expect(layer(page, "Sprint cards")).toHaveAttribute("aria-pressed", "true");
		const row = page.locator('[data-testid="sprint-row-PILOT"] [data-dest="t:21-2026"] .crow[data-key="PIL-12"]');
		await expect(row).toHaveAttribute("data-pulse", "motion");
		await settle(page);
		expect(await inOpenView(row)).toBe("");
		await expect(page).not.toHaveURL(/hideBoards=[^&]*PILOT/);
	});

	test("a sprint card whose app the filter hides brings that app back", async ({ page }) => {
		await open(page, "?sheet=attention&apps=di", "subjects");
		await show(page, "G2~c/PIL-12");
		const row = page.locator('[data-testid="sprint-row-PILOT"] .crow[data-key="PIL-12"]');
		await expect(row).toHaveAttribute("data-pulse", "motion");
		await expect(page).toHaveURL(/apps=di(%2C|,)pa/);
	});

	test("a freeze marker on an unfocused train focuses it", async ({ page }) => {
		await open(page, "?sheet=attention&focus=22-2026", "subjects");
		await show(page, "A6~vd/21-2026/di/freeze");
		const mk = page.locator('.rel-app[data-app="di"] .rel-mk[data-key="21-2026"][data-stage="freeze"]');
		await expect(mk).toHaveAttribute("data-pulse", "motion");
		await expect(page).toHaveURL(/focus=21-2026/);
		await settle(page);
		expect(await inOpenView(mk)).toBe("");
	});

	test("a row whose subject has no mark says where Show goes; a row with a mark says nothing", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		const where = (hit: string) => page.locator(`.rel-sheet .al[data-hit="${hit}"]`).getByTestId("hit-where");
		await expect(where("L9~mr/da/415")).toHaveText(
			"Show goes at its app: the timeline draws no lane for feat/DEMO-201-base",
		);
		await expect(where("A2~jv/7001")).toContainText("at its app");
		await expect(where("A3~m/pi/9001")).toContainText("at its app");
		await expect(where("F1~tt/21-2026/22-2026")).toContainText("at the later train");
		await expect(where("C5~v/21-2026/di")).toHaveCount(0);
		await expect(
			page.locator('.rel-sheet .al[data-hit="L9~mr/da/415"]').getByTestId("hit-show"),
		).toHaveAccessibleDescription(/draws no lane/);
	});

	test("the later train's chip scrolls the timeline to that train", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		await settle(page);
		const before = await page.locator(".rel-sc").evaluate((el) => el.scrollLeft);
		await show(page, "F1~tt/21-2026/22-2026");
		await expect(page).toHaveURL(/focus=22-2026/);
		await expect.poll(() => page.locator(".rel-sc").evaluate((el) => el.scrollLeft)).toBeGreaterThan(before + 100);
	});

	test("an alert of mine on a card Only me does not draw turns Only me off to show it", async ({ page }) => {
		await open(page, "?sheet=attention&me=1", "subjects");
		await expect(page.getByTestId("only-me")).toHaveAttribute("aria-pressed", "true");
		await show(page, "G2~c/DEMO-304");
		await expect(page.getByTestId("only-me")).toHaveAttribute("aria-pressed", "false");
		await expect(page.locator('[data-testid="sprint-row-DEMO"] .crow[data-key="DEMO-304"]')).toHaveAttribute(
			"data-pulse",
			"motion",
		);
	});

	test("an alert of mine keeps Only me on and shows the version without writing the focus", async ({ page }) => {
		await open(page, "?sheet=attention&me=1", "subjects");
		await show(page, "A6~vd/21-2026/di/freeze");
		await expect(
			page.locator('.rel-app[data-app="di"] .rel-mk[data-key="21-2026"][data-stage="freeze"]'),
		).toHaveAttribute("data-pulse", "motion");
		await expect(page.getByTestId("only-me")).toHaveAttribute("aria-pressed", "true");
		expect(page.url()).not.toContain("focus=");
	});

	for (const [name, query, hit, selector] of [
		["train", "?sheet=train:21-2026", "D3~v/21-2026/da", '[data-testid="bar-da-21-2026"]'],
		[
			"version",
			"?sheet=version:21-2026:di",
			"A6~vd/21-2026/di/freeze",
			'.rel-app[data-app="di"] .rel-mk[data-key="21-2026"][data-stage="freeze"]',
		],
		["card", "?sheet=card:DEMO-109", "L3~ct/21-2026/di/DEMO-109", '.crow[data-key="DEMO-109"]'],
	] as const)
		test(`from the ${name} sheet, Show slides the sheet aside and pulses the subject`, async ({ page }) => {
			await open(page, query, "subjects");
			await show(page, hit);
			await expect(sheet(page)).toHaveAttribute("data-aside", "true");
			const target = page.locator(`.rel-sc ${selector}`);
			await expect(target).toHaveAttribute("data-pulse", "motion");
			await settle(page);
			expect(await inOpenView(target)).toBe("");
		});

	test("every alert row, hidden ones too, has a Show button", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		await page.getByRole("button", { name: /hidden/ }).click();
		const rows = page.locator(".rel-sheet .al[data-hit]");
		const n = await rows.count();
		expect(n).toBeGreaterThan(15);
		await expect(page.locator(".rel-sheet .al[data-hit]:not(:has([data-testid=hit-show]))")).toHaveCount(0);
	});
});

test.describe("Show under reduced motion", () => {
	test("outlines the target without animating it", async ({ page }) => {
		await page.emulateMedia({ reducedMotion: "reduce" });
		await open(page, "?sheet=attention", "subjects");
		await show(page, "L3~ct/21-2026/di/DEMO-109");
		const row = page.locator('[data-testid="cards-di:21-2026"] .crow[data-key="DEMO-109"]');
		await expect(row).toHaveAttribute("data-pulse", "static");
		const style = await row.evaluate((el) => {
			const cs = getComputedStyle(el);
			return { animation: cs.animationName, outline: cs.outlineStyle };
		});
		expect(style).toEqual({ animation: "none", outline: "solid" });
		expect(await inOpenView(row)).toBe("");
		await expect(row).not.toHaveAttribute("data-pulse", { timeout: 3000 });
	});
});
