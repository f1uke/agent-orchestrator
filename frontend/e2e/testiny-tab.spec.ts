import { expect, test } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts. ao-demo sets
// testinyProject MOB, and its "demo-qa-testing" crew has three linked runs
// (mockTestinyRuns) and the Jira issue DEMO-150. docs-site sets no Testiny
// project, so its tasks get no tab.
// The preview has no daemon: linking a run posts nowhere, and a result set from
// the tab is written into the mock run in memory (mockRecordTestinyResult), as
// is an evidence upload's Drive links (mockUploadTestinyEvidence).

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
		"Requirements",
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

test("a step's result shows on its line, and setting one saves it at once", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");

	const inspector = page.locator("#inspector");
	await inspector.getByRole("tab", { name: "Testiny" }).click();
	const run = inspector.getByRole("article").first();
	const name = "[Share] Empty state shows when there is nothing to share";
	await run.getByRole("button", { name }).click();
	const steps = run.getByRole("region", { name: `${name} details` }).getByRole("list", { name: "Steps" });
	const step = (n: number) => steps.getByRole("listitem").nth(n - 1);

	await expect(step(3).getByRole("button", { name: "Step 3 result: Failed" })).toBeVisible();
	await expect(step(4).getByRole("button", { name: "Step 4 result: Not run" })).toBeVisible();

	// The result sits on the step's first line, at the row's right edge.
	const action = (await step(2).getByText("Tap Share", { exact: true }).boundingBox())!;
	const menu = step(2).getByRole("button", { name: "Step 2 result: Passed" });
	const menuBox = (await menu.boundingBox())!;
	expect(Math.abs(menuBox.y + menuBox.height / 2 - (action.y + action.height / 2))).toBeLessThan(2);
	expect(menuBox.x).toBeGreaterThan(action.x + action.width);

	await menu.click();
	await page.getByRole("menuitemradio", { name: "Failed" }).click();
	await expect(step(2).getByRole("button", { name: "Step 2 result: Failed" })).toBeVisible();
	await expect(step(2).getByRole("textbox")).toHaveCount(0);
	// The case keeps its own status.
	await expect(run.getByRole("button", { name: "Result: Failed" }).first()).toBeVisible();

	// Written into the preview's run, so a fresh read still has it.
	await inspector.getByRole("button", { name: "Refresh" }).click();
	await expect(step(2).getByRole("button", { name: "Step 2 result: Failed" })).toBeVisible();
	await expect(step(1).getByRole("button", { name: "Step 1 result: Passed" })).toBeVisible();
});

test("a case lists the Jira issues it is linked to, and says when the task's is not one yet", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing-qa");

	const inspector = page.locator("#inspector");
	await inspector.getByRole("tab", { name: "Testiny" }).click();
	const run = inspector.getByRole("article").first();

	const linkedTitle = "[Share] Empty state shows when there is nothing to share";
	await run.getByRole("button", { name: linkedTitle }).click();
	const linked = run.getByRole("region", { name: `${linkedTitle} details` });
	const requirements = linked.getByRole("list", { name: "Requirements" }).getByRole("listitem");
	await expect(requirements).toHaveText([
		"DEMO-150Tighten the share sheet's empty state · In QA",
		"UX-42Empty state copy and illustration · Done",
	]);
	// Each summary starts on one edge, past the widest key, as the facts above do.
	const summaryX = async (n: number) =>
		(await requirements
			.nth(n)
			.getByText(/^(Tighten|Empty)/)
			.boundingBox())!.x;
	expect(await summaryX(0)).toBe(await summaryX(1));
	await expect(linked.getByText(/Not linked to/)).toHaveCount(0);

	const unlinkedTitle = "[Share] Empty state survives a cold launch";
	await run.getByRole("button", { name: unlinkedTitle }).click();
	const unlinked = run.getByRole("region", { name: `${unlinkedTitle} details` });
	await expect(unlinked.getByText("Not linked to DEMO-150 yet")).toBeVisible();
	await expect(unlinked.getByRole("list", { name: "Requirements" })).toHaveCount(0);
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

test("a case's Drive evidence sits under it as quiet links, cut before the extension", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");

	const inspector = page.locator("#inspector");
	await inspector.getByRole("tab", { name: "Testiny" }).click();
	const android = inspector.getByRole("article", { name: /^TR-633 / });
	await android.getByRole("button", { name: "All 4 passed" }).click();

	const opens = android.getByRole("listitem").filter({ hasText: "[Share] Sheet opens from the fund page" });
	const links = opens.getByRole("list", { name: "Evidence" }).getByRole("link");
	await expect(links).toHaveText(["pass.png", "pass - Pixel 8 Pro Android 15 large text and dark mode.mp4"]);
	await expect(links.first()).toHaveAttribute("target", "_blank");
	await expect(links.first()).toHaveAttribute("href", /^https:\/\/drive\.google\.com\/file\/d\//);

	// A name too long for the rail is cut before its extension, inside the card,
	// and keeps the whole of it as the title.
	const long = links.nth(1);
	await expect(long).toHaveAttribute("title", "TC-7201 pass - Pixel 8 Pro Android 15 large text and dark mode.mp4");
	expect(await long.locator(".truncate").evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
	const ext = (await long.getByText(".mp4", { exact: true }).boundingBox())!;
	const card = (await android.boundingBox())!;
	expect(ext.width).toBeGreaterThan(0);
	expect(ext.x + ext.width).toBeLessThanOrEqual(card.x + card.width);

	const pasted = android.getByRole("listitem").filter({ hasText: "Empty state shows when there is nothing to share" });
	await expect(pasted.getByRole("list", { name: "Evidence" }).getByRole("link")).toHaveText([
		"pass.png",
		"Drive file 1",
		"Drive file 2",
	]);
});

test("uploading a run's evidence links each file on its case, and a second upload has nothing to do", async ({
	page,
}) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");

	const inspector = page.locator("#inspector");
	await inspector.getByRole("tab", { name: "Testiny" }).click();
	const upload = (run: RegExp) =>
		inspector.getByRole("article", { name: run }).getByRole("button", { name: "Upload to Drive" });
	// TR-633 is closed and TR-640 has no evidence folder.
	await expect(upload(/^TR-633 /)).toBeDisabled();
	await expect(upload(/^TR-640 /)).toBeDisabled();

	const ios = inspector.getByRole("article", { name: /^TR-632 / });
	const failed = ios.getByRole("listitem").filter({ hasText: "Empty state shows when there is nothing to share" });
	await expect(failed.getByRole("list", { name: "Evidence" }).getByRole("link")).toHaveText(["Drive file"]);

	await upload(/^TR-632 /).click();
	await expect(ios.getByRole("status")).toHaveText("Uploaded 3 files, linked 2 cases");
	await expect(failed.getByRole("list", { name: "Evidence" }).getByRole("link")).toHaveText([
		"Drive file",
		"FAIL DEMO-151.png",
		"FAIL DEMO-151 - iPhone 15 Pro Max iOS 18.4 dark mode.mov",
	]);

	await upload(/^TR-632 /).click();
	await expect(ios.getByRole("status")).toHaveText("Already up to date");
	await expect(failed.getByRole("list", { name: "Evidence" }).getByRole("link")).toHaveCount(3);
});

test("a task in a project without Testiny has no Testiny tab", async ({ page }) => {
	await page.goto("/#/projects/docs-site/sessions/docs-installation");

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Files" })).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Testiny" })).toHaveCount(0);
});
