import { expect, test, type Page } from "@playwright/test";
import { EXPECTED_SECTION_HEADERS } from "./editor-fixture";

/**
 * Browser-only guards for the Monaco file editor. Every assertion here is a fact
 * jsdom structurally cannot produce: what a canvas painted, and what colour the
 * browser resolved a token to.
 *
 * The two failures this exists to catch both look like success from the DOM:
 *
 *  - a `// MARK:` band whose label Monaco middle-truncated to `User…ction`. The
 *    decoration exists, the canvas is the right size, the band is drawn — only
 *    the text is wrong, and only in canvas pixels. It happens whenever
 *    `minimap.size` is `fit`/`fill` and the file outgrows the minimap canvas,
 *    which silently forces `minimap.scale` back to 1.
 *  - a band on a line that merely MENTIONS `MARK:`. Monaco drops non-comment
 *    hits by reading the line's `StandardTokenType`, which for a shiki-tokenized
 *    model is derived from the scope name `@shikijs/monaco` reverse-maps out of
 *    the token's COLOUR. If the theme's `comment` rule is not the first rule
 *    with the comment colour, every band disappears — or the wrong ones appear —
 *    with nothing logged.
 */

const GALLERY = "/e2e/editor-gallery.html";

/** Widths the editor really gets: full window, one rail open, both rails open. */
const EDITOR_WIDTHS = [1240, 900, 620];

type Painted = { text: string; font: string; canvasWidth: number };

/**
 * Record what the minimap actually painted. Section-header labels are the only
 * `fillText` the minimap does — its character rendering goes through
 * `putImageData` — so the recorded strings are exactly the bands a human sees,
 * ellipsis and all.
 */
async function recordCanvasText(page: Page): Promise<void> {
	await page.addInitScript(() => {
		const painted: Painted[] = [];
		(window as unknown as { __painted: Painted[] }).__painted = painted;
		const original = CanvasRenderingContext2D.prototype.fillText;
		CanvasRenderingContext2D.prototype.fillText = function (this: CanvasRenderingContext2D, ...args) {
			const [text] = args;
			if (typeof text === "string" && text.trim() !== "") {
				painted.push({ text, font: this.font, canvasWidth: this.canvas.width });
			}
			return original.apply(this, args as Parameters<typeof original>);
		};
	});
}

async function openGallery(page: Page, { width, theme }: { width: number; theme?: "dark" | "light" }): Promise<void> {
	await recordCanvasText(page);
	await page.goto(`${GALLERY}?width=${width}${theme ? `&theme=${theme}` : ""}`);
	// Section headers are not a synchronous property of the model: Monaco computes
	// them in the editor worker, then filters the results against the line's token
	// type, so the bands only settle once BOTH the worker has answered and the
	// grammar has tokenized the line. Waiting for the decorations is waiting for
	// that whole chain — polling a fixed delay would make this spec a coin toss.
	await page.waitForFunction((expected) => {
		const monaco = (window as unknown as { __monaco?: typeof import("monaco-editor") }).__monaco;
		const model = monaco?.editor.getEditors()[0]?.getModel();
		if (!model) return false;
		const headers = model.getAllDecorations().filter((d) => d.options.minimap?.sectionHeaderText);
		return headers.length === expected;
	}, EXPECTED_SECTION_HEADERS.length);
	// The minimap paints on the next frame after the decorations change.
	await page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))));
}

/** The section-header labels Monaco decided to band, from the model itself. */
async function sectionHeaderLabels(page: Page): Promise<string[]> {
	return page.evaluate(() => {
		const monaco = (window as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		const model = monaco.editor.getEditors()[0].getModel();
		if (!model) return [];
		return model
			.getAllDecorations()
			.filter((d) => typeof d.options.minimap?.sectionHeaderText === "string")
			.sort((a, b) => a.range.startLineNumber - b.range.startLineNumber)
			.map((d) => d.options.minimap?.sectionHeaderText as string);
	});
}

async function paintedMinimapLabels(page: Page): Promise<string[]> {
	const painted = await page.evaluate(() => (window as unknown as { __painted: Painted[] }).__painted);
	const labels = new Set<string>();
	for (const p of painted) labels.add(p.text);
	return [...labels];
}

test.describe("Monaco file editor", () => {
	test("bands only the real // MARK: markers, never a comment that mentions one", async ({ page }) => {
		await openGallery(page, { width: 1240 });
		expect(await sectionHeaderLabels(page)).toEqual(EXPECTED_SECTION_HEADERS);
	});

	for (const width of EDITOR_WIDTHS) {
		test(`prints every // MARK: label in full at ${width}px`, async ({ page }) => {
			await openGallery(page, { width });
			const labels = await paintedMinimapLabels(page);
			for (const expected of EXPECTED_SECTION_HEADERS) {
				expect(labels, `"${expected}" was not painted at ${width}px (painted: ${labels.join(", ")})`).toContain(
					expected,
				);
			}
			// A middle-truncated label is the failure mode; it always carries the ellipsis.
			expect(labels.filter((l) => l.includes("…"))).toEqual([]);
		});
	}

	test("colours code with the app's own syntax tokens, in both themes", async ({ page }) => {
		await openGallery(page, { width: 1240, theme: "dark" });
		const read = () =>
			page.evaluate(() => {
				const root = document.documentElement;
				const css = (name: string) => getComputedStyle(root).getPropertyValue(name).trim();
				const spans = [...document.querySelectorAll(".view-lines span[class^='mtk']")] as HTMLElement[];
				// Monaco renders spaces as non-breaking spaces inside a view line.
				const find = (text: string) => spans.find((s) => (s.textContent ?? "").replace(/\u00a0/g, " ").includes(text));
				const colourOf = (text: string) => {
					const el = find(text);
					return el ? getComputedStyle(el).color : "";
				};
				const hexToRgb = (hex: string) => {
					const h = hex.replace("#", "");
					const n = parseInt(h.slice(0, 6), 16);
					return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
				};
				return {
					comment: colourOf("MARK: - Lifecycle"),
					expectedComment: hexToRgb(css("--code-comment")),
					keyword: colourOf("func"),
					expectedKeyword: hexToRgb(css("--code-keyword")),
					background: getComputedStyle(document.querySelector(".monaco-editor") as HTMLElement).backgroundColor,
					expectedBackground: hexToRgb(css("--viewer-bg")),
				};
			});

		const dark = await read();
		expect(dark.comment).toBe(dark.expectedComment);
		expect(dark.keyword).toBe(dark.expectedKeyword);

		// The runtime switch: one `setTheme` call, no rebuild, and the editor has to
		// follow the app rather than keep Monaco's own palette. Monaco swaps its
		// generated `.mtkN` rules out and back in, so there is a frame where a token
		// renders at the default foreground — wait for the LIGHT comment colour
		// specifically, or this reads the transient.
		await page.getByTestId("toggle-theme").click();
		await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
		await page.waitForFunction(() => {
			const root = document.documentElement;
			const hex = getComputedStyle(root).getPropertyValue("--code-comment").trim().replace("#", "");
			const n = parseInt(hex.slice(0, 6), 16);
			const expected = `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
			const span = [...document.querySelectorAll(".view-lines span[class^='mtk']")].find((s) =>
				(s.textContent ?? "").replace(/\u00a0/g, " ").includes("MARK: - Lifecycle"),
			);
			return Boolean(span) && getComputedStyle(span as HTMLElement).color === expected;
		});

		const light = await read();
		expect(light.comment).toBe(light.expectedComment);
		expect(light.keyword).toBe(light.expectedKeyword);
		expect(light.comment).not.toBe(dark.comment);
		expect(light.background).toBe(light.expectedBackground);
	});

	// The label is measured in minimap-CANVAS pixels, and both the canvas and the
	// header font scale with devicePixelRatio — so a Retina display is a different
	// fit calculation, not the same one drawn larger. Headless Chromium is 1x by
	// default, which would leave the display the human actually uses unmeasured.
	test("prints every // MARK: label in full on a 2x display", async ({ browser }) => {
		const context = await browser.newContext({ deviceScaleFactor: 2, viewport: { width: 1512, height: 900 } });
		const page = await context.newPage();
		try {
			await openGallery(page, { width: 700 });
			const labels = await paintedMinimapLabels(page);
			for (const expected of EXPECTED_SECTION_HEADERS) expect(labels).toContain(expected);
			expect(labels.filter((l) => l.includes("…"))).toEqual([]);
		} finally {
			await context.close();
		}
	});

	test("lands on the referenced line instead of line 1", async ({ page }) => {
		await openGallery(page, { width: 1240 });
		const centred = await page.evaluate(() => {
			const monaco = (window as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
			const editor = monaco.editor.getEditors()[0];
			const range = editor.getVisibleRanges()[0];
			return { position: editor.getPosition()?.lineNumber, from: range.startLineNumber, to: range.endLineNumber };
		});
		// The harness opens the file at :26, the way a terminal file reference does.
		expect(centred.position).toBe(26);
		expect(centred.from).toBeGreaterThan(1);
		expect(centred.to).toBeGreaterThan(26);
	});

	test("marks uncommitted lines in the gutter", async ({ page }) => {
		await openGallery(page, { width: 1240 });
		await expect(page.locator(".ao-change-bar--modified").first()).toBeVisible();
		await expect(page.locator(".ao-change-bar--added").first()).toBeVisible();
		await expect(page.locator(".ao-change-bar--removed").first()).toBeVisible();
	});
});
