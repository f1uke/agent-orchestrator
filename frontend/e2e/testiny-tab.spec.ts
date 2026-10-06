import { expect, test } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts. ao-demo sets
// testinyProject MOB, and its "demo-qa-testing" crew has three linked runs
// (mockTestinyRuns). docs-site sets no Testiny project, so its tasks get no tab.
// The preview has no daemon: linking a run posts nowhere, and a result set from
// the tab is written into the mock run in memory (mockRecordTestinyResult).

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

test("setting a case to Failed asks why, then shows it as the person's result", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");

	const inspector = page.locator("#inspector");
	await inspector.getByRole("tab", { name: "Testiny" }).click();
	const run = inspector.getByRole("article").first();
	const results = run.getByRole("list", { name: "Results" });
	await expect(results.getByRole("listitem", { name: "1 Failed" })).toBeVisible();
	await expect(results.getByRole("listitem", { name: "2 Not run" })).toBeVisible();

	// qa's own result carries its reason, above its provenance.
	const failed = run.getByRole("listitem").filter({ hasText: "Empty state shows when there is nothing to share" });
	const reason = failed.getByText("เปิดหน้าแชร์ตอนไม่มีรายการ แล้วยังเห็นรายการว่างแทนข้อความแจ้ง");
	const provenance = failed.getByText("set by qa · 5 min ago · on 4f2c9e1");
	await expect(reason).toBeVisible();
	await expect(provenance).toBeVisible();
	const reasonBox = (await reason.boundingBox())!;
	expect(reasonBox.y + reasonBox.height).toBeLessThanOrEqual((await provenance.boundingBox())!.y);
	expect(reasonBox.x).toBe(
		(await failed.getByText("Empty state shows when there is nothing to share").boundingBox())!.x,
	);

	const coldLaunch = run.getByRole("listitem").filter({ hasText: "Empty state survives a cold launch" });
	await coldLaunch.getByRole("button", { name: "Result: Not run" }).click();
	await page.getByRole("menuitemradio", { name: "Failed" }).click();

	const field = coldLaunch.getByRole("textbox", { name: "What went wrong" });
	await expect(field).toBeFocused();
	await expect(coldLaunch.getByRole("button", { name: "Save" })).toBeDisabled();
	await field.fill("เปิดแอปใหม่แล้วหน้าแชร์ว่างเปล่า ไม่มีข้อความแจ้ง");
	await field.press("Enter");

	await expect(field).toHaveCount(0);
	await expect(coldLaunch.getByRole("button", { name: "Result: Failed" })).toBeVisible();
	await expect(coldLaunch.getByText("เปิดแอปใหม่แล้วหน้าแชร์ว่างเปล่า ไม่มีข้อความแจ้ง")).toBeVisible();
	await expect(coldLaunch).toContainText("set by you · just now");
	await expect(results.getByRole("listitem", { name: "2 Failed" })).toBeVisible();
	await expect(results.getByRole("listitem", { name: "1 Not run" })).toBeVisible();

	// The result lives in the preview's memory, so a refresh still shows it.
	await inspector.getByRole("button", { name: "Refresh" }).click();
	await expect(results.getByRole("listitem", { name: "2 Failed" })).toBeVisible();
	await expect(run.getByRole("listitem").filter({ hasText: "Empty state survives a cold launch" })).toContainText(
		"set by you",
	);
});

test("a task in a project without Testiny has no Testiny tab", async ({ page }) => {
	await page.goto("/#/projects/docs-site/sessions/docs-installation");

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Files" })).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Testiny" })).toHaveCount(0);
});
