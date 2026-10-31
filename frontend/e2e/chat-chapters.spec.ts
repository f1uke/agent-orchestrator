import { expect, type Locator, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SHOTS = process.env.AO_CHAPTER_SHOTS;
const SESSION = "/#/projects/ao-demo/sessions/demo-chat-chapters";

type Box = { x: number; y: number; width: number; height: number };

const overlaps = (a: Box, b: Box) =>
	a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;

async function boxOf(target: Locator): Promise<Box> {
	const box = await target.boundingBox();
	expect(box, "the element has a box").not.toBeNull();
	return box as Box;
}

async function open(page: Page) {
	await page.goto(SESSION);
	await expect(page.getByTestId("chat-stream")).toBeVisible();
	await expect(page.locator('[data-testid="chat-pane"] [data-connection="live"]')).toBeVisible();
}

for (const viewport of VIEWPORTS) {
	for (const scheme of ["dark", "light"] as const) {
		test.describe(`chat chapters at ${viewport.width}x${viewport.height}, ${scheme}`, () => {
			test.use({ viewport, colorScheme: scheme });

			test.beforeEach(async ({ page }) => {
				await page.addInitScript((t) => localStorage.setItem("ao.theme", t), scheme);
			});

			const shot = (page: Page, info: { outputPath: (name: string) => string }, name: string) =>
				page.screenshot({
					path: SHOTS
						? `${SHOTS}/chat-chapters-${name}-${scheme}-${viewport.width}.png`
						: info.outputPath(`${name}.png`),
				});

			test("marks chapters with dividers and a table of contents that scrolls to them", async ({ page }, info) => {
				await open(page);
				const dividers = page.locator("[data-chapter]");
				await expect(dividers.filter({ hasText: "Widget renderer" })).toBeVisible();
				await expect(dividers.filter({ hasText: "Verification" })).toBeVisible();
				await expect(dividers.filter({ hasText: "Widget renderer" })).toHaveAttribute(
					"title",
					"Frame, bridge and composer prefill",
				);
				await shot(page, info, "dividers");

				const toc = page.getByRole("navigation", { name: "Chapters" });
				const toggle = toc.locator("button[aria-expanded]");
				await expect(toc).toBeVisible();
				await expect(toggle).toHaveAttribute("aria-expanded", "false");
				await expect(toc.getByRole("list")).toHaveCount(0);
				await toggle.click();
				const items = toc.getByRole("list").getByRole("button");
				await expect(items).toHaveText(["Session start", "Codebase exploration", "Widget renderer", "Verification"]);
				const current = toc.locator('[aria-current="true"]');
				await expect(current).toHaveCount(1);
				await expect(toggle).toHaveText((await current.textContent()) ?? "");
				await shot(page, info, "toc-open");

				await items.filter({ hasText: "Codebase exploration" }).click();
				const stream = page.getByTestId("chat-stream");
				const target = dividers.filter({ hasText: "Codebase exploration" });
				await expect(target).toBeVisible();
				await expect
					.poll(async () => (await boxOf(target)).y - (await boxOf(stream)).y, {
						message: "divider glides near the top",
					})
					.toBeLessThan(80);
				await expect(toc.getByRole("list")).toHaveCount(0);
				await expect(toggle).toHaveText("Codebase exploration");

				await toggle.click();
				await expect(toc.locator('[aria-current="true"]')).toHaveText("Codebase exploration");
				await page.keyboard.press("Escape");
				await expect(toc.getByRole("list")).toHaveCount(0);
				await expect(toggle).toBeFocused();
			});

			test("keeps the table of contents clear of the turn rail and the text column", async ({ page }, info) => {
				await open(page);
				const toc = page.getByRole("navigation", { name: "Chapters" });
				const rail = page.locator(".chat-rail");
				const column = page.locator(".chat-col");
				await expect(toc).toBeVisible();
				await expect(rail).toBeVisible();

				const check = async (label: string) => {
					const [t, r, c] = [await boxOf(toc), await boxOf(rail), await boxOf(column)];
					expect(overlaps(t, r), `${label}: toc overlaps the rail`).toBe(false);
					expect(overlaps(t, c), `${label}: toc overlaps the text column`).toBe(false);
					expect(overlaps(r, c), `${label}: rail overlaps the text column`).toBe(false);
				};
				await check("collapsed");
				await toc.locator("button[aria-expanded]").click();
				await expect(toc.getByRole("list")).toBeVisible();
				await check("open");
				await shot(page, info, "gutter");
			});

			test("fills the composer from a wrap-up reply and sends nothing", async ({ page }, info) => {
				await open(page);
				const box = page.locator("[data-wrapup]");
				const toc = page.getByRole("navigation", { name: "Chapters" });
				await toc.locator("button[aria-expanded]").click();
				await toc.getByRole("list").getByRole("button", { name: "Widget renderer" }).click();
				await expect(box).toBeVisible();
				await expect(box).toContainText("Commit these changes?");
				await expect(box.getByRole("button", { name: "Yes, commit" })).toBeVisible();
				await expect(box.getByRole("button", { name: "Not yet" })).toBeVisible();
				await shot(page, info, "wrap-up");

				const composer = page.getByRole("textbox", { name: "Message Claude" });
				await expect(composer).toHaveValue("");
				const asked = await page.locator(".chat-user").count();
				await box.getByRole("button", { name: "Yes, commit" }).click();
				await expect(composer).toHaveValue("Yes, commit");
				await expect(composer).toBeFocused();
				await expect(page.locator(".chat-user")).toHaveCount(asked);
				await expect(page.locator(".chat-user", { hasText: "Yes, commit" })).toHaveCount(0);
				await shot(page, info, "wrap-up-filled");
			});
		});
	}
}
