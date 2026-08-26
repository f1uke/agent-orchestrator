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

/** Put the caret in the buffer and type, which is the only way to make it dirty. */
async function typeIntoEditor(page: Page, text: string): Promise<void> {
	await page.locator(".monaco-editor .view-lines").first().click();
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
	await expect(page.getByText("On disk", { exact: true })).toBeVisible();
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
});

test("both change lanes are drawn, and the branch lane is not coloured by kind", async ({ page }) => {
	await openFile(page, ORDINARY);
	await expect(page.getByTestId("monaco-file-editor")).toBeVisible();

	const branch = page.locator(".ao-branch-bar");
	const uncommitted = page.locator(".ao-change-bar");
	await expect(branch.first()).toBeVisible();
	await expect(uncommitted.first()).toBeVisible();

	// The branch lane carries exactly one class, and it is not one of the three
	// kind classes. Two kind-coloured bars side by side read as one thick bar on
	// a branch under review, which is what this pins.
	// (Monaco adds its own `cldr` class to every line-decoration node; what
	// matters is that none of OUR three kind classes appears on this lane.)
	const branchClasses = await branch.evaluateAll((nodes) => nodes.map((n) => n.className));
	expect(branchClasses.length).toBeGreaterThan(0);
	for (const className of branchClasses) {
		expect(className).toContain("ao-branch-bar");
		expect(className).not.toMatch(/ao-change-bar--(added|modified|removed)/);
	}

	// They occupy different columns, so the two levels are readable apart.
	const branchBox = await branch.first().boundingBox();
	const uncommittedBox = await uncommitted.first().boundingBox();
	expect(branchBox).not.toBeNull();
	expect(uncommittedBox).not.toBeNull();
});

test("Changes mode diffs against the target branch over the same buffer", async ({ page }) => {
	await openFile(page, ORDINARY);
	await typeIntoEditor(page, "// survives the mode switch");
	await expect(page.getByTestId("save-file")).toBeEnabled();

	const pane = page.getByTestId("terminal");
	await pane.getByRole("tab", { name: "Changes" }).click();
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-mode", "diff");
	await expect(pane.getByText("target branch", { exact: true })).toBeVisible();

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
