import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`background command output at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("the waiting row opens to the soak test's output, which grows while it runs", async ({ page }, info) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-live");
			const waiting = page.locator("[data-waiting]");
			await waiting.locator(".chat-live-row").click({ timeout: 20_000 });
			const output = waiting.locator("[data-task-output='live-soak']");
			await expect(output).toContainText("--- PASS: TestSenderSoak");
			const runs = async () => ((await output.textContent()) ?? "").split("--- PASS").length - 1;
			const first = await runs();
			await expect.poll(runs, { timeout: 8_000 }).toBeGreaterThan(first);
			await expect
				.poll(() => output.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight))
				.toBeLessThanOrEqual(8);
			await page.screenshot({ path: info.outputPath(`task-output-waiting-${viewport.width}.png`) });
		});

		test("the background Bash card opens to the same output", async ({ page }) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-live");
			await page.locator("[data-waiting]").waitFor({ timeout: 20_000 });
			await page
				.getByRole("button", { name: /Worked for/ })
				.first()
				.click();
			await page
				.locator(".chat-group__head")
				.filter({ hasText: /^Ran 2 commands/ })
				.click();
			const card = page.locator('[data-tool="Bash"]').filter({ hasText: "Run the soak test" });
			await card.locator(".chat-tool__head").click();
			await expect(card.locator("[data-task-output='live-soak']")).toContainText("=== RUN   TestSenderSoak");
			await expect(card).not.toContainText("Command running in background with ID");
		});
	});
}
