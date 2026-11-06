import { expect, type Locator, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SHOTS = process.env.AO_WIDGET_SHOTS;
const START_HEIGHT = 160;

// The desktop app renders with full Chromium, which stops rendering inside a hidden
// cross-origin frame. Playwright's default headless shell keeps rendering it.
test.use({ channel: "chromium" });

async function expectDrawn(card: Locator) {
	await expect(card).toContainText("Made a visual");
	await expect(card.locator(".chat-widget__loading")).toHaveCount(0);
	const frame = card.locator("iframe.chat-widget__frame");
	await expect(frame).toBeVisible();
	await expect(card.frameLocator("iframe.chat-widget__frame").locator("#timeline rect")).toHaveCount(7);
	await expect.poll(async () => (await frame.boundingBox())?.height ?? 0).toBeGreaterThan(START_HEIGHT + 40);
}

const stream = (page: Page) => page.getByTestId("chat-stream");

async function wheelTo(page: Page, card: Locator, deltaY: number) {
	await stream(page).hover({ position: { x: 8, y: 8 } });
	await expect(async () => {
		await page.mouse.wheel(0, deltaY);
		await expect(card).toBeInViewport({ timeout: 200 });
	}).toPass();
	await card.scrollIntoViewIfNeeded();
}

for (const viewport of VIEWPORTS) {
	test.describe(`inline visuals at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport });

		test("draw inline in a new turn, an old turn from history and after scrolling back", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({
					path: SHOTS ? `${SHOTS}/chat-widgets-render-${name}-${viewport.width}.png` : info.outputPath(`${name}.png`),
				});
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-widgets-history");
			await expect(stream(page)).toBeVisible({ timeout: 30_000 });

			const fresh = page.locator('[data-widget="h-widget-live"]');
			await expectDrawn(fresh);
			await shot("new-turn");

			const old = page.locator('[data-widget="h-widget-1"]');
			await wheelTo(page, old, -300);
			await expectDrawn(old);
			await shot("history");

			await wheelTo(page, fresh, 300);
			await wheelTo(page, old, -300);
			await expectDrawn(old);
			await shot("scrolled-back");

			await old.getByRole("button", { name: "Expand visual" }).click();
			const sheet = page.locator('[data-slot="sheet-content"]');
			await expect(sheet.frameLocator("iframe").locator("#timeline rect")).toHaveCount(7);
			await shot("expanded");
		});
	});
}
