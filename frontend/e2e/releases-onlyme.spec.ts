import { expect, test, type Page } from "@playwright/test";
import { subjectsBoard } from "../src/renderer/lib/releases/fixture";
import { NARROW, VIEWPORTS, expectSpotted, freezeRings, open } from "./releases-kit";

test.use({ viewport: NARROW });

const SHOTS = process.env.AO_RELEASES_SHOTS;

const sheet = (page: Page) => page.getByRole("dialog", { name: "Release details" });
const row = (page: Page, hit: string) => sheet(page).locator(`.al[data-hit="${hit}"]`);
const shownHits = (page: Page) =>
	sheet(page)
		.locator(".al[data-hit]")
		.evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.hit ?? "").sort());

const MINE: Record<string, string> = {
	"C1~v/21-2026/di": "your release MR !77",
	"C5~v/21-2026/di": "your release MR !77",
	"D3~v/21-2026/da": "you have not approved",
	"E1~h/da/2.3.1": "your hotfix MR !88",
	"F2~ht/da/2.3.1/21-2026": "your hotfix MR !88",
	"G5~pf/21-2026/demo/open": "your cards DEMO-104, DEMO-106",
};

const ALL = subjectsBoard(new Date().toISOString().slice(0, 10))
	.alerts.hits.filter((h) => h.bucket === "badge")
	.map((h) => h.id)
	.sort();

for (const viewport of VIEWPORTS)
	test.describe(`Only me at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport });

		test("lists exactly the alerts I own or must act on, each saying why", async ({ page }) => {
			await open(page, "?sheet=attention&me=1", "subjects");
			await expect.poll(() => shownHits(page)).toEqual(Object.keys(MINE).sort());
			for (const [hit, why] of Object.entries(MINE))
				await expect(row(page, hit).getByTestId("hit-mine")).toHaveText(why);
			await expect(page.getByTestId("attention-count")).toHaveText(String(Object.keys(MINE).length));
			if (SHOTS) await page.screenshot({ path: `${SHOTS}/onlyme-attention-${viewport.width}.png` });
		});
	});

test.describe("Only me", () => {
	test("D3 says I have not approved and names nobody else", async ({ page }) => {
		await open(page, "?sheet=attention&me=1", "subjects");
		const d3 = row(page, "D3~v/21-2026/da");
		await expect(d3.locator(".tx")).toHaveText("9 of 9 not approved");
		await expect(d3).not.toContainText("Ben Sample");
		await expect(d3).not.toContainText("Ada Example");
		const label = page.getByTestId("label-da").getByTestId("label-blocker");
		await expect(label).toContainText("9 of 9 not approved");
		await expect(label).not.toContainText("Ben Sample");
	});

	test("Show on a page family rings only my cards", async ({ page }) => {
		await open(page, "?sheet=attention&me=1", "subjects");
		await freezeRings(page);
		await row(page, "G5~pf/21-2026/demo/open").getByTestId("hit-show").click();
		const mine = ["DEMO-104", "DEMO-106"].map((k) => `[data-testid="cards-di:21-2026"] .crow[data-key="${k}"]`);
		await expectSpotted(page, [...mine, '[data-testid="cards-da:21-2026"] .crow[data-key="DEMO-104"]'], { rings: 3 });
		const ringed = await page.evaluate(() => {
			const halos = [...document.querySelectorAll<HTMLElement>(".rel-halo")]
				.filter((h) => !h.hidden)
				.map((h) => h.getBoundingClientRect());
			return [...document.querySelectorAll<HTMLElement>('[data-testid^="cards-"] .crow[data-key]')]
				.filter((el) => {
					const r = el.getBoundingClientRect();
					const y = (r.top + r.bottom) / 2;
					return halos.some((q) => q.top < y && q.bottom > y && q.left < r.right && q.right > r.left);
				})
				.map((el) => el.dataset.key);
		});
		expect([...new Set(ringed)].sort()).toEqual(["DEMO-104", "DEMO-106"]);
		await expect(page.getByTestId("only-me")).toHaveAttribute("aria-pressed", "true");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/onlyme-g5-show.png` });
	});

	test("with Only me off nothing changes: every alert, the rule's own text, no why line", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		await expect.poll(() => shownHits(page)).toEqual(ALL);
		await expect(sheet(page).getByTestId("hit-mine")).toHaveCount(0);
		await expect(row(page, "D3~v/21-2026/da").locator(".tx")).toContainText("Ben Sample");
		await expect(page.getByTestId("label-da").getByTestId("label-blocker")).toContainText("Ben Sample");
	});

	test("Sources names the mine policy behind the why line", async ({ page }) => {
		await open(page, "?sheet=attention&me=1&layers=src", "subjects");
		const tag = row(page, "D3~v/21-2026/da").getByTestId("hit-mine").getByTestId("source-tag");
		await expect(tag).toHaveText("AO");
		await tag.click();
		const card = page.getByTestId("source-card");
		await expect(card).toContainText("D3: mine while your approval is pending");
		await expect(card).toContainText("ao.mine_policy");
	});
});
