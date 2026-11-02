import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SHOTS = process.env.AO_MARKDOWN_SHOTS;

for (const viewport of VIEWPORTS) {
	test.describe(`chat markdown at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("renders every GFM construct, scrolls a wide table inside itself, and wraps code on request", async ({
			page,
		}, info) => {
			const shot = (name: string) =>
				page.screenshot({
					path: SHOTS
						? `${SHOTS}-${name}-${viewport.width}x${viewport.height}.png`
						: info.outputPath(`chat-markdown-${name}-${viewport.width}.png`),
				});
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-markdown");
			const stream = page.getByTestId("chat-stream");
			const reply = stream.locator(".chat-md").filter({ hasText: "Retry budget review" });
			await expect(reply).toBeVisible();
			const streaming = stream.locator("[data-streaming]");
			await expect(streaming).toContainText("if err :=");
			await expect(streaming.locator(".note-prose__code-body")).toContainText("for ev := range store.Parked(ctx)");
			await expect(streaming).not.toContainText("```");
			const code = reply.locator(".note-prose__code").first();
			await reply.locator("h1").scrollIntoViewIfNeeded();
			await shot("top");
			await code.scrollIntoViewIfNeeded();
			await shot("code");
			await reply.locator(".note-prose__table-scroll").scrollIntoViewIfNeeded();
			await shot("table");
			await streaming.scrollIntoViewIfNeeded();
			await shot("streaming");

			await expect(reply.locator("h1")).toHaveText("Retry budget review");
			await expect(reply.locator("del")).toHaveText("sleeps a fixed 2s");
			await expect(reply.locator(".note-prose__checkbox")).toHaveCount(3);
			await expect(reply.locator(".note-prose__checkbox--on")).toHaveCount(2);
			await expect(reply.locator("a.note-prose__link")).toHaveCount(2);
			await expect(reply.locator(".note-prose__link--inert")).toHaveText("a script link");
			await expect(reply.locator(".note-prose__raw-html")).toContainText("<script>");

			const scroller = reply.locator(".note-prose__table-scroll");
			const table = await scroller.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
			expect(table.scroll).toBeGreaterThan(table.client);
			const pageWidth = await page.evaluate(() => ({
				scroll: document.documentElement.scrollWidth,
				client: document.documentElement.clientWidth,
			}));
			expect(pageWidth.scroll).toBeLessThanOrEqual(pageWidth.client);

			await code.scrollIntoViewIfNeeded();
			const toggle = code.getByRole("button", { name: "Wrap lines" });
			await expect(toggle).toHaveAttribute("aria-pressed", "false");
			const body = code.locator(".note-prose__code-body");
			const before = await body.evaluate((el) => el.getBoundingClientRect().height);
			await toggle.click();
			await expect(toggle).toHaveAttribute("aria-pressed", "true");
			const after = await body.evaluate((el) => el.getBoundingClientRect().height);
			expect(after).toBeGreaterThan(before);
			await shot("code-wrapped");
		});
	});
}
