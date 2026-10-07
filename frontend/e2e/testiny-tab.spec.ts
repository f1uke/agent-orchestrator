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

test("a case's title opens its test data, precondition and steps under it", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");

	const inspector = page.locator("#inspector");
	await inspector.getByRole("tab", { name: "Testiny" }).click();
	const run = inspector.getByRole("article").first();

	const title = run.getByRole("button", { name: "[Share] Empty state shows when there is nothing to share" });
	await expect(title).toHaveAttribute("aria-expanded", "false");
	await title.click();
	await expect(title).toHaveAttribute("aria-expanded", "true");

	const details = run.getByRole("region", { name: "[Share] Empty state shows when there is nothing to share details" });
	await expect(details.getByRole("heading")).toHaveText([
		"Test data",
		"Precondition",
		"Steps",
		"Description",
		"Remark",
	]);
	await expect(details.getByText("qa@example.com / fake-password")).toBeVisible();
	await expect(details.getByRole("list", { name: "Precondition" }).getByRole("listitem")).toHaveCount(3);
	const steps = details.getByRole("list", { name: "Steps" }).getByRole("listitem");
	await expect(steps).toHaveCount(4);
	await expect(steps.nth(1)).toContainText("Tap Share");
	await expect(steps.nth(1)).toContainText("Expected: The share sheet opens from the bottom");

	// The panel starts on the title's own edge, under it.
	const titleBox = (await title.boundingBox())!;
	const detailsBox = (await details.boundingBox())!;
	expect(detailsBox.x).toBe(titleBox.x);
	expect(detailsBox.y).toBeGreaterThanOrEqual(titleBox.y + titleBox.height);

	await title.click();
	await expect(details).toHaveCount(0);
});

test("a case Testiny would not answer for says why and offers a retry", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");

	const inspector = page.locator("#inspector");
	await inspector.getByRole("tab", { name: "Testiny" }).click();
	const run = inspector.getByRole("article").first();

	await run.getByRole("button", { name: "[Share] VoiceOver reads the empty state" }).click();
	const details = run.getByRole("region", { name: "[Share] VoiceOver reads the empty state details" });
	await expect(details.getByRole("alert")).toContainText("TESTINY_UNAVAILABLE");
	await expect(details.getByRole("button", { name: "Retry" })).toBeVisible();
});

test("a task in a project without Testiny has no Testiny tab", async ({ page }) => {
	await page.goto("/#/projects/docs-site/sessions/docs-installation");

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Files" })).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Testiny" })).toHaveCount(0);
});
