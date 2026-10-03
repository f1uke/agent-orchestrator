import { expect, type Page, test } from "@playwright/test";

/**
 * Ghost text, measured as what the editor RENDERS and what the buffer then
 * HOLDS - plus what was asked of the model, because "no ghost text" is the
 * correct answer in half these cases and silence proves nothing on its own.
 *
 * The model is a stub (`editor-gallery-predict-stub.ts`); the provider, the
 * cache and Monaco's inline-suggest contribution are the app's own.
 */

const GALLERY = "/e2e/editor-gallery.html?width=1240&line=1";
const ANCHOR = "super.viewDidLoad()";

const ghost = (page: Page) =>
	page.locator(
		".monaco-editor .ghost-text-decoration, .monaco-editor .ghost-text-decoration-preview, .monaco-editor .ghost-text",
	);

/**
 * Everything Monaco is drawing as ghost text, joined. With the suggest widget
 * open it is split: the selected row as `ghost-text-decoration`, then what the
 * model adds after it as `ghost-text-decoration-preview` - so asserting on the
 * first element alone reads only the row.
 */
async function ghostText(page: Page): Promise<string> {
	return (await ghost(page).allTextContents()).join("").replace(/\u00a0/g, " ");
}

/** Caret at the end of `needle`'s line, then a fresh line under it. */
async function caretOnNewLineAfter(page: Page, needle: string): Promise<void> {
	const line = page.locator(".view-lines .view-line", { hasText: needle }).first();
	await expect(line).toBeVisible();
	await line.click();
	// Monaco takes focus asynchronously; keystrokes before the handover vanish.
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
	// Monaco's word-based quick suggestions open 10 ms after a keystroke. The
	// stub's ghost text usually lands first, but on a loaded machine the list
	// wins (1 in ~80 runs: "promotion" selected), and with a row selected the
	// model only continues that row - the behaviour the suggest-widget spec
	// below measures, opening its list with a trigger character, which this
	// leaves on. Everything else here measures the ghost text alone.
	await page.evaluate(() => {
		const m = (globalThis as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		for (const editor of m.editor.getEditors()) editor.updateOptions({ quickSuggestions: false });
	});
	await page.keyboard.press("End");
	await page.keyboard.press("Enter");
}

/** The text of the line the caret is on, straight from the model. */
async function caretLine(page: Page): Promise<string> {
	return page.evaluate(() => {
		const m = (globalThis as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		const editor = m.editor.getEditors().find((e) => e.hasTextFocus()) ?? m.editor.getEditors()[0];
		const position = editor.getPosition();
		return position ? (editor.getModel()?.getLineContent(position.lineNumber) ?? "") : "";
	});
}

/**
 * Every spec here also fails on an uncaught error in the page: Monaco's inline
 * suggest leaks a `Canceled` rejection per ghost-text change unless the app
 * swallows it, and a stack trace per keystroke is exactly the noise this feature
 * must not make. jsdom cannot see it; only a real browser does.
 */
test.beforeEach(async ({ page }) => {
	const errors: string[] = [];
	page.on("pageerror", (err) => errors.push(err.message));
	(page as unknown as { __errors: string[] }).__errors = errors;
});
test.afterEach(async ({ page }) => {
	expect((page as unknown as { __errors: string[] }).__errors).toEqual([]);
});

async function asked(page: Page): Promise<{ prompt: string; nPredict?: number }[]> {
	return page.evaluate(
		() => (globalThis as { __aoPredictAsked?: { prompt: string; nPredict?: number }[] }).__aoPredictAsked ?? [],
	);
}

test("ghost text appears after the cursor and Tab accepts it", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=1`);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("let promo");

	await expect(ghost(page).first()).toBeVisible();
	await expect(ghost(page).first()).toContainText("tionTitle");
	// Not in the buffer until accepted.
	expect((await caretLine(page)).trim()).toBe("let promo");

	await page.keyboard.press("Tab");
	await expect.poll(async () => (await caretLine(page)).trim()).toBe("let promotionTitle = offersTitle");
	await expect(ghost(page)).toHaveCount(0);
});

test("Esc dismisses the ghost text and leaves the buffer alone", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=1`);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("let promo");
	await expect(ghost(page).first()).toBeVisible();

	await page.keyboard.press("Escape");
	await expect(ghost(page)).toHaveCount(0);
	expect((await caretLine(page)).trim()).toBe("let promo");
});

test("typing ALONG the ghost text keeps it, from the cache, without asking the model again", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=1`);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("let promo");
	await expect(ghost(page).first()).toContainText("tionTitle");
	const before = (await asked(page)).filter((a) => a.nPredict !== 0).length;

	await page.keyboard.type("ti");
	await expect(ghost(page).first()).toContainText("onTitle");
	// Give a debounced request every chance to be made before counting.
	await page.waitForTimeout(400);
	expect((await asked(page)).filter((a) => a.nPredict !== 0).length).toBe(before);
});

test("typing something else dismisses it", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=1`);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("let promo");
	await expect(ghost(page).first()).toBeVisible();

	await page.keyboard.type("X");
	await expect(ghost(page)).toHaveCount(0);
	expect((await caretLine(page)).trim()).toBe("let promoX");
});

test("Tab still indents when no prediction is showing", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=1`);
	await caretOnNewLineAfter(page, ANCHOR);
	// The model answers nothing for a blank line.
	await page.waitForTimeout(400);
	await expect(ghost(page)).toHaveCount(0);
	const before = await caretLine(page);

	await page.keyboard.press("Tab");
	await expect.poll(async () => (await caretLine(page)).length).toBeGreaterThan(before.length);
	expect((await caretLine(page)).trim()).toBe("");
});

test("with the suggest widget open, Tab belongs to the widget; the model only continues the row", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=1&lsp=1`);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("self.");
	await expect(page.locator(".suggest-widget.visible")).toBeVisible();
	// The widget's selected row, continued by the model, previews as ghost text.
	await expect.poll(() => ghostText(page)).toContain("offersCount += 1");

	await page.keyboard.press("Tab");
	await expect(page.locator(".suggest-widget.visible")).toHaveCount(0);
	// The first Tab took the language server's row - and only the row.
	await expect.poll(async () => (await caretLine(page)).trim()).toBe("self.offersCount");
	// The continuation is already there (from the cache), and a second Tab takes it.
	await expect.poll(() => ghostText(page)).toContain("+= 1");
	await page.keyboard.press("Tab");
	await expect.poll(async () => (await caretLine(page)).trim()).toBe("self.offersCount += 1");
});

test("switched off: no request, no ghost text - the editor exactly as before", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=off`);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("let promo");
	await page.waitForTimeout(500);

	await expect(ghost(page)).toHaveCount(0);
	expect(await asked(page)).toHaveLength(0);
	expect((await caretLine(page)).trim()).toBe("let promo");
	// Tab is plain Tab - asked on a fresh blank line, because after "let promo"
	// Monaco's own word-based suggestions may open at any moment, and Tab
	// accepting THOSE is the editor as it always was, not something to assert on.
	await caretOnNewLineAfter(page, ANCHOR);
	const before = await caretLine(page);
	await page.keyboard.press("Tab");
	await expect.poll(async () => (await caretLine(page)).length).toBeGreaterThan(before.length);
	expect((await caretLine(page)).trim()).toBe("");
	expect(await asked(page)).toHaveLength(0);
});

test("a keystroke supersedes the prediction in flight, which is cancelled", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=1&predictDelay=400`);
	await caretOnNewLineAfter(page, ANCHOR);
	// Slower than the debounce, faster than the model.
	await page.keyboard.type("let promo", { delay: 150 });
	await expect(ghost(page).first()).toContainText("tionTitle", { timeout: 5_000 });
	const cancelled = await page.evaluate(
		() => (globalThis as { __aoPredictCancelled?: string[] }).__aoPredictCancelled ?? [],
	);
	expect(cancelled.length).toBeGreaterThan(0);
});

test("the header chip says the model is ready, and opens its controls", async ({ page }) => {
	await page.goto(`${GALLERY}&predict=1`);
	const chip = page.getByTestId("inline-completion-chip");
	await expect(chip).toBeVisible();
	await expect(chip).toHaveAttribute("data-state-server", "ready");
	await chip.click();
	await expect(page.getByTestId("inline-completion-controls")).toBeVisible();
	await expect(page.getByTestId("inline-completion-status")).toContainText("Ready - Sweep Next-Edit 1.5B");
});

test("where there is no main process, there is no chip at all", async ({ page }) => {
	await page.goto(GALLERY);
	await expect(page.locator(".view-lines")).toBeVisible();
	await expect(page.getByTestId("inline-completion-chip")).toHaveCount(0);
});

test("the prediction icon never pushes Save out of the header", async ({ page }) => {
	// The busiest header there is: a language server's status, the uncommitted
	// count, a dirty buffer - and the icon - at every width around the point
	// where the header starts shedding (COMPACT_WIDTH).
	for (const width of [700, 800, 830, 844, 860, 900, 1000]) {
		await page.goto(`/e2e/editor-gallery.html?width=${width}&line=1&lsp=1&predict=1`);
		await caretOnNewLineAfter(page, ANCHOR);
		await page.keyboard.type("x");
		await expect(page.getByTestId("save-file")).toBeEnabled();
		await expect(page.getByTestId("inline-completion-chip")).toBeVisible();
		const fit = await page.getByTestId("save-file").evaluate((save) => {
			const header = save.parentElement as HTMLElement;
			const s = save.getBoundingClientRect();
			const h = header.getBoundingClientRect();
			return { overflow: header.scrollWidth - header.clientWidth, saveInside: s.right <= h.right + 0.5 };
		});
		expect({ width, ...fit }).toEqual({ width, overflow: 0, saveInside: true });
	}
});
