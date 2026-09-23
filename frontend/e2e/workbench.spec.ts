import { expect, test } from "@playwright/test";

// The Playwright web server runs `dev:web` (VITE_NO_ELECTRON=1), so
// useWorkspaceQuery serves the deterministic preview fixtures from
// lib/mock-data.ts instead of hitting a daemon. The tests run in Chromium
// (no window.ao), so the terminal shows its browser-preview surface.

const WORKER = "Resolve reviewer feedback on terminal polish";

test("renders the workbench shell", async ({ page }) => {
	await page.goto("/");
	// The Projects group, a project's dashboard + orchestrator anchors, and a worker row.
	await expect(page.getByText("Projects")).toBeVisible();
	await expect(page.getByRole("button", { name: "Open ao-demo dashboard" })).toBeVisible();
	await expect(page.getByRole("button", { name: /^Open ao-demo orchestrator/ })).toBeVisible();
	await expect(page.getByRole("button", { name: new RegExp(`^Open ${WORKER}`) })).toBeAttached();
});

test("deep-links into a worker session", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");
	// Worker view = terminal plus the inspector rail.
	await expect(page.locator("#inspector")).toBeVisible();
	await expect(page.getByRole("tab", { name: "Files" })).toBeVisible();
});

test("drilling into a worker opens its inspector rail", async ({ page }) => {
	await page.goto("/");
	// Clicked on the title, where a person clicks: the row's button is a layer
	// under it, so clicking the button element itself is refused as intercepted.
	await page
		.getByRole("button", { name: new RegExp(`^Open ${WORKER}`) })
		.locator("xpath=ancestor::li[1]")
		.getByText(WORKER)
		.click();
	await expect(page).toHaveURL(/sessions\/demo-needs-input/);
	await expect(page.locator("#inspector")).toBeVisible();
});
