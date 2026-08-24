import { expect, test, type Locator, type Page } from "@playwright/test";

// The qa pip on a task's sidebar row: does this task have a SECOND AGENT, and is
// it up? The rule behind it (`lib/crew.ts qaPresence`) and the markup it produces
// are already covered in jsdom by crew.test.ts and Sidebar.test.tsx, and those
// tests are the right place for "which state does this session read as".
//
// This file exists for the three things jsdom CANNOT answer, because jsdom has no
// layout engine and paints nothing:
//
//   1. Does the indicator's presence move anything else on the row? The whole
//      brief for this feature is that a qa waking must not make the rail twitch,
//      and "the row is the same height and the name box is the same box" is a
//      claim about boxes a browser computed, not about class names. A jsdom test
//      comparing className strings can stay green while the real row reflows.
//   2. Do the two states differ in SILHOUETTE rather than only in colour? The
//      states must be distinguishable without colour vision. `getBBox()` returns
//      the union of an <svg>'s drawn geometry; jsdom does not implement it at all
//      (the same reason status-glyph.spec.ts lives here).
//   3. Does the pip stay inside a 240px rail? Overflow is a layout fact.
//
// The Playwright web server runs `dev:web` (VITE_NO_ELECTRON=1), so the rail is
// the deterministic preview fixture from lib/mock-data.ts, which carries one task
// per live state: demo-stalled's qa is parked (awake), demo-ready's is suspended
// (asleep), demo-working's never started (asleep), and every other task is solo.

/** Rows named by their work, as the rail labels them. `Open <name>` is the row's button. */
const ROWS = {
	awake: "Rename the export job's retry flag",
	paused: "Merge README screenshot asset update",
	notStarted: "Build screenshot-ready dashboard data",
	solo: "Fix flaky NewTaskDialog smoke test",
} as const;

/** Nominal lucide viewBox, used only when the attribute is absent. */
const VIEWBOX = 24;

/**
 * Absolute legibility floor for the pip's mark, in CSS px on its short axis. The
 * pip is drawn in a 9px box, so a mark that paints less than this is a smudge —
 * the failure mode #197 shipped and status-glyph.spec.ts was written to catch.
 */
const MIN_INK_PX = 4;

// The rail nests lists (project > tasks), so a `filter({has})` on `li` matches
// every ancestor too. The row we mean is the INNERMOST li around the row button.
const row = (page: Page, name: string) =>
	page.getByRole("button", { name: new RegExp(`^Open ${escape(name)}`) }).locator("xpath=ancestor::li[1]");

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** The pip on a given task's row. */
const pip = (page: Page, name: string) => row(page, name).locator("[data-qa-pip]");

async function openRail(page: Page) {
	await page.goto("/");
	// The rail is the fixture's own list; wait for a row that is always there.
	await expect(page.getByRole("button", { name: new RegExp(`^Open ${escape(ROWS.solo)}`) })).toBeVisible();
}

test.describe("the qa pip reaches a real browser", () => {
	test("draws one live state per task, and nothing at all on a solo task", async ({ page }) => {
		await openRail(page);

		await expect(pip(page, ROWS.awake)).toHaveAttribute("data-qa-pip", "awake");
		await expect(pip(page, ROWS.paused)).toHaveAttribute("data-qa-pip-detail", "paused");
		await expect(pip(page, ROWS.notStarted)).toHaveAttribute("data-qa-pip-detail", "not started");
		// A solo task draws NOTHING — no empty seat, no reserved outline. Since a
		// whole project can run with automatic crew formation off, "no qa" is an
		// ordinary steady state rather than a sign something is pending.
		await expect(row(page, ROWS.solo).locator("[data-qa-pip]")).toHaveCount(0);
	});
});

test.describe("nothing else on the row moves when the pip is there", () => {
	// The no-twitch rule, measured rather than asserted from class names: take the
	// row's geometry, take the pip OUT of the DOM (the same delta React makes when
	// a task has no qa), force layout, and measure again. Anything that differs is
	// something the indicator's mere presence moved.
	for (const [state, name] of [
		["awake", ROWS.awake],
		["asleep", ROWS.paused],
	] as const) {
		test(`a ${state} qa changes neither the row's height nor the work name's box`, async ({ page }) => {
			await openRail(page);

			const geometry = await pip(page, name).evaluate((el) => {
				const li = el.closest("li") as HTMLElement;
				const nameSpan = li.querySelector("span.truncate") as HTMLElement;
				const snap = () => {
					const r = li.getBoundingClientRect();
					const n = nameSpan.getBoundingClientRect();
					return {
						rowWidth: r.width,
						rowHeight: r.height,
						nameX: n.x,
						nameWidth: n.width,
						// How much of the name is actually rendered rather than clipped.
						nameOverflow: nameSpan.scrollWidth - nameSpan.clientWidth,
					};
				};
				const withPip = snap();
				const parent = el.parentElement as HTMLElement;
				const next = el.nextSibling;
				parent.removeChild(el);
				void li.offsetHeight; // force layout
				const withoutPip = snap();
				parent.insertBefore(el, next); // leave the page as we found it
				return { withPip, withoutPip };
			});

			expect(geometry.withPip).toEqual(geometry.withoutPip);
		});
	}
});

test.describe("the pip stays out of the row's text column", () => {
	// The pip's FIRST placement rode the `@id` line as a flex sibling, and at the
	// 240px rail that line has no slack: the pip took 5px off `@demo-working` and
	// 17px off `@demo-stalled`, so a session ref that had been fully readable was
	// silently ellipsised the moment its task gained a qa. It now sits absolutely
	// in the row's right-hand gutter instead, beside the rename pencil, and takes
	// nothing from the text column at all.
	//
	// This is the guard against it creeping back in. Removing the pip from the DOM
	// must change nothing about the session ref — not its box, and above all not
	// whether it is clipped.
	test("takes no width from the session ref, in any state", async ({ page }) => {
		await openRail(page);

		for (const name of [ROWS.awake, ROWS.paused, ROWS.notStarted]) {
			const ref = await pip(page, name).evaluate((el) => {
				const li = el.closest("li") as HTMLElement;
				const id = li.querySelector('span[title^="@"]') as HTMLElement;
				const snap = () => ({
					text: id.textContent,
					client: id.clientWidth,
					scroll: id.scrollWidth,
					clipped: id.scrollWidth > id.clientWidth + 1,
				});
				const withPip = snap();
				const parent = el.parentElement as HTMLElement;
				const next = el.nextSibling;
				parent.removeChild(el);
				void li.offsetHeight;
				const withoutPip = snap();
				parent.insertBefore(el, next);
				return { withPip, withoutPip };
			});

			expect(ref.withPip, `${name}: the pip narrowed the session ref`).toEqual(ref.withoutPip);
			// And a ref that fits without the pip still fits with it.
			expect(ref.withPip.clipped, `${name}: the pip clipped the session ref`).toBe(ref.withoutPip.clipped);
		}
	});
});

test.describe("the two states are told apart by shape, not by colour", () => {
	/**
	 * What an <svg> icon actually PAINTS: `getBBox()` is the union of the drawn
	 * geometry in viewBox units, which is the fact the DOM cannot express — an icon
	 * can be present, sized, coloured and still draw nothing.
	 */
	async function measure(target: Locator) {
		return target.locator("svg").evaluate((el, fallback) => {
			const svg = el as unknown as SVGGraphicsElement;
			const box = svg.getBBox();
			const viewBox = Number((el.getAttribute("viewBox") ?? "").split(" ")[2]) || fallback;
			const rendered = el.getBoundingClientRect();
			return {
				frac: { width: box.width / viewBox, height: box.height / viewBox },
				inkPx: {
					width: (box.width / viewBox) * rendered.width,
					height: (box.height / viewBox) * rendered.height,
				},
				// "none" vs a colour is the second channel, and it survives greyscale:
				// the awake mark is a solid disc, the asleep one an outline crescent.
				fill: getComputedStyle(el).fill,
			};
		}, VIEWBOX);
	}

	test("the awake mark is filled, the asleep mark is not, and both paint a legible mark", async ({ page }) => {
		await openRail(page);

		const awake = await measure(pip(page, ROWS.awake));
		const asleep = await measure(pip(page, ROWS.paused));

		expect(awake.fill).not.toBe("none");
		expect(asleep.fill).toBe("none");
		// Neither is a smudge at 9px.
		for (const ink of [awake.inkPx, asleep.inkPx]) {
			expect(Math.min(ink.width, ink.height)).toBeGreaterThan(MIN_INK_PX);
		}
		// And the silhouettes are genuinely different geometry, not one shape in two
		// tones — so a reader who cannot see colour still has something to read.
		expect(Math.abs(awake.frac.width - asleep.frac.width)).toBeGreaterThan(0.02);
	});
});

test.describe("the pip fits the rail", () => {
	test("never overflows the row it sits on, in either state", async ({ page }) => {
		await openRail(page);

		for (const name of [ROWS.awake, ROWS.paused, ROWS.notStarted]) {
			const fits = await pip(page, name).evaluate((el) => {
				const content = el.closest("li")!.querySelector("div")! as HTMLElement;
				const p = el.getBoundingClientRect();
				const c = content.getBoundingClientRect();
				return { overflowRight: p.right - c.right, overflowLeft: c.left - p.left };
			});
			expect(fits.overflowRight, `${name}: pip runs past the row's right edge`).toBeLessThanOrEqual(0);
			expect(fits.overflowLeft, `${name}: pip runs past the row's left edge`).toBeLessThanOrEqual(0);
		}
	});
});
