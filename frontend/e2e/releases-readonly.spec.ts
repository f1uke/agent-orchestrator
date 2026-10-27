import { expect, test, type Page } from "@playwright/test";
import { NARROW, open } from "./releases-kit";

test.use({ viewport: NARROW });

const sheet = (page: Page) => page.getByRole("dialog", { name: "Release details" });

test.describe("a read-only Releases page", () => {
	test("draws the rules without their switches and the top bar without Refresh", async ({ page }) => {
		await open(page, "?sheet=rules&readonly");
		await expect(sheet(page).locator(".rule").first()).toBeVisible();
		await expect(sheet(page).getByRole("switch")).toHaveCount(0);
		await expect(sheet(page).getByTestId("rule-state").first()).toHaveText(/^(on|off)$/);
		await expect(page.getByRole("button", { name: "Refresh sources" })).toHaveCount(0);
		await expect(sheet(page).getByRole("tab", { name: "Fixes" })).toHaveCount(0);
	});

	test("draws alerts with no Hide, Restore, Fix or No MR needed control", async ({ page }) => {
		await open(page, "?sheet=attention&readonly", "fixes");
		await expect(sheet(page).locator(".al[data-hit]").first()).toBeVisible();
		await expect(sheet(page).getByRole("button", { name: /^Hide / })).toHaveCount(0);
		await expect(sheet(page).getByRole("button", { name: /Fix ›/ })).toHaveCount(0);
		await expect(sheet(page).getByTestId("hit-fix-state")).toHaveCount(0);
		await expect(sheet(page).getByTestId("no-mr-toggle")).toHaveCount(0);
		await expect(page.getByTestId("no-mr-toggle")).toHaveCount(0);
	});

	test("opens no Fixes sheet, even from a link", async ({ page }) => {
		await open(page, "?sheet=fixes&readonly", "fixes");
		await expect(sheet(page)).toHaveCount(0);
	});

	test("opens no fix sheet, even from a link", async ({ page }) => {
		await open(page, "?sheet=fix:E1~v/20-2026/di&readonly", "fixes");
		await expect(sheet(page)).toHaveCount(0);
	});

	test("shows a long-lived group's plan without its form", async ({ page }) => {
		await open(page, "?sheet=llb:demo-wallet&readonly");
		await expect(sheet(page).getByTestId("llb-status")).toBeVisible();
		await expect(sheet(page).getByRole("button", { name: "Save plan" })).toHaveCount(0);
		await expect(sheet(page).locator("#plan-note")).toHaveCount(0);
	});

	test("lists no Me in the View as picker and offers the host's own person by name", async ({ page }) => {
		await open(page, "?readonly", "subjects");
		await page.getByTestId("view-as").click();
		const options = page.getByTestId("view-as-option");
		await expect(options.first()).toBeVisible();
		for (const text of await options.allTextContents()) expect(text).not.toMatch(/\bMe\b/);
		await expect(page.locator('[data-testid="view-as-option"][data-person="ada"]')).toContainText("Ada Example");
	});

	test("words the host's own person's Mine with the name, never you", async ({ page }) => {
		await open(page, "?sheet=attention&person=ada&readonly", "subjects");
		const mine = sheet(page).getByTestId("hit-mine");
		await expect(mine.first()).toBeVisible();
		for (const line of await mine.allTextContents()) expect(line).not.toMatch(/\byou(r)?\b/i);
		await expect(sheet(page).locator('.al[data-hit="D3~v/21-2026/da"]').getByTestId("hit-mine")).toHaveText(
			"Ada Example has not approved",
		);
	});

	test("is the full page when the flag is absent", async ({ page }) => {
		await open(page, "?sheet=rules");
		await expect(sheet(page).getByRole("switch").first()).toBeVisible();
		await expect(page.getByRole("button", { name: "Refresh sources" })).toBeVisible();
		await page.getByTestId("view-as").click();
		await expect(page.locator('[data-testid="view-as-option"][data-person="ada"]')).toContainText("Me");
	});
});
