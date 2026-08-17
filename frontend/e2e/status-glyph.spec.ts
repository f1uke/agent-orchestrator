import { expect, test, type Locator, type Page } from "@playwright/test";

// Regression guard for the status glyph gutter (#197 shipped it, this fixes it).
//
// The shipped `no_signal` glyph was lucide's `SignalZero`, whose entire geometry
// is `M2 20h.01` — a zero-length path in the bottom-left corner of the 24-unit
// viewBox. Rendered at 15px it is a ~1px stroke cap sitting at 83% down the box:
// on screen, a stray full stop near the text baseline. The <svg> was the right
// size, visible, in the DOM, correctly coloured and correctly labelled, so every
// jsdom assertion in this repo stayed green while the board lost the one mark
// that tells a human which sessions need them.
//
// The only thing that distinguishes that from a real icon is what the browser
// actually paints, so these tests measure `getBBox()` — the union of the drawn
// geometry, in viewBox units — in real Chromium. jsdom does not implement
// getBBox at all; there is no jsdom version of this test.

/** Nominal lucide viewBox, used only as the fallback when the attribute is absent. */
const VIEWBOX = 24;

// Ink thresholds, in fractions of the viewBox. Set from the actual geometry of
// every icon the map uses, with margin on both sides:
//   - the thinnest legitimate icon is `Check` (mergeable): 0.67 wide × 0.46 tall
//   - the broken `SignalZero` was 0.0004 × 0
// So a floor of 0.35 on the short axis and 0.60 on the long axis passes every
// real silhouette and fails a degenerate one by two orders of magnitude.
const MIN_SHORT_AXIS = 0.35;
const MIN_LONG_AXIS = 0.6;

// A glyph whose ink sits off-centre reads as misaligned with the text beside it —
// the broken one was centred at 83% down its box, which is why it looked like a
// full stop on the baseline rather than a mark on the row.
const MAX_CENTRE_DRIFT = 0.12;

/** Absolute legibility floor: the drawn mark must be at least this many CSS px on its short axis. */
const MIN_INK_PX = 4.5;

type Ink = {
	/** Rendered size of the <svg> box, in CSS px. */
	boxPx: { width: number; height: number };
	/** Drawn geometry as a fraction of the viewBox. */
	frac: { width: number; height: number };
	/** Centre of the drawn geometry as a fraction of the viewBox (0.5 = centred). */
	centre: { x: number; y: number };
	/** Drawn geometry in CSS px. */
	inkPx: { width: number; height: number };
	/**
	 * Hit test at the glyph's centre: "hit" = nothing clips or covers it, "offscreen"
	 * = the point is outside the viewport, where elementFromPoint cannot answer.
	 */
	hit: "hit" | "covered" | "offscreen";
};

/**
 * Measure what an <svg> icon actually paints. `getBBox()` returns the union of
 * the element's drawn geometry in user units, which is exactly the fact the DOM
 * cannot express: an icon can be present, sized, coloured and still draw nothing.
 */
async function measureInk(svg: Locator): Promise<Ink> {
	return svg.evaluate((el, viewboxFallback) => {
		const node = el as unknown as SVGSVGElement;
		const rect = node.getBoundingClientRect();
		const vb = (node.getAttribute("viewBox") ?? "").split(/[\s,]+/).map(Number);
		const vbw = Number.isFinite(vb[2]) && vb[2] > 0 ? vb[2] : viewboxFallback;
		const vbh = Number.isFinite(vb[3]) && vb[3] > 0 ? vb[3] : viewboxFallback;
		const bb = node.getBBox();
		const frac = { width: bb.width / vbw, height: bb.height / vbh };
		const centre = { x: (bb.x + bb.width / 2) / vbw, y: (bb.y + bb.height / 2) / vbh };
		const px = rect.x + rect.width / 2;
		const py = rect.y + rect.height / 2;
		// elementFromPoint is only meaningful for a point inside the viewport; a
		// glyph scrolled below the fold would otherwise read as "covered".
		const inViewport = px >= 0 && py >= 0 && px <= window.innerWidth && py <= window.innerHeight;
		const target = inViewport ? document.elementFromPoint(px, py) : null;
		return {
			boxPx: { width: rect.width, height: rect.height },
			frac,
			centre,
			inkPx: { width: frac.width * rect.width, height: frac.height * rect.height },
			hit: !inViewport
				? ("offscreen" as const)
				: target !== null && (target === node || node.contains(target) || target.contains(node))
					? ("hit" as const)
					: ("covered" as const),
		};
	}, VIEWBOX);
}

/**
 * The whole assertion, in one place: this glyph draws a shape big enough and
 * centred enough to read at a glance, and nothing clips or covers it.
 */
async function expectLegibleGlyph(svg: Locator, what: string) {
	await expect(svg, `${what}: glyph is not visible`).toBeVisible();
	// Both the sidebar list and the board columns scroll; bring the glyph into the
	// viewport so the centre hit test below has a point it can actually resolve.
	await svg.scrollIntoViewIfNeeded();
	const ink = await measureInk(svg);
	const detail = `${what}: ${JSON.stringify(ink)}`;

	// The box itself must be a real icon box, not a collapsed gutter column.
	expect(ink.boxPx.width, detail).toBeGreaterThanOrEqual(12);
	expect(ink.boxPx.height, detail).toBeGreaterThanOrEqual(12);

	// The painted geometry must fill that box.
	const short = Math.min(ink.frac.width, ink.frac.height);
	const long = Math.max(ink.frac.width, ink.frac.height);
	expect(short, detail).toBeGreaterThanOrEqual(MIN_SHORT_AXIS);
	expect(long, detail).toBeGreaterThanOrEqual(MIN_LONG_AXIS);
	expect(Math.min(ink.inkPx.width, ink.inkPx.height), detail).toBeGreaterThanOrEqual(MIN_INK_PX);

	// …and sit on the row, not on the baseline under it.
	expect(Math.abs(ink.centre.x - 0.5), detail).toBeLessThanOrEqual(MAX_CENTRE_DRIFT);
	expect(Math.abs(ink.centre.y - 0.5), detail).toBeLessThanOrEqual(MAX_CENTRE_DRIFT);

	expect(ink.hit, `${detail} — glyph centre is clipped or covered`).toBe("hit");
}

const GALLERY = "/e2e/glyph-gallery.html";

// Every status the union holds. The gallery page itself is exhaustive by
// construction (a Record<SessionStatus, true>); this list is the spec's own copy
// so a dropped row fails loudly instead of quietly shrinking the coverage.
const STATUSES = [
	"todo",
	"working",
	"pr_open",
	"draft",
	"ci_failed",
	"review_pending",
	"changes_requested",
	"approved",
	"mergeable",
	"merged",
	"needs_input",
	"no_signal",
	"idle",
	"terminated",
	"unknown",
] as const;

for (const theme of ["dark", "light"] as const) {
	test(`every status glyph draws a legible mark (${theme})`, async ({ page }) => {
		await page.goto(GALLERY);
		await page.evaluate((t) => {
			document.documentElement.dataset.theme = t;
			document.documentElement.style.colorScheme = t;
		}, theme);
		await expect(page.locator("[data-glyph-row]")).toHaveCount(STATUSES.length);

		for (const status of STATUSES) {
			await expectLegibleGlyph(page.locator(`[data-glyph-row="${status}"] svg`), `status ${status} (${theme})`);
		}
	});
}

test("no two statuses in the NEEDS YOU lane share a silhouette", async ({ page }) => {
	await page.goto(GALLERY);
	// One coral bar used to flatten these four into one mark; four unlike
	// silhouettes is the entire reason the gutter replaced it.
	const needsYou = ["needs_input", "no_signal", "ci_failed", "changes_requested"];
	const shapes = await Promise.all(
		needsYou.map((status) =>
			page
				.locator(`[data-glyph-row="${status}"] svg`)
				.evaluate((el) =>
					[...el.querySelectorAll("path,circle,line,rect,polyline,polygon")]
						.map((c) => c.tagName + ":" + [...c.attributes].map((a) => `${a.name}=${a.value}`).join(","))
						.join("|"),
				),
		),
	);
	expect(new Set(shapes).size, `drawn geometry: ${JSON.stringify(shapes)}`).toBe(needsYou.length);
});

// ── The real board and sidebar, not only the harness ────────────────────────
// The gallery proves the icon *choices* are legible. These prove the surfaces
// that actually render them do not shrink, clip or mis-align what they got.

async function openBoard(page: Page) {
	await page.goto("/");
	await expect(page.getByText("Projects")).toBeVisible();
}

test("every board card's status glyph draws a legible mark", async ({ page }) => {
	await openBoard(page);
	const glyphs = page.locator("[data-card-status-glyph] svg");
	const count = await glyphs.count();
	// The demo workspace covers every lane; if it ever stops, the guard silently
	// stops guarding, so assert the board is actually populated.
	expect(count).toBeGreaterThanOrEqual(8);
	for (let i = 0; i < count; i++) {
		const glyph = glyphs.nth(i);
		const status = await glyph.evaluate(
			(el) => el.closest("[data-card-status-glyph]")?.getAttribute("data-card-status-glyph") ?? "?",
		);
		await expectLegibleGlyph(glyph, `board card ${status}`);
	}
});

test("every sidebar row's status glyph draws a legible mark", async ({ page }) => {
	await openBoard(page);
	const glyphs = page.locator("[data-session-glyph] svg");
	const count = await glyphs.count();
	expect(count).toBeGreaterThanOrEqual(8);
	for (let i = 0; i < count; i++) {
		const glyph = glyphs.nth(i);
		const status = await glyph.evaluate(
			(el) => el.closest("[data-session-glyph]")?.getAttribute("data-session-glyph") ?? "?",
		);
		await expectLegibleGlyph(glyph, `sidebar row ${status}`);
	}
});

// The column-header glyph is the reference for "correct": it is the one the
// human could read while the per-card marks were specks. Hold it to the same
// measured bar so the reference itself cannot rot.
test("the board column header glyphs stay legible too", async ({ page }) => {
	await openBoard(page);
	for (const lane of ["todo", "working", "action", "pending", "merge"]) {
		await expectLegibleGlyph(page.locator(`svg[data-lane-glyph="${lane}"]`).first(), `column header ${lane}`);
	}
});

// The per-item glyph is deliberately LARGER than the header's: the card gutter is
// what a human scans down a column, the header is a static label decoration read
// once. 15px card / 13px header / 13px sidebar row — the sidebar matching the
// header because its rows are the denser type. This locks that relationship in,
// so a future tweak to one size has to be a decision about all three.
test("per-item glyphs are larger than the column-header glyph", async ({ page }) => {
	await openBoard(page);
	const size = async (sel: string) =>
		(await page
			.locator(sel)
			.first()
			.evaluate((el) => el.getBoundingClientRect().width)) as number;
	// Not the TODO card: it draws its own dashed ring rather than going through the
	// status map, so measuring it would leave every live card's size unchecked.
	const card = await size('[data-card-status-glyph]:not([data-card-status-glyph="todo"]) svg');
	const header = await size("svg[data-lane-glyph]");
	const sidebar = await size("[data-session-glyph] svg");
	expect(card).toBe(15);
	expect(header).toBe(13);
	expect(sidebar).toBe(13);
	expect(card).toBeGreaterThan(header);
});
