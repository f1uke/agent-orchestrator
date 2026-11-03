import { expect, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

const SCREENS = "/Users/fluke/.ao/knowledge/agent-orchestrator/plans/feature-chat-trajectory--screens-pr2";

async function openTrajectory(page: Page) {
	await page.goto("/#/projects/ao-demo/sessions/demo-chat-long");
	await expect(page.getByTestId("chat-stream")).toBeVisible();
	await page.getByRole("group", { name: "Session view" }).getByRole("button", { name: "Trajectory" }).click();
	const trajectory = page.getByTestId("chat-trajectory");
	await expect(trajectory).toBeVisible();
	await expect(trajectory.locator(".chat-traj__row").first()).toBeVisible();
	return trajectory;
}

for (const viewport of VIEWPORTS) {
	test.describe(`chat trajectory at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("one line per step, the strip and the rows agree, and search narrows and clears", async ({ page }) => {
			const trajectory = await openTrajectory(page);

			expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
			const scroller = page.getByTestId("chat-trajectory-rows");
			expect(await scroller.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);

			const rows = trajectory.locator(".chat-traj__row");
			const heights = await rows.evaluateAll((els) => els.map((el) => el.getBoundingClientRect().height));
			expect(heights.length).toBeGreaterThan(5);
			expect(Math.max(...heights)).toBeLessThanOrEqual(32);

			const calls = Number(await trajectory.getAttribute("data-calls"));
			expect(calls).toBeGreaterThan(20);
			const toolBars = page.getByTestId("chat-trajectory-strip").locator('[data-lane="tools"][data-key]');
			await expect(toolBars).toHaveCount(calls);
			const toolsChip = page.getByRole("group", { name: "Show steps" }).getByRole("button", { name: /^Tools/ });
			await expect(toolsChip).toContainText(String(calls));
			await toolsChip.click();
			await expect(rows.first()).toBeVisible();
			expect(await rows.evaluateAll((els) => els.every((el) => el.getAttribute("data-kind") === "tool"))).toBe(true);
			const stripHeight = await page
				.getByTestId("chat-trajectory-strip")
				.evaluate((el) => el.getBoundingClientRect().height);
			expect(stripHeight).toBe(64);
			await page.getByRole("group", { name: "Show steps" }).getByRole("button", { name: "All" }).click();

			const all = await rows.count();
			const search = page.getByRole("searchbox", { name: "Search steps" });
			await search.fill("slow-check");
			await expect(rows.first()).toContainText("slow-check");
			const narrowed = await rows.count();
			expect(narrowed).toBeGreaterThan(0);
			expect(narrowed).toBeLessThan(all);
			await search.press("Escape");
			await expect(search).toHaveValue("");
			await expect(rows).not.toHaveCount(narrowed);
		});

		test("a bar scrolls its row into view and flashes it; a row opens to its input and result", async ({
			page,
		}, info) => {
			const shot = (name: string) =>
				page.screenshot({ path: `${SCREENS}/after-${name}-${viewport.width}x${viewport.height}.png` });
			const trajectory = await openTrajectory(page);
			await shot("trajectory");

			const bars = page.getByTestId("chat-trajectory-strip").locator('[data-lane="tools"][data-key]');
			const last = bars.last();
			const key = (await last.getAttribute("data-key"))!;
			await last.click();
			const row = trajectory.locator(`.chat-traj__row[data-key="${key}"]`);
			await expect(row).toBeVisible();
			await expect(row).toHaveAttribute("data-flash", "");
			await expect(row).toBeInViewport();

			await row.locator(".chat-traj__head").click();
			const detail = row.getByTestId("chat-trajectory-detail");
			await expect(detail.locator('[data-section="input"]')).toBeVisible();
			await row.getByRole("button", { name: "Raw events" }).click();
			await expect(row.getByTestId("chat-trajectory-raw")).toBeVisible();
			const overlaps = () =>
				trajectory.locator(".chat-traj__item").evaluateAll((els) => {
					const boxes = els.map((el) => el.getBoundingClientRect()).sort((a, b) => a.top - b.top);
					return boxes.filter((box, i) => i > 0 && box.top < boxes[i - 1].bottom - 1).length;
				});
			await expect.poll(overlaps).toBe(0);
			await shot("row-open");

			await page.getByRole("button", { name: "Turns", exact: true }).click();
			await expect(trajectory).toHaveAttribute("data-axis", "turns");
			await bars.first().hover();
			await expect(page.getByRole("tooltip")).toBeVisible();
			await shot("turns-axis");

			await page
				.getByRole("group", { name: "Show steps" })
				.getByRole("button", { name: /^Errors/ })
				.click();
			await expect(trajectory.locator(".chat-traj__row").first()).toHaveAttribute("data-tone", "error");
			await shot("errors");
			info.annotations.push({ type: "viewport", description: `${viewport.width}x${viewport.height}` });
		});

		test("follows the end while live, and stops when the user scrolls up", async ({ page }) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-tall");
			await expect(page.getByTestId("chat-stream")).toBeVisible();
			await page.getByRole("group", { name: "Session view" }).getByRole("button", { name: "Trajectory" }).click();
			const scroller = page.getByTestId("chat-trajectory-rows");
			await expect(page.getByTestId("chat-trajectory")).toBeVisible();
			const gap = () => scroller.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);
			await expect.poll(() => scroller.evaluate((el) => el.scrollHeight > el.clientHeight * 3)).toBe(true);
			await expect.poll(gap).toBeLessThan(4);
			await scroller.hover();
			await page.mouse.wheel(0, -600);
			await expect.poll(gap).toBeGreaterThan(100);
			await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
			await page.getByRole("button", { name: "Jump to latest" }).click();
			await expect.poll(gap).toBeLessThan(4);
		});
	});
}
