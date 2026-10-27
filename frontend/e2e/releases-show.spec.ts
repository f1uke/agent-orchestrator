import { expect, test, type Page } from "@playwright/test";
import {
	animationNow,
	expectFiltered,
	expectRinged,
	expectSpotted,
	freezeRings,
	layer,
	NARROW,
	open,
	outerScrolls,
	ringLife,
	ringLog,
	settle,
} from "./releases-kit";
import { subjectsBoard } from "../src/renderer/lib/releases/fixture";

test.use({ viewport: NARROW });

const sheet = (page: Page) => page.getByRole("dialog", { name: "Release details" });
const row = (page: Page, hit: string) => page.locator(`.rel-sheet .al[data-hit="${hit}"]`);

async function openFrozen(page: Page, query: string) {
	await open(page, query, "subjects");
	await freezeRings(page);
}

async function show(page: Page, hit: string) {
	await row(page, hit).getByTestId("hit-show").click();
}

const BADGE = subjectsBoard(new Date().toISOString().slice(0, 10))
	.alerts.hits.filter((h) => h.bucket === "badge")
	.map((h) => h.id);

const G5_ROWS = [
	...["DEMO-103", "DEMO-104", "DEMO-105", "DEMO-106"].map(
		(k) => `[data-testid="cards-di:21-2026"] .crow[data-key="${k}"]`,
	),
	...["DEMO-103", "DEMO-104"].map((k) => `[data-testid="cards-da:21-2026"] .crow[data-key="${k}"]`),
];

const G5_RINGS = 2;
const ringsFor = (selectors: readonly string[]) => (selectors === G5_ROWS ? G5_RINGS : selectors.length);

const CASES: [string, string, readonly string[]][] = [
	["C5~v/21-2026/di", "the version bar", ['[data-testid="bar-di-21-2026"]']],
	[
		"A6~vd/21-2026/di/freeze",
		"the freeze marker",
		['.rel-app[data-app="di"] .rel-mk[data-key="21-2026"][data-stage="freeze"]'],
	],
	["E1~h/da/2.3.1", "the hotfix bar", ['[data-testid="hotfix-da-2.3.1"]']],
	["F2~ht/da/2.3.1/21-2026", "the hotfix bar", ['[data-testid="hotfix-da-2.3.1"]']],
	["L3~ct/21-2026/di/DEMO-109", "the parked card row", ['[data-testid="cards-di:21-2026"] .crow[data-key="DEMO-109"]']],
	["G5~pf/21-2026/demo/open", "every missing card row of the family", G5_ROWS],
	[
		"G2~c/DEMO-304",
		"the sprint card row",
		['[data-testid="sprint-row-DEMO"] [data-dest="none"] .crow[data-key="DEMO-304"]'],
	],
	[
		"L1~mr/di/330",
		"the long-lived lane",
		['.rel-app[data-app="di"] [data-testid="llb-lane"][data-branch="feature/demo-wallet"]'],
	],
	[
		"L8~mr/pi/405",
		"the MR glyph",
		[
			'[data-testid="sprint-row-DEMO"] [data-dest="off"] .crow[data-key="DEMO-303"] [data-testid="mr-glyph"][data-app="pi"]',
		],
	],
	["L9~mr/da/415", "the app label (fallback)", ['[data-testid="label-da"]']],
	["A2~jv/7001", "the app label (fallback)", ['[data-testid="label-da"]']],
	["A3~m/pi/9001", "the app label (fallback)", ['[data-testid="label-pi"]']],
	["F1~tt/21-2026/22-2026", "the later train's chip (fallback)", ['.rel-tchip[data-train="22-2026"]']],
];

test.describe("Show on an alert", () => {
	for (const [hit, what, selectors] of CASES)
		test(`${hit} keeps the sheet open and rings ${what} beside it`, async ({ page }) => {
			await openFrozen(page, "?sheet=attention");
			await settle(page);
			await show(page, hit);
			await expectSpotted(page, selectors, { rings: ringsFor(selectors) });
			await expect(sheet(page)).toBeVisible();
			expect(await outerScrolls(page)).toEqual([]);
		});

	test("Needs attention lists exactly the fixture's badge alerts", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		const shown = await page
			.locator(".rel-sheet .al[data-hit]")
			.evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.hit ?? ""));
		expect(shown.sort()).toEqual([...BADGE].sort());
	});

	for (const hit of BADGE)
		test(`${hit} from Needs attention rings its subject beside the sheet, twice in a row`, async ({ page }) => {
			await openFrozen(page, "?sheet=attention");
			for (const press of [1, 2]) {
				const since = await animationNow(page);
				await show(page, hit);
				await expectRinged(page, since, `press ${press}`);
				await expect(sheet(page), `press ${press}`).toBeVisible();
			}
		});

	test("a second press after the pulse ended rings the subject again", async ({ page }) => {
		const hit = "C5~v/21-2026/di";
		await open(page, "?sheet=attention", "subjects");
		await show(page, hit);
		await expect.poll(() => ringLife(page), { timeout: 9_000 }).toBeGreaterThan(4_500);
		await freezeRings(page);
		const since = await animationNow(page);
		await show(page, hit);
		await expectSpotted(page, ['[data-testid="bar-di-21-2026"]'], { since, rings: 1 });
	});

	test("a second press while the pulse runs starts it over and re-centres a target scrolled away", async ({ page }) => {
		const hit = "C5~v/21-2026/di";
		const target = '[data-testid="bar-di-21-2026"]';
		await openFrozen(page, "?sheet=attention");
		await show(page, hit);
		await expectSpotted(page, [target]);
		const since = await animationNow(page);
		await show(page, hit);
		await expectSpotted(page, [target], { since, rings: 1 });

		const away = await page.locator(".rel-sc").evaluate((el) => {
			el.scrollLeft += 700;
			el.scrollTop += 300;
			return { left: el.scrollLeft, top: el.scrollTop };
		});
		await show(page, hit);
		await expectSpotted(page, [target]);
		await settle(page);
		const at = await page.locator(target).evaluate((el) => {
			const sc = document.querySelector(".rel-sc")!;
			const b = sc.getBoundingClientRect();
			const top = document.querySelector(".rel-axis")!.getBoundingClientRect().bottom;
			const sheetLeft = document.querySelector(".rel-sheet")!.getBoundingClientRect().left;
			const right = Math.min(b.left + sc.clientWidth, sheetLeft);
			const r = el.getBoundingClientRect();
			const pinned = (v: number, max: number) => v <= 0 || v >= max - 1;
			return {
				x: Math.round((r.left + r.right) / 2 - (b.left + 280 + right) / 2),
				y: Math.round((r.top + r.bottom) / 2 - (top + b.top + sc.clientHeight) / 2),
				pinnedX: pinned(sc.scrollLeft, sc.scrollWidth - sc.clientWidth),
				pinnedY: pinned(sc.scrollTop, sc.scrollHeight - sc.clientHeight),
				left: sc.scrollLeft,
				top: sc.scrollTop,
			};
		});
		expect(at.left !== away.left || at.top !== away.top, "scrolled back").toBe(true);
		expect(Math.abs(at.x) <= 5 || at.pinnedX, `centred across the part the sheet leaves (${at.x})`).toBe(true);
		expect(Math.abs(at.y) <= 5 || at.pinnedY, `centred down, or as far as the timeline scrolls (${at.y})`).toBe(true);
	});

	test("a press on another alert takes the old ring away at once and rings the new subject", async ({ page }) => {
		await openFrozen(page, "?sheet=attention&hideBoards=PILOT");
		await show(page, "C5~v/21-2026/di");
		await expectSpotted(page, ['[data-testid="bar-di-21-2026"]'], { rings: 1 });
		const since = await animationNow(page);
		await show(page, "G2~c/PIL-12");
		await expectSpotted(page, ['[data-testid="sprint-row-PILOT"] .crow[data-key="PIL-12"]'], { since, rings: 1 });
		const [gone, born] = (await ringLog(page)).slice(-2);
		expect(gone.sign, "the old ring goes first").toBe("-");
		expect(born.sign).toBe("+");
		expect(gone.at, "before the new subject is drawn and still").toBeLessThan(born.at);
	});

	test("a page family alert rings all of its missing cards at once, one ring per app's list, twice in a row", async ({
		page,
	}) => {
		const hit = "G5~pf/21-2026/demo/open";
		await openFrozen(page, "?sheet=attention");
		for (let press = 0; press < 2; press++) {
			const since = await animationNow(page);
			await show(page, hit);
			await expectSpotted(page, G5_ROWS, { since, rings: G5_RINGS });
		}
	});

	test("a card on a hidden board turns on Sprint cards, shows the board and opens its folds", async ({ page }) => {
		await openFrozen(page, "?sheet=attention&hideBoards=PILOT");
		await expect(layer(page, "Sprint cards")).toHaveAttribute("aria-pressed", "false");
		await expect(page.getByTestId("sprint-row-PILOT")).toHaveCount(0);
		await show(page, "G2~c/PIL-12");
		await expect(layer(page, "Sprint cards")).toHaveAttribute("aria-pressed", "true");
		await expectSpotted(page, ['[data-testid="sprint-row-PILOT"] [data-dest="t:21-2026"] .crow[data-key="PIL-12"]']);
		await expect(page).not.toHaveURL(/hideBoards=[^&]*PILOT/);
	});

	test("a sprint card whose app the filter hides brings that app back", async ({ page }) => {
		await openFrozen(page, "?sheet=attention&apps=di");
		await show(page, "G2~c/PIL-12");
		await expectSpotted(page, ['[data-testid="sprint-row-PILOT"] .crow[data-key="PIL-12"]']);
		await expect(page).toHaveURL(/apps=di(%2C|,)pa/);
	});

	test("a freeze marker on an unfocused train focuses it", async ({ page }) => {
		await openFrozen(page, "?sheet=attention&focus=22-2026");
		await show(page, "A6~vd/21-2026/di/freeze");
		await expectSpotted(page, ['.rel-app[data-app="di"] .rel-mk[data-key="21-2026"][data-stage="freeze"]']);
		await expect(page).toHaveURL(/focus=21-2026/);
	});

	test("a row whose subject has no mark says where Show goes; a row with a mark says nothing", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		const where = (hit: string) => row(page, hit).getByTestId("hit-where");
		await expect(where("L9~mr/da/415")).toHaveText(
			"Show lands at its app: the timeline draws no lane for feat/DEMO-201-base",
		);
		await expect(where("A2~jv/7001")).toContainText("at its app");
		await expect(where("A3~m/pi/9001")).toContainText("at its app");
		await expect(where("F1~tt/21-2026/22-2026")).toContainText("at the later train");
		await expect(where("C5~v/21-2026/di")).toHaveCount(0);
		await expect(row(page, "L9~mr/da/415").getByTestId("hit-show")).toHaveAccessibleDescription(/draws no lane/);
	});

	test("the later train's chip scrolls the timeline to that train", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		await settle(page);
		const before = await page.locator(".rel-sc").evaluate((el) => el.scrollLeft);
		await show(page, "F1~tt/21-2026/22-2026");
		await expect(page).toHaveURL(/focus=22-2026/);
		await expect.poll(() => page.locator(".rel-sc").evaluate((el) => el.scrollLeft)).toBeGreaterThan(before + 100);
	});

	test("an alert of mine on a hotfix keeps Only me on and shows the hotfix bar", async ({ page }) => {
		await openFrozen(page, "?sheet=attention&person=ada");
		await expectFiltered(page);
		await show(page, "F2~ht/da/2.3.1/21-2026");
		await expectSpotted(page, ['[data-testid="hotfix-da-2.3.1"]']);
		await expectFiltered(page);
	});

	test("an alert of mine keeps Only me on and shows the version without writing the focus", async ({ page }) => {
		await openFrozen(page, "?sheet=attention&person=ada");
		await show(page, "C5~v/21-2026/di");
		await expectSpotted(page, ['[data-testid="bar-di-21-2026"]']);
		await expectFiltered(page);
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
		["card", "?sheet=card:DEMO-109", "L3~ct/21-2026/di/DEMO-109", '.rel-sc .crow[data-key="DEMO-109"]'],
	] as const)
		test(`from the ${name} sheet, Show keeps the sheet open and rings the subject beside it, twice`, async ({
			page,
		}) => {
			await openFrozen(page, query);
			await settle(page);
			for (let press = 0; press < 2; press++) {
				const since = await animationNow(page);
				await show(page, hit);
				await expectSpotted(page, [selector], { since, rings: 1 });
				await expect(sheet(page)).toBeVisible();
				await expect(page).toHaveURL(/sheet=/);
			}
		});

	test("every alert row, hidden ones too, has a Show button", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		await page.getByRole("button", { name: /hidden/ }).click();
		const rows = page.locator(".rel-sheet .al[data-hit]");
		expect(await rows.count()).toBeGreaterThan(15);
		await expect(page.locator(".rel-sheet .al[data-hit]:not(:has([data-testid=hit-show]))")).toHaveCount(0);
	});
});

test.describe("sheets float beside the timeline", () => {
	test("a sheet has no backdrop and stays open through Show, a pan and a click on the timeline", async ({ page }) => {
		await openFrozen(page, "?sheet=attention");
		await settle(page);
		await expect(sheet(page)).toHaveAttribute("aria-modal", "false");
		await expect(page.locator(".rel-scrim")).toHaveCount(0);
		await show(page, "C5~v/21-2026/di");
		await expectSpotted(page, ['[data-testid="bar-di-21-2026"]']);
		await settle(page);
		const sc = page.locator(".rel-sc");
		const box = (await sc.boundingBox())!;
		const y = box.y + box.height - 30;
		const before = await sc.evaluate((el) => el.scrollLeft);
		await page.mouse.move(box.x + 400, y);
		await page.mouse.down();
		await page.mouse.move(box.x + 600, y, { steps: 8 });
		await page.mouse.up();
		await expect.poll(() => sc.evaluate((el) => el.scrollLeft)).toBeLessThan(before - 100);
		await page.mouse.click(box.x + 420, y);
		await expect(sheet(page)).toBeVisible();
		await expect(page).toHaveURL(/sheet=attention/);
	});

	test("opening a card from the timeline while a sheet is open replaces it, with no back stack", async ({ page }) => {
		await open(page, "?layers=cards&sheet=version:21-2026:di", "subjects");
		const group = page.getByTestId("cards-di:21-2026");
		const reach = async (key: string) => {
			const crow = group.locator(`.crow[data-key="${key}"]`);
			await crow.evaluate((el) => {
				const sc = document.querySelector(".rel-sc")!;
				sc.scrollLeft += el.getBoundingClientRect().left - sc.getBoundingClientRect().left - 300;
			});
			await settle(page);
			return crow;
		};
		await group.locator(".csum").click();
		await (await reach("DEMO-101")).click();
		await expect(page).toHaveURL(/sheet=card(%3A|:)DEMO-101/);
		await (await reach("DEMO-102")).click();
		await expect(page).toHaveURL(/sheet=card(%3A|:)DEMO-102/);
		await expect(page.locator(".rel-sheet")).toHaveCount(1);
		await expect(sheet(page).getByRole("button", { name: "Back", exact: true })).toHaveCount(0);
	});

	test("Esc and the close button close the sheet", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		await page.keyboard.press("Escape");
		await expect(sheet(page)).toHaveCount(0);
		await page.getByRole("button", { name: /Needs attention/ }).click();
		await expect(sheet(page)).toBeVisible();
		await sheet(page).getByRole("button", { name: "Close" }).click();
		await expect(sheet(page)).toHaveCount(0);
	});
});

test.describe("the page never shifts", () => {
	test.use({ viewport: { width: 1100, height: 800 } });

	test("in a window too narrow for the top bar, focus and Show scroll only the timeline", async ({ page }) => {
		await openFrozen(page, "?sheet=attention");
		await settle(page);
		await page.getByRole("button", { name: /Needs attention/ }).focus();
		expect(await outerScrolls(page)).toEqual([]);
		await show(page, "L1~mr/di/330");
		await expectSpotted(page, ['.rel-app[data-app="di"] [data-testid="llb-lane"][data-branch="feature/demo-wallet"]']);
		expect(await outerScrolls(page)).toEqual([]);
	});
});

test.describe("an alert's subject line in its own sheet", () => {
	test("A6 rings the freeze stage row it names, keeps the sheet, and rings it again on a second press", async ({
		page,
	}) => {
		await openFrozen(page, "?sheet=version:21-2026:di");
		await settle(page);
		const subject = sheet(page).locator('.al[data-hit="A6~vd/21-2026/di/freeze"]').getByTestId("hit-subject");
		const part = '.rel-sheet [data-testid="stage-row"][data-stage="freeze"]';
		await subject.click();
		await expectSpotted(page, [part], { rings: 1 });
		await expect(page).toHaveURL(/sheet=version(%3A|:)21-2026(%3A|:)di/);
		const since = await animationNow(page);
		await subject.click();
		await expectSpotted(page, [part], { since, rings: 1 });
	});

	test("A6 from Needs attention opens the version sheet and rings the same row", async ({ page }) => {
		await openFrozen(page, "?sheet=attention");
		await sheet(page).locator('.al[data-hit="A6~vd/21-2026/di/freeze"]').getByTestId("hit-subject").click();
		await expect(page).toHaveURL(/sheet=version/);
		await expectSpotted(page, ['.rel-sheet [data-testid="stage-row"][data-stage="freeze"]']);
		await sheet(page).getByRole("button", { name: "Back", exact: true }).click();
		await expect(page).toHaveURL(/sheet=attention/);
	});

	test("an approval alert rings the approvers it names, as one run", async ({ page }) => {
		await openFrozen(page, "?sheet=version:21-2026:da");
		await settle(page);
		await sheet(page).locator('.al[data-hit="D3~v/21-2026/da"]').getByTestId("hit-subject").click();
		await expectSpotted(page, ['.rel-sheet [data-part="approvers"] [data-name]'], { rings: 1 });
		await expect(page.locator('.rel-sheet [data-part="approvers"] [data-name]')).toHaveCount(9);
	});
});

test.describe("Show under reduced motion", () => {
	test("rings the target without a pulse, holds it for 5 s, then takes it away", async ({ page }) => {
		await page.emulateMedia({ reducedMotion: "reduce" });
		await open(page, "?sheet=attention", "subjects");
		await show(page, "L3~ct/21-2026/di/DEMO-109");
		const target = '[data-testid="cards-di:21-2026"] .crow[data-key="DEMO-109"]';
		await expectSpotted(page, [target], { motion: false });
		await expect.poll(() => ringLife(page), { timeout: 9_000, intervals: [100] }).toBeGreaterThan(0);
		const life = await ringLife(page);
		expect(life, "still there at 4 s").toBeGreaterThan(4_000);
		expect(life, "held the full 5 s").toBeGreaterThanOrEqual(4_950);
		expect(life, "gone by about 5.5 s").toBeLessThan(5_600);
		await expect(page.locator(".rel-halo")).toHaveCount(0);
	});
});
