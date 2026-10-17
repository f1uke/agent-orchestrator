import { expect, type Page } from "@playwright/test";

export async function open(
	page: Page,
	query = "",
	variant?: "failed-source" | "identity-unknown" | "fixes" | "stale-kind" | "subjects",
) {
	await page.addInitScript((v) => {
		try {
			if (!sessionStorage.getItem("releases-spec")) {
				localStorage.removeItem("ao.releases.layers.v1");
				localStorage.removeItem("ao.releases.folds.v1");
				localStorage.removeItem("ao.releases.search.v1");
				localStorage.removeItem("ao.releases.scroll.v1");
				localStorage.removeItem("ao.releases.hidden-boards.v1");
			}
			sessionStorage.setItem("releases-spec", "1");
		} catch {}
		if (v) (globalThis as { __aoReleaseBoardVariant?: string }).__aoReleaseBoardVariant = v;
	}, variant);
	await page.goto(`/#/releases${query}`);
	await expect(page.getByTestId("releases-page")).toBeVisible();
	await expect(page.getByTestId("label-di")).toBeVisible();
}

export const frame = (page: Page) =>
	page.evaluate(() =>
		[...document.querySelectorAll(".rel-canvas *")]
			.map((e) => {
				const r = e.getBoundingClientRect();
				return `${r.x},${r.y},${r.width},${r.height},${getComputedStyle(e).opacity}`;
			})
			.join("|"),
	);

export async function settle(page: Page) {
	let last = await frame(page);
	for (let i = 0; i < 40; i++) {
		await page.waitForTimeout(50);
		const now = await frame(page);
		if (now === last) return;
		last = now;
	}
	throw new Error("the page kept moving for 2 s");
}

export const layer = (page: Page, name: string) =>
	page.getByRole("group", { name: "Layers" }).getByRole("button", { name, exact: true });

export function halfLabels(page: Page): Promise<string[]> {
	return page.evaluate(() => {
		const edge = document.querySelector(".rel-sc")!.getBoundingClientRect().left + 280;
		const half: string[] = [];
		for (const el of document.querySelectorAll<HTMLElement>(".rel-bar .bl, .rel-spr .sl, .rel-llb .lb-lbl")) {
			if (getComputedStyle(el).visibility === "hidden") continue;
			const r = el.getBoundingClientRect();
			const clip = el.parentElement!.getBoundingClientRect();
			const visibleLeft = Math.max(r.left, clip.left, edge);
			if (r.right > edge && r.left < edge - 1 && visibleLeft > r.left + 1) half.push(el.innerText.slice(0, 30));
		}
		return half;
	});
}

export function labelOverlaps(page: Page): Promise<{ n: number; overlaps: string[] }> {
	return page.evaluate(() => {
		const vw = innerWidth;
		const vh = innerHeight;
		const seen = (e: Element) => {
			const r = e.getBoundingClientRect();
			const h = document.elementFromPoint(
				Math.min(vw - 1, Math.max(0, r.left + r.width / 2)),
				Math.min(vh - 1, Math.max(0, r.top + r.height / 2)),
			);
			return !!h && (h === e || e.contains(h) || h.contains(e));
		};
		const els = [
			...document.querySelectorAll(
				".bl, .sl, .lb-lbl, .csum, .ls, .rel-lyr, .dsum, .rel-sprow .crow, .rel-lab.top .spn, .rel-lab.top .sps, .rel-lab.top .spt, .rel-lab.top > .hide, .rel-app > .rel-lab > *, .rel-lab .stag",
			),
		].filter((e) => {
			const r = e.getBoundingClientRect();
			const visible = r.width > 0 && r.height > 0 && r.right > 0 && r.left < vw && r.top < vh && r.bottom > 0;
			return visible && getComputedStyle(e).visibility !== "hidden" && !e.closest(".tight") && seen(e);
		});
		const labRight = document.querySelector(".rel-app > .rel-lab")?.getBoundingClientRect().right ?? 0;
		const axisBottom = document.querySelector(".rel-axis")?.getBoundingClientRect().bottom ?? 0;
		const visible = (e: Element) => {
			const r = e.getBoundingClientRect();
			const left = e.closest(".rel-lab") ? r.left : Math.max(r.left, labRight);
			const top = e.closest(".rel-axis") ? r.top : Math.max(r.top, axisBottom);
			return { left, top, right: r.right, bottom: r.bottom };
		};
		const overlaps: string[] = [];
		for (let i = 0; i < els.length; i++)
			for (let j = i + 1; j < els.length; j++) {
				const [a, b] = [els[i], els[j]];
				if (a.contains(b) || b.contains(a)) continue;
				const r = visible(a);
				const q = visible(b);
				const ix = Math.min(r.right, q.right) - Math.max(r.left, q.left);
				const iy = Math.min(r.bottom, q.bottom) - Math.max(r.top, q.top);
				if (ix > 2 && iy > 2)
					overlaps.push(`${a.textContent?.trim().slice(0, 24)} x ${b.textContent?.trim().slice(0, 24)}`);
			}
		return { n: els.length, overlaps };
	});
}
