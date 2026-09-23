import { expect, test } from "@playwright/test";

/**
 * Where a click on a changed file lands, driven from the Files rail the way a
 * reviewer drives it: row after row, with a file already open in the center.
 */

const SESSION = "/#/projects/ao-demo/sessions/demo-working";
const DELETED = "frontend/src/renderer/lib/legacy-diff.ts";

test("a deleted row opens its diff even while another file is open", async ({ page }) => {
	await page.goto(SESSION);
	await page.getByRole("tab", { name: "Files" }).click();
	const tree = page.locator("#inspector");

	await tree.getByRole("treeitem", { name: /FilesPanel\.tsx/ }).click();
	await expect(page.getByTestId("monaco-file-editor")).toBeVisible();

	// A deleted file has no buffer to edit, so it goes to the stacked diff. The
	// open editor used to win the center and the click did nothing visible.
	await tree.getByRole("treeitem", { name: /legacy-diff\.ts/ }).click();
	await expect(page.getByRole("region", { name: DELETED })).toBeVisible();
	await expect(page.getByTestId("monaco-file-editor")).toBeHidden();
});
