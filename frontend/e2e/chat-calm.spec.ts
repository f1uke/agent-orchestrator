import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`calm chat at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("folds the recorded long session into counted groups and turns", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({ path: info.outputPath(`chat-calm-${name}-${viewport.width}.png`) });
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-long");
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();

			await expect(stream.locator("[data-turn] .chat-group__head").first()).toHaveText(
				/^Worked for \d+(s|m \d+s)$/,
			);
			await expect(stream.locator("[data-group]")).toHaveCount(0);
			await expect(stream.locator('[data-tool="Bash"]')).toHaveCount(0);
			await expect(stream.getByRole("button", { name: /Thought/ })).toHaveCount(0);
			await expect(page.locator("body")).not.toContainText("local_bash");
			await expect(page.locator("body")).not.toContainText("/work/project/");
			await shot("closed");

			const first = stream.locator("[data-turn] .chat-group__head").first();
			await first.click();
			await expect(first).toHaveAttribute("aria-expanded", "true");
			const group = stream.locator("[data-group] .chat-group__head").first();
			await expect(group).toContainText(/^(Ran|Read|Edited|Started) (a|an|\d+) /);
			await expect(group).not.toContainText("failed");
			const bash = stream.locator('[data-tool="Bash"]');
			await expect(bash, "a closed group hides its failed call").toHaveCount(0);
			await group.click();
			await expect(stream.locator("[data-tool]").first()).toBeVisible();
			await expect(bash.filter({ hasText: "failed · exit 1" })).toHaveCount(1);
			await shot("turn-open");

			const nested = stream.locator("[data-turn] .chat-group__body").first();
			expect(await nested.evaluate((el) => getComputedStyle(el).borderLeftWidth), "one rule per open turn").toBe("0px");
			const tool = stream.locator("[data-turn] [data-group] .chat-tool").first();
			const boxed = () => tool.evaluate((el) => getComputedStyle(el).borderTopColor !== "rgba(0, 0, 0, 0)");
			expect(await boxed(), "a closed tool in an open group is a plain row").toBe(false);
			await tool.locator(".chat-tool__head").click();
			await expect(tool.locator(".chat-tool__head")).toHaveAttribute("aria-expanded", "true");
			expect(await boxed(), "the tool you open gets its box").toBe(true);
			await shot("tool-open");
		});
	});
}
