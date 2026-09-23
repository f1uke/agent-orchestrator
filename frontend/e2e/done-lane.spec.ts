import { expect, test, type Page } from "@playwright/test";

// The Done lane at the size it really gets. The machine this was built on held
// 369 terminated sessions when it was asked for (426 by the time it shipped), so
// the demo board is widened by 369 more and every number below is measured on
// ~370 finished cards, never on the fixture's four.
//
// Measured in real Chromium: what is at stake is DOM nodes, layout and paint,
// which jsdom has none of.
//
// Run it directly for numbers:
//   npx playwright test e2e/done-lane.spec.ts --reporter=list

const EXTRA = 369;

async function openBoard(page: Page, width = 1440, lanes?: Record<string, boolean>) {
	await page.addInitScript(
		({ extra, lanes }) => {
			const minute = 60_000;
			(globalThis as { __aoMockWorkspaces?: unknown }).__aoMockWorkspaces = (
				base: { id: string; sessions: unknown[] }[],
			) =>
				base.map((workspace, index) =>
					index !== 0
						? workspace
						: {
								...workspace,
								sessions: [
									...workspace.sessions,
									// Oldest first on purpose, so the lane has to sort them. Session
									// n ended n+1 days ago; its row was touched later than that, so
									// ranking by updatedAt would put them in reverse.
									...Array.from({ length: extra }, (_, i) => {
										const n = extra - i;
										const endedAt = new Date(Date.now() - (n + 1) * 1440 * minute).toISOString();
										return {
											id: `bench-done-${n}`,
											terminalHandleId: `bench-done-${n}/terminal_0`,
											workspaceId: workspace.id,
											workspaceName: workspace.id,
											title: `Archived task number ${n} ${n % 7 === 0 ? "about webhooks" : "about the sidebar"}`,
											provider: "claude-code",
											kind: "worker",
											branch: `bench/done-${n}`,
											issueId: n % 10 === 0 ? `jira:BENCH-${n}` : undefined,
											status: n % 3 === 0 ? "merged" : "terminated",
											isTerminated: true,
											createdAt: new Date(Date.now() - (n + 2) * 1440 * minute).toISOString(),
											updatedAt: new Date(Date.now() - i * minute).toISOString(),
											termination: { source: "ao", reason: "kill", at: endedAt },
											activity: { state: "exited", lastActivityAt: endedAt },
											prs: [],
										};
									}),
								],
							},
				);
			try {
				if (lanes) localStorage.setItem("ao.board.collapsedLanes", JSON.stringify(lanes));
				else localStorage.removeItem("ao.board.collapsedLanes");
			} catch {
				// A first-load page may refuse storage; the defaults are what we want then.
			}
		},
		{ extra: EXTRA, lanes },
	);
	await page.setViewportSize({ width, height: 900 });
	await page.goto("/");
	await expect(page.locator("[data-status]").first()).toBeVisible();
}

const doneCards = (page: Page) => page.locator("[data-done-card]");
const settle = (page: Page) =>
	page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));

test("folded, the Done lane is a count and nothing else", async ({ page }) => {
	await openBoard(page);
	const strip = page.getByRole("button", { name: /^Expand Done, \d+ cards$/ });
	await expect(strip).toBeVisible();
	const total = Number((await strip.getAttribute("aria-label"))!.match(/(\d+) cards/)![1]);
	expect(total).toBeGreaterThanOrEqual(EXTRA);
	expect(await doneCards(page).count()).toBe(0);

	// The badge fits the 2.25rem strip at three digits.
	const fits = await page.locator('[data-lane="done"]').evaluate((lane) => {
		const badge = [...lane.querySelectorAll("span")].find((el) => /^\d+$/.test(el.textContent ?? ""))!;
		const a = lane.getBoundingClientRect();
		const b = badge.getBoundingClientRect();
		return b.left >= a.left && b.right <= a.right;
	});
	expect(fits).toBe(true);
});

test(`open, ${EXTRA}+ finished sessions stay a window, newest ended first`, async ({ page }) => {
	await openBoard(page);
	const nodesFolded = await page.evaluate(() => document.getElementsByTagName("*").length);

	const t0 = Date.now();
	await page.getByRole("button", { name: /^Expand Done, / }).click();
	await expect(doneCards(page).first()).toBeVisible();
	await settle(page);
	const openMs = Date.now() - t0;
	const drawn = await doneCards(page).count();
	const nodesOpen = await page.evaluate(() => document.getElementsByTagName("*").length);

	// Newest ending first: the fixture's own 25-minute-old merge, then bench
	// session 1 (ended 2 days ago), 2, 3...
	const titles = await doneCards(page).evaluateAll((cards) =>
		cards
			.map((card) => ({ top: card.getBoundingClientRect().top, title: card.querySelector("button")!.textContent }))
			.sort((a, b) => a.top - b.top)
			.map((c) => c.title),
	);
	expect(titles[0]).toBe("Ship sidebar footer alignment fix");
	const bench = titles.filter((t) => t!.startsWith("Archived task number"));
	expect(bench.slice(0, 3)).toEqual([
		"Archived task number 1 about the sidebar",
		"Archived task number 2 about the sidebar",
		"Archived task number 3 about the sidebar",
	]);

	// Scroll the whole lane: the worst frame is what "slow" would feel like, and
	// the DOM must stay a window the whole way down.
	const scroll = await page.locator('[data-lane="done"] .overflow-y-auto').evaluate(async (body) => {
		const frames: number[] = [];
		let last = performance.now();
		let most = 0;
		while (body.scrollTop + body.clientHeight < body.scrollHeight - 1) {
			body.scrollTop += 600;
			await new Promise((r) => requestAnimationFrame(r));
			const now = performance.now();
			frames.push(now - last);
			last = now;
			most = Math.max(most, body.querySelectorAll("[data-done-card]").length);
		}
		await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
		frames.sort((a, b) => a - b);
		const cards = [...body.querySelectorAll("[data-done-card]")];
		const lastCard = cards.sort((a, b) => b.getBoundingClientRect().top - a.getBoundingClientRect().top)[0];
		return {
			p50: frames[Math.floor(frames.length / 2)],
			worst: frames[frames.length - 1],
			most,
			last: lastCard.querySelector("button")!.textContent,
		};
	});

	console.log(
		`done lane: open ${openMs}ms, ${drawn} cards drawn, DOM ${nodesFolded} -> ${nodesOpen} nodes; ` +
			`scroll p50 ${scroll.p50.toFixed(1)}ms worst ${scroll.worst.toFixed(1)}ms, at most ${scroll.most} cards`,
	);
	expect(drawn).toBeLessThan(30);
	expect(scroll.most).toBeLessThan(30);
	expect(scroll.last).toBe(`Archived task number ${EXTRA} ${EXTRA % 7 === 0 ? "about webhooks" : "about the sidebar"}`);
});

test("search is instant over the whole archive, hits and misses", async ({ page }) => {
	await openBoard(page, 1440, { done: false });
	const box = page.getByRole("searchbox", { name: "Search finished sessions" });
	await expect(box).toBeVisible();

	// Keystroke to painted result, one character at a time.
	const perKey: number[] = [];
	for (const ch of "webhooks") {
		const t0 = Date.now();
		await box.press(ch);
		await settle(page);
		perKey.push(Date.now() - t0);
	}
	console.log(`search: per keystroke ${perKey.join(", ")} ms`);
	const hits = Math.floor(EXTRA / 7);
	await expect(page.getByText(`${hits} of `)).toBeVisible();
	for (const title of await doneCards(page).locator("button").first().allTextContents()) {
		expect(title).toContain("webhooks");
	}

	await box.fill("BENCH-120");
	await expect(doneCards(page)).toHaveCount(1);
	await expect(doneCards(page).first()).toContainText("Archived task number 120");

	await box.fill("bench-done-42");
	await expect(doneCards(page).first()).toContainText("Archived task number 42 about webhooks");

	await box.fill("no such session anywhere");
	await expect(doneCards(page)).toHaveCount(0);
	await expect(page.getByText("Nothing finished matches “no such session anywhere”")).toBeVisible();
});

test("the whole header strip folds a lane; the Done search box does not", async ({ page }) => {
	await openBoard(page, 1440, { done: false });

	// A click in the header's empty middle - not on the glyph, name or count.
	const header = page.getByRole("button", { name: /^Collapse Working, / });
	await expect(header).toHaveCSS("cursor", "pointer");
	const box = (await header.boundingBox())!;
	await page.mouse.click(box.x + box.width * 0.62, box.y + box.height / 2);
	await expect(page.getByRole("button", { name: /^Expand Working, / })).toBeVisible();

	await page.getByRole("searchbox", { name: "Search finished sessions" }).click();
	await page.keyboard.type("sidebar");
	await expect(page.getByRole("button", { name: /^Collapse Done, / })).toHaveAttribute("aria-expanded", "true");
});

test("opening Done at 1280px brings it into view instead of unfolding it off the edge", async ({ page }) => {
	await openBoard(page, 1280);
	await page.getByRole("button", { name: /^Expand Done, / }).click();
	const box = page.getByRole("searchbox", { name: "Search finished sessions" });
	await expect(box).toBeVisible();
	await expect
		.poll(() =>
			page.locator('[data-lane="done"]').evaluate((lane) => {
				const board = (lane.parentElement as HTMLElement).getBoundingClientRect();
				return Math.round(lane.getBoundingClientRect().right - board.right);
			}),
		)
		.toBeLessThanOrEqual(0);
});
