import { expect, test } from "@playwright/test";

// Repro for the titlebar history arrows: navigate home → project → back,
// then the forward arrow must be enabled and actually traverse forward.
test("titlebar back/forward arrows traverse history", async ({ page }) => {
	await page.goto("/");
	await expect(page.getByText("Projects")).toBeVisible();

	// Navigate: home → session view (in-app push).
	// Clicked where a person clicks - on the row's title. The row's button is a
	// full-bleed layer UNDER the title, so clicking the button itself is refused
	// as intercepted even though the same point opens the session for a person.
	await page
		.getByRole("button", { name: /^Open Resolve reviewer feedback on terminal polish/ })
		.locator("xpath=ancestor::li[1]")
		.getByText("Resolve reviewer feedback on terminal polish")
		.click();
	await expect(page).toHaveURL(/sessions\/demo-needs-input/);

	const back = page.getByRole("button", { name: "Go back" });
	const forward = page.getByRole("button", { name: "Go forward" });

	await expect(forward).toBeDisabled();
	await expect(back).toBeEnabled();

	await back.click();
	await expect(page).not.toHaveURL(/sessions\/demo-needs-input/);

	await expect(forward).toBeEnabled();
	await forward.click();
	await expect(page).toHaveURL(/sessions\/demo-needs-input/);
});
