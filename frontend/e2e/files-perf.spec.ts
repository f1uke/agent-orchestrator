import { expect, test } from "@playwright/test";

/**
 * What the Files rail costs on a large real project.
 *
 * The reported slowdown ("very slow" on a ~7,000-file iOS app) is a RENDER cost,
 * so it has to be measured in a real browser: jsdom has no layout, no paint and
 * no renderer heap, and the pure path→tree JS is only ~40 ms of it. The harness
 * (`files-bench.html`) mounts the shipped `FilesPanel` over a synthetic index of
 * the measured size and shape.
 *
 * Run it directly for numbers:
 *   npx playwright test e2e/files-perf.spec.ts --reporter=list
 */

type Metrics = { mountMs: number; nodes: number; rows: number; heapMb: number };

test("Files rail: Browse mode on a ~7,000-file workspace", async ({ page }) => {
	const cdp = await page.context().newCDPSession(page);
	await cdp.send("Performance.enable");
	const heapMb = async () => {
		await cdp.send("HeapProfiler.collectGarbage").catch(() => {});
		const { metrics } = await cdp.send("Performance.getMetrics");
		return (metrics.find((m) => m.name === "JSHeapUsedSize")?.value ?? 0) / 1024 / 1024;
	};

	await page.goto("/e2e/files-bench.html");
	await expect(page.getByTestId("bench-count")).toHaveText("6940");
	const baselineHeap = await heapMb();

	const mount: Metrics = await page.evaluate(async () => {
		const settle = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
		const t0 = performance.now();
		(document.querySelector('[data-testid="bench-mount"]') as HTMLElement).click();
		// React 18 renders in a microtask; two frames means it also laid out and painted.
		await settle();
		const mountMs = performance.now() - t0;
		return {
			mountMs,
			nodes: document.getElementsByTagName("*").length,
			rows: document.querySelectorAll(".file-tree__row").length,
			heapMb: 0,
		};
	});
	mount.heapMb = (await heapMb()) - baselineHeap;

	// One keystroke in the filter box: filter + rebuild tree + re-render every row.
	const type = (query: string) =>
		page.evaluate(async (q) => {
			const settle = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
			const input = document.querySelector(".files-panel__search-input") as HTMLInputElement;
			const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
			const t0 = performance.now();
			setter.call(input, q);
			input.dispatchEvent(new Event("input", { bubbles: true }));
			await settle();
			return { ms: performance.now() - t0, nodes: document.getElementsByTagName("*").length };
		}, query);

	const keystroke = await type("View");
	// `*` is a glob matching every path, so this is the whole tree with every
	// folder open — the state the panel used to mount in, and the only number
	// directly comparable to the pre-virtualisation baseline.
	const expanded = await type("*");
	const expandedHeap = (await heapMb()) - baselineHeap;

	// Scrolling: the worst frame is what "very slow" actually feels like.
	const scroll = await page.evaluate(async () => {
		const list = document.querySelector(".files-panel__list") as HTMLElement;
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
		`\nFILES RAIL @ 6,940 files\n` +
			`  rows rendered   : ${mount.rows}\n` +
			`  DOM nodes       : ${mount.nodes}\n` +
			`  mount to paint  : ${mount.mountMs.toFixed(0)} ms\n` +
			`  renderer heap   : +${mount.heapMb.toFixed(1)} MB\n` +
			`  filter keystroke: ${keystroke.ms.toFixed(0)} ms (${keystroke.nodes} nodes)\n` +
			`  ALL rows expanded: ${expanded.ms.toFixed(0)} ms, ${expanded.nodes} nodes, +${expandedHeap.toFixed(1)} MB heap\n` +
			`  scroll frame p50: ${scroll.p50.toFixed(1)} ms, worst ${scroll.worst.toFixed(1)} ms\n`,
	);

	// Asserted on NODE COUNT, not on the timings: the counts are deterministic on
	// any machine, and they are the thing that actually regressed — 8,466 rows and
	// 91,295 nodes for a workspace this size. Timings are printed above for a human
	// to read, never asserted, because a shared CI runner cannot promise them.
	expect(mount.rows).toBeGreaterThan(0);
	expect(mount.nodes).toBeLessThan(2_000);
	// Even with every folder open and every path matching, the DOM stays a window.
	expect(expanded.nodes).toBeLessThan(2_000);
});
