import { expect, test, type Page } from "@playwright/test";

// The pinch overlay's two contacts, painted in a real browser.
//
// ⚠ These dots are the one surface in the app that may not take their contrast
// from the theme, because of what they sit on: not an AO surface, but whatever
// the app being driven is showing - a white article one moment and a dark chart
// the next. So each dot carries its own contrast, a light ring with dark on both
// sides of it, and the gallery puts both grounds side by side in both themes.
//
// jsdom can say the span exists, is sized, and holds the right custom
// properties, and every one of those assertions passes for a dot that is
// invisible on white. "Can a person see this against what the device is
// showing" is a question about paint, and only a browser paints.
//
// The gallery also draws a device body around the picture, because the dots are
// positioned against the PICTURE and inset by that body: a dot measured against
// the pane instead lands a few percent out, which on a phone is the edge of one
// button and the middle of the next.

/** The pointer position the gallery pins the dots at, in device coordinates. */
const AT = { x: 0.32, y: 0.7 };
/** Its mirror through the middle, which is where the second contact must be. */
const MIRRORED = { x: 1 - AT.x, y: 1 - AT.y };
/** Half a pixel of rounding is fine; a dot placed against the wrong box is not. */
const TOLERANCE_PX = 1;

async function open(page: Page) {
	await page.goto("/e2e/pinch-dots-gallery.html");
	await expect(page.getByTestId("pane-article")).toBeVisible();
}

/** The centre of an element, in page pixels. */
async function centreOf(page: Page, testId: string) {
	const box = await page.getByTestId(testId).first().boundingBox();
	if (!box) throw new Error(`${testId} has no box`);
	return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

for (const pane of ["pane-article", "pane-chart"] as const) {
	test(`${pane}: both contacts land on the picture, inside the device body`, async ({ page }) => {
		await open(page);
		const picture = await page.getByTestId(`${pane}-picture`).boundingBox();
		if (!picture) throw new Error("no picture box");

		// The gallery renders one pane per ground, so the dots are indexed in the
		// same order as the panes.
		const index = pane === "pane-article" ? 0 : 1;
		const a = await page.getByTestId("sim-pinch-dot-a").nth(index).boundingBox();
		const b = await page.getByTestId("sim-pinch-dot-b").nth(index).boundingBox();
		const pivot = await page.getByTestId("sim-pinch-dots").nth(index).boundingBox();
		if (!a || !b || !pivot) throw new Error("no dot boxes");

		const centre = (box: { x: number; y: number; width: number; height: number }) => ({
			x: box.x + box.width / 2,
			y: box.y + box.height / 2,
		});
		const want = (at: { x: number; y: number }) => ({
			x: picture.x + at.x * picture.width,
			y: picture.y + at.y * picture.height,
		});

		expect(centre(a).x).toBeCloseTo(want(AT).x, -Math.log10(TOLERANCE_PX));
		expect(centre(a).y).toBeCloseTo(want(AT).y, -Math.log10(TOLERANCE_PX));
		expect(centre(b).x).toBeCloseTo(want(MIRRORED).x, -Math.log10(TOLERANCE_PX));
		expect(centre(b).y).toBeCloseTo(want(MIRRORED).y, -Math.log10(TOLERANCE_PX));

		// And the box the dots are placed in IS the picture - which is the whole
		// claim, stated once rather than inferred from two dots.
		expect(pivot.width).toBeCloseTo(picture.width, -Math.log10(TOLERANCE_PX));
		expect(pivot.height).toBeCloseTo(picture.height, -Math.log10(TOLERANCE_PX));
	});
}

// The two contacts are symmetric about the middle of the screen, which is the
// whole shape of the gesture: the midpoint of the pair is the point it zooms
// about, and it does not move as the fingers spread.
test("the pair is symmetric about the middle of the screen", async ({ page }) => {
	await open(page);
	const a = await centreOf(page, "sim-pinch-dot-a");
	const b = await centreOf(page, "sim-pinch-dot-b");
	const picture = await page.getByTestId("pane-article-picture").boundingBox();
	if (!picture) throw new Error("no picture box");

	expect((a.x + b.x) / 2).toBeCloseTo(picture.x + picture.width / 2, 0);
	expect((a.y + b.y) / 2).toBeCloseTo(picture.y + picture.height / 2, 0);
});

// ⚠ The dots sit on whatever the app being driven is showing, so they may not
// take their contrast from the theme - a colour that reads against `--bg` says
// nothing about a white article. Each dot carries its own: a light ring with a
// dark halo outside it and a dark line inside it. This asserts the ring survives
// the theme flipping, because the failure it guards against is somebody
// "tidying" these into theme tokens, at which point one theme goes invisible
// over one kind of content and no other test in the repo notices.
for (const theme of ["dark", "light"] as const) {
	test(`the dot keeps its own contrast in the ${theme} theme`, async ({ page }) => {
		await open(page);
		await page.evaluate((t) => document.documentElement.setAttribute("data-theme", t), theme);
		const style = await page
			.getByTestId("sim-pinch-dot-a")
			.first()
			.evaluate((el) => {
				const s = getComputedStyle(el);
				return { border: s.borderTopColor, shadow: s.boxShadow, width: s.borderTopWidth };
			});
		// A light ring…
		expect(style.border).toMatch(/rgba?\(255, 255, 255/);
		expect(Number.parseFloat(style.width)).toBeGreaterThanOrEqual(1.5);
		// …with dark on both sides of it, so neither a white page nor a black one
		// can swallow the edge.
		expect(style.shadow).toMatch(/rgba\(0, 0, 0/);
		expect(style.shadow).toMatch(/inset/);
	});
}
