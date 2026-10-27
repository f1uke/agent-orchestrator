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

	test("is the full page when the flag is absent", async ({ page }) => {
		await open(page, "?sheet=rules");
		await expect(sheet(page).getByRole("switch").first()).toBeVisible();
		await expect(page.getByRole("button", { name: "Refresh sources" })).toBeVisible();
	});
});
