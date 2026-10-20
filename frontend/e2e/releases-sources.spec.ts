import { expect, test, type Locator } from "@playwright/test";
import { NARROW, VIEWPORTS, labelOverlaps, layer, open, settle } from "./releases-kit";

const SHOTS = process.env.AO_RELEASES_SHOTS;
const APPS = ["di", "da", "pi", "pa"];
const tags = (scope: Locator) => scope.getByTestId("source-tag");
const card = (scope: { getByTestId: Locator["getByTestId"] }) => scope.getByTestId("source-card");

test.describe("releases sources layer", () => {
	test.use({ viewport: NARROW });

	test("off by default with no tags; on, every app label has tags and quiet bars have none", async ({ page }) => {
		await open(page);
		await expect(layer(page, "Sources")).toHaveAttribute("aria-pressed", "false");
		await expect(tags(page.getByTestId("releases-page"))).toHaveCount(0);
		await layer(page, "Sources").click();
		await expect(page).not.toHaveURL(/layers=/);
		for (const app of APPS) expect(await tags(page.getByTestId(`label-${app}`)).count(), app).toBeGreaterThan(2);
		await expect(tags(page.getByTestId("bar-di-21-2026")).first()).toBeVisible();
		await expect(tags(page.getByTestId("bar-di-22-2026"))).toHaveCount(0);
		await expect(tags(page.locator(".rel-cg.quiet"))).toHaveCount(0);
		await page.reload();
		await expect(layer(page, "Sources")).toHaveAttribute("aria-pressed", "true");
		await layer(page, "Sources").click();
		await expect(tags(page.getByTestId("releases-page"))).toHaveCount(0);
	});

	test("a link with src in layers turns the layer on", async ({ page }) => {
		await open(page, "?layers=llb,cards,src");
		await expect(layer(page, "Sources")).toHaveAttribute("aria-pressed", "true");
		await expect(tags(page.getByTestId("label-di")).first()).toBeVisible();
	});

	test("the focused card group header tags GitLab and Jira; each row's tag is its why in fixed order", async ({
		page,
	}) => {
		await open(page, "?layers=llb,cards,src");
		const group = page.getByTestId("cards-di:21-2026");
		const head = tags(group.locator(".csum"));
		await expect(head).toHaveText(["GitLab", "Jira"]);
		await group.locator(".csum").click();
		const row = (key: string) => tags(group.locator(`.crow[data-key="${key}"]`));
		await expect(row("DEMO-101")).toHaveText("fixVersion");
		await expect(row("DEMO-103")).toHaveText("fixVersion + sprint MR");
		await expect(row("DEMO-104")).toHaveText("page");
		await expect(row("DEMO-106")).toHaveText("fixVersion + page + sprint MR");
		await expect(tags(page.getByTestId("cards-di:22-2026"))).toHaveCount(0);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-sources-cards.png` });
	});

	test("a tag opens its card with each source and its last read; Esc closes only the card", async ({ page }) => {
		await open(page, "?layers=llb,cards,src&sheet=version:21-2026:di");
		const sheet = page.getByRole("dialog", { name: "Release details" });
		const tag = tags(sheet.getByTestId("stage-row").filter({ hasText: "scan" }));
		await expect(tag).toHaveText("GitLab");
		await tag.click();
		const c = card(page);
		await expect(c).toBeVisible();
		await expect(c).toContainText("Worked out by AO from");
		await expect(c).toContainText("ao.convention");
		await expect(c).toContainText(/GitLab read (\d+[mhd] ago|just now)/);
		await page.keyboard.press("Escape");
		await expect(c).toHaveCount(0);
		await expect(sheet).toBeVisible();
		await expect(page).toHaveURL(/sheet=/);
	});

	test("a tag on a clickable label does not open the label's sheet", async ({ page }) => {
		await open(page, "?layers=llb,cards,src");
		await tags(page.getByTestId("label-di").locator(".meta")).first().click();
		await expect(card(page)).toBeVisible();
		await expect(page.getByRole("dialog", { name: "Release details" })).toHaveCount(0);
	});

	test("hovering a value opens its card after the delay, and not before", async ({ page }) => {
		await open(page, "?layers=llb,cards,src");
		const value = page.getByTestId("label-di").locator(".ver [data-srcv]");
		await value.evaluate((el) => {
			const w = window as unknown as { __hover: { in?: number; card?: number } };
			w.__hover = {};
			el.addEventListener("pointerenter", () => (w.__hover.in ??= performance.now()));
			new MutationObserver(() => {
				if (document.querySelector('[data-testid="source-card"]')) w.__hover.card ??= performance.now();
			}).observe(document.body, { childList: true, subtree: true });
		});
		await value.hover();
		await expect(card(page)).toContainText("One dot per stage");
		const t = await page.evaluate(() => (window as unknown as { __hover: { in: number; card: number } }).__hover);
		expect(t.card - t.in, "the card opened before the 400 ms value delay").toBeGreaterThanOrEqual(380);
	});

	test("with the layer off a value hover opens nothing", async ({ page }) => {
		await open(page);
		await page.getByTestId("label-di").locator(".rel-step").hover();
		await page.waitForTimeout(700);
		await expect(card(page)).toHaveCount(0);
	});

	test("every stage row of the version sheet carries a tag; alerts show their rule wide", async ({ page }) => {
		await open(page, "?layers=llb,cards,src&sheet=version:21-2026:di");
		const sheet = page.getByRole("dialog", { name: "Release details" });
		const rows = sheet.getByTestId("stage-row");
		await expect(rows).toHaveCount(6);
		for (const r of await rows.all()) await expect(tags(r)).toHaveCount(1);
		await expect(tags(sheet.locator('.al[data-hit^="C5"]'))).toHaveText("C5 · GitLab");
		await expect(tags(page.getByTestId("label-di").getByTestId("label-blocker"))).toHaveText("rule C5");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-sources-sheet.png` });
	});

	test("an off-board sprint card is tagged sprint MR; the board's own cards are not", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards,src");
		for (const key of ["DEMO", "PILOT"]) {
			const row = page.getByTestId(`sprint-row-${key}`);
			await row.locator(".spsrc[data-sprint='active'] .csum").click();
			for (const d of await row.locator(".dsum").all()) await d.click();
		}
		const off = page.getByTestId("sprint-row-PILOT").locator('.crow[data-key="PIL-12"]');
		await expect(off).toContainText("not on the board");
		await expect(tags(off)).toHaveText("sprint MR");
		await expect(tags(page.getByTestId("sprint-row-PILOT").locator('.crow[data-key="PIL-11"]'))).toHaveCount(0);
		const demoCards = page.getByTestId("sprint-row-DEMO").locator(".crow[data-key]");
		expect(await demoCards.count()).toBeGreaterThan(3);
		await expect(tags(demoCards)).toHaveCount(0);
	});

	test("the top bar shows J G C with no minutes; the age lives in each mark's card", async ({ page }) => {
		await open(page);
		const bar = page.getByTestId("release-sources");
		await expect(bar).toHaveText("JGC");
		await expect(bar).not.toContainText(/\d/);
		await bar.locator('[data-source="jira"]').hover();
		await expect(card(page)).toContainText(/Jira · read (\d+[mhd] ago|just now)/);
		await expect(card(page)).toContainText("jira.versions");
	});

	test("a stale kind dots only the tags that read it and turns its system's mark amber", async ({ page }) => {
		await open(page, "?layers=llb,cards,src", "stale-kind");
		await expect(page.getByTestId("release-sources").locator('[data-source="jira"]')).toHaveText("Junknown");
		const label = page.getByTestId("label-di");
		const approved = tags(label.locator(".meta > span", { hasText: "approved" }));
		await expect(approved).toHaveText("Jira");
		await expect(approved).toHaveAttribute("data-unknown", "true");
		const group = page.getByTestId("cards-di:21-2026");
		const ready = tags(group.locator(".csum")).filter({ hasText: "Jira" });
		await expect(ready).toHaveCount(1);
		await expect(ready).not.toHaveAttribute("data-unknown");
		const merged = tags(label.locator(".meta > span", { hasText: "merged" }));
		await expect(merged).not.toHaveAttribute("data-unknown");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-sources-stale.png` });
	});

	test("the Key sheet explains the tags", async ({ page }) => {
		await open(page, "?sheet=key");
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet.getByTestId("key-sources")).toContainText("J Jira · G GitLab · C Confluence");
		await expect(sheet).toContainText("worked out by AO from these systems");
	});
});

for (const { width, height } of VIEWPORTS) {
	test(`at ${width}x${height} with Sources on, 4/4 app labels sit above the fold and nothing overlaps`, async ({
		page,
	}) => {
		await page.setViewportSize({ width, height });
		await open(page, "?layers=llb,cards,src");
		await settle(page);
		for (const app of APPS) {
			const b = await page.getByTestId(`label-${app}`).getByTestId("label-blocker").boundingBox();
			expect(b, app).not.toBeNull();
			expect(b!.y + b!.height, `${app} blocker above the fold`).toBeLessThanOrEqual(height);
		}
		const clipped = await page.evaluate(
			() =>
				[...document.querySelectorAll(".rel-app > .rel-lab")].filter(
					(l) => l.scrollWidth > l.clientWidth + 1 || l.scrollHeight > (l.parentElement?.clientHeight ?? 0) + 1,
				).length,
		);
		expect(clipped, "no label overflows its row").toBe(0);
		const report = await labelOverlaps(page);
		expect(report.n).toBeGreaterThan(10);
		expect(report.overlaps).toEqual([]);
		const top = await page.evaluate(() => {
			const kids = [...document.querySelectorAll(".rel-top > *")].map((e) => e.getBoundingClientRect());
			const box = document.querySelector(".rel-top")!.getBoundingClientRect();
			const hits: number[] = [];
			for (let i = 1; i < kids.length; i++) if (kids[i].width > 0 && kids[i].left < kids[i - 1].right - 1) hits.push(i);
			return { hits, inside: kids.every((r) => r.right <= box.right + 1) };
		});
		expect(top.hits, "top bar items never overlap").toEqual([]);
		expect(top.inside, "top bar items stay inside the bar").toBe(true);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-sources-${width}.png` });
	});
}
