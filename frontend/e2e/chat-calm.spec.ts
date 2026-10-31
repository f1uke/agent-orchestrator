import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`calm chat at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("folds the recorded long session into counted groups and turns", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({ path: info.outputPath(`chat-calm-${name}-${viewport.width}.png`) });
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-long");
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();

			await expect(stream.locator("[data-turn] .chat-group__head").first()).toHaveText(
				/^Worked for \d+(s|m \d+s) · 1 failed$/,
			);
			await expect(stream.locator("[data-group]")).toHaveCount(0);
			await expect(stream.locator('[data-tool="Bash"]')).toHaveCount(0);
			await expect(stream.getByRole("button", { name: /Thought/ })).toHaveCount(0);
			await expect(page.locator("body")).not.toContainText("local_bash");
			await expect(page.locator("body")).not.toContainText("/work/project/");
			await shot("closed");

			const first = stream.locator("[data-turn] .chat-group__head").first();
			await first.click();
			await expect(first).toHaveAttribute("aria-expanded", "true");
			const group = stream.locator("[data-group] .chat-group__head").first();
			await expect(group).toContainText(/^(Ran|Read|Edited|Started) (a|an|\d+) /);
			const bash = stream.locator('[data-tool="Bash"]');
			await expect(bash).toHaveCount(1);
			await expect(bash).toContainText("failed · exit 1");
			await group.click();
			await expect(stream.locator("[data-tool]").first()).toBeVisible();
			await shot("turn-open");
		});

		test("shows the context, todos and a folded done count in the Session tab", async ({ page }, info) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-long");
			await expect(page.getByTestId("chat-stream")).toBeVisible();

			const rail = page.getByRole("complementary", { name: "Session inspector" });
			await expect(rail.getByRole("tab", { name: "Session" })).toHaveAttribute("aria-selected", "true");

			const todo = rail.getByRole("region", { name: "Todo" });
			await expect(todo).toContainText("3 / 3");
			await expect(todo).toContainText("Find failing tests");

			const activity = rail.getByRole("region", { name: "Subagents & background" });
			const fold = activity.locator("details.chat-insp__fold");
			await expect(fold).toContainText("2 done");
			await expect(fold).not.toHaveAttribute("open", "");
			await expect(activity).not.toContainText("local_bash");
			await expect(activity).not.toContainText("local_agent");
			await expect(rail.getByRole("region", { name: "Context" })).toContainText("of the window");
			await fold.locator("summary").click();
			await expect(activity.getByText("Bash · background")).toBeVisible();
			await page.screenshot({ path: info.outputPath(`chat-calm-session-tab-${viewport.width}.png`) });
		});
	});
}
