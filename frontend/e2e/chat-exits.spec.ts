import { expect, test } from "@playwright/test";

const SHOTS = process.env.AO_EXITS_SHOTS;

for (const viewport of [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
]) {
	const size = `${viewport.width}x${viewport.height}`;

	test.describe(`chat command exits at ${size}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("shows no failure on the closed turn, and only real failures in red once opened", async ({ page }, info) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-exits");
			const turn = page.locator("[data-turn] .chat-group__head").first();
			await expect(turn).toHaveText(/^Worked for \d+s$/);
			await page.screenshot({ path: SHOTS ? `${SHOTS}-folded-${size}.png` : info.outputPath(`folded-${size}.png`) });

			await turn.click();
			const body = page.locator("[data-turn] .chat-turn__body").first();
			await expect(body.locator("[data-tool]")).toHaveCount(7);
			await expect(body.getByText("no match · exit 1")).toHaveCount(2);
			await expect(body.getByText("differs · exit 1")).toHaveCount(1);
			await expect(body.getByText("false · exit 1")).toHaveCount(1);
			await expect(body.locator(".chat-tool__status--error")).toHaveText(["Failed", "Failed"]);
			await expect(body.locator("[data-tool]:has(.chat-tool__status--error)")).toContainText([
				"Run the retry tests",
				"retryctl --dry-run",
			]);

			const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
			expect(scrollWidth).toBeLessThanOrEqual(viewport.width);
			await page.screenshot({ path: SHOTS ? `${SHOTS}-open-${size}.png` : info.outputPath(`open-${size}.png`) });
		});
	});
}
