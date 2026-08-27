import { expect, type Page, test } from "@playwright/test";

/**
 * Autocompletion, measured as ROWS IN THE WIDGET and as requests on the wire.
 *
 * 🗝 Every cheaper check passes while the feature does nothing. The provider can
 * be registered and never asked, because Monaco reads `triggerCharacters` once
 * at registration and a provider registered before the server attached has none.
 * The request can be sent and its answer dropped. The items can come back and be
 * mapped to the wrong `CompletionItemKind`, which renders a full widget with the
 * wrong icon on every row. All three are silent. So this asks the browser what
 * it actually rendered, and the stub what was actually asked of it.
 *
 * The server is a stub (`editor-gallery-lsp-stub.ts`) shaped like sourcekit-lsp's
 * real replies; everything from the bridge inwards is the app's own code.
 */

const GALLERY = "/e2e/editor-gallery.html?width=1240&lsp=1&line=1";
/** Where the caret goes: inside `viewDidLoad`, after the super call. */
const ANCHOR = "super.viewDidLoad()";

const widget = (page: Page) => page.locator(".suggest-widget.visible");
const rows = (page: Page) => widget(page).locator(".monaco-list-row");

/**
 * Put the caret at the end of a line and open a fresh line under it.
 *
 * The focus wait is not padding. Monaco takes focus ASYNCHRONOUSLY and every
 * keystroke sent before the handover lands nowhere at all - silently, with the
 * failure surfacing much later somewhere unrelated.
 */
async function caretOnNewLineAfter(page: Page, needle: string): Promise<void> {
	const line = page.locator(".view-lines .view-line", { hasText: needle }).first();
	await expect(line).toBeVisible();
	await line.click();
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
	await page.keyboard.press("End");
	await page.keyboard.press("Enter");
}

/** What the stub was asked, and how much of it was on the wire at once. */
async function wire(page: Page): Promise<{ completions: number; peak: number }> {
	return page.evaluate(() => {
		const asked = (globalThis as { __aoLspAsked?: string[] }).__aoLspAsked ?? [];
		const w = (globalThis as { __aoLspWire?: { peak: number } }).__aoLspWire;
		return { completions: asked.filter((m) => m === "textDocument/completion").length, peak: w?.peak ?? 0 };
	});
}

async function labels(page: Page): Promise<string[]> {
	return rows(page).locator(".monaco-icon-name-container").allInnerTexts();
}

test("a trigger character opens the widget with the server's own members", async ({ page }) => {
	await page.goto(GALLERY);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("self.");

	await expect(widget(page)).toBeVisible();
	// On WHAT was rendered. A widget that opened with Monaco's own word-based
	// suggestions would also be "visible", and would prove nothing.
	await expect
		.poll(async () => (await labels(page)).sort())
		.toEqual(["configure(userDefaultManager:)", "offersCount", "offersTitle"]);

	// 🗝 The trigger character reached the provider at all, which is the failure
	// mode registering before the capability arrives would have produced.
	expect((await wire(page)).completions).toBeGreaterThan(0);
});

test("the icons come from the TRANSLATED kind, not from LSP's numbers", async ({ page }) => {
	await page.goto(GALLERY);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("self.");
	await expect(widget(page)).toBeVisible();
	await expect.poll(async () => (await labels(page)).length).toBe(3);

	// The stub sends LSP kinds: 10 = Property, 2 = Method. Monaco's own enum has
	// Property at 9 and Method at 0, so a cast would render `Event` and
	// `Constructor` here - a full, plausible, entirely wrong widget.
	const iconOf = async (label: string) =>
		rows(page).filter({ hasText: label }).first().locator(".suggest-icon").getAttribute("class");
	expect(await iconOf("offersCount")).toContain("codicon-symbol-property");
	expect(await iconOf("configure(userDefaultManager:)")).toContain("codicon-symbol-method");
});

test("typing more RE-ASKS the server, and surfaces an item the first answer did not carry", async ({ page }) => {
	await page.goto(GALLERY);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("self.");
	await expect(widget(page)).toBeVisible();
	await expect.poll(async () => (await labels(page)).length).toBe(3);
	const before = await wire(page);

	await page.keyboard.type("offers");

	// 🗝 THE assertion of this slice. `offersDeepCut` is absent from the answer
	// for the bare `.`, exactly as sourcekit-lsp's 200-item cap hides items until
	// the prefix narrows (measured: 6 of the 9 items for `emailLabel.numb` are
	// absent from the list for `emailLabel.`). A client that filtered its previous
	// list locally - the tempting way to dodge the latency - could not produce
	// this row by any means.
	await expect.poll(async () => (await labels(page)).includes("offersDeepCut")).toBe(true);
	expect((await wire(page)).completions).toBeGreaterThan(before.completions);
});

test("typing faster than the server keeps exactly one request on the wire", async ({ page }) => {
	// 600 ms is between the measured first completion in an already-type-checked
	// Swift file (400 ms) and a cold one (1 019-1 333 ms). Typing through that
	// window is the case the policy exists for.
	await page.goto(`${GALLERY}&lspDelay=600`);
	await caretOnNewLineAfter(page, ANCHOR);
	// 🗝 Trigger CHARACTERS, not ordinary letters, and that distinction is the
	// whole measurement. While a request is out Monaco does not re-ask for the
	// incomplete list - it waits for the model - so plain typing never shows this.
	// A trigger character calls `trigger()`, which CANCELS the pending call and
	// starts a new one, and a provider that sends on every call then has five
	// requests in the air at once. Measured here: peak 5 without the policy.
	await page.keyboard.type("self.a.b.c.d.", { delay: 40 });
	await page.waitForTimeout(3_000);

	const seen = await wire(page);
	// One at a time, and never cancelled. Measured against the real server, the
	// textbook alternative - a request per keystroke with `$/cancelRequest` -
	// took 2 172-2 422 ms to an answer against 2 ms for this, because each
	// cancellation threw away the type-check the next request had to redo.
	expect(seen.peak, "more than one completion request was in flight").toBe(1);
	// And the burst did not become one request per trigger character.
	expect(seen.completions).toBeLessThanOrEqual(2);
});

test("a burst of keystrokes does not become a burst of requests", async ({ page }) => {
	await page.goto(`${GALLERY}&lspDelay=400`);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("self.");
	await page.keyboard.type("offersDe", { delay: 0 });

	await expect(widget(page)).toBeVisible();
	await expect.poll(async () => (await labels(page)).includes("offersDeepCut"), { timeout: 15_000 }).toBe(true);

	const seen = await wire(page);
	expect(seen.peak).toBe(1);
	// Nine keystrokes, two requests: Monaco holds the re-ask for the incomplete
	// list until the pending one has answered, and the provider then sends only
	// the newest prefix.
	expect(seen.completions).toBeLessThanOrEqual(3);
});

test("accepting a snippet inserts the expansion, not ${1:} as text", async ({ page }) => {
	await page.goto(GALLERY);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("self.conf");
	await expect(widget(page)).toBeVisible();
	await expect.poll(async () => (await labels(page)).includes("configure(userDefaultManager:)")).toBe(true);
	await page.keyboard.press("Enter");

	const line = await page.evaluate(() => {
		const monaco = (globalThis as { __monaco?: { editor: { getModels(): { getValue(): string }[] } } }).__monaco;
		return (
			monaco?.editor
				.getModels()[0]
				?.getValue()
				.split("\n")
				.find((l) => l.includes("configure(")) ?? ""
		);
	});
	// The placeholder became selected text, which is what a snippet is. Without
	// `InsertAsSnippet` the buffer would hold the literal `${1:…}`.
	expect(line).toContain("configure(userDefaultManager: any UserDefaultManagerProtocol)");
	expect(line).not.toContain("${1:");
});

test("documentation is fetched only for the row being looked at", async ({ page }) => {
	await page.goto(GALLERY);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.type("self.");
	await expect(widget(page)).toBeVisible();
	await expect.poll(async () => (await labels(page)).length).toBe(3);

	const resolves = () =>
		page.evaluate(
			() =>
				((globalThis as { __aoLspAsked?: string[] }).__aoLspAsked ?? []).filter((m) => m === "completionItem/resolve")
					.length,
		);
	// One row is highlighted, so at most one row has been resolved - not all three.
	expect(await resolves()).toBeLessThanOrEqual(1);

	await page.keyboard.press("Control+Space");
	await page.locator(".suggest-details-container .monaco-scrollable-element").first().waitFor({ state: "attached" });
	await expect.poll(async () => await resolves()).toBeGreaterThan(0);
});

test("with no server at all, an explicit ⌃Space says why instead of 'No suggestions'", async ({ page }) => {
	await page.goto(`${GALLERY}&lspFail=sourcekit-lsp is not on PATH`);
	await caretOnNewLineAfter(page, ANCHOR);
	await page.keyboard.press("Control+Space");

	// 🗝 Monaco's own message widget - the one it uses for "no definition found".
	// Six silent failures have been paid for on this feature; "No suggestions",
	// which is what a provider returning an empty list would render, is
	// indistinguishable from a type that genuinely has no members.
	const message = page.locator(".monaco-editor-overlaymessage .message");
	await expect(message).toBeVisible();
	await expect(message).toHaveText(/sourcekit-lsp is not on PATH/);

	// And the pill above says the same thing, in slice 3's words.
	await expect(page.getByTestId("lsp-status")).toHaveText(/no language server/i);
});
