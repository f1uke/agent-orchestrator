import { expect, test } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts with no daemon behind it,
// so ReviewsView draws its preview reviews and comment threads. Two sessions in
// ao-demo show the tab's two shapes: "demo-review-stack" owns three PRs, and
// "demo-question" has none yet, so its review is the pre-merge pass on the branch.

test("the Reviews tab renders the reviewer panel and threads for a session that owns PRs", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-review-stack");

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();

	await inspector.getByRole("tab", { name: "Reviews" }).click();

	// The reviewer card surfaces the harness, its latest verdict, and both actions.
	await expect(inspector.getByText("codex", { exact: true })).toBeVisible();
	await expect(inspector.getByText("· Changes requested")).toBeVisible();
	await expect(inspector.getByRole("button", { name: "Re-run review" })).toBeVisible();
	await expect(inspector.getByRole("button", { name: "Open terminal" })).toBeVisible();

	// The threads render per PR, never a daemon error in their place.
	await expect(inspector.getByText("Browser preview rail renders inside AO")).toBeVisible();
	await expect(inspector.getByText("Reviews tab nests comment threads per PR")).toBeVisible();
	await expect(
		inspector.getByText("Prefer a stable dependency here; the array identity changes each poll."),
	).toBeVisible();
	await expect(inspector.getByText("Loading reviews…")).toHaveCount(0);
	await expect(inspector.getByText("AO daemon is not ready.")).toHaveCount(0);
});

test("the Reviews tab shows the pre-merge review for a session with no PR", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-question");

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();

	await inspector.getByRole("tab", { name: "Reviews" }).click();
	await expect(inspector.getByText("Reviewed before the PR · no PR yet")).toBeVisible();
	await expect(inspector.getByText("Pre-merge review")).toBeVisible();
	await expect(inspector.getByText("No blocking findings.", { exact: false })).toBeVisible();
});
