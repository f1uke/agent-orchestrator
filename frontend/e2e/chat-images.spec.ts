import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`chat images at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("shows a Read of a PNG and a pasted image as thumbnails that open in a lightbox", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({ path: info.outputPath(`chat-images-${name}-${viewport.width}.png`) });
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-images");
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();

			const bubble = stream.locator(".chat-user");
			await expect(bubble).toContainText("The export page looks off.");
			await expect(bubble).not.toContainText(/\d images?/);
			const pasted = bubble.getByRole("img");
			await expect(pasted).toHaveCount(2);
			await expect(pasted.first()).toBeVisible();
			await expect(pasted.first()).toHaveAttribute("src", /^data:image\/png;base64,/);
			await shot("user-bubble");

			const turn = stream.locator("[data-turn] .chat-group__head").first();
			if ((await turn.count()) > 0 && (await turn.getAttribute("aria-expanded")) === "false") await turn.click();
			const group = stream.locator("[data-group] .chat-group__head").first();
			if ((await group.count()) > 0 && (await group.getAttribute("aria-expanded")) === "false") await group.click();
			const read = stream.locator('[data-tool="Read"]');
			await expect(read).toContainText("docs/export-page.png");
			await read.locator(".chat-tool__head").click();
			const thumb = read.getByRole("img", { name: "export-page.png" });
			await expect(thumb).toBeVisible();
			await expect(thumb).toHaveAttribute("src", /^data:image\/png;base64,/);
			await shot("read-card");

			await thumb.click();
			const dialog = page.getByRole("dialog");
			await expect(dialog).toBeVisible();
			await expect(dialog.getByRole("img", { name: "export-page.png" })).toBeVisible();
			await shot("lightbox");
			await page.keyboard.press("Escape");
			await expect(dialog).toHaveCount(0);

			await pasted.nth(1).click();
			await expect(dialog.getByText("2 / 2")).toBeVisible();
			await page.keyboard.press("ArrowRight");
			await expect(dialog.getByText("1 / 2")).toBeVisible();
			await page.keyboard.press("Escape");
			await expect(dialog).toHaveCount(0);
		});
	});
}
