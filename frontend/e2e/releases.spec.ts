import { expect, test, type Page } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/releases/fixture.ts: a fictional Board
// with DEMO apps, made-up people and versions. Sticky labels, clipping, pan and
// Motion need a real browser, so these checks live here and not in jsdom.

const SHOTS = process.env.AO_RELEASES_SHOTS;

async function open(page: Page, query = "", variant?: "failed-source") {
	await page.addInitScript((v) => {
		try {
			if (!sessionStorage.getItem("releases-spec")) localStorage.removeItem("ao.releases.layers.v1");
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
		await expect(page.getByTestId("llb-summary").first()).toContainText("2 long-lived");
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
}
