import { expect, test, type Page } from "@playwright/test";
import { halfLabels, labelOverlaps, layer, open, settle } from "./releases-kit";

// dev:web (VITE_NO_ELECTRON=1) serves lib/releases/fixture.ts: a fictional Board
// with DEMO apps, made-up people and versions. Sticky labels, clipping, pan and
// Motion need a real browser, so these checks live here and not in jsdom.

const SHOTS = process.env.AO_RELEASES_SHOTS;

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
		await expect(sheet.getByLabel("Demo Android").first()).toBeVisible();
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
		await expect(count).toHaveText("8");
		await page.getByRole("button", { name: "Hide C1~v/21-2026/di" }).click();
		await expect(count).toHaveText("7");
		await page
			.getByTestId("hidden-fold")
			.getByRole("button", { name: /hidden/ })
			.click();
		const hidden = page.locator('.al[data-hit="C1~v/21-2026/di"]');
		await hidden.getByRole("button", { name: "Restore" }).click();
		await expect(count).toHaveText("8");
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
		const report = {
			half: await halfLabels(page),
			...(await page.evaluate(() => ({
				zero: [...document.querySelectorAll<HTMLElement>(".rel-bar")].filter((b) => {
					const t = getComputedStyle(b).transform;
					return t.startsWith("matrix(0,") || b.getBoundingClientRect().width === 0;
				}).length,
				moved: document.querySelector(".rel-sc")!.scrollLeft,
			}))),
		};
		expect(report.moved).toBeGreaterThan(0);
		expect(report.half).toEqual([]);
		expect(report.zero).toBe(0);
	});

	test("the sticky app label stays opaque over the lanes when hovered, focused and under Only me", async ({ page }) => {
		await open(page, "?me=1");
		await page.getByTestId("release-timeline").evaluate((el) => (el.scrollLeft = el.scrollWidth));
		await page.waitForTimeout(200);
		const label = page.getByTestId("label-di");
		const fill = () =>
			label.evaluate((el) => {
				const parse = (c: string) => (c.match(/[\d.]+/g) ?? []).map(Number);
				const lab = el.closest(".rel-lab") as HTMLElement;
				const [, , , a = 1] = parse(getComputedStyle(lab).backgroundColor);
				const r = lab.getBoundingClientRect();
				const hit = document.elementsFromPoint(r.left + r.width - 12, r.top + r.height / 2);
				const above = hit.indexOf(lab) >= 0 && hit.slice(0, hit.indexOf(lab)).every((e) => lab.contains(e));
				return { alpha: a, opacity: getComputedStyle(lab).opacity, above };
			});
		await expect.poll(fill).toEqual({ alpha: 1, opacity: "1", above: true });
		await label.hover();
		await expect.poll(fill).toEqual({ alpha: 1, opacity: "1", above: true });
		await label.focus();
		await expect.poll(fill).toEqual({ alpha: 1, opacity: "1", above: true });
	});

	test("a failed source shows unknown with its age, never a blank", async ({ page }) => {
		await open(page, "", "failed-source");
		await expect(page.getByTestId("release-sources").locator('[data-source="gitlab"]')).toHaveText(
			/gitlab unknown · \d+[mhd]/,
		);
		await expect(page.getByTestId("release-sources").locator('[data-source="jira"]')).not.toContainText("unknown");
		await expect(page.getByTestId("attention-count")).toHaveText("8 · 1 unknown");
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
		await expect(sheet.getByTestId("card-unit").first()).toContainText("Dem · #21");
		await expect(sheet.getByTestId("card-unit").first().getByLabel("Demo iOS")).toBeVisible();
		await expect(sheet.getByRole("link", { name: "Open card" })).toHaveAttribute("target", "_blank");
	});

	test("the long-lived line unfolds to a lane per branch with wires into its trains, and folds back", async ({
		page,
	}) => {
		await open(page);
		const sum = page.locator('[data-testid="llb-summary"][data-app="di"]').locator(".ls[aria-expanded]");
		await sum.click();
		const app = page.locator('.rel-app[data-app="di"]');
		await expect(app.getByTestId("llb-lane")).toHaveCount(3);
		const wires = app.getByTestId("llb-wires");
		await expect(wires.locator("g")).toHaveCount(2);
		await settle(page);
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
		await page.locator('[data-testid="llb-summary"][data-app="di"]').locator(".ls[aria-expanded]").click();
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
		await page.locator('[data-testid="llb-summary"][data-app="di"]').locator(".ls[aria-expanded]").click();
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

	test("the sidebar has no Releases sub-entries; the page opens the long-lived list, Sprint layer and rules", async ({
		page,
	}) => {
		await open(page);
		const nav = page.locator('[data-sidebar="sidebar"]');
		await expect(nav.getByRole("button", { name: "Releases" })).toBeVisible();
		for (const name of [/^Sprint/, /^Long-lived/, /^Alert rules$/])
			await expect(nav.getByRole("button", { name })).toHaveCount(0);
		await layer(page, "Sprint cards").click();
		await expect(page.locator("[data-board]")).toHaveCount(2);
		await page.getByRole("button", { name: /Needs attention/ }).click();
		await page.getByRole("dialog", { name: "Release details" }).getByRole("tab", { name: "Rules" }).click();
		await expect(page.locator('.rule[data-rule="L9"]')).toBeVisible();
		await page.keyboard.press("Escape");
		const entry = page.getByTestId("llbs-button");
		await expect(entry).toHaveText(/Long-lived\s*2/);
		await entry.click();
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(sheet).toContainText("Long-lived branches");
		await expect(sheet).toContainText("Open 2");
		await sheet.getByRole("button", { name: /Wallet/ }).click();
		await expect(page.getByTestId("plan-form")).toBeVisible();
	});

	test("a plan marked dropped by mistake is found from the page and set back to a train", async ({ page }) => {
		await open(page);
		await page.evaluate(() => {
			type Seam = { __aoReleaseBoard?: { board: { llbs: { id: string; target: { kind: string } }[] } } };
			(globalThis as Seam).__aoReleaseBoard!.board.llbs.find((x) => x.id === "demo-chat")!.target = { kind: "dropped" };
		});
		await page.getByRole("button", { name: "Refresh sources" }).click();
		await expect(page.locator('[data-testid="llb-summary"][data-app="di"]')).toContainText("2 long-lived");
		await page.locator('[data-testid="llb-summary"][data-app="di"]').getByTestId("llbs-from-line").click();
		const sheet = page.getByRole("dialog", { name: "Release details" });
		const row = sheet.locator(".lbrow", { hasText: "demo-chat" });
		await expect(row).toContainText("dropped");
		await row.click();
		await expect(page.getByTestId("llb-status")).toContainText("Marked dropped");
		const form = page.getByTestId("plan-form");
		await form.getByLabel("Name").fill("Advisor chat");
		await form.getByRole("group", { name: "Target train" }).getByRole("button", { name: /#22/ }).click();
		await form.getByRole("button", { name: "Save plan" }).click();
		await expect(page.getByTestId("llb-status")).toContainText("Targets #22");
		await page.keyboard.press("Escape");
		await page.locator('[data-testid="llb-summary"][data-app="di"]').locator(".ls[aria-expanded]").click();
		await expect(page.locator('[data-testid="llb-lane"][data-branch="feature/demo-chat"]')).toContainText("→ #22");
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
		await expect(routes.nth(0)).toContainText("Dem · sprint branch · ships in #21");
		await expect(routes.nth(0).getByLabel("Demo iOS")).toBeVisible();
		await expect(routes.nth(1)).toContainText("MR open → sprint branch");
		await expect(routes.nth(1)).toContainText("!402");
		await expect(sheet.getByText("Subtasks (2)")).toBeVisible();
		await expect(sheet.locator('.crow[data-key="DEMO-312"]')).toContainText("In Progress");
		await expect(sheet.getByTestId("card-mine")).toHaveText("assignee");
		await sheet.getByRole("button", { name: "DEMO 20" }).click();
		await expect(sheet.getByTestId("sprint-sheet-cards")).toBeVisible();
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-sprint-sheet.png` });
	});

	test("Only me filters every kind of thing and changes the badge", async ({ page }) => {
		await open(page, "?layers=sprint,llb,cards");
		await expect(page.getByTestId("attention-count")).toHaveText("8");
		await page.getByTestId("only-me").click();
		await expect(page).toHaveURL(/me=%221%22/);
		await expect(page.getByTestId("only-me")).toHaveAttribute("aria-pressed", "true");
		await expect(page.getByTestId("attention-count")).toHaveText("3");
		await expect(page.getByTestId("label-di").getByTestId("label-mine")).toHaveText(
			"your release MR !77 · 1 of my card",
		);
		await expect(page.getByTestId("label-da").getByTestId("label-mine")).toHaveText("Nothing of mine in #21");
		await expect(page.getByTestId("bar-da-21-2026")).toHaveClass(/notmine/);
		await expect(page.getByTestId("bar-di-21-2026")).not.toHaveClass(/notmine/);
		await expect(page.getByTestId("cards-di:21-2026").locator(".csum")).toContainText("6 cards");
		await expect(page.getByTestId("cards-da:21-2026").locator(".csum")).toContainText("4 cards");
		await expect(page.locator('[data-testid="llb-summary"][data-app="di"]')).toContainText("1 long-lived of mine");
		await expect(page.getByTestId("sprint-row-DEMO").getByTestId("sprint-scope")).toHaveText("1 new · 1 carried, done");
		await expect(page.getByTestId("sprint-row-PILOT").getByTestId("sprint-scope")).toHaveText("1 new");
		await page.getByRole("button", { name: /Needs attention/ }).click();
		await expect(page.locator(".al[data-hit]")).toHaveCount(3);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-only-me.png` });
	});

	test("?me=1 leaves the train's card groups whole and puts my face on my cards", async ({ page }) => {
		await open(page, "?me=1");
		const group = page.getByTestId("cards-di:21-2026");
		await expect(group.locator(".csum")).toContainText("6 cards");
		await expect(group.locator(".csum")).not.toContainText("of mine");
		await group.locator(".csum").click();
		await expect(group.locator(".crow:not(.lbr)")).toHaveCount(6);
		await expect(group.locator(".crow.mine")).toHaveCount(1);
		const mine = group.locator('.crow[data-key="DEMO-101"]');
		await expect(mine).toHaveClass(/mine/);
		const face = mine.getByTestId("mine-face");
		await expect(face).toHaveAttribute("aria-label", "Yours: assignee");
		await expect(face.locator(".av")).toHaveAttribute("data-account", "acc-ada");
		await expect(group.locator('.crow[data-key="DEMO-102"]').getByTestId("mine-face")).toHaveCount(0);

		const wallet = page.getByTestId("cards-di:22-2026");
		await wallet.locator(".csum").click();
		await wallet.locator(".crow.lbr").click();
		await expect(wallet.locator(".nest .crow")).toHaveCount(3);
		await expect(wallet.locator('.nest .crow[data-key="DEMO-108"]').getByTestId("mine-face")).toHaveAttribute(
			"aria-label",
			"Yours: your MR !303",
		);
		await expect(wallet.locator('.nest .crow[data-key="DEMO-107"]').getByTestId("mine-face")).toHaveCount(0);

		const da = page.getByTestId("cards-da:21-2026");
		await da.locator(".csum").click();
		await expect(da.locator(".crow")).toHaveCount(4);
		await expect(da.getByTestId("mine-face")).toHaveCount(0);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-only-me-faces.png` });
	});

	test("without Only me the card groups show no face", async ({ page }) => {
		await open(page);
		const group = page.getByTestId("cards-di:21-2026");
		await group.locator(".csum").click();
		await expect(group.locator(".crow:not(.lbr)")).toHaveCount(6);
		await expect(group.getByTestId("mine-face")).toHaveCount(0);
		await expect(group.locator(".crow.mine")).toHaveCount(0);
	});

	test("an L9 hit shows in Needs attention with its MR and link", async ({ page }) => {
		await open(page, "?sheet=attention");
		const hit = page.locator('.al[data-hit="L9~mr/da/415"]');
		await expect(hit).toContainText(
			"!415 feat/DEMO-203 → feat/DEMO-201-base: not a sprint branch, a long-lived branch or develop",
		);
		await expect(hit.getByLabel("Demo Android")).toBeVisible();
		await expect(hit.getByRole("link", { name: "Open MR" })).toHaveAttribute(
			"href",
			"https://gitlab.example.com/demo/merge_requests/415",
		);
		await open(page, "?sheet=rules");
		await expect(page.locator('.rule[data-rule="L9"]')).toContainText(
			"MR into a branch that is not a sprint branch, a long-lived branch or develop",
		);
	});

	test("every place that names an app uses the family short name and a platform glyph", async ({ page }) => {
		const noLongName = async (scope: ReturnType<Page["locator"]>) => {
			const text = await scope.evaluate((el) => {
				const clone = el.cloneNode(true) as HTMLElement;
				for (const m of clone.querySelectorAll("[aria-label], [title]")) {
					m.removeAttribute("aria-label");
					m.removeAttribute("title");
				}
				return clone.innerText;
			});
			expect(text).not.toMatch(/\b(Demo|Pilot) (iOS|Android)\b/);
			expect(text).not.toMatch(/\b(di|da|pi|pa)\b/);
		};
		const marks = (scope: ReturnType<Page["locator"]>) => scope.getByTestId("app-mark");

		await open(page, "?sheet=train:21-2026");
		const apps = page.getByRole("group", { name: "Apps" });
		await expect(marks(apps)).toHaveCount(4);
		await expect(apps.locator('svg[data-platform="android"]')).toHaveCount(2);
		await noLongName(apps);
		for (const id of ["di", "da", "pi", "pa"]) {
			await expect(marks(page.getByTestId(`label-${id}`))).toHaveCount(1);
			await noLongName(page.getByTestId(`label-${id}`).locator(".nm"));
		}
		const sheet = page.getByRole("dialog", { name: "Release details" });
		await expect(marks(sheet.locator(".tapp"))).toHaveCount(4);
		const chips = sheet.getByTestId("approver-chip");
		await expect(chips.first()).toBeVisible();
		for (const chip of await chips.all()) {
			await expect(marks(chip)).toHaveCount(1);
			await noLongName(chip);
		}
		await noLongName(sheet.locator(".al .who").first());

		await open(page, "?sheet=attention");
		const who = page.locator(".rel-sheet .al .who");
		for (const w of await who.all()) await noLongName(w);
		await expect(marks(page.locator('.al[data-hit="A6~vd/21-2026/di/freeze"]'))).toHaveCount(1);

		await open(page, "?sheet=version:21-2026:da");
		await expect(marks(page.getByRole("dialog", { name: "Release details" }).locator(".sh-h"))).toHaveCount(1);
		await noLongName(page.getByRole("dialog", { name: "Release details" }).locator(".sh-h"));
	});

	test("an A6 hit opens both sources it compares, in Attention and the version sheet", async ({ page }) => {
		const expectBoth = async (scope: ReturnType<Page["locator"]>) => {
			await expect(scope.getByRole("link", { name: "Open page" })).toHaveAttribute(
				"href",
				"https://wiki.example.com/pages/21-demo",
			);
			await expect(scope.getByRole("link", { name: "Open milestone" })).toHaveAttribute(
				"href",
				"https://gitlab.example.com/demo/milestones/1",
			);
		};
		await open(page, "?sheet=attention");
		const hit = page.locator('.al[data-hit="A6~vd/21-2026/di/freeze"]');
		await expect(hit).toContainText("Page says freeze");
		await expectBoth(hit);
		await hit.getByRole("button", { name: "Show" }).click();
		await expectBoth(page.locator('.rel-sheet .al[data-hit="A6~vd/21-2026/di/freeze"]'));
	});

	test("an A1 hit's mismatch sheet opens the Jira release and the milestone", async ({ page }) => {
		await open(page, "?sheet=attention");
		await page.locator('.al[data-hit="A1~v/21-2026/pi"]').getByRole("button", { name: "Show" }).click();
		const links = page.locator(".rel-sheet .kv .links");
		await expect(links.getByRole("link", { name: "Open Jira release" })).toBeVisible();
		await expect(links.getByRole("link", { name: "Open milestone" })).toBeVisible();
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
		await expect(page.getByTestId("attention-count")).toHaveText("8");
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

test.describe("release fixes", () => {
	test.use({ viewport: { width: 1512, height: 945 } });

	const sheet = (page: Page) => page.getByRole("dialog", { name: "Release details" });
	const alertRow = (page: Page, hit: string) => page.locator(`.al[data-hit="${hit}"]`);

	test("an E1 fix shows its request, records a dry run on the hit, and is withdrawn from the Fixes list", async ({
		page,
	}) => {
		await open(page, "?sheet=attention", "fixes");
		await alertRow(page, "E1~v/20-2026/di").getByRole("button", { name: "Fix ›" }).click();
		const s = sheet(page);
		await expect(s.getByTestId("fix-title")).toHaveText("Mark di 2.3.0 released");
		await expect(s.getByTestId("fix-mode")).toContainText("Nothing is sent to Jira, GitLab or Confluence");
		await expect(s.getByTestId("fix-request")).toContainText(
			"PUT https://demo.example.com/rest/api/3/version/20-2026-di",
		);
		await expect(s.getByTestId("fix-request")).toContainText('"released": true');
		await expect(s.getByTestId("fix-step")).toContainText("dry run: nothing is sent");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-fix-e1.png` });
		await s.getByRole("button", { name: "Record as a dry run" }).click();
		await expect(s.getByTestId("fix-recorded")).toContainText("fix recorded (dry run)");
		await s.getByRole("button", { name: "Back" }).click();
		await expect(alertRow(page, "E1~v/20-2026/di").getByTestId("hit-fix-state")).toHaveText("fix recorded (dry run)");
		await alertRow(page, "E1~v/20-2026/di").getByTestId("hit-fix-state").click();
		const row = s.locator(".fixrow").first();
		await expect(row).toContainText("E1");
		await expect(row.getByTestId("fix-state")).toHaveText("fix recorded (dry run)");
		await expect(row).toContainText("by the app");
		await expect(row.getByTestId("fix-events")).toContainText("dry_run_recorded");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-fixes-list.png` });
		await row.getByRole("button", { name: "Withdraw" }).click();
		await expect(row.getByTestId("fix-state")).toHaveText("withdrawn");
		await s.getByRole("tab", { name: "Alerts" }).click();
		await expect(alertRow(page, "E1~v/20-2026/di").getByRole("button", { name: "Fix ›" })).toBeVisible();
	});

	test("A5 shows two choices, neither picked, and picking one replans", async ({ page }) => {
		await open(page, "?sheet=fix:A5~v/21-2026/da", "fixes");
		const s = sheet(page);
		await expect(s.locator(".fixpick")).toHaveCount(2);
		await expect(s.getByTestId("fix-step")).toHaveCount(0);
		await expect(s.getByRole("button", { name: "Record as a dry run" })).toHaveCount(0);
		await s.getByRole("button", { name: /^Milestone due ← Jira release date/ }).click();
		await expect(page).toHaveURL(/sheet=fix\.gitlab/);
		await expect(s.getByTestId("fix-request")).toContainText(
			"PUT https://gitlab.example.com/api/v4/projects/1/milestones/1",
		);
		await expect(s.getByRole("button", { name: /^Milestone due/ })).toHaveAttribute("aria-pressed", "true");
		await s.getByRole("button", { name: /^Jira release date ← milestone due/ }).click();
		await expect(s.getByTestId("fix-request")).toContainText("rest/api/3/version/21-2026-da");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-fix-a5.png` });
		await s.getByRole("button", { name: "Record as a dry run" }).click();
		await expect(s.getByTestId("fix-recorded")).toContainText("fix recorded (dry run)");
	});

	test("G5 copies the keys and has no confirm", async ({ page, context }) => {
		await context.grantPermissions(["clipboard-read", "clipboard-write"]);
		await open(page, "?sheet=fix:G5~pf/21-2026/demo/merged", "fixes");
		const s = sheet(page);
		await expect(s.getByTestId("fix-step")).toContainText("nothing is sent: paste it by hand");
		await expect(s.getByRole("button", { name: "Record as a dry run" })).toHaveCount(0);
		await expect(s.getByRole("button", { name: "Apply" })).toHaveCount(0);
		await s.getByRole("button", { name: "Copy keys" }).click();
		await expect(s).toContainText("Copied");
		expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("DEMO-103\nDEMO-106");
	});

	test("L3 is blocked until its group has a plan, then applies and undoes", async ({ page }) => {
		await open(page, "?sheet=fix:L3~ct/21-2026/di/DEMO-110", "fixes");
		const s = sheet(page);
		await expect(s.getByTestId("fix-blocked")).toContainText("demo-chat has no plan yet");
		await expect(s.getByTestId("fix-mode")).toHaveCount(0);
		await page.waitForTimeout(400);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-fix-l3-blocked.png` });
		await s.getByRole("button", { name: "Make a plan first ›" }).click();
		await expect(page).toHaveURL(/sheet=llb%3Ademo-chat|sheet=llb:demo-chat/);
		const form = page.getByTestId("plan-form");
		await form.getByLabel("Note").fill("Advisor chat, no train yet.");
		await form.getByRole("button", { name: "Save plan" }).click();
		await expect(form).toContainText("Saved");
		await s.getByRole("button", { name: "Back" }).click();
		await expect(s.getByTestId("fix-mode")).toContainText("Nothing is written to GitLab, Jira or Confluence");
		await expect(s.getByTestId("fix-request")).toContainText("PUT /api/v1/release-trains/llb-plans/demo-chat");
		await s.getByRole("button", { name: "Apply" }).click();
		await expect(s.getByTestId("fix-recorded")).toContainText("applied");
		await s.getByRole("button", { name: "See it in Fixes ›" }).click();
		const row = s.locator('.fixrow[data-focus="true"]');
		await expect(row.getByTestId("fix-state")).toHaveText("applied");
		await expect(row.getByTestId("fix-events")).toContainText("applied");
		await row.getByRole("button", { name: "Undo" }).click();
		await expect(row.getByTestId("fix-state")).toHaveText("undone");
		await s.getByRole("tab", { name: "Alerts" }).click();
		await expect(alertRow(page, "L3~ct/21-2026/di/DEMO-110").getByRole("button", { name: "Fix ›" })).toBeVisible();
	});

	test("an L3 undo refused because the plan moved on keeps the fix applied", async ({ page }) => {
		await open(page, "?sheet=llb:demo-chat", "fixes");
		await page.getByTestId("plan-form").getByLabel("Note").fill("Advisor chat, no train yet.");
		await page.getByTestId("plan-form").getByRole("button", { name: "Save plan" }).click();
		await expect(page.getByTestId("plan-form")).toContainText("Saved");
		await page.evaluate(() => {
			location.hash = "#/releases?sheet=fix:L3~ct/21-2026/di/DEMO-110";
		});
		const s = sheet(page);
		await s.getByRole("button", { name: "Apply" }).click();
		await expect(s.getByTestId("fix-recorded")).toContainText("applied");
		await page.evaluate(() => {
			type Seam = { __aoReleaseBoard?: { board: { llbs: { id: string; plan?: { version: number } }[] } } };
			(globalThis as Seam).__aoReleaseBoard!.board.llbs.find((g) => g.id === "demo-chat")!.plan!.version += 1;
		});
		await s.getByRole("button", { name: "See it in Fixes ›" }).click();
		const row = s.locator('.fixrow[data-focus="true"]');
		await row.getByRole("button", { name: "Undo" }).click();
		await expect(row.getByTestId("undo-refused")).toContainText("reload it and try again");
		await expect(row.getByTestId("undo-refused")).toContainText("The fix stays applied.");
		await expect(row.getByTestId("fix-state")).toHaveText("applied");
		await expect(row.getByTestId("fix-events")).toContainText("undo_failed");
	});

	test("a confirm after the data changed shows the new plan and asks for a second review", async ({ page }) => {
		await open(page, "?sheet=fix:E1~v/20-2026/di", "fixes");
		const s = sheet(page);
		await expect(s.getByTestId("fix-request")).toContainText('"releaseDate"');
		const due = await page.evaluate(() => {
			type Seam = {
				__aoReleaseBoard?: {
					board: { trains: { id: string; versions: { app: string; milestone: { due?: string } }[] }[] };
				};
			};
			const v = (globalThis as Seam).__aoReleaseBoard!.board.trains[0].versions.find((x) => x.app === "di")!;
			v.milestone.due = "2026-01-02";
			return v.milestone.due;
		});
		await s.getByRole("button", { name: "Record as a dry run" }).click();
		await expect(s.getByTestId("fix-changed")).toHaveText(
			"The data changed, review again. The plan above is the new one.",
		);
		await expect(s.getByTestId("fix-request")).toContainText(due);
		await expect(s.getByTestId("fix-recorded")).toHaveCount(0);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-fix-changed.png` });
		await s.getByRole("button", { name: "Record as a dry run" }).click();
		await expect(s.getByTestId("fix-recorded")).toContainText("fix recorded (dry run)");
	});

	test("the Key sheet explains fixes and opens the Fixes list", async ({ page }) => {
		await open(page, "?sheet=key", "fixes");
		const s = sheet(page);
		await expect(s).toContainText("Every fix except L3 is a dry run");
		await s.getByRole("button", { name: "Open the Fixes list ›" }).click();
		await expect(s.getByRole("heading", { name: "Fixes" })).toBeVisible();
		await expect(s).toContainText("No fixes yet");
	});

	test("the card sheet lists the card's alerts with their Fix button", async ({ page }) => {
		await open(page, "?sheet=card:DEMO-110", "fixes");
		const row = alertRow(page, "L3~ct/21-2026/di/DEMO-110");
		await expect(row).toBeVisible();
		await row.getByRole("button", { name: "Fix ›" }).click();
		await expect(sheet(page).getByTestId("fix-blocked")).toBeVisible();
	});

	test("the version sheet's alerts carry the Fix button", async ({ page }) => {
		await open(page, "?sheet=version:21-2026:di", "fixes");
		await expect(alertRow(page, "C1~v/21-2026/di").getByRole("button", { name: "Fix ›" })).toBeVisible();
		await expect(alertRow(page, "C5~v/21-2026/di").getByRole("button", { name: "Fix ›" })).toHaveCount(0);
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
		const chips = await page.getByRole("group", { name: "Apps" }).boundingBox();
		const lastChip = await page.getByRole("group", { name: "Apps" }).getByRole("button").last().boundingBox();
		expect(lastChip!.x + lastChip!.width, "top bar chips stay inside their group").toBeLessThanOrEqual(
			chips!.x + chips!.width + 1,
		);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-${width}.png` });
	});

	test(`at ${width}x${height} no labels overlap with every layer on and every long-lived line unfolded`, async ({
		page,
	}) => {
		await page.setViewportSize({ width, height });
		await open(page, "?layers=sprint,llb,cards,past");
		for (const s of await page.getByTestId("llb-summary").all()) await s.locator(".ls[aria-expanded]").click();
		for (const g of await page.locator(".rel-cg .csum").all()) await g.click();
		for (const d of await page.locator(".rel-cg.dest .dsum").all()) await d.click();
		await page.waitForTimeout(100);
		const report = await labelOverlaps(page);
		expect(report.n).toBeGreaterThan(10);
		expect(report.overlaps).toEqual([]);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/releases-all-${width}.png` });
	});
}
