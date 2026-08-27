import { expect, type Page, test } from "@playwright/test";

/**
 * Diagnostics, hover, find-all-references and peek definition — each measured by
 * what the browser actually RENDERED.
 *
 * 🗝 Every one of these fails by showing nothing, and every cheaper check passes
 * while it does. A marker can be set with the wrong severity and draw no
 * squiggle at all (LSP counts 1..4 down; Monaco's `MarkerSeverity` is a bitmask
 * counting up, and every LSP value is a legal Monaco value). A hover provider
 * can be registered and never asked. A reference provider can answer perfectly
 * and open a peek widget whose preview pane is BLANK, because monaco 0.56's
 * standalone `createModelReference` resolves a model by synchronous lookup and
 * rejects when there is none. None of those logs anything.
 *
 * The server is a stub (`editor-gallery-lsp-stub.ts`) shaped like sourcekit-lsp's
 * real replies — an unsolicited `publishDiagnostics` with no `version`, a
 * `MarkupContent` hover, references spanning two files. Everything from the
 * bridge inwards is the app's own code.
 */

const GALLERY = "/e2e/editor-gallery.html?width=1240&lsp=1&line=1";

/** The word the stub puts an ERROR on, and the one it puts a WARNING on. */
const ERROR_WORD = "page";
const WARNING_WORD = "reuseIdentifier";
/** Declared once and read once in the fixture, which is what makes the reference list legible. */
const REFERENCED_WORD = "offers";

const editor = (page: Page) => page.locator(".monaco-editor").first();
const hoverWidget = (page: Page) => page.locator(".monaco-hover").first();
const peek = (page: Page) => page.locator(".monaco-editor .peekview-widget").first();

/**
 * Click a word in the file and leave the caret on it.
 *
 * The focus wait is not padding. Monaco takes focus ASYNCHRONOUSLY, and a key
 * sent before the handover lands nowhere at all — silently.
 */
/**
 * The viewport point that sits on `word`, in the middle of the word itself.
 *
 * 🗝 A LINE's own bounding box is mostly empty space to the right of the code,
 * so hovering or clicking the line asks the server about a column past the end
 * of the text — which answers nothing, correctly, and looks exactly like a
 * feature nobody wired up. And a word is not reliably its own span: shiki emits
 * one span per TOKEN RUN, so `offers` can arrive inside `offers: [Offer]`. Hence
 * the offset within the span, computed from its own width.
 */
async function findPoint(page: Page, word: string): Promise<{ x: number; y: number } | null> {
	return page.evaluate((needle: string) => {
		for (const line of document.querySelectorAll<HTMLElement>(".view-lines .view-line")) {
			for (const span of line.querySelectorAll<HTMLElement>("span span")) {
				const text = span.textContent ?? "";
				const index = text.indexOf(needle);
				if (index < 0) continue;
				const rect = span.getBoundingClientRect();
				if (rect.width === 0) continue;
				const perCharacter = rect.width / Math.max(1, text.length);
				return { x: rect.left + perCharacter * (index + needle.length / 2), y: rect.top + rect.height / 2 };
			}
		}
		return null;
	}, word);
}

async function pointAt(page: Page, word: string): Promise<{ x: number; y: number }> {
	// Polled, because Monaco renders lines asynchronously and the shiki grammar
	// arrives later still — a single lookup would race the tokenizer and fail
	// with "no rendered span", which says nothing about the feature under test.
	await expect.poll(async () => (await findPoint(page, word)) !== null, { timeout: 15_000 }).toBe(true);
	return (await findPoint(page, word)) as { x: number; y: number };
}

/**
 * Hover a word until the widget says what it should.
 *
 * Re-hovering is not padding either: Monaco starts its 150 ms timer on a move
 * that CHANGES the target, and a first hover can land while the server is still
 * paying for the file's first type-check — 1 919 ms on a cold Swift file — which
 * is the ordinary case this feature has to survive, not a flake.
 */
async function expectHover(page: Page, word: string, text: string): Promise<void> {
	await expect
		.poll(
			async () => {
				await hoverWord(page, word);
				await page.waitForTimeout(400);
				const widget = page.locator(".monaco-hover").first();
				return (await widget.count()) > 0 ? await widget.innerText() : "";
			},
			{ timeout: 15_000 },
		)
		.toContain(text);
}

async function clickWord(page: Page, word: string): Promise<void> {
	const { x, y } = await pointAt(page, word);
	await page.mouse.click(x, y);
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
}

async function hoverWord(page: Page, word: string): Promise<void> {
	const { x, y } = await pointAt(page, word);
	// Two moves: Monaco's hover only starts its timer on a move that CHANGES the
	// target, and a single move onto a point the pointer is notionally already at
	// starts nothing.
	await page.mouse.move(x - 40, y);
	await page.mouse.move(x, y);
}

test.describe("diagnostics", () => {
	test("an unsolicited publish becomes a squiggle, a ruler mark and a header count", async ({ page }) => {
		await page.goto(GALLERY);
		await expect(editor(page)).toBeVisible();

		// 🗝 On the SQUIGGLE's own class, not on "a decoration exists". `squiggly-error`
		// is what Monaco draws for `MarkerSeverity.Error`; a severity mapped by a
		// naive cast would land on Hint, which draws nothing, and every other check
		// here would still pass.
		await expect(page.locator(".squiggly-error").first()).toBeVisible();
		await expect(page.locator(".squiggly-warning").first()).toBeVisible();

		// The header says how many, and says it only because there ARE some.
		const problems = page.getByTestId("lsp-problems");
		await expect(problems).toHaveText("1⨯ 1⚠");

		// The overview ruler carries them too — the same `setModelMarkers` call, and
		// the only part of the file a reader can see without scrolling to it.
		await expect(page.locator(".decorationsOverviewRuler")).toBeVisible();
	});

	/**
	 * The band Xcode draws, and the one thing `setModelMarkers` does NOT give:
	 * every marker rendering is bounded by the marker's own range, so a reader
	 * asking for a tinted LINE gets a tinted TOKEN unless a decoration is laid
	 * beside the marker.
	 */
	test("the whole line is tinted, not just the token under the squiggle", async ({ page }) => {
		await page.goto(GALLERY);
		await expect(page.locator(".squiggly-error").first()).toBeVisible();

		const band = page.locator(".ao-diagnostic-line--error").first();
		await expect(band).toBeVisible();
		await expect(page.locator(".ao-diagnostic-line--warning").first()).toBeVisible();

		// 🗝 On the WIDTH, because the failure mode is a band that renders and is
		// merely the size of the squiggle - which looks deliberate and answers a
		// different request. The band has to be several times the token it covers.
		const squiggle = await page.locator(".squiggly-error").first().boundingBox();
		const tinted = await band.boundingBox();
		expect(squiggle, "no squiggle to compare against").not.toBeNull();
		expect(tinted, "no band to measure").not.toBeNull();
		expect(tinted!.width).toBeGreaterThan((squiggle?.width ?? 0) * 4);

		// …and on the same line as the squiggle, not merely somewhere in the file.
		expect(Math.abs(tinted!.y - (squiggle?.y ?? 0))).toBeLessThan(tinted!.height);
	});

	/**
	 * 🗝 The density rule, through the real editor rather than through the mapper
	 * alone. `?lspWarnEvery=1` puts a warning on EVERY code line - including the
	 * one that already carries the error - which is the shape the human's own
	 * 3 284-warning screenshot has. Two things have to hold: translucent bands
	 * must not stack into an opaque slab, and the error must still read as an
	 * error on the line it shares with a warning.
	 */
	test("a warning-heavy file draws one band per line, and the error still wins its own", async ({ page }) => {
		await page.goto(`${GALLERY}&lspWarnEvery=1`);
		await expect(page.locator(".squiggly-error").first()).toBeVisible();
		await expect(page.locator(".ao-diagnostic-line").first()).toBeVisible();

		// Exactly one band on the error's line, and it is the error's.
		await expect(page.locator(".ao-diagnostic-line--error")).toHaveCount(1);

		// No two bands share a line. Measured off the rendered tops, because that
		// is what "stacked" would actually look like.
		const tops = await page
			.locator(".ao-diagnostic-line")
			.evaluateAll((nodes) => nodes.map((node) => Math.round((node as HTMLElement).getBoundingClientRect().top)));
		expect(tops.length).toBeGreaterThan(5);
		expect(new Set(tops).size).toBe(tops.length);
	});

	test("the message is in the hover, with the server's own source name", async ({ page }) => {
		await page.goto(GALLERY);
		await expect(page.locator(".squiggly-error").first()).toBeVisible();
		// Monaco's `markerHoverParticipant` renders this from the marker — nothing
		// in this app draws it. Which is exactly why it is worth asserting: it only
		// appears if the marker's RANGE landed on the word the server named.
		await expectHover(page, ERROR_WORD, "cannot find type 'Paginator' in scope");
	});

	// 🗝 gopls's first publish after a file opens is EMPTY and lands ~932 ms before
	// the real one. A header that rendered zero as "no problems" would be lying
	// for four seconds — so the count exists only when there is something to
	// count, and this is the window in which that is visible.
	test("before the first publish the header says nothing at all", async ({ page }) => {
		await page.goto(`${GALLERY}&lspDiagnosticsDelay=3000`);
		await expect(editor(page)).toBeVisible();
		await expect(page.getByTestId("lsp-problems")).toHaveCount(0);
		// …and the status pill is still the only thing making a claim about the
		// server, which is slice 3's vocabulary and this slice adds no fourth state.
		await expect(page.getByTestId("lsp-status")).toBeVisible();
	});
});

test.describe("hover", () => {
	test("the type under the pointer is rendered from the server's answer", async ({ page }) => {
		await page.goto(GALLERY);
		await expect(editor(page)).toBeVisible();
		// On what the widget SAYS. A hover that opened with only the marker message,
		// or with Monaco's own word-based nothing, would also be "visible".
		await expectHover(page, REFERENCED_WORD, "PromotionOffer");
	});

	// 🗝 Monaco asks the provider 150 ms after the pointer COMES TO REST
	// (`hoverOperation.js`: `_firstWaitTime = editor.hover.delay / 2`, and the
	// delay defaults to 300 ms) — never while it is moving. So a sweep across a
	// line does not produce a request per pixel, and this is the assertion that
	// says so rather than trusting the source reading.
	test("a pointer sweep does not put one request on the wire per position", async ({ page }) => {
		await page.goto(GALLERY);
		await expect(editor(page)).toBeVisible();
		const { x, y } = await pointAt(page, REFERENCED_WORD);
		// Twelve positions across the line, faster than the hover delay, ending on
		// the word so there is an answer to wait for.
		for (let i = 11; i >= 0; i--) await page.mouse.move(x - i * 8, y);
		await expect(hoverWidget(page)).toBeVisible();
		const hovers = await page.evaluate(
			() =>
				((globalThis as { __aoLspAsked?: string[] }).__aoLspAsked ?? []).filter((m) => m === "textDocument/hover")
					.length,
		);
		// Far below one per position. The exact number depends on how the pointer
		// happens to rest; what is being pinned is that it is not twelve.
		expect(hovers).toBeLessThan(6);
	});

	test("a server that offers no hover says so once, and never asks", async ({ page }) => {
		const warnings: string[] = [];
		page.on("console", (message) => {
			if (message.type() === "warning") warnings.push(message.text());
		});
		await page.goto(`${GALLERY}&lspNoHover=1`);
		await expect(editor(page)).toBeVisible();
		// Re-hovering inside the poll, for the same reason `expectHover` does it:
		// Monaco starts its 150 ms timer only on a move that CHANGES the target, so
		// a single hover that lands while the tokenizer is still moving spans under
		// the pointer asks nothing and is never retried. The sibling tests are
		// covered by waiting on the widget; this one has no widget to wait for —
		// the whole point is that nothing opens — so the retry has to be here.
		await expect
			.poll(
				async () => {
					await hoverWord(page, REFERENCED_WORD);
					await page.waitForTimeout(400);
					return warnings.filter((w) => w.includes("offers no hover")).length;
				},
				{ timeout: 15_000 },
			)
			.toBeGreaterThan(0);
		const hovers = await page.evaluate(
			() =>
				((globalThis as { __aoLspAsked?: string[] }).__aoLspAsked ?? []).filter((m) => m === "textDocument/hover")
					.length,
		);
		expect(hovers).toBe(0);
	});
});

test.describe("peek", () => {
	/**
	 * 🗝 THE assertion of this slice. The peek widget opens whether or not a model
	 * exists for the target, and Monaco degrades the tree ROW to `File.swift:9:22`
	 * on its own — so "the widget opened" and "there are rows" both pass while the
	 * preview pane is empty. The only proof is source text from the OTHER file
	 * appearing inside the widget.
	 */
	test("peek definition shows the target file's source, not an empty pane", async ({ page }) => {
		await page.goto(GALLERY);
		await clickWord(page, REFERENCED_WORD);
		// Monaco's own command, from the barrel import. Nothing in this app
		// registers it, and the point is that nothing had to.
		await page.evaluate(() => {
			const monaco = (globalThis as { __monaco?: { editor: { getEditors(): unknown[] } } }).__monaco;
			const editors = monaco?.editor.getEditors() as { trigger: (s: string, id: string, a?: unknown) => void }[];
			editors[0].trigger("e2e", "editor.action.peekDefinition");
		});

		await expect(peek(page)).toBeVisible();
		// The file's name in the widget header, and its CONTENTS in the preview.
		await expect(peek(page)).toContainText("OfferStore.swift");
		await expect(peek(page).locator(".view-lines")).toContainText("final class OfferStore");
	});

	test("find all references lists every hit, and previews the file the reader is not in", async ({ page }) => {
		await page.goto(GALLERY);
		await clickWord(page, REFERENCED_WORD);
		await page.evaluate(() => {
			const monaco = (globalThis as { __monaco?: { editor: { getEditors(): unknown[] } } }).__monaco;
			const editors = monaco?.editor.getEditors() as { trigger: (s: string, id: string, a?: unknown) => void }[];
			editors[0].trigger("e2e", "editor.action.referenceSearch.trigger");
		});

		await expect(peek(page)).toBeVisible();
		// Two files in the tree, which is what the stub answered with, and the
		// widget says how many hits in total.
		await expect(peek(page)).toContainText("References (3)");
		await expect(peek(page)).toContainText("OfferStore.swift");
		// The current file's rows carry their source, which they could get from the
		// pane's own model. The interesting one is the other file, whose group the
		// tree only resolves when it is opened.
		await expect(peek(page)).toContainText("index < offers.count");
		await page.locator(".monaco-list-row", { hasText: "OfferStore.swift" }).first().click();

		// 🗝 THE assertion. Monaco renders an unresolvable row as
		// `OfferStore.swift:9:22` (`referencesTree.js:155`) — legible, and proof of
		// nothing — and leaves the preview pane blank. Only a materialised model
		// produces the code itself, from a file that has no pane anywhere.
		// The text is trimmed to a window around the match, which is Monaco's own
		// preview and not something to fight — `set)` is the part of that window
		// only the OTHER file can have supplied.
		await expect(peek(page)).toContainText("set) var offers: [Offer] = []");
		await expect(peek(page)).not.toContainText("OfferStore.swift:9:22");
	});

	test("a server that offers no reference search says so at the cursor", async ({ page }) => {
		await page.goto(`${GALLERY}&lspNoReferences=1`);
		await clickWord(page, REFERENCED_WORD);
		await page.evaluate(() => {
			const monaco = (globalThis as { __monaco?: { editor: { getEditors(): unknown[] } } }).__monaco;
			const editors = monaco?.editor.getEditors() as { trigger: (s: string, id: string, a?: unknown) => void }[];
			editors[0].trigger("e2e", "editor.action.referenceSearch.trigger");
		});
		// Monaco's own `MessageController` — the widget it uses for "no definition
		// found for 'x'". No new vocabulary, rendered where the reader is looking.
		await expect(page.locator(".monaco-editor-overlaymessage")).toContainText("offers no reference search");
	});
});

test.describe("what the server actually holds", () => {
	// 🗝 `document-sync.ts` is the single owner of "what text does the server
	// have". A hover answered against the SAVED text would name the type of a
	// word that is no longer under the pointer — silently, because every offset
	// is still in range. The stub reads the LIVE model, so this only passes if
	// the position sent matches the buffer on screen.
	test("hover after an edit is about the edited buffer", async ({ page }) => {
		await page.goto(GALLERY);
		const line = page.locator(".view-lines .view-line", { hasText: WARNING_WORD }).first();
		await expect(line).toBeVisible();
		await line.click();
		await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
		await page.keyboard.press("End");
		await page.keyboard.press("Enter");
		await page.keyboard.type("    let neverSavedName = 1");

		await expectHover(page, "neverSavedName", "let neverSavedName: PromotionOffer");
	});
});
