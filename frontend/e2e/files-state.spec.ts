import { expect, test } from "@playwright/test";

/**
 * What the Files rail remembers, measured where it can actually be measured.
 *
 * jsdom has no layout, so a virtualised tree there has no scroll: the scroller
 * measures zero, `scrollTop` never moves, and a restore that quietly does
 * nothing passes every unit test. Scroll position is the one piece of the
 * remembered arrangement that only a real browser can be asked about, and the
 * rows it has to land on only exist at a size worth windowing — so this runs
 * over the same 6,940-file harness `files-perf.spec.ts` uses.
 *
 * Run it directly:
 *   npx playwright test e2e/files-state.spec.ts --reporter=list
 */

const ROW_HEIGHT = 30;

const mount = async (page: import("@playwright/test").Page) => {
	await page.getByTestId("bench-mount").click();
	await expect(page.locator(".file-tree__row").first()).toBeVisible();
};

test.beforeEach(async ({ page }) => {
	await page.goto("/e2e/files-bench.html");
	await expect(page.getByTestId("bench-count")).toHaveText("6940");
	// The harness sets its own mode/view keys on load; only the per-task
	// arrangement is under test, and each test starts without one.
	await page.evaluate(() => window.localStorage.removeItem("ao.files.state"));
});

test("Browse opens with every folder collapsed", async ({ page }) => {
	await mount(page);
	// 10 top-level entries on this fixture and nothing below them — every row is
	// at depth 1. The rows are windowed, so this is also the whole DOM.
	await expect(page.locator(".file-tree__row")).toHaveCount(10);
	await expect(page.locator('.file-tree__row:not([aria-level="1"])')).toHaveCount(0);
	await expect(page.locator(".file-tree__row--dir").first()).toHaveAttribute("aria-expanded", "false");
});

test("a folder opened, a place scrolled to, and both still there next time", async ({ page }) => {
	await mount(page);
	// The fixture's one big module: opening it makes the tree far taller than the
	// viewport, which is the only state in which a scroll offset means anything.
	const big = page.locator(".file-tree__row--dir").first();
	const label = (await big.textContent())?.trim() ?? "";
	await big.click();
	const opened = await page.locator(".file-tree__row").count();
	expect(opened).toBeGreaterThan(10);

	// Read the offset BACK rather than assuming it: the scroller clamps to the
	// content, and the assertion is about the RESTORE, not about the number.
	const scroller = page.locator(".file-tree");
	const left = await scroller.evaluate((el) => {
		el.scrollTop = 400;
		return el.scrollTop;
	});
	expect(left).toBeGreaterThan(0);
	// The offset is written on a rest timer as well as on the way out.
	await page.waitForTimeout(700);

	await page.getByTestId("bench-unmount").click();
	await expect(page.locator(".file-tree__row")).toHaveCount(0);
	await mount(page);

	// The same folder is open...
	await expect(page.locator(".file-tree__row")).toHaveCount(opened);
	await expect(page.locator(".file-tree__row--dir").first()).toHaveText(label);
	// ...and the tree is where it was left, not back at the top.
	await expect.poll(() => scroller.evaluate((el) => el.scrollTop)).toBe(left);
	// Restoring a scroll must not mean rendering everything above it: the window
	// is still a window.
	expect(await page.locator(".file-tree__row").count()).toBeLessThan(Math.ceil(900 / ROW_HEIGHT) + 40);
});

test("a fresh task starts collapsed, however the last one was left", async ({ page }) => {
	await mount(page);
	await page.locator(".file-tree__row--dir").first().click();
	await expect.poll(() => page.locator(".file-tree__row").count()).toBeGreaterThan(10);
	await page.getByTestId("bench-unmount").click();

	// The arrangement is filed under the TASK key and nothing else — the bug this
	// guards is one memory serving two identities, or two serving one.
	const keys = await page.evaluate(() =>
		Object.keys(JSON.parse(window.localStorage.getItem("ao.files.state") ?? "{}")),
	);
	expect(keys).toEqual(["bench-task"]);
});
