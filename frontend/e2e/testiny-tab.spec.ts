import { expect, test } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts. ao-demo sets
// testinyProject MOB, and its "demo-qa-testing" crew has three linked runs
// (mockTestinyRuns). docs-site sets no Testiny project, so its tasks get no tab.
// The preview has no daemon, so linking a run posts nowhere; only the read path
// is checked here.

test("a task in a Testiny project gets the Testiny tab after Files", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	await expect(inspector.getByRole("tab")).toHaveText(["Summary", "Reviews", "Files", "Testiny", "Device", "Browser"]);
});

test("the Testiny tab lists the task's runs, open cases before the passed ones", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");

	const inspector = page.locator("#inspector");
	await inspector.getByRole("tab", { name: "Testiny" }).click();

	const runs = inspector.getByRole("article");
	await expect(runs).toHaveCount(3);
	await expect(runs.first()).toHaveAccessibleName(/^TR-632 /);

	// The failed case leads the open list, and the passed cases fold into a
	// disclosure after it.
	const run = runs.first();
	const open = run.getByRole("list", { name: "Cases" });
	await expect(open.getByRole("listitem").first()).toContainText("Failed");
	await expect(open.getByRole("listitem").first()).toContainText("Empty state shows when there is nothing to share");
	const passed = run.getByRole("button", { name: "5 passed" });
	await expect(passed).toHaveAttribute("aria-expanded", "false");
	const openBox = await open.boundingBox();
	const passedBox = await passed.boundingBox();
	expect(openBox!.y + openBox!.height).toBeLessThanOrEqual(passedBox!.y);

	await passed.click();
	await expect(run.getByRole("list", { name: "Passed cases" }).getByRole("listitem")).toHaveCount(5);
});

test("a task in a project without Testiny has no Testiny tab", async ({ page }) => {
	await page.goto("/#/projects/docs-site/sessions/docs-installation");

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Files" })).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Testiny" })).toHaveCount(0);
});
