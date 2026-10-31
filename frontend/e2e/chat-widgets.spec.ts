import { expect, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SHOTS = process.env.AO_WIDGET_SHOTS;

for (const viewport of VIEWPORTS) {
	for (const scheme of ["dark", "light"] as const) {
		test.describe(`chat visuals at ${viewport.width}x${viewport.height}, ${scheme}`, () => {
			test.use({ viewport, colorScheme: scheme });

			test.beforeEach(async ({ page }) => {
				await page.addInitScript((t) => localStorage.setItem("ao.theme", t), scheme);
			});

			test("draws, runs and talks back from inside its sandbox", async ({ page }, info) => {
				const shot = (name: string) =>
					page.screenshot({
						path: SHOTS
							? `${SHOTS}/chat-widgets-${name}-${scheme}-${viewport.width}.png`
							: info.outputPath(`${name}.png`),
					});
				await page.goto("/#/projects/ao-demo/sessions/demo-chat-widgets");
				await expect(page.getByTestId("chat-stream")).toBeVisible();

				const card = page.locator('[data-widget="w-calc"]');
				const flow = page.locator('[data-widget="w-flow"]');
				await expect(card).toBeVisible();
				await expect(card).toContainText("Made a visual");
				await expect(card).toContainText("dca_calculator");
				await expect(page.locator('[data-turn] .chat-group__head[title*="ead visual guidelines"]')).toBeVisible();

				const frameEl = card.locator("iframe.chat-widget__frame");
				await expect(frameEl).toHaveAttribute("sandbox", "allow-scripts");
				const frame = card.frameLocator("iframe.chat-widget__frame");
				const invested = frame.locator("#invested");
				await expect(invested).toHaveText("600,000");
				const box = await frameEl.boundingBox();
				expect(box!.height).toBeGreaterThan(300);
				expect(box!.height).toBeLessThan(900);

				await frame.locator("#years").fill("20");
				await expect(frame.locator("#years-out")).toHaveText("20");
				await expect(invested).toHaveText("1,200,000");
				await expect(frame.locator("#chart path")).toHaveCount(2);

				await expect(flow).toBeVisible();
				await expect(flow.frameLocator("iframe").locator("svg")).toBeVisible();
				await shot("inline");

				const composer = page.getByRole("textbox", { name: "Message Claude" });
				await expect(composer).toHaveValue("");
				const before = await page.locator(".chat-user").count();
				await frame.getByRole("button", { name: "Ask how this works ↗" }).click();
				await expect(composer).toHaveValue("Explain how dollar-cost averaging smooths out a falling market");
				await expect(composer).toBeFocused();
				await expect(page.locator(".chat-user")).toHaveCount(before);
				await expect(page.locator('[data-state="queued"], [data-state="started"]')).toHaveCount(0);
				await shot("prefilled");

				await composer.fill("");
				const ink = () => frame.locator("#amount-out").evaluate((el) => getComputedStyle(el).color);
				const surface = () =>
					frame
						.locator(".dca .stat")
						.first()
						.evaluate((el) => getComputedStyle(el).backgroundColor);
				const was = { ink: await ink(), surface: await surface() };
				await page.evaluate(() => {
					(window as unknown as { __kept: boolean }).__kept = true;
				});
				await page.getByRole("button", { name: "Settings", exact: true }).click();
				await page.getByRole("menuitem", { name: scheme === "dark" ? "Light mode" : "Dark mode" }).click();
				await expect.poll(ink).not.toBe(was.ink);
				await expect.poll(surface).not.toBe(was.surface);
				await expect(frame.locator("#years")).toHaveValue("20");
				await expect(invested).toHaveText("1,200,000");
				expect(await page.evaluate(() => (window as unknown as { __kept?: boolean }).__kept)).toBe(true);
				await shot("toggled");
			});

			test("expands into a sheet with its own frame", async ({ page }, info) => {
				await page.goto("/#/projects/ao-demo/sessions/demo-chat-widgets");
				const card = page.locator('[data-widget="w-calc"]');
				await expect(card).toBeVisible();
				await card.getByRole("button", { name: "Expand visual" }).click();
				const sheet = page.locator('[data-slot="sheet-content"]');
				await expect(sheet).toBeVisible();
				const big = sheet.frameLocator("iframe");
				await expect(big.locator("#invested")).toHaveText("600,000");
				const box = await sheet.locator("iframe").boundingBox();
				expect(box!.height).toBeGreaterThan(viewport.height * 0.6);
				expect(box!.width).toBeGreaterThan(viewport.width * 0.5);
				await big.locator("#amount").fill("10000");
				await expect(big.locator("#invested")).toHaveText("1,200,000");
				await page.screenshot({
					path: SHOTS
						? `${SHOTS}/chat-widgets-expanded-${scheme}-${viewport.width}.png`
						: info.outputPath("expanded.png"),
				});
				await sheet.getByRole("button", { name: "Close" }).click();
				await expect(sheet).toHaveCount(0);
			});

			test("the frame cannot reach the daemon, other hosts or the app", async ({ page }) => {
				await page.goto("/#/projects/ao-demo/sessions/demo-chat-widgets");
				const frame = page.locator('[data-widget="w-calc"]').frameLocator("iframe.chat-widget__frame");
				await expect(frame.locator("#invested")).toHaveText("600,000");
				const reach = await frame.locator("body").evaluate(async () => {
					const tryFetch = (url: string) =>
						fetch(url).then(
							() => "reached",
							() => "blocked",
						);
					let storage = "readable";
					try {
						void localStorage.length;
					} catch {
						storage = "blocked";
					}
					return {
						loopback: await tryFetch("http://127.0.0.1:3001/api/v1/health"),
						other: await tryFetch("https://example.com/"),
						storage,
						parentDom: (() => {
							try {
								return String(parent.document.title);
							} catch {
								return "blocked";
							}
						})(),
					};
				});
				expect(reach).toEqual({ loopback: "blocked", other: "blocked", storage: "blocked", parentDom: "blocked" });
			});
		});
	}
}
