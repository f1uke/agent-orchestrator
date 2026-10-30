import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`chat Session tab at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("shows todos, background work, context, cost, permissions and history", async ({ page }, info) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat");
			const stream = page.getByTestId("chat-stream");
			await expect(stream.locator("[data-streaming]")).toContainText("So far 31 of 50 passed with no failures.");

			const rail = page.getByRole("complementary", { name: "Session inspector" });
			await expect(rail.getByRole("tab", { name: "Session" })).toHaveAttribute("aria-selected", "true");

			const todo = rail.getByRole("region", { name: "Todo" });
			await expect(todo).toContainText("3 / 6");
			await expect(todo.locator('[data-status="in_progress"]')).toHaveText(
				"Fix parked-attempt count and rename callers",
			);

			const activity = rail.getByRole("region", { name: "Subagents & background" });
			await expect(activity).toContainText("1 running");
			await expect(activity.locator('[data-state="running"]')).toContainText("Bash · background");
			await expect(activity.locator('[data-state="done"]')).toContainText("done · 3 tools");

			await expect(rail.getByRole("region", { name: "Context" })).toContainText("124k / 200k");
			await expect(rail.getByRole("meter", { name: "Context used" })).toHaveAttribute("aria-valuenow", "62");
			await expect(rail.getByRole("region", { name: "Cost & usage" })).toContainText("$1.84");
			await expect(rail.getByRole("region", { name: "Permissions" })).toContainText("Mode");

			const history = rail.getByRole("region", { name: "History" });
			await expect(history.getByRole("button")).toContainText("6 turns");
			await page.screenshot({ path: info.outputPath(`chat-session-tab-${viewport.width}.png`) });

			await expect(stream.locator('[data-notice="spawn"]')).toHaveCount(0);
			await history.getByRole("button").click();
			await expect(stream.locator('[data-notice="spawn"]')).toBeInViewport();
			await page.screenshot({ path: info.outputPath(`chat-session-jump-${viewport.width}.png`) });
		});
	});
}

test("a history row brings the chat back from the terminal view", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-chat");
	await page.getByRole("group", { name: "Session view" }).getByRole("button", { name: "Terminal" }).click();
	await expect(page.getByTestId("chat-pane")).toHaveCount(0);

	const rail = page.getByRole("complementary", { name: "Session inspector" });
	await rail.getByRole("region", { name: "Todo" }).waitFor();
	await rail.getByRole("region", { name: "History" }).getByRole("button").click();
	await expect(page.getByTestId("chat-stream").locator('[data-notice="spawn"]')).toBeInViewport();
});

test("a terminal session has no Session tab", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-question");
	const rail = page.getByRole("complementary", { name: "Session inspector" });
	await expect(rail.getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "true");
	await expect(rail.getByRole("tab", { name: "Session" })).toHaveCount(0);
});
