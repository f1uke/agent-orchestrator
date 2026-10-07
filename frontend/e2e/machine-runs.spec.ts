import { expect, test } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts. The "demo-qa-testing"
// crew has two machine runs (mockCrewRuns): qa's device pass, discarded, and
// dev's build, passed. The Summary strip reads the whole task, so both members
// see both runs, each tagged with the member that made it.

const SHOTS = process.env.AO_E2E_SHOTS;

for (const id of ["demo-qa-testing", "demo-qa-testing-qa"]) {
	test(`${id} shows the whole task's machine runs, tagged by member`, async ({ page }) => {
		await page.goto(`/#/projects/ao-demo/sessions/${id}`);

		const strip = page.locator("#inspector").getByRole("region", { name: "Machine runs" });
		const rows = strip.getByTestId("crew-run-row");
		await expect(rows).toHaveCount(2);
		await expect(rows.getByTestId("crew-run-role")).toHaveText(["qa", "dev"]);
		await expect(rows.nth(0)).toContainText("DISCARDED");
		await expect(rows.nth(1)).toContainText("PASSED");

		// The tag sits in the left rail under the state pill, centred on it, so it
		// takes no width from the title; every row's title starts from one edge.
		// Polled because the inspector slides in after the route loads.
		await expect
			.poll(async () => {
				const geometry = await Promise.all(
					(await rows.all()).map(async (row) => {
						const pill = (await row.getByText(/^(DISCARDED|PASSED)$/).boundingBox())!;
						const tag = (await row.getByTestId("crew-run-role").boundingBox())!;
						const title = (await row.getByText(/^(Device|Build) · /).boundingBox())!;
						return {
							tagBelowPill: tag.y >= pill.y + pill.height,
							tagCentredOnPill: Math.abs(tag.x + tag.width / 2 - (pill.x + pill.width / 2)) <= 1,
							titleX: Math.round(title.x),
						};
					}),
				);
				return geometry.every((g) => g.tagBelowPill && g.tagCentredOnPill && g.titleX === geometry[0].titleX);
			})
			.toBe(true);
	});
}

test("the escalation names the member whose runs keep being discarded", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");

	const strip = page.locator("#inspector").getByRole("region", { name: "Machine runs" });
	await expect(strip.getByTestId("crew-run-role").first()).toHaveText("dev");
	await expect(strip).toContainText("3 of dev's runs discarded in a row");
});

for (const theme of ["dark", "light"]) {
	test(`the Summary strip reads in ${theme}`, async ({ page }) => {
		test.skip(!SHOTS, "set AO_E2E_SHOTS to a folder to capture the Summary tab");
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto("/#/projects/ao-demo/sessions/demo-qa-testing");
		const strip = page.locator("#inspector").getByRole("region", { name: "Machine runs" });
		await expect(strip.getByTestId("crew-run-row")).toHaveCount(2);
		await page.evaluate((t) => {
			document.documentElement.dataset.theme = t;
		}, theme);
		await page.locator("#inspector").screenshot({ path: `${SHOTS}/summary-${theme}.png` });
		await strip.screenshot({ path: `${SHOTS}/strip-${theme}.png` });
	});
}
