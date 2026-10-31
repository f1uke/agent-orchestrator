import { expect, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

const STREAMED_TAIL = "So far 31 of 50 passed with no failures.";

async function openChat(page: Page) {
	await page.goto("/#/projects/ao-demo/sessions/demo-chat");
	const stream = page.getByTestId("chat-stream");
	await expect(stream).toBeVisible();
	await expect(stream.locator("[data-streaming]")).toContainText(STREAMED_TAIL);
	return stream;
}

for (const viewport of VIEWPORTS) {
	test.describe(`chat view at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("shows the stream, a diff opened by click, a pending question and the events", async ({ page }, info) => {
			const shot = (name: string) => page.screenshot({ path: info.outputPath(`chat-${name}-${viewport.width}.png`) });

			const stream = await openChat(page);
			const toggle = page.getByRole("group", { name: "Session view" });
			await expect(toggle.getByRole("button", { name: "Chat" })).toHaveAttribute("aria-pressed", "true");
			await expect(page.locator(".chat-caret")).toBeVisible();
			await expect(page.getByRole("textbox", { name: "Message Claude" })).toBeVisible();

			const question = stream.locator('[data-pending="question"]');
			await expect(question).toBeVisible();
			await expect(question).toContainText("Claude is asking · 2 questions");
			await expect(question.getByRole("button", { name: "Submit answers" })).toBeDisabled();
			await shot("bottom");

			await question.scrollIntoViewIfNeeded();
			await question.getByRole("tab", { name: "Alerts" }).click();
			await expect(question).toContainText("How should a parked event be reported?");
			await question.getByRole("tab", { name: "Backoff" }).click();
			await shot("question");

			const edit = stream.locator('[data-tool="Edit"]');
			const closedGroups = stream.locator('[data-group] .chat-group__head[aria-expanded="false"]');
			for (let i = 0; i < 40 && (await edit.count()) === 0; i++) {
				if ((await closedGroups.count()) > 0) await closedGroups.first().click();
				else await stream.evaluate((el) => el.scrollBy(0, -400));
			}
			await edit.scrollIntoViewIfNeeded();
			await expect(edit.getByRole("button", { expanded: false })).toBeVisible();
			await edit.getByRole("button").click();
			await expect(edit.getByRole("button", { expanded: true })).toBeVisible();
			await expect(edit).toContainText("sender.go");
			await expect(edit).toContainText("sleepCtx(ctx, s.budget.Delay(attempt))");
			await expect(stream.locator('[data-tool="Bash"]').first()).toContainText("Failed");
			await shot("diff");

			await stream.evaluate((el) => el.scrollTo(0, 0));
			await expect(stream.locator('[data-notice="spawn"]')).toBeVisible();
			await stream.locator("[data-turn] .chat-group__head").first().click();
			await expect(stream.locator('[data-resolved="allowed"]').first()).toContainText("Allowed Write");
			await shot("top");

			await toggle.getByRole("button", { name: "Events" }).click();
			const events = page.getByTestId("chat-events");
			await expect(events).toBeVisible();
			await expect(events.locator(".chat-ev").filter({ hasText: "control_request/can_use_tool" }).last()).toBeVisible();
			await shot("events");

			await toggle.getByRole("button", { name: "Terminal" }).click();
			await expect(page.getByTestId("chat-pane")).toHaveCount(0);
			await expect(page.locator(".terminal-toolbar").first()).toBeVisible();
		});
	});
}

test("a terminal session shows no view toggle", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-question");
	await expect(page.locator(".terminal-toolbar").first()).toBeVisible();
	await expect(page.getByRole("group", { name: "Session view" })).toHaveCount(0);
	await expect(page.getByTestId("chat-pane")).toHaveCount(0);
});
