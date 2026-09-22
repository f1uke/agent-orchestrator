import { expect, test, type Page } from "@playwright/test";

// Regression guard for the board card's header row, which has now broken on a
// width twice, each fix trading one defect for another:
//   - one shrinkable line squeezed the status to 3px and clipped it to "C.";
//   - one wrapping line with a shrink-0 right-hand cluster dropped "Claude" to a
//     line of its own, and, holding the 169px "Undelivered · Move to Done" chip,
//     ran past the card's edge.
// All three are facts about painted boxes at a real width, which jsdom does not
// lay out, so they are measured here in real Chromium on the demo board (which
// carries an undelivered card and a stalled crew holding one).

// 960 is the app's minimum window; 1280 is the narrowest that still fits five
// open lanes; 1440 wraps the undelivered chip's button; 1800 keeps it on one line.
const WIDTHS = [960, 1280, 1440, 1800];

type HeaderReport = {
	status: string;
	/** Anything on the status or chip line that crosses the card's content edge. */
	escapes: string[];
	/** The agent label sits above the status's first line, i.e. on a line of its own. */
	agentAlone: boolean;
	/** The status text as rendered, to prove no character went missing. */
	text: string;
};

async function openBoard(page: Page, width: number) {
	await page.setViewportSize({ width, height: 900 });
	await page.goto("/");
	await page.evaluate(() => localStorage.removeItem("ao.board.collapsedLanes"));
	await page.reload();
	await expect(page.getByText("Projects")).toBeVisible();
	await expect(page.locator("[data-status]").first()).toBeVisible();
}

function readHeaders(page: Page): Promise<HeaderReport[]> {
	return page.locator("[data-status]").evaluateAll((statuses) =>
		statuses.map((status) => {
			const line = status.parentElement as HTMLElement;
			const edge = line.getBoundingClientRect();
			const chipLine = line.nextElementSibling?.hasAttribute("data-card-chips") ? line.nextElementSibling : null;
			const escapes: string[] = [];
			for (const el of [...line.querySelectorAll("*"), ...(chipLine?.querySelectorAll("*") ?? [])]) {
				const box = el.getBoundingClientRect();
				if (box.width === 0) continue;
				if (box.left < edge.left - 0.5 || box.right > edge.right + 0.5) {
					escapes.push(`${el.tagName} "${(el.textContent ?? "").slice(0, 24)}" [${box.left}, ${box.right}]`);
				}
			}
			const agent = line.firstElementChild!.getBoundingClientRect();
			const firstStatusLine = status.getClientRects()[0];
			return {
				status: status.getAttribute("data-status") ?? "",
				escapes,
				agentAlone: firstStatusLine.top > agent.top + 2,
				text: status.textContent ?? "",
			};
		}),
	);
}

function boardOverflow(page: Page): Promise<number> {
	return page
		.locator("[data-lane]")
		.first()
		.evaluate((lane) => {
			const board = lane.parentElement as HTMLElement;
			return board.scrollWidth - board.clientWidth;
		});
}

for (const width of WIDTHS) {
	test(`at ${width}px every card header stays inside its card, whole, with the agent beside its status`, async ({
		page,
	}) => {
		await openBoard(page, width);
		const headers = await readHeaders(page);
		// If the demo board ever stops carrying the long cases, the guard silently
		// stops guarding, so assert they are on it.
		expect(headers.map((h) => h.status)).toContain("Nobody is working on this");
		await expect(page.getByRole("button", { name: "Move to Done" }).first()).toBeVisible();

		for (const header of headers) {
			expect(header.escapes, `${header.status}: crosses the card's content edge`).toEqual([]);
			expect(header.agentAlone, `${header.status}: agent label on a line of its own`).toBe(false);
			expect(header.text, `${header.status}: status lost characters`).toBe(header.status);
		}
	});
}

test("the undelivered chip keeps its button on its line when there is room, and wraps it under the label when not", async ({
	page,
}) => {
	const chipHeight = () =>
		page
			.locator("[data-card-chips] span[aria-label^='Undelivered']")
			.first()
			.evaluate((el) => el.getBoundingClientRect().height);

	await openBoard(page, 1800);
	expect(await chipHeight()).toBeLessThan(26);

	await openBoard(page, 1440);
	expect(await chipHeight()).toBeGreaterThan(36);
});

test("five lanes fit from 1280px; at 960px the board scrolls until two lanes are folded", async ({ page }) => {
	await openBoard(page, 1280);
	expect(await boardOverflow(page)).toBe(0);

	await openBoard(page, 960);
	expect(await boardOverflow(page)).toBeGreaterThan(0);

	await page.getByRole("button", { name: "Collapse Todo" }).click();
	await page.getByRole("button", { name: "Collapse Ready to merge" }).click();
	expect(await boardOverflow(page)).toBe(0);

	// Folding is remembered across a reload, and the strip opens the lane again.
	await page.reload();
	const todo = page.getByRole("button", { name: /^Expand Todo, \d+ cards?$/ });
	await expect(todo).toBeVisible();
	await todo.click();
	await expect(page.getByRole("button", { name: "Collapse Todo" })).toHaveAttribute("aria-expanded", "true");
});
