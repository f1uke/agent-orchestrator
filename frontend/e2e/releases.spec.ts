import { expect, test, type Page } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/releases/fixture.ts: a fictional Board
// with DEMO apps, made-up people and versions. Sticky labels, clipping, pan and
// Motion need a real browser, so these checks live here and not in jsdom.

const SHOTS = process.env.AO_RELEASES_SHOTS;

async function open(page: Page, query = "", variant?: "failed-source" | "identity-unknown") {
	await page.addInitScript((v) => {
		try {
			if (!sessionStorage.getItem("releases-spec")) {
				localStorage.removeItem("ao.releases.layers.v1");
				localStorage.removeItem("ao.releases.folds.v1");
			}
			sessionStorage.setItem("releases-spec", "1");
		} catch {}
		if (v) (globalThis as { __aoReleaseBoardVariant?: string }).__aoReleaseBoardVariant = v;
	}, variant);
	await page.goto(`/#/releases${query}`);
	await expect(page.getByTestId("releases-page")).toBeVisible();
	await expect(page.getByTestId("label-di")).toBeVisible();
}

const layer = (page: Page, name: string) =>
	page.getByRole("group", { name: "Layers" }).getByRole("button", { name, exact: true });

test.describe("releases page", () => {
	test.use({ viewport: { width: 1512, height: 945 } });

	test("default layers: no sprint card rows, markers only on the focused train", async ({ page }) => {
		await open(page);
		await expect(layer(page, "Sprint cards")).toHaveAttribute("aria-pressed", "false");
		await expect(layer(page, "Long-lived")).toHaveAttribute("aria-pressed", "true");
		await expect(layer(page, "Cards")).toHaveAttribute("aria-pressed", "true");
		await expect(layer(page, "History")).toHaveAttribute("aria-pressed", "false");
		await expect(page.locator("[data-board]")).toHaveCount(0);
		const focused = page.getByTestId("bar-di-21-2026");
		await expect(focused).toHaveClass(/focus/);
		await expect(page.getByTestId("bar-di-22-2026")).toHaveClass(/quiet/);
		const app = page.locator('.rel-app[data-app="di"]');
		const markerTops = await app.locator(".rel-mk").evaluateAll((els) => els.map((e) => e.getBoundingClientRect().top));
		const barBox = await focused.boundingBox();
		expect(markerTops.length).toBe(6 - 1);
		for (const top of markerTops) expect(top).toBeGreaterThan((barBox?.y ?? 0) + (barBox?.height ?? 0));
		await expect(page.getByTestId("llb-summary").first()).toContainText("3 long-lived");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-1512.png` });
	});

	test("focusing another train moves the markers and the label to it", async ({ page }) => {
		await open(page);
		await page.getByRole("group", { name: "Train in focus" }).getByRole("button", { name: "#20" }).click();
		await expect(page).toHaveURL(/focus=20-2026/);
		await expect(page.getByTestId("bar-di-20-2026")).toHaveClass(/focus/);
		await expect(page.getByTestId("bar-di-21-2026")).toHaveClass(/quiet/);
		await expect(page.getByTestId("label-di")).toContainText("2.3.0");
		await expect(page.getByTestId("label-di").getByTestId("label-stage")).toContainText("shipped");
	});

	test("a bar opens its version sheet and Esc closes it", async ({ page }) => {
		await open(page);
		await page.getByTestId("bar-da-22-2026").click({ position: { x: 4, y: 4 } });
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet).toContainText("Demo Android");
		await expect(sheet).toContainText("2.5.0");
		await page.keyboard.press("Escape");
		await expect(sheet).toHaveCount(0);
		await expect(page).not.toHaveURL(/sheet=/);
	});

	test("a layer toggle changes the page and survives a reload", async ({ page }) => {
		await open(page);
		await layer(page, "History").click();
		await expect(layer(page, "History")).toHaveAttribute("aria-pressed", "true");
		await expect(page.getByTestId("bar-di-20-2026")).not.toHaveClass(/quiet/);
		await layer(page, "Long-lived").click();
		await expect(page.getByTestId("llb-summary")).toHaveCount(0);
		await page.reload();
		await expect(layer(page, "History")).toHaveAttribute("aria-pressed", "true");
		await expect(layer(page, "Long-lived")).toHaveAttribute("aria-pressed", "false");
	});

	test("the Key sheet explains bars, stages and status colours", async ({ page }) => {
		await open(page);
		await page.getByTestId("releases-page").getByRole("button", { name: "Key", exact: true }).click();
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet.getByRole("heading", { name: "Key" })).toBeVisible();
		await expect(sheet).toContainText("train in focus, with its stage markers");
		await expect(sheet).toContainText("Jira release approval");
		await expect(sheet).toContainText("unknown: the read failed or is stale");
	});

	test("toggling a rule turns it off and back on", async ({ page }) => {
		await open(page, "?sheet=rules");
		const rule = page.locator('.rule[data-rule="C5"]').getByRole("switch");
		await expect(rule).toHaveAttribute("aria-checked", "true");
		await rule.click();
		await expect(rule).toHaveAttribute("aria-checked", "false");
		await expect(page.locator('.rule[data-rule="C5"]')).toContainText("changed from its default");
		await rule.click();
		await expect(rule).toHaveAttribute("aria-checked", "true");
	});

	test("hiding an alert moves it to Hidden and Restore brings it back", async ({ page }) => {
		await open(page, "?sheet=attention");
		const count = page.getByTestId("attention-count");
		await expect(count).toHaveText("6");
		await page.getByRole("button", { name: "Hide C1~v/21-2026/di" }).click();
		await expect(count).toHaveText("5");
		await page
			.getByTestId("hidden-fold")
			.getByRole("button", { name: /hidden/ })
			.click();
		const hidden = page.locator('.al[data-hit="C1~v/21-2026/di"]');
		await hidden.getByRole("button", { name: "Restore" }).click();
		await expect(count).toHaveText("6");
	});

	test("panning 500 px leaves no half-visible label and no bar at scaleX(0)", async ({ page }) => {
		await open(page);
		const sc = page.getByTestId("release-timeline");
		const box = await sc.boundingBox();
		if (!box) throw new Error("no timeline");
		const y = box.y + 300;
		await page.mouse.move(box.x + 900, y);
		await page.mouse.down();
		await page.mouse.move(box.x + 650, y, { steps: 8 });
		await page.mouse.move(box.x + 400, y, { steps: 8 });
		await page.mouse.up();
		await page.waitForTimeout(400);
		const report = await page.evaluate(() => {
			const edge = document.querySelector(".rel-sc")!.getBoundingClientRect().left + 280;
			const half: string[] = [];
			for (const el of document.querySelectorAll<HTMLElement>(".rel-bar .bl, .rel-spr .sl")) {
				if (getComputedStyle(el).visibility === "hidden") continue;
				const r = el.getBoundingClientRect();
				const clip = el.parentElement!.getBoundingClientRect();
				const visibleLeft = Math.max(r.left, clip.left, edge);
				if (r.right > edge && r.left < edge - 1 && visibleLeft > r.left + 1) half.push(el.innerText.slice(0, 30));
			}
			const zero = [...document.querySelectorAll<HTMLElement>(".rel-bar")].filter((b) => {
				const t = getComputedStyle(b).transform;
				return t.startsWith("matrix(0,") || b.getBoundingClientRect().width === 0;
			}).length;
			return { half, zero, moved: document.querySelector(".rel-sc")!.scrollLeft };
		});
		expect(report.moved).toBeGreaterThan(0);
		expect(report.half).toEqual([]);
		expect(report.zero).toBe(0);
	});

	test("a failed source shows unknown with its age, never a blank", async ({ page }) => {
		await open(page, "", "failed-source");
		await expect(page.getByTestId("release-sources").locator('[data-source="gitlab"]')).toHaveText(
			/gitlab unknown · \d+[mhd]/,
		);
		await expect(page.getByTestId("release-sources").locator('[data-source="jira"]')).not.toContainText("unknown");
		await expect(page.getByTestId("attention-count")).toHaveText("6 · 1 unknown");
		await expect(page.locator('.rel-app[data-app="di"] .rel-mk[data-stage="scan"]')).toHaveAttribute(
			"data-st",
			"unknown",
		);
		await page.getByRole("button", { name: /Needs attention/ }).click();
		await expect(page.getByTestId("unknown-cell")).toContainText(
			/unknown: gitlab\.mrs unavailable, last read \d+[mhd] ago/,
		);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-failed-source.png` });
	});

	test("a card group unfolds under its train's bar, nests its long-lived branch, and folds back", async ({ page }) => {
		await open(page);
		const group = page.getByTestId("cards-di:21-2026");
		const toggle = group.locator(".csum");
		const bar = await page.getByTestId("bar-di-21-2026").boundingBox();
		const sum = await toggle.boundingBox();
		expect(sum!.y).toBeGreaterThan(bar!.y + bar!.height);
		expect(sum!.y - (bar!.y + bar!.height)).toBeLessThan(40);
		await expect(group).toContainText("6 cards");
		await expect(group).toContainText("3 merged · 1 MR open · 1 parked");
		await expect(group).toContainText("1 long-lived");
		await toggle.click();
		await expect(group.locator(".crow:not(.lbr)")).toHaveCount(6);
		await expect(group.locator(".crow.lbr")).toContainText("demo-search");
		await expect(group.locator('.crow[data-key="DEMO-101"]')).toContainText("Sign-in with a passkey");
		await group.locator(".crow.lbr").click();
		await expect(group.locator('.nest .crow[data-key="DEMO-109"]')).toContainText("Search by fund code");
		await toggle.click();
		await expect(group.locator(".crow")).toHaveCount(0);
	});

	test("a card row opens the card sheet with the trains it ships in", async ({ page }) => {
		await open(page);
		await page.getByTestId("cards-di:21-2026").locator(".csum").click();
		await page.getByTestId("cards-di:21-2026").locator('.crow[data-key="DEMO-103"] .t').click();
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet.getByTestId("card-summary")).toHaveText("Show the fee before the order");
		await expect(sheet.getByTestId("card-unit")).toHaveCount(2);
		await expect(sheet.getByTestId("card-unit").first()).toContainText("Demo iOS · #21");
		await expect(sheet.getByRole("link", { name: "Open card" })).toHaveAttribute("target", "_blank");
	});

	test("the long-lived line unfolds to a lane per branch with wires into its trains, and folds back", async ({
		page,
	}) => {
		await open(page);
		const sum = page.locator('[data-testid="llb-summary"][data-app="di"]').getByRole("button");
		await sum.click();
		const app = page.locator('.rel-app[data-app="di"]');
		await expect(app.getByTestId("llb-lane")).toHaveCount(3);
		const wires = app.getByTestId("llb-wires");
		await expect(wires.locator("g")).toHaveCount(2);
		await expect(wires.locator('g[data-wire="feature/demo-wallet"] path')).toHaveClass("plan");
		await expect(wires.locator('g[data-wire="feature/demo-search"] path')).not.toHaveClass("plan");
		const dot = await wires.locator('g[data-wire="feature/demo-wallet"] circle').boundingBox();
		const bar = await page.getByTestId("bar-di-22-2026").boundingBox();
		expect(Math.abs(dot!.y + dot!.height / 2 - bar!.y)).toBeLessThan(2);
		expect(dot!.x).toBeGreaterThan(bar!.x);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-llb-unfolded.png` });
		await sum.click();
		await expect(app.getByTestId("llb-lane")).toHaveCount(0);
		await expect(app.getByTestId("llb-wires")).toHaveCount(0);
	});

	test("the Long-lived layer off hides the line, its lanes and its wires", async ({ page }) => {
		await open(page);
		await page.locator('[data-testid="llb-summary"][data-app="di"]').getByRole("button").click();
		await expect(page.getByTestId("llb-lane").first()).toBeVisible();
		await layer(page, "Long-lived").click();
		await expect(page.getByTestId("llb-summary")).toHaveCount(0);
		await expect(page.getByTestId("llb-lane")).toHaveCount(0);
		await expect(page.getByTestId("llb-wires")).toHaveCount(0);
	});

	test("saving a plan shows its target on the long-lived line", async ({ page }) => {
		await open(page, "?sheet=llb:demo-chat");
		const form = page.getByTestId("plan-form");
		await form.getByLabel("Name").fill("Advisor chat");
		await form.getByRole("group", { name: "Target train" }).getByRole("button", { name: /#22/ }).click();
		await form.getByLabel("Note").fill("Ships with the new inbox.");
		await form.getByRole("button", { name: "Save plan" }).click();
		await expect(form).toContainText("Saved");
		await expect(form).toContainText("v1");
		await expect(page.getByTestId("llb-status")).toContainText("Targets #22");
		await page.keyboard.press("Escape");
		await page.locator('[data-testid="llb-summary"][data-app="di"]').getByRole("button").click();
		await expect(page.locator('[data-testid="llb-lane"][data-branch="feature/demo-chat"]')).toContainText("→ #22");
	});

	test("a stale plan version shows the conflict and keeps the edit", async ({ page }) => {
		await open(page, "?sheet=llb:demo-wallet");
		const form = page.getByTestId("plan-form");
		await form.getByLabel("Note").fill("my edit");
		await page.evaluate(() => {
			type Seam = { __aoReleaseBoard?: { board: { llbs: { id: string; plan?: { version: number } }[] } } };
			const b = (globalThis as Seam).__aoReleaseBoard!.board;
			b.llbs.find((g) => g.id === "demo-wallet")!.plan!.version = 5;
		});
		await form.getByRole("button", { name: "Save plan" }).click();
		await expect(page.getByTestId("plan-conflict")).toContainText("Changed elsewhere, reload");
		await expect(form.getByLabel("Note")).toHaveValue("my edit");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-plan-conflict.png` });
	});

	test("the sidebar's Long-lived entry counts open groups and opens their list", async ({ page }) => {
		await open(page);
		const entry = page.getByRole("button", { name: /^Long-lived\s*2$/ });
		await entry.click();
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet).toContainText("Long-lived branches");
		await expect(sheet).toContainText("Open 2");
		await sheet.getByRole("button", { name: /Wallet/ }).click();
		await expect(page.getByTestId("plan-form")).toBeVisible();
	});

	test("the plan form refuses a plan with no name", async ({ page }) => {
		await open(page, "?sheet=llb:demo-chat");
		const form = page.getByTestId("plan-form");
		await form.getByLabel("Name").fill("");
		await expect(page.getByTestId("plan-problems")).toContainText("Give the plan a name.");
		await expect(form.getByRole("button", { name: "Save plan" })).toBeDisabled();
	});

	test("the Sprint cards layer adds one row per board with its three-line label", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards");
		const demo = page.getByTestId("sprint-row-DEMO");
		await expect(page.locator("[data-board]")).toHaveCount(2);
		await expect(demo.locator(".rel-lab .spn")).toContainText(/DEMO 20\s*\d+ \w+ → \d+ \w+/);
		await expect(demo.getByTestId("sprint-scope")).toHaveText("4 new · 2 carried with MRs · 2 carried, done");
		await expect(demo.getByTestId("sprint-dests")).toHaveText(/<#20 1\s*#21 3\s*⑂ 1\s*off 1\s*no MR 1/);
		const label = await demo.locator(".rel-lab").boundingBox();
		expect(label!.height).toBeGreaterThanOrEqual(76);
		await expect(page.getByTestId("sprint-row-PILOT").getByTestId("sprint-scope")).toHaveText(
			"1 new · 1 carried with MRs",
		);
	});

	test("a board row unfolds into destinations at their train, wired from the sprint, with MR pills by destination", async ({
		page,
	}) => {
		await open(page, "?layers=sprint,llb,cards");
		const demo = page.getByTestId("sprint-row-DEMO");
		await demo.locator(".spsrc .csum").click();
		const blocks = demo.getByTestId("sprint-dest-block");
		await expect(blocks).toHaveCount(6);
		await expect(blocks.locator(".dsum .to")).toHaveText([
			"→ before #20",
			"→ #21",
			"⑂ demo-wallet",
			"! off the sprint flow",
			"· not routed yet",
			"↺ carried over",
		]);
		const t21 = demo.locator('[data-dest="t:21-2026"]');
		await expect(t21.locator(".dsum")).toContainText("2.4.0");
		await expect(t21.locator(".dsum")).toContainText("3 cards");
		await expect(t21.locator(".dsum")).toContainText("1 not as planned");
		const freeze = await page.getByTestId("bar-di-21-2026").boundingBox();
		const block = await t21.boundingBox();
		expect(Math.abs(block!.x - freeze!.x)).toBeLessThan(40);
		await expect(demo.locator('[data-dest="l:demo-wallet"] .dsum')).toContainText("then → #22");
		const wires = demo.getByTestId("sprint-wires");
		await expect(wires.locator("g[data-wire]")).toHaveCount(4);
		await expect(wires.getByTestId("via-wire")).toContainText("#22");

		await t21.locator(".dsum").click();
		const row = t21.locator('.crow[data-key="DEMO-301"]');
		await expect(row).toContainText("Filter the fund list by risk level");
		const glyphs = row.getByTestId("mr-glyph");
		await expect(glyphs).toHaveCount(2);
		await expect(glyphs.nth(0)).toHaveAttribute("data-kind", "sprint");
		await expect(glyphs.nth(0)).toHaveAttribute("data-st", "done");
		await expect(glyphs.nth(1)).toHaveAttribute("data-kind", "sprint_open");
		await expect(glyphs.nth(1)).toHaveAttribute("data-st", "running");
		await expect(glyphs.nth(1)).toHaveAttribute("href", /merge_requests\/402$/);
		const split = t21.locator('.crow[data-key="DEMO-302"]').getByTestId("mr-glyph");
		await expect(split.and(page.locator('[data-app="di"]'))).toHaveClass(/away/);
		await expect(t21.locator('.crow[data-key="DEMO-305"]')).toContainText("page says #22");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-sprint-unfolded.png` });
		await demo.locator(".spsrc .csum").click();
		await expect(demo.getByTestId("sprint-dest-block")).toHaveCount(0);
	});

	test("a board label opens the sprint sheet, and a card in it opens the full card sheet", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards");
		await page.getByTestId("sprint-row-DEMO").locator(".rel-lab").click();
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet.getByRole("button", { name: "Train #21" })).toBeVisible();
		await expect(sheet.getByTestId("sprint-sheet-cards")).toContainText(
			"4 new this sprint, 2 carried over with MRs or open mobile work, 2 carried over with mobile done.",
		);
		await expect(sheet.getByTestId("sprint-dest")).toHaveCount(6);
		await sheet.locator('.crow[data-key="DEMO-301"]').click();
		await expect(sheet.getByTestId("card-summary")).toHaveText("Filter the fund list by risk level");
		const routes = sheet.getByTestId("card-route");
		await expect(routes).toHaveCount(2);
		await expect(routes.nth(0)).toContainText("Demo iOS · sprint branch · ships in #21");
		await expect(routes.nth(1)).toContainText("MR open → sprint branch");
		await expect(routes.nth(1)).toContainText("!402");
		await expect(sheet.getByText("Subtasks (2)")).toBeVisible();
		await expect(sheet.locator('.crow[data-key="DEMO-312"]')).toContainText("In Progress");
		await expect(sheet.getByTestId("card-mine")).toHaveText("assignee");
		await sheet.getByRole("button", { name: "DEMO 20" }).click();
		await expect(sheet.getByTestId("sprint-sheet-cards")).toBeVisible();
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-sprint-sheet.png` });
	});

	test("the sidebar's Sprint entry turns the layer on and unfolds every board", async ({ page }) => {
		await open(page);
		await expect(page.locator("[data-board]")).toHaveCount(0);
		await page.getByRole("button", { name: /^Sprint\s*8$/ }).click();
		await expect(layer(page, "Sprint cards")).toHaveAttribute("aria-pressed", "true");
		await expect(page.locator("[data-board]")).toHaveCount(2);
		await expect(page.getByTestId("sprint-row-DEMO").getByTestId("sprint-dest-block")).toHaveCount(6);
		await expect(page.getByTestId("sprint-row-PILOT").getByTestId("sprint-dest-block")).toHaveCount(1);
	});

	test("Only me filters every kind of thing and changes the badge", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards");
		await expect(page.getByTestId("attention-count")).toHaveText("6");
		await page.getByTestId("only-me").click();
		await expect(page).toHaveURL(/me=%221%22/);
		await expect(page.getByTestId("only-me")).toHaveAttribute("aria-pressed", "true");
		await expect(page.getByTestId("attention-count")).toHaveText("2");
		await expect(page.getByTestId("label-di").getByTestId("label-mine")).toHaveText(
			"your release MR !77 · 1 of my card",
		);
		await expect(page.getByTestId("label-da").getByTestId("label-mine")).toHaveText("Nothing of mine in #21");
		await expect(page.getByTestId("bar-da-21-2026")).toHaveClass(/notmine/);
		await expect(page.getByTestId("bar-di-21-2026")).not.toHaveClass(/notmine/);
		await expect(page.getByTestId("cards-di:21-2026")).toContainText("1 card of mine");
		await expect(page.getByTestId("cards-da:21-2026")).toHaveCount(0);
		await expect(page.locator('[data-testid="llb-summary"][data-app="di"]')).toContainText("1 long-lived of mine");
		await expect(page.getByTestId("sprint-row-DEMO").getByTestId("sprint-scope")).toHaveText("1 new · 1 carried, done");
		await expect(page.getByTestId("sprint-row-PILOT").getByTestId("sprint-scope")).toHaveText("1 new");
		await page.getByRole("button", { name: /Needs attention/ }).click();
		await expect(page.locator(".al[data-hit]")).toHaveCount(2);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-only-me.png` });
	});

	test("with the identity unknown Only me is disabled with the reason and ?me=1 filters nothing", async ({ page }) => {
		await open(page, "?me=1", "identity-unknown");
		const toggle = page.getByTestId("only-me");
		await expect(toggle).toBeDisabled();
		await expect(toggle).toHaveAttribute("aria-pressed", "false");
		await toggle.hover({ force: true });
		await expect(page.getByRole("tooltip")).toContainText(
			"Only me needs your Jira and GitLab accounts: gitlab.user has not been read yet",
		);
		await expect(page.getByTestId("attention-count")).toHaveText("6");
		await expect(page.getByTestId("label-di").getByTestId("label-mine")).toHaveCount(0);
	});

	test("with no config the page says how to make one", async ({ page }) => {
		await page.addInitScript(() => {
			(globalThis as { __aoReleaseBoard?: unknown }).__aoReleaseBoard = {
				config: { state: "absent", path: "~/.ao/data/release-train.json" },
			};
		});
		await page.goto("/#/releases");
		await expect(page.getByTestId("releases-page")).toContainText("ao train config init");
	});
});

for (const [width, height] of [
	[1280, 800],
	[1512, 945],
] as const) {
	test(`at ${width}x${height} every app label's stage and blocker sit inside the viewport`, async ({ page }) => {
		await page.setViewportSize({ width, height });
		await open(page);
		for (const app of ["di", "da", "pi", "pa"]) {
			const label = page.getByTestId(`label-${app}`);
			for (const part of [label.getByTestId("label-stage"), label.getByTestId("label-blocker")]) {
				const b = await part.boundingBox();
				expect(b, `${app} part`).not.toBeNull();
				expect(b!.y + b!.height, `${app} bottom`).toBeLessThanOrEqual(height);
				expect(b!.x + b!.width).toBeLessThanOrEqual(width);
			}
		}
		await expect(page.getByRole("button", { name: /Needs attention/ })).toBeInViewport({ ratio: 1 });
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-${width}.png` });
	});

	test(`at ${width}x${height} no labels overlap with every layer on and every long-lived line unfolded`, async ({
		page,
	}) => {
		await page.setViewportSize({ width, height });
		await open(page, "?layers=sprint,llb,cards,past");
		for (const s of await page.getByTestId("llb-summary").all()) await s.getByRole("button").click();
		for (const g of await page.locator(".rel-cg .csum").all()) await g.click();
		for (const d of await page.locator(".rel-cg.dest .dsum").all()) await d.click();
		await page.waitForTimeout(100);
		const report = await page.evaluate(() => {
			const vw = innerWidth;
			const vh = innerHeight;
			const seen = (e: Element) => {
				const r = e.getBoundingClientRect();
				const h = document.elementFromPoint(
					Math.min(vw - 1, Math.max(0, r.left + r.width / 2)),
					Math.min(vh - 1, Math.max(0, r.top + r.height / 2)),
				);
				return !!h && (h === e || e.contains(h) || h.contains(e));
			};
			const els = [
				...document.querySelectorAll(".bl, .sl, .lb-lbl, .csum, .ls, .rel-lyr, .dsum, .rel-sprow .crow"),
			].filter((e) => {
				const r = e.getBoundingClientRect();
				const visible = r.width > 0 && r.height > 0 && r.right > 0 && r.left < vw && r.top < vh && r.bottom > 0;
				return visible && getComputedStyle(e).visibility !== "hidden" && !e.closest(".tight") && seen(e);
			});
			const overlaps: string[] = [];
			for (let i = 0; i < els.length; i++)
				for (let j = i + 1; j < els.length; j++) {
					const [a, b] = [els[i], els[j]];
					if (a.contains(b) || b.contains(a)) continue;
					const r = a.getBoundingClientRect();
					const q = b.getBoundingClientRect();
					const ix = Math.min(r.right, q.right) - Math.max(r.left, q.left);
					const iy = Math.min(r.bottom, q.bottom) - Math.max(r.top, q.top);
					if (ix > 2 && iy > 2)
						overlaps.push(`${a.textContent?.trim().slice(0, 24)} x ${b.textContent?.trim().slice(0, 24)}`);
				}
			return { n: els.length, overlaps };
		});
		expect(report.n).toBeGreaterThan(10);
		expect(report.overlaps).toEqual([]);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-all-${width}.png` });
	});
}
