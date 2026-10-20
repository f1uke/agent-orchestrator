import { expect, test, type Locator } from "@playwright/test";
import { NARROW, WIDE, labelOverlaps, layer, open, settle } from "./releases-kit";

const LAYERS = "?layers=sprint,llb,cards";
const DAY = 36;

const bar = (row: Locator, which: "active" | "next") => row.locator(`.spsrc[data-sprint='${which}']`);
async function unfold(button: Locator, shown: Locator, count = 1) {
	await expect(async () => {
		if ((await button.getAttribute("aria-expanded")) !== "true")
			await button.click({ position: { x: 8, y: 8 }, timeout: 2_000 });
		await expect(shown).toHaveCount(count, { timeout: 2_000 });
	}).toPass({ timeout: 20_000 });
}

const toggle = (page: import("@playwright/test").Page) => page.getByTestId("mobile-only");

test.describe("releases Mobile only filter", () => {
	test.use({ viewport: NARROW });

	test("it is off by default, on the Sprint line inside the label column, and every card shows", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		await expect(toggle(page)).toHaveAttribute("aria-pressed", "false");
		const label = (await page.locator(".rel-sched > .rel-row").first().locator(".rel-lab").boundingBox())!;
		const mob = (await toggle(page).boundingBox())!;
		expect(mob.x).toBeGreaterThanOrEqual(label.x);
		expect(mob.x + mob.width).toBeLessThanOrEqual(label.x + label.width);
		await layer(page, "Sprint cards").click();
		await expect(toggle(page)).toHaveCount(0);
		await layer(page, "Sprint cards").click();
		await expect(bar(demo, "active").getByTestId("sprint-mobile-count")).toHaveText("8 mobile");
		await expect(bar(demo, "active").locator(".sl > b")).toHaveText("9 cards");
	});

	test("on, it counts mobile cards only and drops the web block, on both bars", async ({ page }) => {
		await open(page, LAYERS);
		const demo = page.getByTestId("sprint-row-DEMO");
		await bar(demo, "active").locator(".csum").click();
		await expect(demo.locator("[data-dest='web']")).toHaveCount(1);
		await toggle(page).click();
		await expect(toggle(page)).toHaveAttribute("aria-pressed", "true");
		await expect(bar(demo, "active").locator(".sl > b")).toHaveText("8 mobile cards");
		await expect(bar(demo, "active").getByTestId("sprint-mobile-count")).toHaveCount(0);
		await expect(bar(demo, "next").locator(".sl > b")).toContainText("mobile cards");
		await expect(demo.locator("[data-dest='web']")).toHaveCount(0);
	});

	test("a next sprint of web cards shows them with the filter off and no mobile cards with it on", async ({ page }) => {
		await open(page, LAYERS);
		const pilot = page.getByTestId("sprint-row-PILOT");
		await settle(page);
		await expect(bar(pilot, "next").locator(".sl > b")).toHaveText("3 cards");
		await expect(bar(pilot, "next").getByTestId("sprint-mobile-count")).toHaveText("0 mobile");
		const web = pilot.locator("[data-sprint='next'][data-dest='web']");
		await unfold(bar(pilot, "next").locator(".csum"), web);
		await unfold(web.locator(".dsum"), web.locator(".crow[data-key]"), 3);
		await expect(web.getByTestId("web-tag")).toHaveCount(3);
		await expect(web).toContainText("not mobile");

		await toggle(page).click();
		await expect(bar(pilot, "next").locator(".sl > b")).toHaveText("0 mobile cards");
		await expect(pilot.locator("[data-sprint='next'][data-dest='web']")).toHaveCount(0);
	});

	test("the state is in the URL and survives a reload and a trip to another page", async ({ page }) => {
		await open(page, LAYERS);
		await toggle(page).click();
		await expect(page).toHaveURL(/mobile=%221%22/);
		await page.reload();
		await expect(toggle(page)).toHaveAttribute("aria-pressed", "true");
		await expect(page.getByTestId("sprint-row-DEMO").locator(".spsrc[data-sprint='active'] .sl > b")).toHaveText(
			"8 mobile cards",
		);
		await toggle(page).click();
		await expect(page).not.toHaveURL(/mobile=%221%22/);
		await page.reload();
		await expect(toggle(page)).toHaveAttribute("aria-pressed", "false");
	});

	test("Only me keeps its of mine wording with the filter on", async ({ page }) => {
		await open(page, `${LAYERS}&me=1&mobile=1`);
		await expect(bar(page.getByTestId("sprint-row-DEMO"), "active").locator(".sl > b")).toContainText("of mine");
	});

	test("the sprint sheet counts the same way and names the rule in its Sources note", async ({ page }) => {
		await open(page, `${LAYERS},src&sheet=sprint:DEMO`);
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet.getByTestId("sprint-sheet-cards")).toContainText("9 cards, 8 mobile");
		await sheet.getByTestId("sprint-sheet-cards").getByTestId("source-tag").click();
		await expect(page.getByTestId("source-card")).toContainText("titled App, iOS or ADR");
	});

	for (const theme of ["dark", "light"] as const) {
		test(`the toggle reads in the ${theme} theme, on and off`, async ({ page }) => {
			await page.addInitScript((t) => localStorage.setItem("ao.theme", t), theme);
			await open(page, LAYERS);
			await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
			const readings = async () =>
				toggle(page).evaluate((el) => {
					const sw = el.querySelector(".sw")!;
					const cs = getComputedStyle(el);
					const knob = getComputedStyle(sw, "::after").backgroundColor;
					return {
						text: cs.color,
						track: getComputedStyle(sw).backgroundColor,
						knob,
						bg: getComputedStyle(el.closest(".rel-lab")!).backgroundColor,
					};
				});
			const off = await readings();
			await toggle(page).click();
			const on = await readings();
			for (const r of [off, on]) {
				expect(r.text, "label colour differs from its background").not.toBe(r.bg);
				expect(r.knob, "knob is painted").not.toBe("rgba(0, 0, 0, 0)");
				expect(r.track, "track is painted").not.toBe("rgba(0, 0, 0, 0)");
			}
			expect(on.knob, "the knob changes colour when on").not.toBe(off.knob);
			await expect(toggle(page)).toHaveAttribute("aria-pressed", "true");
		});
	}
});

test.describe("releases estimated next sprint", () => {
	test.use({ viewport: NARROW });

	test("PILOT's next sprint sits right after the active bar, reads no dates yet and is dashed", async ({ page }) => {
		await open(page, LAYERS);
		const pilot = page.getByTestId("sprint-row-PILOT");
		const a = (await bar(pilot, "active").boundingBox())!;
		const n = (await bar(pilot, "next").boundingBox())!;
		expect(n.y, "same lane").toBeCloseTo(a.y, 0);
		expect(n.x - a.x, "the day after the active sprint ends").toBeCloseTo(15 * DAY, 0);
		await expect(bar(pilot, "next")).toContainText("no dates yet");
		await expect(bar(pilot, "next")).toHaveAttribute("data-estimated", "true");
		await expect(bar(pilot, "active")).not.toHaveAttribute("data-estimated", /.*/);
		const outline = await bar(pilot, "next")
			.locator(".csum")
			.evaluate((el) => getComputedStyle(el).outlineStyle);
		expect(outline).toBe("dashed");
		await expect(bar(page.getByTestId("sprint-row-DEMO"), "next")).not.toContainText("no dates yet");
	});

	test("its tooltip and sheet say the dates are estimated from the active sprint's length", async ({ page }) => {
		await open(page, LAYERS);
		const pilot = page.getByTestId("sprint-row-PILOT");
		await expect(bar(pilot, "next").locator(".csum")).toHaveAttribute(
			"title",
			/estimated from the active sprint's length/,
		);
		await bar(pilot, "next").locator(".lbn").click();
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet.getByTestId("sprint-sheet-estimated")).toContainText("no dates yet, estimated");
	});
});

for (const vp of [NARROW, WIDE]) {
	test.describe(`releases Mobile only at ${vp.width}x${vp.height}`, () => {
		test.use({ viewport: vp });

		for (const mobile of [false, true]) {
			test(`no label overlaps with both bars unfolded, Mobile only ${mobile ? "on" : "off"}`, async ({ page }) => {
				test.setTimeout(90_000);
				await open(page, `${LAYERS}${mobile ? "&mobile=1" : ""}`);
				for (const key of ["DEMO", "PILOT"]) {
					const row = page.getByTestId(`sprint-row-${key}`);
					await bar(row, "active").locator(".csum").click();
					await bar(row, "next").locator(".csum").click();
				}
				await settle(page);
				for (const d of await page.locator(".rel-cg.dest .dsum").all()) await d.click();
				await settle(page);
				const { n, overlaps } = await labelOverlaps(page);
				expect(n).toBeGreaterThan(10);
				expect(overlaps).toEqual([]);
			});
		}
	});
}
