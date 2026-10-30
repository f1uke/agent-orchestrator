import { expect, type Locator, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

async function openChat(page: Page) {
	await page.goto("/#/projects/ao-demo/sessions/demo-chat");
	const stream = page.getByTestId("chat-stream");
	await expect(stream).toBeVisible();
	await expect(stream.locator("[data-streaming]")).toContainText("So far 31 of 50 passed");
	await stream.evaluate((el) => el.scrollTo(0, el.scrollHeight));
	return page.getByRole("textbox", { name: "Message Claude" });
}

async function settled(overlay: Locator) {
	await overlay.evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)));
}

for (const viewport of VIEWPORTS) {
	test.describe(`chat pickers at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("slash menu lists commands and skills and completes one", async ({ page }, info) => {
			const input = await openChat(page);
			await input.fill("/");
			const menu = page.getByRole("listbox", { name: "Commands and skills" });
			await expect(menu.getByRole("group", { name: "Commands" })).toContainText("/compact");
			await expect(menu.getByRole("group", { name: "Skills" })).toContainText("/open-pr");
			await expect(menu.getByRole("option", { selected: true })).toContainText("/compact");
			await settled(page.locator("[data-radix-popper-content-wrapper]"));
			await page.screenshot({ path: info.outputPath(`chat-slash-${viewport.width}.png`) });

			await input.press("ArrowDown");
			await expect(menu.getByRole("option", { selected: true })).toContainText("/clear");
			await input.pressSequentially("rev");
			await input.press("Enter");
			await expect(input).toHaveValue("/review ");
			await expect(page.getByTestId("chat-arg-hint")).toHaveText("/review [pr]");
			await expect(menu).toHaveCount(0);
		});

		test("@ menu ranks workspace files and inserts the path", async ({ page }, info) => {
			const input = await openChat(page);
			await input.fill("Also cover @intern");
			const menu = page.getByRole("listbox", { name: "Files" });
			await expect(menu.getByRole("option").first()).toContainText("internal/");
			await settled(page.locator("[data-radix-popper-content-wrapper]"));
			await page.screenshot({ path: info.outputPath(`chat-mention-${viewport.width}.png`) });

			const first = await menu.getByRole("option").first().textContent();
			await input.press("Enter");
			await expect(input).toHaveValue(`Also cover @${first} `);
			await expect(menu).toHaveCount(0);
		});

		test("model picker marks the model in use and switches it", async ({ page }, info) => {
			await openChat(page);
			const picker = page.getByRole("button", { name: "Model and mode: Opus 5.5 · Ask before edits" });
			await picker.click();
			const opus = page.getByRole("menuitemradio", { name: /Opus 5\.5/ });
			await expect(opus).toHaveAttribute("aria-checked", "true");
			await expect(opus).toContainText("default");
			await expect(page.getByTestId("chat-effort-hint")).toContainText("low · medium · high · xhigh · max");
			await settled(page.getByRole("menu"));
			await page.screenshot({ path: info.outputPath(`chat-model-${viewport.width}.png`) });

			await page.getByRole("menuitemradio", { name: /Sonnet 5\.5/ }).click();
			const sonnet = page.getByRole("button", { name: "Model and mode: Sonnet 5.5 · Ask before edits" });
			await expect(sonnet).toBeVisible();

			await sonnet.click();
			await page.getByRole("menuitem", { name: "Other versions" }).hover();
			await page.getByRole("menuitemradio", { name: /Opus 4\.8/ }).click();
			await expect(page.getByRole("button", { name: "Model and mode: Opus 4.8 · Ask before edits" })).toBeVisible();
		});
	});
}
