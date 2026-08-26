import { expect, test, type Page } from "@playwright/test";
import { EDITOR_THEME_TOKENS } from "../src/renderer/lib/monaco-theme";

/**
 * Semantic tokens, measured as COLOURS on screen.
 *
 * 🗝 Every other way of checking this passes while the feature does nothing. The
 * request can be sent and the answer dropped; the provider can be registered and
 * never asked, because Monaco standalone has semantic highlighting off by
 * default and says so nowhere; the tokens can be applied against a theme rule
 * that does not exist, which repaints them with the DEFAULT foreground and looks
 * exactly like nothing happening. All three are silent. So this asks the browser
 * what colour it resolved, on the four cases the mapping is made of.
 *
 * The server is a stub (`editor-gallery-lsp-stub.ts`) sending sourcekit-lsp's
 * own legend; everything from the bridge inwards is the app's real code.
 */

/** Line 7 of the fixture: `final class PromotionHubViewController: UIViewController {`. */
const CLASS_LINE = "final class PromotionHubViewController";
/** Line 8: `private let reuseIdentifier = "promotion-cell"`. */
const PROPERTY_LINE = "private let reuseIdentifier";

const GALLERY = "/e2e/editor-gallery.html?width=1240&lsp=1&line=1";

function rgb(hex: string): string {
	const [r, g, b] = [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16));
	return `rgb(${r}, ${g}, ${b})`;
}

type Span = { text: string; colour: string };

/**
 * The spans Monaco painted for one line, with the colour the BROWSER resolved.
 *
 * Monaco emits one span per token and a class like `mtk12` whose meaning lives
 * in a generated stylesheet, so the class name proves nothing on its own.
 * `getComputedStyle` is what makes each assertion a statement about pixels.
 */
async function spansOfLine(page: Page, contains: string): Promise<Span[]> {
	return page.evaluate((needle) => {
		// Monaco renders every space as a non-breaking one, so a plain substring
		// match against the DOM finds nothing and says only "0 spans".
		const flatten = (text: string) => text.replace(/\u00a0/g, " ");
		const line = [...document.querySelectorAll<HTMLElement>(".view-lines .view-line")].find((l) =>
			flatten(l.textContent ?? "").includes(needle),
		);
		if (!line) return [];
		return [...line.querySelectorAll<HTMLElement>("span span")].map((s) => ({
			text: flatten(s.textContent ?? ""),
			colour: getComputedStyle(s).color,
		}));
	}, contains);
}

/** The colour of the span that is EXACTLY this token, or why there is none. */
function tokenColour(spans: Span[], text: string): string {
	const found = spans.filter((s) => s.text === text);
	if (found.length !== 1) return `expected one span "${text}", found ${found.length} of ${spans.length}`;
	return found[0].colour;
}

/** The colour of whatever run this text sits inside, split out or not. */
function colourAround(spans: Span[], text: string): string {
	return spans.find((s) => s.text.includes(text))?.colour ?? `no span containing "${text}"`;
}

/**
 * Waits for the semantic layer to have landed, not for a fixed delay.
 *
 * `reuseIdentifier` is inside a plain run until the server answers - the
 * grammar emits no scope for a property name at all - so its becoming a span of
 * its own IS the signal, and polling it waits for the whole chain.
 */
async function openWithTokens(page: Page, theme: "dark" | "light" = "dark"): Promise<void> {
	await page.goto(`${GALLERY}${theme === "light" ? "&theme=light" : ""}`);
	await page.locator(".view-lines").first().waitFor();
	await expect
		.poll(async () => tokenColour(await spansOfLine(page, PROPERTY_LINE), "reuseIdentifier"), { timeout: 15_000 })
		.toBe(rgb(EDITOR_THEME_TOKENS[theme]["--code-declaration"]));
}

test.describe("LSP semantic tokens", () => {
	test("the request goes out through the real client", async ({ page }) => {
		await openWithTokens(page);
		const asked = await page.evaluate(() => (window as unknown as { __aoLspAsked: string[] }).__aoLspAsked);
		expect(asked).toContain("textDocument/didOpen");
		expect(asked).toContain("textDocument/semanticTokens/full");
	});

	/**
	 * The headline case, and the one #255 could not fix: a property declaration
	 * the Swift grammar emits no scope for. Plain before, Xcode's
	 * `declaration.other` after.
	 */
	test("a property declaration the grammar left plain gains Xcode's declaration colour", async ({ page }) => {
		await openWithTokens(page);
		const spans = await spansOfLine(page, PROPERTY_LINE);
		expect(tokenColour(spans, "reuseIdentifier")).toBe(rgb(EDITOR_THEME_TOKENS.dark["--code-declaration"]));
	});

	/**
	 * The second thing no grammar can do: tell UIKit's types from yours. The
	 * grammar has `UIViewController` as an inherited class, so it was mint;
	 * Xcode paints every SDK type purple.
	 */
	test("an SDK type is re-tinted to the system colour", async ({ page }) => {
		await openWithTokens(page);
		const spans = await spansOfLine(page, CLASS_LINE);
		expect(tokenColour(spans, "UIViewController")).toBe(rgb(EDITOR_THEME_TOKENS.dark["--code-type-system"]));
	});

	/**
	 * 🗝 And the half that must NOT move. The server calls a type declaration
	 * `identifier`, exactly as it calls a property declaration one - so a mapping
	 * that trusted it blindly would repaint `class PromotionHubViewController`
	 * with the declaration-OTHER colour and lose Xcode's split.
	 */
	test("a type declaration the grammar already knew keeps its own colour", async ({ page }) => {
		await openWithTokens(page);
		const spans = await spansOfLine(page, CLASS_LINE);
		expect(tokenColour(spans, "PromotionHubViewController")).toBe(rgb(EDITOR_THEME_TOKENS.dark["--code-type"]));
	});

	/**
	 * 🗝 Xcode has no operator role and #255 made operators plain to match.
	 * sourcekit-lsp reports `=` as a static SDK method, so this is the assertion
	 * that stops the semantic layer quietly undoing that: the `=` is never split
	 * out, and the run it sits in is still plain.
	 */
	test("an operator stays plain", async ({ page }) => {
		await openWithTokens(page);
		const spans = await spansOfLine(page, PROPERTY_LINE);
		expect(spans.filter((s) => s.text === "=")).toHaveLength(0);
		expect(colourAround(spans, "=")).toBe(rgb(EDITOR_THEME_TOKENS.dark["--code-plain"]));
	});

	/**
	 * Both themes resolve the `ao.*` scopes, from their own palettes. Worth its
	 * own case because the colour→scope map `@shikijs/monaco` keeps is rebuilt on
	 * every `setTheme`, so one theme working proves nothing about the other.
	 */
	test("the light theme paints the same roles from its own palette", async ({ page }) => {
		await openWithTokens(page, "light");
		expect(tokenColour(await spansOfLine(page, CLASS_LINE), "UIViewController")).toBe(
			rgb(EDITOR_THEME_TOKENS.light["--code-type-system"]),
		);
	});
});
