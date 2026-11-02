import { expect, test } from "@playwright/test";

const SHOTS = process.env.AO_TURN_ERROR_SHOTS;

test.describe("chat turn errors at 1920x1080", () => {
	test.use({ viewport: { width: 1920, height: 1080 }, colorScheme: "dark" });

	test("names each failed turn in plain words and folds the raw gateway error", async ({ page }, info) => {
		await page.goto("/#/projects/ao-demo/sessions/demo-chat-errors");
		const ends = page.getByTestId("chat-stream").locator('[data-notice="turn_end"]');
		await expect(ends).toHaveCount(3);
		await expect(ends.nth(0)).toHaveText(/^Rate limited - resets in 1m 45s/);
		await expect(ends.nth(1)).toHaveText(/^Spend limit reached - resets 10:30pm/);
		await expect(ends.nth(2)).toHaveText(/^Interrupted/);
		await expect(page.locator('[data-notice="interrupted"]')).toHaveCount(0);
		await expect(page.getByTestId("chat-stream")).not.toContainText("success");

		const gateway = page.locator("[data-turn-error]").first();
		await expect(gateway.locator(".chat-turn-error__detail")).toHaveText(
			"This request would exceed the rate limit for this demo account.",
		);
		const raw = gateway.locator("pre");
		await expect(raw).toBeHidden();
		await gateway.getByText("Raw error").click();
		await expect(raw).toContainText('"rate_limit_error"');
		await page.screenshot({ path: SHOTS ? `${SHOTS}-1920x1080.png` : info.outputPath("chat-turn-error-1920.png") });
	});
});
