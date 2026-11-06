import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`chat ! commands at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("shows a finished and a running run, stops the running one, then runs a typed command", async ({
			page,
		}, info) => {
			const shot = (name: string) =>
				page.screenshot({ path: info.outputPath(`chat-bash-${name}-${viewport.width}.png`) });
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-bash");
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();

			const finished = stream.locator('[data-bash="demo-bash-1"]');
			await expect(finished).toContainText("git status --short");
			await expect(finished.getByTestId("chat-bash-badge")).toHaveText("exit 0");
			await expect(finished).toContainText("ChatComposer.tsx");
			await expect(stream.getByText("<bash-input>")).toHaveCount(0);
			await expect(stream.locator('[data-notice="turn_end"]')).toHaveCount(1);

			const running = stream.locator('[data-bash="demo-bash-2"]');
			await expect(running.getByTestId("chat-bash-badge")).toHaveText("running");
			await expect(running.locator('[data-stream="stderr"]')).toHaveClass(/chat-bash__err/);
			await expect(running).toContainText("src/retry.test.ts");
			await shot("rows");

			const composer = page.getByTestId("chat-pane").locator(".chat-composer");
			await page.getByRole("textbox", { name: "Message Claude" }).fill("!");
			await expect(page.getByTestId("chat-bash-mode")).toContainText("A command is still running");
			await expect(page.getByRole("button", { name: "Run command" })).toBeDisabled();
			await page.keyboard.press("Escape");
			await expect(page.getByTestId("chat-bash-mode")).toHaveCount(0);

			await running.getByRole("button", { name: /^Stop/ }).click();
			await expect(running.getByTestId("chat-bash-badge")).toHaveText("stopped");
			await expect(running.getByRole("button", { name: /^Stop/ })).toHaveCount(0);

			await page.getByRole("textbox", { name: "Message Claude" }).pressSequentially("!");
			const mode = page.getByTestId("chat-bash-mode");
			await expect(mode).toContainText("! bash");
			await expect(mode).toContainText("Runs in the worktree as you");
			await expect(composer).toHaveAttribute("data-mode", "bash");
			const field = page.getByRole("textbox", { name: "Shell command" });
			await field.pressSequentially("echo hello");
			await shot("composer");
			await page.keyboard.press("Enter");

			const typed = stream.locator('[data-bash^="stub-bash-"]');
			await expect(typed).toContainText("echo hello");
			await expect(typed).toContainText("ran: echo hello");
			await expect(typed.getByTestId("chat-bash-badge")).toHaveText("exit 0");
			await expect(mode).toHaveCount(0);
			await expect(page.getByRole("textbox", { name: "Message Claude" })).toHaveValue("");
		});

		test("refuses to run while a turn runs and says why", async ({ page }, info) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat");
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();
			await expect(page.getByTestId("chat-pane").getByRole("button", { name: "Stop", exact: true })).toBeVisible();

			await page.getByRole("textbox", { name: "Message Claude" }).pressSequentially("!");
			await expect(page.getByTestId("chat-bash-mode")).toContainText("Claude is working");
			await page.getByRole("textbox", { name: "Shell command" }).pressSequentially("echo hello");
			await expect(page.getByRole("button", { name: "Run command" })).toBeDisabled();
			await page.keyboard.press("Enter");
			await expect(stream.locator("[data-bash]")).toHaveCount(0);
			await expect(page.getByRole("textbox", { name: "Shell command" })).toHaveValue("echo hello");
			await page.screenshot({ path: info.outputPath(`chat-bash-refused-${viewport.width}.png`) });
		});
	});
}
