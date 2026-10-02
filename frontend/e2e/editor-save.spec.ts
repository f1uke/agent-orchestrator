import { expect, test, type Page } from "@playwright/test";

/**
 * The save path, in a real browser, driven the way a person drives it.
 *
 * This spec is only possible because the file viewer now has a
 * `VITE_NO_ELECTRON` branch: before that this surface rendered a header and
 * then nothing under `dev:web`, so nothing here could be reached at all.
 *
 * What is browser-only about it, and therefore why it is not a jsdom test:
 * every assertion below depends on Monaco actually holding a buffer — typing
 * into its edit context, an edit reaching the model, the mode switch swapping
 * one editor for another over the SAME model. jsdom has no Monaco, so the unit
 * tests stub it and can only check the contract the chrome hands it.
 */

const SESSION = "/#/projects/ao-demo/sessions/demo-working";
/** The mock path whose save always answers 409, so the conflict flow is reachable. */
const CONFLICTING = "workspace_changes";
const ORDINARY = "FilesPanel";

async function openFile(page: Page, query: string): Promise<void> {
	await page.goto(SESSION);
	await expect(page.locator("#inspector")).toBeVisible();
	await page.locator("body").click({ position: { x: 400, y: 400 } });
	await page.keyboard.press("Meta+Shift+KeyO");
	await page.getByLabel("Open Quickly: search files by name").fill(query);
	await page.keyboard.press("Enter");
	await expect(page.getByTestId("monaco-file-editor")).toBeVisible();
}

/**
 * Open another file WITHOUT reloading the page.
 *
 * `openFile` navigates, which wipes the react-query cache — fine when each case
 * wants a cold start, and fatal to any case whose point is that something was
 * already cached.
 */
async function reopenFile(page: Page, query: string): Promise<void> {
	await page.locator("body").click({ position: { x: 400, y: 400 } });
	await page.keyboard.press("Meta+Shift+KeyO");
	await page.getByLabel("Open Quickly: search files by name").fill(query);
	await page.keyboard.press("Enter");
	await expect(page.getByTestId("monaco-file-editor")).toBeVisible();
}

/**
 * Put the caret in the buffer and type, which is the only way to make it dirty.
 *
 * The wait is not padding. Monaco takes focus ASYNCHRONOUSLY: the click hands it
 * to the view's edit context a tick or more later (`view.js` only adds `focused`
 * to `.monaco-editor` once `_editContext.isFocused()`), and every keystroke sent
 * before that handover lands nowhere at all. The buffer stays clean, `save-file`
 * stays disabled, and the failure surfaces 30 seconds later as a click timing out
 * on a disabled button — nowhere near the typing that actually went missing.
 *
 * And the BUFFER has to be there first. The editor mounts, and can take focus,
 * before its model is attached (that waits on the grammar), so a click and a
 * burst of typing can land in an editor with no model at all - then the model
 * arrives, the caret sits at 1:1, and nothing was typed. Measured: about one run
 * in five once the editor did a little more work at mount.
 */
async function typeIntoEditor(page: Page, text: string): Promise<void> {
	await expect(page.locator(".monaco-editor .view-lines").first()).toContainText(/\S/);
	await page.locator(".monaco-editor .view-lines").first().click();
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
	await page.keyboard.type(text);
}

test("an in-workspace file is editable, and saving clears the dirty state", async ({ page }) => {
	await openFile(page, ORDINARY);
	const host = page.getByTestId("monaco-file-editor");
	await expect(host).toHaveAttribute("data-editable", "true");

	const save = page.getByTestId("save-file");
	await expect(save).toBeDisabled();

	await typeIntoEditor(page, "// typed by a person");
	await expect(save).toBeEnabled();
	await expect(page.getByLabel("unsaved changes")).toBeVisible();

	await save.click();
	await expect(save).toBeDisabled();
	await expect(page.getByLabel("unsaved changes")).toBeHidden();
	await expect(page.getByTestId("save-failure")).toBeHidden();
});

// 🗝 The flow this slice exists to get right. An AO worktree has agents writing
// in it, so the 409 is the normal case — and the only way past it is to have
// looked at what changed.
test("a conflicting save offers a comparison, not an error", async ({ page }) => {
	await openFile(page, CONFLICTING);
	await typeIntoEditor(page, "// my edit");
	await page.getByTestId("save-file").click();

	const banner = page.getByTestId("file-drift-banner");
	await expect(banner).toBeVisible();
	await expect(banner).toContainText(/changed on disk/i);
	// Nothing was written and nothing was lost: the edit is still there to save.
	await expect(page.getByTestId("save-file")).toBeEnabled();
	await expect(page.getByTestId("save-failure")).toBeHidden();

	await page.getByRole("button", { name: /review changes/i }).click();
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "diff");
	await expect(page.getByText(/^On disk/)).toBeVisible();
	// Two real editors now, over one buffer: the reader can still type.
	await expect(page.locator(".monaco-diff-editor")).toBeVisible();

	// Saving from the comparison succeeds, because it preconditions on the
	// version just shown rather than the stale one.
	await page.getByTestId("save-file").click();
	await expect(banner).toBeHidden();
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "code");
});

test("discarding an edit is a two-step gesture that asks first", async ({ page }) => {
	await openFile(page, CONFLICTING);
	await typeIntoEditor(page, "// my edit");
	await page.getByTestId("save-file").click();
	await expect(page.getByTestId("file-drift-banner")).toBeVisible();

	const discard = page.getByRole("button", { name: /discard mine and reload/i });
	await discard.click();
	// One click only ARMS it. A one-click destroy beside a primary button is how
	// unsaved work gets lost.
	await expect(page.getByRole("button", { name: /really discard my edits/i })).toBeVisible();
	await page.getByRole("button", { name: /really discard my edits/i }).click();
	await expect(page.getByTestId("file-drift-banner")).toBeHidden();
	// 🗝 And the edit is actually GONE. Hiding the banner was all this used to
	// do: the typed text stayed in the buffer under a header that called it
	// clean, with Save disabled - work neither saved nor discarded.
	await expect(page.locator(".monaco-editor .view-lines").first()).not.toContainText("// my edit");
	await expect(page.getByLabel("unsaved changes")).toBeHidden();
	await expect(page.getByTestId("save-file")).toBeDisabled();
});

/** The glyph-margin node and line number of buffer line `n`, read off the paint. */
async function gutterOf(page: Page, n: number) {
	return page.evaluate((line) => {
		const editor = document.querySelector(".monaco-editor") as HTMLElement;
		const numbers = [...editor.querySelectorAll<HTMLElement>(".line-numbers")];
		const number = numbers.find((node) => node.textContent?.trim() === String(line));
		if (!number) return null;
		const top = number.parentElement?.style.top ?? (number as HTMLElement).style.top;
		const glyph = [...editor.querySelectorAll<HTMLElement>(".ao-gutter-lane")].find((node) => node.style.top === top);
		const rect = glyph?.getBoundingClientRect();
		return {
			glyph: glyph?.className ?? "",
			unsaved: number.classList.contains("ao-unsaved-line"),
			// Where the uncommitted bar is drawn (::after, 9px in), for a real click.
			bar: rect ? { x: rect.left + 10, y: rect.top + rect.height / 2 } : null,
		};
	}, n);
}

test("both git lanes are drawn coloured by kind, in two columns that never fuse", async ({ page }) => {
	await openFile(page, ORDINARY);
	await expect(page.locator(".ao-branch-bar").first()).toBeVisible();
	await expect(page.locator(".ao-change-bar").first()).toBeVisible();

	// 🗝 Asserted on the PAINT, not on class names. The glyph margin holds one
	// node per line, so Monaco concatenates both lanes' classes onto it - the two
	// bars are ::before and ::after of the same element, and only their computed
	// styles say whether both drew, in which colour and where.
	const painted = await page
		.locator(".ao-branch-bar--modified.ao-change-bar--modified")
		.first()
		.evaluate((node) => {
			const before = getComputedStyle(node, "::before");
			const after = getComputedStyle(node, "::after");
			return {
				branch: { colour: before.backgroundColor, left: parseFloat(before.left), width: parseFloat(before.width) },
				uncommitted: { colour: after.backgroundColor, left: parseFloat(after.left), width: parseFloat(after.width) },
			};
		});
	expect(painted.branch.width).toBeGreaterThan(0);
	expect(painted.uncommitted.width).toBeGreaterThan(0);
	// A visible gap between the columns: two same-kind bars touching read as ONE
	// thick bar, which is how the branch lane once ended up a kindless grey.
	expect(painted.uncommitted.left - (painted.branch.left + painted.branch.width)).toBeGreaterThanOrEqual(2);
	// The branch lane is the softer of the two, so the pair still reads as two.
	expect(painted.branch.colour).not.toBe(painted.uncommitted.colour);

	// Coloured BY KIND: an added line and a modified line differ in both lanes.
	const inks = await page.evaluate(() => {
		const ink = (selector: string, pseudo: string) => {
			const node = document.querySelector(selector);
			return node ? getComputedStyle(node, pseudo).backgroundColor : null;
		};
		return {
			branchAdded: ink(".ao-branch-bar--added", "::before"),
			branchModified: ink(".ao-branch-bar--modified", "::before"),
			changeAdded: ink(".ao-change-bar--added", "::after"),
			changeModified: ink(".ao-change-bar--modified", "::after"),
		};
	});
	expect(inks.branchAdded).not.toBeNull();
	expect(inks.branchAdded).not.toBe(inks.branchModified);
	expect(inks.changeAdded).not.toBe(inks.changeModified);
});

// 🗝 The 2026-10-02 report: a line typed and not yet saved had no mark at all,
// because every lane was measured against the file ON DISK.
test("a typed line is marked at once - in both git lanes and as unsaved - and saving clears only the unsaved mark", async ({
	page,
}) => {
	await openFile(page, ORDINARY);
	await expect(page.locator(".ao-change-bar").first()).toBeVisible();
	// Line 6 is untouched in the fixture; open a new line under it.
	await page.locator(".monaco-editor .view-line").nth(5).click();
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
	await page.keyboard.press("End");
	await page.keyboard.press("Enter");
	await page.keyboard.type("const typedButNotSaved = true;");

	await expect.poll(() => gutterOf(page, 7)).toMatchObject({ unsaved: true });
	const typed = await gutterOf(page, 7);
	expect(typed?.glyph).toContain("ao-branch-bar--added");
	expect(typed?.glyph).toContain("ao-change-bar--added");
	// Only the typed line is unsaved.
	await expect(page.locator(".line-numbers.ao-unsaved-line")).toHaveCount(1);

	await page.getByTestId("save-file").click();
	await expect(page.getByTestId("save-file")).toBeDisabled();
	await expect(page.locator(".line-numbers.ao-unsaved-line")).toHaveCount(0);
	// Saved is not committed: the git lanes keep it.
	expect((await gutterOf(page, 7))?.glyph).toContain("ao-change-bar--added");
});

test("undoing back to the saved text clears the unsaved mark", async ({ page }) => {
	await openFile(page, ORDINARY);
	await typeIntoEditor(page, "xyz");
	await expect(page.locator(".line-numbers.ao-unsaved-line")).toHaveCount(1);
	await page.keyboard.press("Meta+KeyZ");
	await expect(page.locator(".line-numbers.ao-unsaved-line")).toHaveCount(0);
	await expect(page.getByTestId("save-file")).toBeDisabled();
});

// The uncommitted lane is live, so the hunk a click discards is the live one -
// including a line that has never been saved.
test("a gutter click discards an unsaved, uncommitted line", async ({ page }) => {
	await openFile(page, ORDINARY);
	await expect(page.locator(".ao-change-bar").first()).toBeVisible();
	await page.locator(".monaco-editor .view-line").nth(5).click();
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
	await page.keyboard.press("End");
	await page.keyboard.press("Enter");
	await page.keyboard.type("const discardMe = 1;");
	await expect.poll(() => gutterOf(page, 7)).toMatchObject({ unsaved: true });

	const bar = (await gutterOf(page, 7))?.bar;
	expect(bar).toBeTruthy();
	await page.mouse.click(bar!.x, bar!.y);
	const popover = page.getByTestId("discard-hunk-popover");
	await expect(popover).toBeVisible();
	await popover.getByRole("button", { name: "Discard", exact: true }).click();
	await expect(page.locator(".monaco-editor .view-lines").first()).not.toContainText("discardMe");
	await expect(page.getByTestId("save-file")).toBeDisabled();
});

test("Changes mode diffs against the target branch over the same buffer", async ({ page }) => {
	await openFile(page, ORDINARY);
	await typeIntoEditor(page, "// survives the mode switch");
	await expect(page.getByTestId("save-file")).toBeEnabled();

	const pane = page.getByTestId("terminal");
	await pane.getByRole("tab", { name: "Changes" }).click();
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "diff");
	await expect(pane.getByText(/^target branch/)).toBeVisible();

	// 🗝 One model per file. The obvious implementation gives the diff its own
	// model, and then the edit made here is invisible in Browse and lost on the
	// next open. Still dirty after a round trip is the proof it did not happen.
	await pane.getByRole("tab", { name: "Browse" }).click();
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "code");
	await expect(page.getByTestId("save-file")).toBeEnabled();
});

test("a truncated read is read-only, and says what saving would destroy", async ({ page }) => {
	await openFile(page, "routeTree.gen");

	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-editable", "false");
	await expect(page.getByTestId("read-only-chip")).toContainText("truncated");
	await expect(page.getByTestId("read-only-detail")).toContainText(/delete everything after them/i);
	// Never a control that always fails.
	await expect(page.getByTestId("save-file")).toBeHidden();
});

// ── nothing thrown into the renderer ─────────────────────────────────────────

/**
 * Collect everything the page throws, from before the first navigation.
 *
 * 🗝 These two errors were invisible to every other spec in this file. The next
 * `setModel` papered over them, so the diff still drew and every assertion about
 * what is on screen still passed — while `TextModel got disposed before
 * DiffEditorWidget model got reset` and `no diff result available` were thrown
 * on EVERY entry into diff mode, a whole diff computation was discarded each
 * time, and in Electron those are real unhandled errors in the renderer. Only a
 * test that watches the error channel itself can see that, which is why this one
 * asserts on nothing visible.
 */
function collectPageErrors(page: Page): string[] {
	const errors: string[] = [];
	page.on("pageerror", (error) => errors.push(error.message.split("\n")[0]));
	return errors;
}

/**
 * Wait for the diff to have actually been COMPUTED, not merely mounted.
 *
 * A decoration on a changed line is the proof: "no diff result available" is the
 * diff worker giving up, and a widget that never computed has no insert or
 * delete lines to show. This is also what keeps the assertion below off a sleep
 * — the errors arrive asynchronously after `setModel`, so the test has to wait
 * for something real rather than for a duration.
 */
async function waitForDiffToCompute(page: Page): Promise<void> {
	await expect(page.locator(".monaco-diff-editor")).toBeVisible();
	await expect(
		page.locator(".monaco-diff-editor .line-insert, .monaco-diff-editor .line-delete").first(),
	).toBeVisible();
}

test("entering Changes mode throws nothing, and re-entering it throws nothing either", async ({ page }) => {
	const errors = collectPageErrors(page);
	await openFile(page, ORDINARY);
	// The baseline matters: if opening a file already threw, a clean diff
	// assertion below would be measuring the wrong thing.
	expect(errors).toEqual([]);

	const pane = page.getByTestId("terminal");
	await pane.getByRole("tab", { name: "Changes" }).click();
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "diff");
	await waitForDiffToCompute(page);
	expect(errors).toEqual([]);

	// The second entry is its own case: the original model is reused when its
	// text has not moved, and reuse is exactly where a disposed model would be
	// handed back to the widget.
	await pane.getByRole("tab", { name: "Browse" }).click();
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "code");
	await pane.getByRole("tab", { name: "Changes" }).click();
	await waitForDiffToCompute(page);
	expect(errors).toEqual([]);
});

// 🗝 The same guard on this slice's headline path. A reader reaches the diff
// editor here having just been refused a save, which is the worst possible
// moment for the pane to be quietly throwing away its diff computation.
test("resolving a conflict throws nothing into the renderer", async ({ page }) => {
	const errors = collectPageErrors(page);
	await openFile(page, CONFLICTING);
	await typeIntoEditor(page, "// my edit");
	await page.getByTestId("save-file").click();
	await expect(page.getByTestId("file-drift-banner")).toBeVisible();
	// A refused save is not an error channel event either.
	expect(errors).toEqual([]);

	await page.getByRole("button", { name: /review changes/i }).click();
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "diff");
	await waitForDiffToCompute(page);

	expect(errors).toEqual([]);
});

/**
 * 🗝 Monaco throws `TextModel got disposed before DiffEditorWidget model got
 * reset` when a model is dropped while a live editor still holds it — and it
 * throws it into the page, where jsdom tests and a green suite both see
 * nothing. In Electron that is a real unhandled error in the renderer.
 *
 * qa found the first occurrence (entering diff mode). This is the second, in
 * the path that only opens when a file's branch diff is ALREADY CACHED: a file
 * opened for the first time has none, so the editor is not on screen while it
 * loads and there is no live diff editor to hold the old model.
 *
 * The warm-up below is therefore load-bearing, not ceremony. Without it this
 * test passes against the broken code.
 */
test("switching to an already-visited file while in diff mode throws nothing", async ({ page }) => {
	const errors = collectPageErrors(page);

	// Warm both files' diffs.
	await openFile(page, CONFLICTING);
	const pane = page.getByTestId("terminal");
	await pane.getByRole("tab", { name: "Changes" }).click();
	await waitForDiffToCompute(page);

	// The mode is the reader's and outlives the file, so the second file opens
	// straight into Changes with no second click.
	await reopenFile(page, ORDINARY);
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "diff");
	await waitForDiffToCompute(page);
	// Anything thrown up to here belongs to the two cases above, not this one.
	errors.length = 0;

	// Back to the first file. Its diff is cached, so the pane goes from one diff
	// straight to the next with no Browse render in between - and a live diff
	// editor still holding the model the path change is about to drop is exactly
	// where Monaco throws.
	await reopenFile(page, CONFLICTING);
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "diff");
	await waitForDiffToCompute(page);
	await expect(page.getByTestId("terminal").getByRole("tab", { name: "Changes" })).toHaveAttribute(
		"aria-selected",
		"true",
	);

	expect(errors, `switching to a cached file: ${errors.join(" | ")}`).toEqual([]);
});

// The same switch into a file whose diff has NOT been fetched yet: the pane
// holds the reader's Changes pick while the diff loads, instead of mounting
// Browse and rebuilding itself as a diff a moment later.
test("opening a new file while in Changes lands in Changes and throws nothing", async ({ page }) => {
	const errors = collectPageErrors(page);
	await openFile(page, ORDINARY);
	const pane = page.getByTestId("terminal");
	await pane.getByRole("tab", { name: "Changes" }).click();
	await waitForDiffToCompute(page);
	errors.length = 0;

	await reopenFile(page, CONFLICTING);
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "diff");
	await waitForDiffToCompute(page);
	await expect(pane.getByRole("tab", { name: "Changes" })).toHaveAttribute("aria-selected", "true");

	expect(errors, `switching to an uncached file: ${errors.join(" | ")}`).toEqual([]);
});
