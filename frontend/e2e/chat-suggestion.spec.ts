import { expect, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SESSION = "/#/projects/ao-demo/sessions/demo-chat-next";
const SUGGESTION = "what happens if a worker never calls Cancel?";

for (const viewport of VIEWPORTS) {
	test.describe(`chat prompt suggestion at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport });

		async function open(page: Page) {
			await page.goto(SESSION);
			await expect(page.getByTestId("chat-stream")).toBeVisible();
			const input = page.getByRole("textbox", { name: "Message Claude" });
			await expect(page.getByTestId("chat-suggestion")).toHaveText(`${SUGGESTION}Tab`);
			return input;
		}

		const shot = (page: Page, info: { outputPath: (name: string) => string }, name: string) =>
			page.locator(".chat-composer").screenshot({ path: info.outputPath(`${name}-${viewport.width}.png`) });

		test("shows the guess in the empty box, and Tab fills the box without sending", async ({ page }, info) => {
			const input = await open(page);
			await expect(input).toHaveValue("");
			await expect(input).toHaveAccessibleDescription(`Suggestion: ${SUGGESTION}, press Tab to use`);
			const [field, ghost] = await Promise.all([
				input.boundingBox(),
				page.getByTestId("chat-suggestion").boundingBox(),
			]);
			expect(ghost!.x).toBeGreaterThanOrEqual(field!.x);
			expect(ghost!.x + ghost!.width).toBeLessThanOrEqual(field!.x + field!.width);
			expect(ghost!.y).toBeGreaterThanOrEqual(field!.y);
			await shot(page, info, "ghost");

			const asked = await page.locator(".chat-user").count();
			await input.focus();
			await page.keyboard.press("Tab");
			await expect(input).toHaveValue(SUGGESTION);
			await expect(input).toBeFocused();
			await expect(page.getByTestId("chat-suggestion")).toHaveCount(0);
			await expect(page.locator(".chat-user")).toHaveCount(asked);
			await shot(page, info, "accepted");
		});

		test("typing replaces it, and emptying the box brings it back", async ({ page }, info) => {
			const input = await open(page);
			await input.focus();
			await page.keyboard.type("and then?");
			await expect(page.getByTestId("chat-suggestion")).toHaveCount(0);
			await expect(input).toHaveValue("and then?");
			await shot(page, info, "typed");
			await input.fill("");
			await expect(page.getByTestId("chat-suggestion")).toHaveText(`${SUGGESTION}Tab`);
		});

		test("Esc dismisses it, and Enter on the empty box sends nothing", async ({ page }) => {
			const input = await open(page);
			const asked = await page.locator(".chat-user").count();
			await input.focus();
			await page.keyboard.press("Escape");
			await expect(page.getByTestId("chat-suggestion")).toHaveCount(0);
			await page.keyboard.press("Enter");
			await expect(input).toHaveValue("");
			await expect(page.locator(".chat-user")).toHaveCount(asked);
		});

		test("sending a message clears it until claude offers another", async ({ page }) => {
			const input = await open(page);
			await input.fill("sure");
			await page.keyboard.press("Enter");
			await expect(input).toHaveValue("");
			await expect(page.getByTestId("chat-suggestion")).toHaveCount(0);
		});
	});
}
