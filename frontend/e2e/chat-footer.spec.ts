import { expect, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

const rail = (page: Page) => page.getByRole("complementary", { name: "Session inspector" });

for (const viewport of VIEWPORTS) {
	test.describe(`chat todos, context and usage at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("puts the todos above the composer and the context and usage in its footer", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({ path: info.outputPath(`chat-footer-${name}-${viewport.width}.png`) });
			await page.goto("/#/projects/ao-demo/sessions/demo-chat");
			await expect(page.getByTestId("chat-stream").locator("[data-streaming]")).toContainText("So far 31 of 50");

			const todos = page.getByRole("button", { name: /To-dos/ });
			await expect(todos).toHaveText(/To-dos\s*3 completed · 1 in progress · 2 pending/);
			await expect(todos).toHaveAttribute("aria-expanded", "false");
			await todos.click();
			const list = page.getByRole("list", { name: "To-dos" });
			await expect(list.getByRole("listitem")).toHaveCount(6);
			await expect(list.locator('[data-status="in_progress"]')).toHaveText(
				"Fix parked-attempt count and rename callers",
			);
			await shot("todos-open");

			await page.getByRole("button", { name: /Context window/ }).click();
			const context = page.getByRole("dialog", { name: "Context window" });
			await expect(context).toContainText("62% of context used");
			await expect(context).toContainText("124k / 200k");
			await expect(context.getByRole("listitem")).toHaveCount(7);
			await shot("context-popover");
			await page.keyboard.press("Escape");

			await page.getByRole("button", { name: "Subscription usage" }).click();
			const usage = page.getByRole("dialog", { name: "Subscription usage" });
			await expect(usage).toContainText("5-hour window");
			await expect(usage).toContainText("Weekly window");
			await expect(usage).toContainText(/updated \d+ min ago/);
			await shot("usage-popover");
		});

		test("has no Session tab for a chat session, and opens on Summary", async ({ page }) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat");
			await expect(page.getByTestId("chat-stream")).toBeVisible();
			await expect(rail(page).getByRole("tab", { name: "Session" })).toHaveCount(0);
			await expect(rail(page).getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "true");
		});

		test("shows a live Thinking row while only thinking_tokens arrive", async ({ page }, info) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-thinking");
			const stream = page.getByTestId("chat-stream");
			const live = stream.locator(".chat-live-row");
			await expect(live).toHaveCount(1);
			await expect(live).toHaveText(/^Thinking \d+s$/);
			const first = await live.textContent();
			await expect(live).not.toHaveText(first ?? "", { timeout: 3000 });
			await page.screenshot({ path: info.outputPath(`chat-footer-thinking-${viewport.width}.png`) });
		});
	});
}

test("colours each usage bar by its window's status", async ({ page }) => {
	const soon = (ms: number) => new Date(Date.now() + ms).toISOString();
	const usageFor = (status: string) => ({
		fiveHour: { utilization: 0.5, resetsAt: soon(3_600_000), status },
		sevenDay: { utilization: 0.5, resetsAt: soon(86_400_000), status },
		updatedAt: new Date().toISOString(),
	});
	let status = "allowed";
	await page.route("**/hooks/useChatUsage.ts*", async (route) => {
		const res = await route.fetch();
		const body = (await res.text()).replace(
			"Promise.resolve(PREVIEW_USAGE)",
			`Promise.resolve(${JSON.stringify(usageFor(status))})`,
		);
		await route.fulfill({ response: res, body });
	});
	const fills: Record<string, string> = {};
	const labels: Record<string, string> = {};
	for (status of ["allowed", "allowed_warning", "rejected"]) {
		await page.goto("about:blank");
		await page.goto("/#/projects/ao-demo/sessions/demo-chat");
		await page.getByRole("button", { name: "Subscription usage" }).click();
		const pop = page.getByRole("dialog", { name: "Subscription usage" });
		await expect(pop.getByRole("meter")).toHaveCount(2);
		const bar = pop.getByRole("meter", { name: "5-hour window" });
		await expect(bar).toHaveAttribute("aria-valuenow", "50");
		fills[status] = await bar.locator("span").evaluate((el) => getComputedStyle(el).backgroundColor);
		labels[status] = await pop
			.locator(".chat-meter-pop__status")
			.first()
			.evaluate((el) => getComputedStyle(el).color);
	}
	expect(new Set(Object.values(fills)).size).toBe(3);
	expect(fills.allowed_warning).toBe(labels.allowed_warning);
	expect(fills.rejected).toBe(labels.rejected);
});

test("a terminal session has no Session tab either", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-question");
	await expect(rail(page).getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "true");
	await expect(rail(page).getByRole("tab", { name: "Session" })).toHaveCount(0);
});
