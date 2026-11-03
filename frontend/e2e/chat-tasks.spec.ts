import { expect, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SESSION = "/#/projects/ao-demo/sessions/demo-chat-tasks";

const widthOf = async (page: Page, selector: string) =>
	page.locator(selector).evaluate((el) => el.getBoundingClientRect().width);

/** The width once it stops moving: the inspector animates its size when it opens, widens and restores. */
async function settledWidth(page: Page, selector: string) {
	let last = -1;
	await expect
		.poll(async () => {
			const now = Math.round(await widthOf(page, selector));
			const stable = now === last;
			last = now;
			return stable;
		})
		.toBe(true);
	return last;
}

const noPageScroll = (page: Page) =>
	page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);

/** The agent rows sit in the finished turn's fold, inside its process group. */
async function openAgentRows(page: Page) {
	await page.locator("[data-waiting]").waitFor({ timeout: 20_000 });
	await page
		.getByRole("button", { name: /Worked for/ })
		.first()
		.click();
	await page
		.locator(".chat-group__head")
		.filter({ hasText: /^Ran a command/ })
		.click();
}

for (const viewport of VIEWPORTS) {
	test.describe(`background tasks tab at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test.beforeEach(async ({ page }) => {
			await page.addInitScript(() => window.localStorage.setItem("ao.inspector.open", "false"));
		});

		test("a Ran agent row opens the Tasks tab on that subagent, and the chat column narrows to make room", async ({
			page,
		}, info) => {
			await page.goto(SESSION);
			await openAgentRows(page);
			const before = await widthOf(page, "[data-testid=chat-pane]");
			await page.locator(".chat-agent__link", { hasText: "Audit the webhook retry paths" }).click();

			const panel = page.getByTestId("tasks-panel");
			await expect(panel.getByRole("heading", { name: "Audit the webhook retry paths" })).toBeVisible();
			await expect(page.getByRole("tab", { name: "Tasks" })).toHaveAttribute("aria-selected", "true");
			await expect(panel.getByText(/Audit the retry paths of the webhook sender/)).toBeVisible();
			await expect(panel.getByText("Model Sonnet 5")).toBeVisible();
			await expect(panel.getByText(/retries on a fixed two second sleep/)).toBeVisible();
			await expect(panel.getByRole("textbox")).toHaveCount(0);

			await expect.poll(() => widthOf(page, "[data-testid=chat-pane]")).toBeLessThan(before - 200);
			expect(await noPageScroll(page)).toBe(true);
			await page.screenshot({ path: info.outputPath(`tasks-drill-${viewport.width}.png`) });
		});

		test("back shows Running 2 and Finished 1, and Stop ends the background command", async ({ page }, info) => {
			await page.goto(SESSION);
			await openAgentRows(page);
			await page.locator(".chat-agent__link", { hasText: "Audit the webhook retry paths" }).click();
			const panel = page.getByTestId("tasks-panel");
			await panel.getByRole("button", { name: "Back to the task list" }).click();

			await expect(panel.getByRole("heading", { name: "Running 2" })).toBeVisible();
			await expect(panel.getByRole("heading", { name: "Finished 1" })).toBeVisible();
			await page.screenshot({ path: info.outputPath(`tasks-list-${viewport.width}.png`) });

			await panel.getByRole("button", { name: "Stop Run the soak test" }).click();
			await expect(panel.getByRole("heading", { name: "Running 1" })).toBeVisible();
			await expect(panel.getByRole("heading", { name: "Finished 2" })).toBeVisible();
			await expect(panel.getByRole("button", { name: "Stop Run the soak test" })).toHaveCount(0);
			await expect(panel.getByText("stopped")).toBeVisible();
			await expect(page.locator(".chat-statusline")).toContainText("1 running in background");
			expect(await noPageScroll(page)).toBe(true);
		});

		test("the status line count opens the list, and expand widens the pane, then restores it", async ({
			page,
		}, info) => {
			await page.goto(SESSION);
			await page.locator("[data-waiting]").waitFor({ timeout: 20_000 });
			await page.getByRole("button", { name: /running in background/ }).click();
			const panel = page.getByTestId("tasks-panel");
			await expect(panel.getByRole("heading", { name: "Running 2" })).toBeVisible();

			const inspector = "aside[aria-label='Session inspector']";
			const normal = await settledWidth(page, inspector);
			expect(normal).toBeGreaterThan(280);
			await panel.getByRole("button", { name: "Widen the panel" }).click();
			const wide = await settledWidth(page, inspector);
			expect(wide).toBeGreaterThan(viewport.width * 0.45);
			expect(wide).toBeGreaterThan(normal + 150);
			expect(await noPageScroll(page)).toBe(true);
			await page.screenshot({ path: info.outputPath(`tasks-wide-${viewport.width}.png`) });

			await panel.getByRole("button", { name: "Restore the panel width" }).click();
			expect(await settledWidth(page, inspector)).toBe(normal);
		});

		test("the Waiting on row opens the list instead of unfolding", async ({ page }) => {
			await page.goto(SESSION);
			await page.locator("[data-waiting] .chat-group__head").click({ timeout: 20_000 });
			await expect(page.getByTestId("tasks-panel").getByRole("heading", { name: "Running 2" })).toBeVisible();
			await expect(page.locator("[data-waiting] .chat-group__body")).toHaveCount(0);
		});
	});
}
