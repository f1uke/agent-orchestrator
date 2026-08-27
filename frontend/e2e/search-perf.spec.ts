import { expect, test } from "@playwright/test";

/**
 * What ⌘⇧F costs when the answer is large.
 *
 * "self" on the human's real 6,940-file iOS project matches **12,847 times in
 * 1,570 files**. The server caps what travels back at 2,000 matches over 500
 * files, so 2,500 rows is the worst list this panel can ever be handed — and
 * that is exactly what the harness supplies. Rendered eagerly that is the shape
 * #254 measured at 568 ms to paint and 292 ms per keystroke; windowed it has to
 * stay a viewport.
 *
 * Measured in a real browser because the cost is React reconciliation and DOM
 * nodes, which jsdom has neither layout nor paint to show.
 *
 * Run it directly for numbers:
 *   npx playwright test e2e/search-perf.spec.ts --reporter=list
 */
test("⌘⇧F results: 2,000 matches over 500 files stay a window", async ({ page }) => {
	await page.goto("/e2e/files-bench.html");
	await expect(page.getByTestId("bench-count")).toHaveText("6940");
	await page.getByTestId("bench-mount").click();

	const baselineNodes = await page.evaluate(() => document.getElementsByTagName("*").length);

	await page.getByRole("tab", { name: /Search/ }).click();
	const box = page.getByRole("searchbox", { name: "Search in project" });
	await expect(box).toBeFocused();

	const typed = await page.evaluate(async () => {
		const settle = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
		const input = document.querySelector(".files-search__input") as HTMLInputElement;
		const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
		const t0 = performance.now();
		setter.call(input, "self");
		input.dispatchEvent(new Event("input", { bubbles: true }));
		// The panel debounces by 150 ms before it asks; wait past that, then let
		// the results paint.
		await new Promise((r) => setTimeout(r, 400));
		await settle();
		return { ms: performance.now() - t0, nodes: document.getElementsByTagName("*").length };
	});

	const rows = await page.locator(".files-search__row").count();
	const listed = await page.evaluate(() => document.querySelectorAll(".files-search__row").length);

	// Scrolling the result list: the worst frame is what "slow" would feel like.
	const scroll = await page.evaluate(async () => {
		const list = document.querySelector(".files-search__list") as HTMLElement;
		const frames: number[] = [];
		let last = performance.now();
		for (let i = 0; i < 40; i++) {
			list.scrollTop += 400;
			await new Promise((r) => requestAnimationFrame(r));
			const now = performance.now();
			frames.push(now - last);
			last = now;
		}
		frames.sort((a, b) => a - b);
		return { p50: frames[20], worst: frames[frames.length - 1] };
	});

	// eslint-disable-next-line no-console
	console.log(
		`\n⌘⇧F RESULTS @ 2,000 matches / 500 files (2,500 rows)\n` +
			`  rows in the DOM : ${rows}\n` +
			`  DOM nodes total : ${typed.nodes} (empty panel: ${baselineNodes})\n` +
			`  type to painted : ${typed.ms.toFixed(0)} ms (includes the 150 ms debounce)\n` +
			`  scroll frame p50: ${scroll.p50.toFixed(1)} ms, worst ${scroll.worst.toFixed(1)} ms\n`,
	);

	// Asserted on NODE COUNTS, never on timings — #254's rule. The counts are
	// deterministic on any machine and they are the thing that would regress; a
	// shared CI runner cannot promise a millisecond.
	expect(listed).toBeGreaterThan(0);
	// 2,500 rows exist; a window of them is on screen. Anything near 2,500 means
	// the virtualiser stopped virtualising.
	expect(listed).toBeLessThan(200);
	expect(typed.nodes).toBeLessThan(2_000);

	// The truncation has to be VISIBLE: a silently shortened list reads as
	// "that's all there is".
	await expect(page.getByText(/12,847 results in 1,570 files — showing 2,000/)).toBeVisible();
});
