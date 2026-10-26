import { expect, test } from "@playwright/test";
import { NARROW, open } from "./releases-kit";

test.use({ viewport: NARROW });

const SHOTS = process.env.AO_RELEASES_SHOTS;

test("a branch the plan does not name shows as auto in the list and the sheet", async ({ page }) => {
	await open(page, "?sheet=llbs", "twin");
	const row = page.locator(".rel-sheet .lbrow", { hasText: "Wallet" });
	await expect(row).toContainText("· auto");
	await expect(row.locator(".plat-pill")).toHaveCount(2);
	if (SHOTS) await page.screenshot({ path: `${SHOTS}/llb-auto-list.png` });

	await row.click();
	const branches = page.getByTestId("llb-branch");
	await expect(branches).toHaveCount(2);
	await expect(branches.nth(0).getByTestId("llb-auto")).toHaveCount(0);
	await expect(branches.nth(1).getByTestId("llb-auto")).toBeVisible();
	const form = page.getByTestId("plan-form");
	await expect(form.locator(".brow", { hasText: "feature/demo-wallet" })).toHaveCount(2);
	await expect(form.getByTestId("llb-auto")).toHaveCount(1);
	await expect(form.getByRole("button", { name: /^Remove da:/ })).toHaveCount(0);
	if (SHOTS) await page.screenshot({ path: `${SHOTS}/llb-auto-sheet.png` });
});
