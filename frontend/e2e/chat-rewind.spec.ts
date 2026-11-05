import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`chat rewind at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("rewinds from a hovered user message and draws the cut", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({
					path: process.env.AO_SHOTS
						? `${process.env.AO_SHOTS}/rewind-${name}-${viewport.width}.png`
						: info.outputPath(`rewind-${name}-${viewport.width}.png`),
				});
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-long");
			const stream = page.getByTestId("chat-stream");
			const bubbles = stream.locator(".chat-user");
			await expect(bubbles.first()).toBeVisible();
			const count = await bubbles.count();
			const target = bubbles.last();
			const text = (await target.locator(".chat-user__bubble").innerText()).trim();

			await target.hover();
			const trigger = target.getByRole("button", { name: "Rewind to here" });
			await expect(trigger).toBeVisible();
			await shot("hover");

			await trigger.click();
			const dialog = page.getByRole("dialog");
			await expect(dialog).toContainText("Rewind the conversation to here");
			await dialog.getByRole("switch", { name: /Restore files/ }).click();
			await expect(dialog).toContainText("2 files");
			await shot("popover-files");

			await dialog.getByRole("switch", { name: /Restore files/ }).click();
			await dialog.getByRole("button", { name: "Rewind" }).click();
			await expect(dialog).toBeHidden();
			await expect(stream.locator('[data-notice="rewound"]')).toHaveText("Rewound here · later messages dropped");
			await expect(bubbles).toHaveCount(count - 1);
			await expect(page.getByRole("textbox")).toHaveValue(text);
			await shot("cut");
		});
	});
}
