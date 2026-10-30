import { expect, type Locator, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

const QUEUED_TEXT = "Also add a CHANGELOG entry under Unreleased.";

async function openChat(page: Page) {
	await page.goto("/#/projects/ao-demo/sessions/demo-chat");
	const stream = page.getByTestId("chat-stream");
	await expect(stream).toBeVisible();
	await expect(stream.locator("[data-streaming]")).toContainText("So far 31 of 50 passed");
	return stream;
}

async function pasteImage(input: Locator) {
	await input.evaluate((el) => {
		const canvas = document.createElement("canvas");
		canvas.width = 128;
		canvas.height = 88;
		const ctx = canvas.getContext("2d")!;
		const gradient = ctx.createLinearGradient(0, 0, 128, 88);
		gradient.addColorStop(0, "#1e2a44");
		gradient.addColorStop(1, "#2c3f2f");
		ctx.fillStyle = gradient;
		ctx.fillRect(0, 0, 128, 88);
		const bytes = Uint8Array.from(atob(canvas.toDataURL("image/png").split(",")[1]), (c) => c.charCodeAt(0));
		const data = new DataTransfer();
		data.items.add(new File([bytes], "pasted-image.png", { type: "image/png" }));
		el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
	});
}

for (const viewport of VIEWPORTS) {
	test.describe(`chat interaction at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("answers a permission, two questions and a plan, queues while busy, and stops", async ({ page }, info) => {
			const shot = (name: string) => page.screenshot({ path: info.outputPath(`chat-${name}-${viewport.width}.png`) });

			const stream = await openChat(page);
			const stop = page.getByTestId("chat-pane").getByRole("button", { name: "Stop", exact: true });
			await expect(stop).toBeVisible();

			const permission = stream.locator('[data-pending="permission"]');
			await permission.scrollIntoViewIfNeeded();
			await expect(permission).toContainText("go test -race -count=1 ./...");
			await expect(permission.getByTestId("chat-always-rule")).toHaveText(
				"Always adds Bash(go test:*) to .claude/settings.local.json",
			);
			await page.evaluate(() => {
				const card = document.querySelector('[data-pending="permission"]');
				card?.scrollIntoView({ block: "start" });
			});
			await shot("permission");
			await permission.getByRole("button", { name: /Always allow/ }).click();
			await expect(stream.locator('[data-pending="permission"]')).toHaveCount(0);
			await expect(stream.locator('[data-resolved="allowed"]').last()).toContainText(
				"Always allowed Bash with Bash(go test:*)",
			);

			const question = stream.locator('[data-pending="question"]');
			await question.scrollIntoViewIfNeeded();
			await question.getByRole("radio", { name: /Full jitter/ }).click();
			await expect(question.getByRole("tab", { name: "Backoff" })).toHaveClass(/chat-qtab--done/);
			await expect(question.getByTestId("chat-option-preview")).toContainText("rand(0, min(30s, 500ms · 2^n))");
			await expect(question.getByRole("button", { name: "Submit answers" })).toBeDisabled();
			await page.evaluate(() =>
				document.querySelector('[data-pending="question"]')?.scrollIntoView({ block: "center" }),
			);
			await shot("question");
			await question.getByRole("tab", { name: "Alerts" }).click();
			await question.getByRole("checkbox", { name: /Metrics counter/ }).click();
			await question.getByRole("checkbox", { name: /Merchant email/ }).click();
			await question.getByRole("button", { name: "Submit answers" }).click();
			await expect(stream.locator('[data-pending="question"]')).toHaveCount(0);
			await expect(stream.locator('[data-resolved="allowed"]').last()).toContainText(
				"Answered: Backoff = Full jitter (Recommended) · Alerts = Metrics counter, Merchant email",
			);

			const plan = stream.locator('[data-pending="plan"]');
			await plan.scrollIntoViewIfNeeded();
			await expect(plan).toContainText("Retry budget for the webhook sender");
			await page.evaluate(() => document.querySelector('[data-pending="plan"]')?.scrollIntoView({ block: "center" }));
			await shot("plan");
			await plan.getByRole("button", { name: "Approve · auto-accept edits" }).click();
			await expect(stream.locator('[data-pending="plan"]')).toHaveCount(0);
			await expect(page.getByRole("button", { name: "Permission mode: Auto-accept edits" })).toBeVisible();

			const input = page.getByRole("textbox", { name: "Message Claude" });
			await input.fill(QUEUED_TEXT);
			await input.press("Enter");
			const queued = page.getByTestId("chat-queued");
			await expect(queued).toHaveCount(1);
			await expect(queued).toContainText(QUEUED_TEXT);
			await expect(input).toHaveValue("");

			await input.fill("Use the backoff from this sketch for the alert digest too.");
			await pasteImage(input);
			await expect(page.getByRole("img", { name: "pasted-image.png" })).toBeVisible();
			await stream.evaluate((el) => el.scrollTo(0, el.scrollHeight));
			await shot("composer");

			await stop.click();
			await expect(queued).toHaveCount(0);
			await expect(stream.locator('[data-notice="interrupted"]').last()).toBeVisible();
			await expect(stream.locator('.chat-user[data-state="started"]')).toContainText(QUEUED_TEXT);
			await expect(stop).toBeVisible();
			await stop.click();
			await expect(stop).toHaveCount(0);
			await expect(stream.locator(".chat-user").last()).toHaveAttribute("data-state", "cancelled");
		});
	});
}
