import { expect, test, type Page } from "@playwright/test";
import { subjectsBoard } from "../src/renderer/lib/releases/fixture";
import { NARROW, VIEWPORTS, expectFiltered, expectSpotted, freezeRings, open, viewAs } from "./releases-kit";

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
	test.describe(`View as a person at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport });

		test("lists exactly the alerts I own or must act on, each saying why", async ({ page }) => {
			await open(page, "?sheet=attention&person=ada", "subjects");
			await expect.poll(() => shownHits(page)).toEqual(Object.keys(MINE).sort());
			for (const [hit, why] of Object.entries(MINE))
				await expect(row(page, hit).getByTestId("hit-mine")).toHaveText(why);
			await expect(page.getByTestId("attention-count")).toHaveText(String(Object.keys(MINE).length));
			if (SHOTS) await page.screenshot({ path: `${SHOTS}/onlyme-attention-${viewport.width}.png` });
		});
	});

test.describe("View as", () => {
	test("D3 says I have not approved and names nobody else", async ({ page }) => {
		await open(page, "?sheet=attention&person=ada", "subjects");
		const d3 = row(page, "D3~v/21-2026/da");
		await expect(d3.locator(".tx")).toHaveText("9 of 9 not approved");
		await expect(d3).not.toContainText("Ben Sample");
		await expect(d3).not.toContainText("Ada Example");
		const label = page.getByTestId("label-da").getByTestId("label-blocker");
		await expect(label).toContainText("9 of 9 not approved");
		await expect(label).not.toContainText("Ben Sample");
	});

	test("Show on a page family rings only my cards", async ({ page }) => {
		await open(page, "?sheet=attention&person=ada", "subjects");
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
		await expectFiltered(page);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/onlyme-g5-show.png` });
	});

	test("viewing everyone changes nothing: every alert, the rule's own text, no why line", async ({ page }) => {
		await open(page, "?sheet=attention", "subjects");
		await expect.poll(() => shownHits(page)).toEqual(ALL);
		await expect(sheet(page).getByTestId("hit-mine")).toHaveCount(0);
		await expect(row(page, "D3~v/21-2026/da").locator(".tx")).toContainText("Ben Sample");
		await expect(page.getByTestId("label-da").getByTestId("label-blocker")).toContainText("Ben Sample");
	});

	test("another person's why lines name them, never you", async ({ page }) => {
		await open(page, "?sheet=attention&person=bsample", "subjects");
		await expect(row(page, "D3~v/21-2026/da").getByTestId("hit-mine")).toHaveText("Ben Sample has not approved");
		const lines = await sheet(page).getByTestId("hit-mine").allTextContents();
		expect(lines.length).toBeGreaterThan(0);
		for (const line of lines) expect(line).not.toMatch(/\byou(r)?\b/i);
	});

	test("Sources names the mine policy behind the why line", async ({ page }) => {
		await open(page, "?sheet=attention&person=ada&layers=src", "subjects");
		const tag = row(page, "D3~v/21-2026/da").getByTestId("hit-mine").getByTestId("source-tag");
		await expect(tag).toHaveText("AO");
		await tag.click();
		const card = page.getByTestId("source-card");
		await expect(card).toContainText("D3: mine while the person's approval is pending");
		await expect(card).toContainText("ao.mine_policy");
	});
});

test.describe("the View as picker", () => {
	const option = (page: Page, person: string) =>
		page.locator(`[data-testid="view-as-option"][data-person="${person}"]`);

	test("lists Everyone, then Me, then the others with a tag where an account is missing, and searches a long list", async ({
		page,
	}) => {
		await open(page, "", "subjects");
		await expect(page.getByTestId("view-as")).toHaveText(/View as\s*Everyone/);
		await page.getByTestId("view-as").click();
		const people = await page
			.getByTestId("view-as-option")
			.evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.person ?? ""));
		expect(people.slice(0, 2)).toEqual(["", "ada"]);
		expect(people).toContain("jira:acc-dot");
		expect(people).toContain("rae");
		await expect(option(page, "ada")).toContainText("Me");
		await expect(option(page, "jira:acc-dot")).toContainText("Jira only");
		await expect(option(page, "rae")).toContainText("GitLab only");
		await expect(option(page, "bsample")).not.toContainText("only");

		const search = page.getByTestId("view-as-search");
		await search.fill("dot");
		await expect(page.getByTestId("view-as-option")).toHaveCount(1);
		await expect(option(page, "jira:acc-dot")).toBeVisible();
		await search.fill("nobody at all");
		await expect(page.getByTestId("view-as-option")).toHaveCount(0);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/viewas-menu.png` });
	});

	const openAttention = (page: Page) => page.getByRole("button", { name: /Needs attention/ }).click();

	test("choosing someone puts them in the URL and shows their alerts, not mine; Everyone clears it", async ({
		page,
	}) => {
		await open(page, "", "subjects");
		await viewAs(page, "bsample");
		await expect(page).toHaveURL(/person=bsample/);
		await expectFiltered(page);
		await expect(page.getByTestId("view-as")).toContainText("Ben Sample");
		await openAttention(page);
		await expect(row(page, "D3~v/21-2026/da")).toBeVisible();
		await expect(row(page, "C1~v/21-2026/di")).toHaveCount(0);
		await page.keyboard.press("Escape");
		await expect(sheet(page)).toHaveCount(0);

		await viewAs(page, "");
		await expect(page).not.toHaveURL(/person=/);
		await expectFiltered(page, false);
		await openAttention(page);
		await expect.poll(() => shownHits(page)).toEqual(ALL);
	});

	test("a person with only a Jira account is offered and filters by what that account does", async ({ page }) => {
		await open(page, "", "subjects");
		await viewAs(page, "jira:acc-dot");
		await expect(page).toHaveURL(/person=jira(:|%3A)acc-dot/);
		await expectFiltered(page);
		await openAttention(page);
		await expect(row(page, "D3~v/21-2026/da")).toBeVisible();
		await expect(row(page, "E1~h/da/2.3.1")).toHaveCount(0);
	});

	test("an id the Board does not list shows as is with the reason, and filters nothing", async ({ page }) => {
		await open(page, "?person=zed&sheet=attention", "subjects");
		const picker = page.getByTestId("view-as");
		await expect(picker).toHaveAttribute("data-st", "unknown");
		await expect(picker).toContainText("zed");
		await expectFiltered(page, false);
		await picker.hover();
		await expect(page.getByRole("tooltip")).toContainText("zed");
		await expect.poll(() => shownHits(page)).toEqual(ALL);
	});

	test("the old Only me link still opens the page, unfiltered", async ({ page }) => {
		await open(page, "?me=1&sheet=attention", "subjects");
		await expect(page.getByTestId("view-as")).toHaveText(/View as\s*Everyone/);
		await expectFiltered(page, false);
		await expect.poll(() => shownHits(page)).toEqual(ALL);
	});
});
