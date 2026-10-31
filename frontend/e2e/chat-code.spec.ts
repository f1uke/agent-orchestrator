import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`chat code cards at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("shows a Write, an Edit and a Read with line numbers and shiki colours", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({ path: info.outputPath(`chat-code-${name}-${viewport.width}.png`) });
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-code");
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();
			const turn = stream.locator("[data-turn] .chat-group__head").first();
			if ((await turn.count()) > 0 && (await turn.getAttribute("aria-expanded")) === "false") await turn.click();

			const write = stream.locator('[data-tool="Write"]');
			await expect(write.locator(".chat-tool__head")).toContainText("Created");
			await expect(write.locator(".chat-tool__arg")).toHaveText("retry-flow.svg");
			await expect(write.locator(".chat-tool__arg")).toHaveAttribute("title", "docs/retry-flow.svg");
			await expect(write.locator(".chat-tool__add")).toHaveText("+15");
			await shot("collapsed");

			await write.locator(".chat-tool__head").click();
			await expect(write.locator(".chat-codebox__title")).toHaveText("retry-flow.svg");
			await expect(write.locator(".chat-codebox__num")).toHaveText(Array.from({ length: 15 }, (_, i) => String(i + 1)));
			await expect(write.locator(".chat-codebox--new")).toBeVisible();
			await expect(write.locator(".chat-codebox__text span[style*='color']").first()).toBeVisible();
			await shot("write");
			await write.locator(".chat-tool__head").click();

			const edit = stream.locator('[data-tool="Edit"]');
			await expect(edit.locator(".chat-tool__head")).toContainText("Edited");
			await expect(edit.locator(".chat-tool__arg")).toHaveText("report.go");
			await edit.locator(".chat-tool__head").click();
			await expect(edit.locator(".chat-codebox__num").first()).toHaveText("456");
			await expect(edit.locator(".chat-codebox__num").last()).toHaveText("467");
			await expect(edit.locator(".chat-codebox__row--del")).toHaveCount(1);
			await expect(edit.locator(".chat-codebox__row--add")).toHaveCount(6);
			await expect(edit).not.toContainText("the whole file text stays out of the fold");
			const keyword = edit.locator(".chat-codebox__text span", { hasText: /^if$/ }).first();
			await expect(keyword).toBeVisible();
			await expect(keyword).toHaveCSS("color", /^rgb\(/);
			await shot("edit");
			await edit.locator(".chat-tool__head").click();

			const read = stream.locator('[data-tool="Read"]');
			await expect(read.locator(".chat-tool__head")).toContainText("budget.ts");
			await expect(read.locator(".chat-tool__head")).toContainText("12 lines");
			await read.locator(".chat-tool__head").click();
			await expect(read.locator(".chat-codebox__num")).toHaveText(Array.from({ length: 12 }, (_, i) => String(i + 1)));
			await expect(read.locator(".chat-codebox__text", { hasText: "export function allow" })).toBeVisible();
			await expect(read.locator(".chat-codebox__text span[style*='color']").first()).toBeVisible();
			await shot("read");
		});
	});
}
