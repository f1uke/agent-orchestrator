import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`chat Claude in Chrome at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("names each browser action, shows the screenshot as an image, and switches Chrome from the model menu", async ({
			page,
		}, info) => {
			const settled = () =>
				page.getByRole("menu").evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)));
			const shot = (name: string) =>
				page.screenshot({ path: info.outputPath(`chat-chrome-${name}-${viewport.width}.png`) });
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-chrome");
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();

			const turn = stream.locator("[data-turn] .chat-group__head").first();
			if ((await turn.count()) > 0 && (await turn.getAttribute("aria-expanded")) === "false") await turn.click();
			const group = stream.locator("[data-group] .chat-group__head").first();
			if ((await group.count()) > 0 && (await group.getAttribute("aria-expanded")) === "false") await group.click();

			const card = (action: string) => stream.locator(`[data-tool="mcp__claude-in-chrome__${action}"]`);
			await expect(card("tabs_context_mcp")).toContainText("List tabs");
			await expect(card("navigate")).toContainText("Open page");
			await expect(card("navigate")).toContainText("https://example.com/signup");
			await expect(card("find")).toContainText('"submit button"');

			const computer = card("computer");
			await expect(computer.first()).toContainText("Screenshot");
			await expect(computer.nth(1)).toContainText("Click");
			await expect(computer.nth(1)).toContainText("360, 214");
			await computer.first().locator(".chat-tool__head").click();
			const image = computer.first().getByRole("img", { name: "screenshot" });
			await expect(image).toBeVisible();
			await expect(image).toHaveAttribute("src", /^data:image\/png;base64,/);
			await shot("cards");

			await page.getByRole("button", { name: /Model and mode/ }).click();
			const item = page.getByRole("menuitemcheckbox", { name: /Claude in Chrome/ });
			await expect(item).toHaveAttribute("aria-checked", "true");
			await settled();
			await shot("menu-on");
			await item.click();

			await page.getByRole("button", { name: /Model and mode/ }).click();
			await expect(page.getByRole("menuitemcheckbox", { name: /Claude in Chrome/ })).toHaveAttribute(
				"aria-checked",
				"false",
			);
			await settled();
			await shot("menu-off");
			await page.keyboard.press("Escape");
		});
	});
}
