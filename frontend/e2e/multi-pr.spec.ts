import { expect, test } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts. The ao-demo workspace
// owns a "demo-review-stack" session carrying three PRs: !319 open (GitLab),
// #320 open, #321 draft - the multi-PR-per-session case this suite guards
// across the inspector rail and the PR board.

test("the inspector rail stacks every PR a session owns, actionable-first", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-review-stack");

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();

	// Plural heading reflects the stack size.
	await expect(inspector.getByText("Pull requests (3)")).toBeVisible();

	// One card per PR, ordered open → draft (by number within a state).
	// Scope to the PR section: the Activity timeline also renders "Opened PR #n".
	const prSection = inspector.locator("section.inspector-section", { hasText: "Pull requests (3)" });
	const cards = prSection.locator("text=/^(PR #|MR !)\\d+$/");
	await expect(cards).toHaveText(["MR !319", "PR #320", "PR #321"]);
});

test("the PR board lists one row per attributed PR, actionable PRs first", async ({ page }) => {
	await page.goto("/#/prs");

	await expect(page.getByRole("heading", { name: "Pull requests" })).toBeVisible();
	await expect(page.locator("tbody tr").first()).toBeVisible();

	// Open PRs, then drafts, then merged ones sink to the bottom.
	const numbers = await page.locator("tbody tr td:first-child").allTextContents();
	expect(numbers.indexOf("!319")).toBeGreaterThanOrEqual(0);
	expect(numbers.indexOf("!319")).toBeLessThan(numbers.indexOf("#321"));
	expect(numbers.indexOf("#321")).toBeLessThan(numbers.indexOf("#325"));
	expect(numbers.slice(-2).sort()).toEqual(["#325", "#326"]);

	// A crew's dev and qa both answer for the task's PR; the board lists it once.
	expect(numbers.filter((n) => n === "!323")).toHaveLength(1);
	expect(new Set(numbers).size).toBe(numbers.length);

	// Open/draft rows are actionable; the merged row is not.
	const mergedRow = page.locator("tbody tr", { hasText: "#326" });
	await expect(mergedRow.getByRole("button", { name: "Merge" })).toHaveCount(0);
	const openRow = page.locator("tbody tr", { hasText: "!319" });
	await expect(openRow.getByRole("button", { name: "Merge" })).toBeVisible();
});
