import { expect, test } from "@playwright/test";
import { NARROW, open, settle } from "./releases-kit";

test.describe("releases page at rest", () => {
	test.use({ viewport: NARROW });

	test("an idle page does no work: no DOM writes and no scroll saves", async ({ page }) => {
		await open(page);
		await settle(page);
		const counts = await page.evaluate(async () => {
			let mutations = 0;
			let saves = 0;
			const mo = new MutationObserver((l) => (mutations += l.length));
			mo.observe(document.body, { subtree: true, childList: true, attributes: true, characterData: true });
			const setItem = Storage.prototype.setItem;
			Storage.prototype.setItem = function (k: string, v: string) {
				if (k === "ao.releases.scroll.v1") saves++;
				return setItem.call(this, k, v);
			};
			await new Promise((r) => setTimeout(r, 2_000));
			mo.disconnect();
			Storage.prototype.setItem = setItem;
			return { mutations, saves };
		});
		expect(counts).toEqual({ mutations: 0, saves: 0 });
	});
});
