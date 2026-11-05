import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`chat Remote Control at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("turns Remote Control on from the model menu and shows the link with a QR code", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({ path: info.outputPath(`chat-remote-${name}-${viewport.width}.png`) });
			await page.goto("/#/projects/ao-demo/sessions/demo-chat");
			await expect(page.getByTestId("chat-stream")).toBeVisible();
			await expect(page.getByRole("button", { name: /^Remote / })).toHaveCount(0);

			await page.getByRole("button", { name: /^Model and mode:/ }).click();
			const toggle = page.getByRole("menuitemcheckbox", { name: "Remote Control" });
			await expect(toggle).toHaveAttribute("aria-checked", "false");
			await shot("menu");
			await toggle.click();

			const chip = page.getByRole("button", { name: "Remote connected" });
			await expect(chip).toBeVisible();
			await chip.click();
			const pop = page.getByRole("dialog", { name: "Remote Control" });
			await expect(pop).toContainText("https://claude.ai/code/session_stub");
			await expect(pop.getByRole("img", { name: "QR code for the session link" })).toBeVisible();
			await shot("popover");
			await page.keyboard.press("Escape");

			await page.getByRole("button", { name: /^Model and mode:/ }).click();
			await expect(toggle).toHaveAttribute("aria-checked", "true");
			await toggle.click();
			await expect(page.getByRole("button", { name: /^Remote / })).toHaveCount(0);
		});
	});
}
