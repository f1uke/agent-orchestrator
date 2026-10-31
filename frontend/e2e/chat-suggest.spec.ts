import { expect, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SHOTS = process.env.AO_SUGGEST_SHOTS;
const SESSION = "/#/projects/ao-demo/sessions/demo-chat-suggest";
const RUN = "Ask Claude to run this";

for (const viewport of VIEWPORTS) {
	for (const scheme of ["dark", "light"] as const) {
		test.describe(`chat suggestions at ${viewport.width}x${viewport.height}, ${scheme}`, () => {
			test.use({ viewport, colorScheme: scheme });

			test.beforeEach(async ({ page }) => {
				await page.addInitScript((t) => localStorage.setItem("ao.theme", t), scheme);
			});

			const shot = (page: Page, info: { outputPath: (name: string) => string }, name: string) =>
				page.screenshot({
					path: SHOTS
						? `${SHOTS}/chat-suggest-${name}-${scheme}-${viewport.width}.png`
						: info.outputPath(`${name}.png`),
				});

			async function open(page: Page) {
				const spawns: string[] = [];
				page.on("request", (request) => {
					if (request.method() === "POST" && new URL(request.url()).pathname === "/api/v1/sessions") {
						spawns.push(request.url());
					}
				});
				await page.goto(SESSION);
				await expect(page.getByTestId("chat-stream")).toBeVisible();
				await expect(page.locator("[data-suggest]")).toHaveCount(2);
				return spawns;
			}

			test("offers Run only on the one-line shell block, and a click only fills the composer", async ({
				page,
			}, info) => {
				const spawns = await open(page);
				const blocks = page.locator(".note-prose__code");
				await expect(blocks).toHaveCount(3);
				const run = page.getByRole("button", { name: RUN });
				await expect(run).toHaveCount(1);
				await expect(
					blocks.filter({ hasText: "pnpm test chat-widget" }).getByRole("button", { name: RUN }),
				).toBeVisible();
				await expect(blocks.filter({ hasText: "npm run typecheck" }).getByRole("button", { name: RUN })).toHaveCount(0);
				await expect(blocks.filter({ hasText: "OWN_ROW_TOOLS" }).getByRole("button", { name: RUN })).toHaveCount(0);
				await shot(page, info, "run-button");

				const head = blocks.filter({ hasText: "pnpm test chat-widget" }).locator(".note-prose__code-head");
				const [runBox, copyBox] = await Promise.all([
					run.boundingBox(),
					head.getByRole("button", { name: "Copy code" }).boundingBox(),
				]);
				expect(runBox!.width).toBe(copyBox!.width);
				expect(runBox!.height).toBe(copyBox!.height);
				expect(runBox!.x + runBox!.width).toBeLessThanOrEqual(copyBox!.x);

				const composer = page.getByRole("textbox", { name: "Message Claude" });
				await expect(composer).toHaveValue("");
				const asked = await page.locator(".chat-user").count();
				await run.click();
				await expect(composer).toHaveValue("Run this command:\n\n```bash\npnpm test chat-widget\n```");
				await expect(composer).toBeFocused();
				await expect(page.locator(".chat-user")).toHaveCount(asked);
				await expect(page.locator('[data-state="queued"], [data-state="started"]')).toHaveCount(0);
				expect(spawns).toEqual([]);
				await shot(page, info, "run-filled");
			});

			test("draws both cards as the mock does", async ({ page }, info) => {
				await open(page);
				const session = page.locator('[data-suggest="session"]');
				const skill = page.locator('[data-suggest="skill"]');
				await expect(session).toContainText("Start a new session");
				await expect(session).toContainText("Fix the flaky Esc test");
				await expect(skill).toContainText("Skill: /verify");
				await expect(skill).toContainText("Drives the app and saves screenshots for this repo");
				const [a, b] = await Promise.all([session.boundingBox(), skill.boundingBox()]);
				expect(a!.width).toBe(b!.width);
				expect(a!.y + a!.height).toBeLessThan(b!.y);
				await session.scrollIntoViewIfNeeded();
				await shot(page, info, "cards");
			});

			test("fills the composer from the skill card and from Do it here, and sends nothing", async ({ page }, info) => {
				const spawns = await open(page);
				const composer = page.getByRole("textbox", { name: "Message Claude" });
				const asked = await page.locator(".chat-user").count();
				await page.getByRole("button", { name: "Use /verify" }).click();
				await expect(composer).toHaveValue("/verify ");
				await expect(composer).toBeFocused();
				await composer.fill("");
				await page.getByRole("button", { name: "Do it here" }).click();
				await expect(composer).toHaveValue("Do it here: Fix the flaky Esc test");
				await expect(page.locator(".chat-user")).toHaveCount(asked);
				await expect(page.locator('[data-state="queued"], [data-state="started"]')).toHaveCount(0);
				expect(spawns).toEqual([]);
				await shot(page, info, "skill-filled");
			});

			test("opens the New task form filled in, and closing it creates nothing", async ({ page }, info) => {
				const spawns = await open(page);
				const sessions = page.getByText(/^@demo-/);
				const before = await sessions.count();
				expect(before, "the sidebar lists sessions to compare against").toBeGreaterThan(5);
				await page.getByRole("button", { name: "Open new session…" }).click();
				const dialog = page.getByRole("dialog");
				await expect(dialog).toBeVisible();
				await expect.poll(() => dialog.evaluate((el) => getComputedStyle(el).opacity)).toBe("1");
				await expect(dialog.getByLabel("Title")).toHaveValue("Fix the flaky Esc test");
				await expect(dialog.getByLabel("Brief")).toHaveValue(
					"The Esc-interrupts test in ChatComposer fails about one run in five. Find the race, fix it, and show a hundred passing runs.",
				);
				await shot(page, info, "dialog");

				await dialog.getByRole("button", { name: "Cancel" }).click();
				await expect(dialog).toHaveCount(0);
				expect(spawns).toEqual([]);
				await expect(page).toHaveURL(/sessions\/demo-chat-suggest/);
				await expect(sessions).toHaveCount(before);

				await page.getByRole("button", { name: "Open new session…" }).click();
				await expect(page.getByRole("dialog").getByLabel("Title")).toHaveValue("Fix the flaky Esc test");
				await page.keyboard.press("Escape");
				await expect(page.getByRole("dialog")).toHaveCount(0);
				expect(spawns).toEqual([]);
			});
		});
	}
}
