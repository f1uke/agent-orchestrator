import { expect, type Page, test } from "@playwright/test";
import type { NextEditRequest } from "../src/main/inline-completion/next-edit";

/**
 * Next-edit suggestions from a next-edit model (sweep-next-edit), measured as
 * what the editor DRAWS, what the buffer then HOLDS, and what the model was
 * ASKED - the recent change included, since that is what makes it a next-edit
 * model at all.
 *
 * The model is a stub (`editor-gallery-predict-stub.ts`, `?predict=edit`) that
 * carries a rename through the window the way the real one does; the edit
 * history, the provider and Monaco's inline-edit view are the app's own.
 */

const GALLERY = "/e2e/editor-gallery.html?width=1240&line=36&predict=edit";

/**
 * Monaco's next-edit view, through the one part every presentation of it has:
 * the indicator in the gutter beside the line the edit lands on. (Its other
 * parts differ by the kind of edit - a word replacement, an insertion, a
 * side-by-side preview - and most of its elements exist, empty, at all times.)
 */
const inlineEdit = (page: Page) =>
	page.locator(".monaco-editor .inline-edits-view-gutter-indicator .icon").filter({ visible: true });
const ghost = (page: Page) => page.locator(".monaco-editor .ghost-text-decoration, .monaco-editor .ghost-text");

test.beforeEach(async ({ page }) => {
	const errors: string[] = [];
	page.on("pageerror", (err) => errors.push(err.message));
	(page as unknown as { __errors: string[] }).__errors = errors;
});
test.afterEach(async ({ page }) => {
	expect((page as unknown as { __errors: string[] }).__errors).toEqual([]);
});

/** Focus the editor, then select `word` on the first line containing `needle`, through Monaco itself. */
async function selectWord(page: Page, needle: string, word: string): Promise<void> {
	const line = page.locator(".view-lines .view-line", { hasText: needle.replace(/ /g, " ") }).first();
	await expect(line).toBeVisible();
	await line.click();
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
	await page.evaluate(
		([needle, word]) => {
			const m = (globalThis as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
			const editor = m.editor.getEditors()[0];
			const model = editor.getModel();
			if (!model) throw new Error("no model");
			const n = model.getLinesContent().findIndex((l) => l.includes(needle)) + 1;
			const col = model.getLineContent(n).indexOf(word) + 1;
			editor.setSelection(new m.Selection(n, col, n, col + word.length));
		},
		[needle, word] as const,
	);
}

async function lineContaining(page: Page, needle: string): Promise<string> {
	return page.evaluate((needle) => {
		const m = (globalThis as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		const model = m.editor.getModels().find((x) => x.uri.scheme === "ao-file");
		return model?.getLinesContent().find((l) => l.includes(needle)) ?? "";
	}, needle);
}

async function caret(page: Page): Promise<{ lineNumber: number; column: number }> {
	return page.evaluate(() => {
		const m = (globalThis as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		const p = m.editor.getEditors()[0].getPosition();
		return { lineNumber: p?.lineNumber ?? 0, column: p?.column ?? 0 };
	});
}

async function lineNumberOf(page: Page, needle: string): Promise<number> {
	return page.evaluate((needle) => {
		const m = (globalThis as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		const model = m.editor.getModels().find((x) => x.uri.scheme === "ao-file");
		return (model?.getLinesContent().findIndex((l) => l.includes(needle)) ?? -1) + 1;
	}, needle);
}

async function edits(page: Page): Promise<NextEditRequest[]> {
	return page.evaluate(() => (globalThis as { __aoPredictEdits?: NextEditRequest[] }).__aoPredictEdits ?? []);
}

test("a rename is suggested where it lands, on the next line, and Tab applies it", async ({ page }) => {
	await page.goto(GALLERY);
	await selectWord(page, "didSelect(offer", "index");
	await page.keyboard.type("position");

	await expect(inlineEdit(page)).toBeVisible();
	// Drawn as an edit at its own place, not as ghost text after the cursor.
	await expect(ghost(page)).toHaveCount(0);
	// Not in the buffer until accepted.
	expect(await lineContaining(page, "offers.count")).toContain("guard index < offers.count");

	// What the model was asked: the change just made, as one before/after pair.
	const last = (await edits(page)).at(-1);
	expect(last?.recent.at(-1)?.original).toContain("at index: Int");
	expect(last?.recent.at(-1)?.updated).toContain("at position: Int");
	expect(last?.original).toContain("at index: Int");
	expect(last?.current).toContain("at position: Int");

	// One line from the cursor counts as at it: Tab applies at once.
	await page.keyboard.press("Tab");
	await expect.poll(() => lineContaining(page, "offers.count")).toContain("guard position < offers.count");
	expect((await caret(page)).lineNumber).toBe(await lineNumberOf(page, "guard position < offers.count"));
	await expect(inlineEdit(page)).toHaveCount(0);
});

test("an edit further away: the first Tab jumps to it, the second applies it", async ({ page }) => {
	await page.goto(GALLERY);
	await selectWord(page, "func refreshIfNeeded(force: Bool)", "force");
	await page.keyboard.type("forced");
	await expect(inlineEdit(page)).toBeVisible();

	const signature = await lineNumberOf(page, "refreshIfNeeded(forced: Bool)");
	const guard = await lineNumberOf(page, "guard force || age");
	expect(guard - signature).toBe(2);

	// Polled: Tab is handled by Monaco's inline-edit controller, and under load
	// the jump lands a frame after the key - a one-shot read flaked (~1 in 100).
	await page.keyboard.press("Tab");
	// Jumped, not applied.
	await expect.poll(async () => (await caret(page)).lineNumber).toBe(guard);
	expect(await lineContaining(page, "|| age > deadline")).toContain("guard force ||");

	await page.keyboard.press("Tab");
	await expect.poll(() => lineContaining(page, "|| age > deadline")).toContain("guard forced ||");
});

test("a suggestion made for text that has since changed is taken down, not applied", async ({ page }) => {
	// A model slow enough that typing outruns it, as the real one is on a busy
	// machine: the answer to "f" is on screen while "orced" is typed.
	await page.goto(`${GALLERY}&predictDelay=600`);
	await selectWord(page, "func refreshIfNeeded(force: Bool)", "force");
	await page.keyboard.type("f");
	// The answer to "f": rename force to f on the guard line below.
	await expect(inlineEdit(page)).toBeVisible();

	await page.keyboard.type("orced");
	// Monaco would keep that edit through typing that does not touch its range,
	// and Tab would then rename the guard to "f" (CI did, once). It is gone
	// with the first letter typed after it.
	await expect(inlineEdit(page)).toHaveCount(0, { timeout: 300 });
	expect(await lineContaining(page, "|| age > deadline")).toContain("guard force ||");

	// The answer to "forced" arrives and is the one Tab takes.
	await expect(inlineEdit(page)).toBeVisible();
	expect((await edits(page)).at(-1)?.current).toContain("refreshIfNeeded(forced: Bool)");
	const guard = await lineNumberOf(page, "guard force || age");
	await page.keyboard.press("Tab");
	await expect.poll(async () => (await caret(page)).lineNumber).toBe(guard);
	await page.keyboard.press("Tab");
	await expect.poll(() => lineContaining(page, "|| age > deadline")).toContain("guard forced ||");
});

test("Esc dismisses the suggestion and leaves the buffer as typed", async ({ page }) => {
	await page.goto(GALLERY);
	await selectWord(page, "didSelect(offer", "index");
	await page.keyboard.type("position");
	await expect(inlineEdit(page)).toBeVisible();

	await page.keyboard.press("Escape");
	await expect(inlineEdit(page)).toHaveCount(0);
	expect(await lineContaining(page, "offers.count")).toContain("guard index < offers.count");
	// Tab is a plain Tab again.
	const before = await lineContaining(page, "at position: Int");
	await page.keyboard.press("Tab");
	expect(await lineContaining(page, "at position")).not.toBe(before);
	expect(await lineContaining(page, "offers.count")).toContain("guard index < offers.count");
});

test("finishing the line being typed is ghost text from the model's fill-in-the-middle, before any next edit", async ({
	page,
}) => {
	await page.goto(GALLERY.replace("line=36", "line=1"));
	const anchor = page.locator(".view-lines .view-line", { hasText: "super.viewDidLoad()" }).first();
	await expect(anchor).toBeVisible();
	await anchor.click();
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
	await page.keyboard.press("End");
	await page.keyboard.press("Enter");
	await page.keyboard.type("let promo");

	await expect(ghost(page).first()).toContainText("tionTitle");
	await expect(inlineEdit(page)).toHaveCount(0);
	// Answered at the cursor, so the model was never asked to rewrite the window
	// for that keystroke.
	const asked = await page.evaluate(
		() => (globalThis as { __aoPredictAsked?: { prompt: string }[] }).__aoPredictAsked ?? [],
	);
	expect(asked.some((a) => a.prompt.endsWith("let promo"))).toBe(true);
	expect((await edits(page)).some((e) => e.current.includes("let promo\n") || e.current.endsWith("let promo"))).toBe(
		false,
	);
	await page.keyboard.press("Tab");
	expect((await lineContaining(page, "let promo")).trim()).toBe("let promotionTitle = offersTitle");
});

test("a fill-in-the-middle model is never asked for a rewrite, and keeps no edit history", async ({ page }) => {
	await page.goto(GALLERY.replace("predict=edit", "predict=fim"));
	await selectWord(page, "didSelect(offer", "index");
	await page.keyboard.type("position");
	await page.waitForTimeout(300);
	expect(await edits(page)).toEqual([]);
	await expect(inlineEdit(page)).toHaveCount(0);
});
