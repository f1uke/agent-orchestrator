import { expect, test } from "@playwright/test";
import { FOLD_ANSWER_HEADING, FOLD_CLOSING, FOLD_NARRATION } from "../src/renderer/lib/chat-mock-fold";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SHOTS = process.env.AO_FOLD_SHOTS;

for (const viewport of VIEWPORTS) {
	test.describe(`chat fold around an answered question at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("keeps the answer and the answered card in view, and folds only the work", async ({ page }, info) => {
			const shot = (name: string) =>
				page.screenshot({
					path: SHOTS ? `${SHOTS}/chat-fold-answer-${name}-${viewport.width}.png` : info.outputPath(`${name}.png`),
				});
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-fold");
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();

			const turn = stream.getByRole("button", { name: /^Worked/ });
			await expect(turn).toHaveCount(1);
			await expect(turn).toHaveAttribute("aria-expanded", "false");
			await expect(stream.getByRole("heading", { name: FOLD_ANSWER_HEADING })).toBeVisible();
			await expect(stream.getByRole("table")).toBeVisible();
			await expect(stream.getByRole("table").getByRole("row")).toHaveCount(6);
			await expect(stream.getByText(/^Answered: Cache = Per-request memo/)).toBeVisible();
			await expect(stream.getByText(FOLD_CLOSING)).toBeVisible();
			await expect(stream.getByText(FOLD_NARRATION)).toHaveCount(0);
			await expect(stream.locator('[data-tool="Bash"]')).toHaveCount(0);

			const order = await Promise.all(
				[
					turn,
					stream.getByRole("heading", { name: FOLD_ANSWER_HEADING }),
					stream.getByText(/^Answered: /),
					stream.getByText(FOLD_CLOSING),
				].map(async (l) => (await l.boundingBox())?.y ?? -1),
			);
			expect(order, "fold, answer, card and closing text read top to bottom").toEqual([...order].sort((a, b) => a - b));
			await turn.scrollIntoViewIfNeeded();
			await shot("folded");

			await turn.click();
			await expect(turn).toHaveAttribute("aria-expanded", "true");
			await expect(stream.getByText(FOLD_NARRATION)).toBeVisible();
			await stream
				.getByRole("button", { name: /^Ran|^Read|^Searched/ })
				.first()
				.click();
			await expect(stream.locator('[data-tool="Bash"]')).toHaveCount(1);
			await turn.scrollIntoViewIfNeeded();
			await shot("expanded");
		});
	});
}
