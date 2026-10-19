import { expect, test, type Page } from "@playwright/test";
import { NARROW, VIEWPORTS, WIDE, labelOverlaps, layer, open, settle } from "./releases-kit";

const SHOTS = process.env.AO_RELEASES_SHOTS;
const G2 = "G2~c/DEMO-304";
const demo = (page: Page) => page.getByTestId("sprint-row-DEMO");
const block = (page: Page, key: string) => demo(page).locator(`[data-dest="${key}"]`);
const row = (page: Page, key: string, card: string) => block(page, key).locator(`.crow[data-key="${card}"]`);

async function unfoldSprint(page: Page, ...dests: string[]) {
	await demo(page).locator(".spsrc .csum").click();
	await settle(page);
	for (const d of dests) {
		await block(page, d).locator(".dsum").click();
		await settle(page);
	}
}

async function expectMarked(page: Page) {
	await expect(demo(page).getByTestId("sprint-dests")).toHaveText(/off 1\s*no code 1/);
	await expect(demo(page).getByTestId("sprint-dests")).not.toContainText("no MR");
	await expect(page.locator(`[data-hit="${G2}"]`)).toHaveCount(0);
}

async function expectUnmarked(page: Page) {
	await expect(demo(page).getByTestId("sprint-dests")).toHaveText(/off 1\s*no MR 1/);
}

async function attentionCount(page: Page) {
	return Number((await page.getByTestId("attention-count").textContent())?.match(/\d+/)?.[0]);
}

test.describe("No MR needed", () => {
	test.use({ viewport: SHOTS ? WIDE : NARROW });

	test("from the card row: the card moves to the quiet no-code block, counts update, its G2 goes; undo restores all", async ({
		page,
	}) => {
		await open(page, "?layers=sprint,llb,cards", "subjects");
		const before = await attentionCount(page);
		await unfoldSprint(page, "none");
		const r = row(page, "none", "DEMO-304");
		await r.hover();
		await r.getByRole("button", { name: "Mark DEMO-304 No MR needed" }).click();
		await settle(page);
		await expectMarked(page);
		await expect(block(page, "none")).toHaveCount(0);
		await expect(block(page, "nomr").locator(".dsum")).toContainText("∅ no code");
		await expect(block(page, "nomr").locator(".dsum")).toContainText("marked No MR needed in AO");
		expect(await attentionCount(page)).toBe(before - 1);
		await block(page, "nomr").locator(".dsum").click();
		await settle(page);
		const marked = row(page, "nomr", "DEMO-304");
		await expect(marked).toHaveClass(/nomr/);
		await expect(marked.locator(".cg")).toHaveText("∅");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/nomr-row-marked.png` });

		await marked.getByRole("button", { name: "Undo No MR needed on DEMO-304" }).click();
		await settle(page);
		await expectUnmarked(page);
		await expect(block(page, "nomr")).toHaveCount(0);
		await expect(row(page, "none", "DEMO-304")).toBeVisible();
		expect(await attentionCount(page)).toBe(before);
	});

	test("from the card sheet, which shows who marked it and offers the undo", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards&sheet=card:DEMO-304", "subjects");
		const sheet = page.getByRole("dialog", { name: "Release details" });
		const line = sheet.getByTestId("card-no-mr");
		await expect(line).toContainText("no MR in any repo");
		await expect(sheet.locator(`[data-hit="${G2}"]`)).toHaveCount(1);
		await line.getByRole("button", { name: "Mark DEMO-304 No MR needed" }).click();
		await expect(line).toContainText("No MR needed");
		await expect(line).toContainText("marked by you · app");
		await expect(sheet.locator(`[data-hit="${G2}"]`)).toHaveCount(0);
		await expectMarked(page);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/nomr-sheet-marked.png` });
		await line.getByRole("button", { name: "Undo No MR needed on DEMO-304" }).click();
		await expect(line).toContainText("no MR in any repo");
		await expect(sheet.locator(`[data-hit="${G2}"]`)).toHaveCount(1);
		await expectUnmarked(page);
	});

	test("from the G2 alert row, and a card with an MR offers no mark", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards&sheet=attention", "subjects");
		const sheet = page.getByRole("dialog", { name: "Release details" });
		const hit = sheet.locator(`[data-hit="${G2}"]`);
		await expect(sheet.locator('[data-hit="G2~c/PIL-12"]').getByTestId("no-mr-toggle")).toHaveCount(0);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/nomr-g2-row.png` });
		await hit.getByRole("button", { name: "Mark DEMO-304 No MR needed" }).click();
		await expect(hit).toHaveCount(0);
		await expectMarked(page);
		await page.goto("/#/releases?layers=sprint,llb,cards&sheet=card:DEMO-304");
		const line = page.getByRole("dialog", { name: "Release details" }).getByTestId("card-no-mr");
		await line.getByRole("button", { name: "Undo No MR needed on DEMO-304" }).click();
		await expectUnmarked(page);
		await page.goto("/#/releases?layers=sprint,llb,cards&sheet=attention");
		await expect(page.locator(`[data-hit="${G2}"]`)).toHaveCount(1);
	});

	test("a train card with no MR gets the no-code state and leaves the merged ratio", async ({ page }) => {
		await open(page, "?layers=llb,cards", "subjects");
		const group = page.getByTestId("cards-di:21-2026");
		await expect(group.locator(".csum")).not.toContainText("no code");
		const merged = page.getByTestId("label-di").locator(".meta");
		await expect(merged).toContainText("3/6");
		await group.locator(".csum").click();
		await settle(page);
		const r = group.locator('.crow[data-key="DEMO-105"]');
		await r.hover();
		await r.getByRole("button", { name: "Mark DEMO-105 No MR needed" }).click();
		await expect(group.locator(".csum")).toContainText("1 no code");
		await expect(r.locator(".cg")).toHaveText("∅");
		await expect(merged).toContainText("3/5");
		await expect(group.locator('.crow[data-key="DEMO-101"]').getByTestId("no-mr-toggle")).toHaveCount(0);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/nomr-train-group.png` });
		await r.getByRole("button", { name: "Undo No MR needed on DEMO-105" }).click();
		await expect(group.locator(".csum")).not.toContainText("no code");
		await expect(merged).toContainText("3/6");
	});

	test("Only me keeps a card I marked and says marked by you; Sources names AO's mark", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards,src&me=1", "subjects");
		await expect(demo(page).getByTestId("sprint-dests")).not.toContainText("no code");
		await page.goto("/#/releases?layers=sprint,llb,cards,src&sheet=card:DEMO-304");
		const line = page.getByRole("dialog", { name: "Release details" }).getByTestId("card-no-mr");
		await line.getByRole("button", { name: "Mark DEMO-304 No MR needed" }).click();
		await expect(page.getByRole("dialog", { name: "Release details" }).getByTestId("card-mine")).toContainText(
			"marked by you",
		);
		const tag = line.getByTestId("source-tag");
		await expect(tag).toHaveText("AO mark");
		await tag.click();
		const card = page.getByTestId("source-card");
		await expect(card).toContainText("marked by you · app");
		await expect(card).toContainText("ao.no_mr");
		await expect(card).not.toContainText("GitLab");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/nomr-sources-card.png` });
		await page.keyboard.press("Escape");
		await page.goto("/#/releases?layers=sprint,llb,cards,src&me=1");
		await expect(demo(page).getByTestId("sprint-dests")).toContainText("no code 1");
		await demo(page).locator(".spsrc .csum").click();
		await settle(page);
		await block(page, "nomr").locator(".dsum").click();
		await settle(page);
		await expect(row(page, "nomr", "DEMO-304")).toBeVisible();
		await expect(row(page, "nomr", "DEMO-304").getByTestId("source-tag")).toHaveCount(0);
	});

	for (const vp of VIEWPORTS)
		test(`the marked row and its block sit cleanly at ${vp.width}x${vp.height}`, async ({ page }) => {
			await page.setViewportSize(vp);
			await open(page, "?layers=sprint,llb,cards", "subjects");
			await unfoldSprint(page, "none");
			const r = row(page, "none", "DEMO-304");
			await r.hover();
			await r.getByRole("button", { name: "Mark DEMO-304 No MR needed" }).click();
			await settle(page);
			await block(page, "nomr").locator(".dsum").click();
			await settle(page);
			const box = await row(page, "nomr", "DEMO-304").boundingBox();
			const btn = await row(page, "nomr", "DEMO-304").getByTestId("no-mr-toggle").boundingBox();
			expect(btn!.x + btn!.width).toBeLessThanOrEqual(box!.x + box!.width);
			expect(btn!.height).toBeLessThanOrEqual(box!.height);
			expect((await labelOverlaps(page)).overlaps).toEqual([]);
			if (SHOTS && vp.width === 2560) await page.screenshot({ path: `${SHOTS}/nomr-block-2560.png` });
			await expect(layer(page, "Sprint cards")).toHaveAttribute("aria-pressed", "true");
		});
});
