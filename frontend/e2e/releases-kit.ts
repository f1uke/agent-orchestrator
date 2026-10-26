import { expect, type Page } from "@playwright/test";

export const NARROW = { width: 1920, height: 1080 } as const;
export const WIDE = { width: 2560, height: 1440 } as const;
export const VIEWPORTS = [NARROW, WIDE] as const;

const warnings = new WeakMap<Page, string[]>();

type RingWatch = { born: WeakMap<Element, number>; log: string[]; freeze: boolean };

export const ringLife = (page: Page) =>
	page.evaluate(() => {
		const log = (globalThis as unknown as { __rings: RingWatch }).__rings.log;
		const [born, gone] = log.map((e) => Number(e.slice(1)));
		return log.length === 2 && log[0].startsWith("+") && log[1].startsWith("-") ? gone - born : 0;
	});

export const ringLog = (page: Page) =>
	page.evaluate(() =>
		(globalThis as unknown as { __rings: RingWatch }).__rings.log.map((e) => ({
			sign: e.slice(0, 1),
			at: Number(e.slice(1)),
		})),
	);

export const freezeRings = (page: Page) =>
	page.evaluate(() => {
		(globalThis as unknown as { __rings: RingWatch }).__rings.freeze = true;
	});

export async function open(
	page: Page,
	query = "",
	variant?: "dense-lists" | "failed-source" | "identity-unknown" | "fixes" | "stale-kind" | "subjects" | "twin",
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
		const rings: RingWatch = { born: new WeakMap(), log: [], freeze: false };
		(globalThis as { __rings?: RingWatch }).__rings = rings;
		const isRing = (n: Node): n is HTMLElement => n instanceof HTMLElement && n.classList.contains("rel-halo");
		new MutationObserver((records) => {
			const now = Math.round(performance.now());
			for (const r of records) {
				for (const n of [...r.addedNodes].filter(isRing)) {
					rings.born.set(n, performance.now());
					rings.log.push(`+${now}`);
					if (rings.freeze)
						for (const a of n.getAnimations()) {
							a.pause();
							a.currentTime = Number(a.effect?.getComputedTiming().duration) * 0.42;
						}
				}
				for (const _ of [...r.removedNodes].filter(isRing)) rings.log.push(`-${now}`);
			}
		}).observe(document, { childList: true, subtree: true });
	}, variant);
	const warned: string[] = [];
	warnings.set(page, warned);
	page.on("console", (m) => {
		if (m.type() === "warning" && m.text().startsWith("Show found")) warned.push(m.text());
	});
	await page.goto(`/#/releases${query}`);
	await expect(page.getByTestId("releases-page")).toBeVisible({ timeout: 15_000 });
	await expect(page.getByTestId("label-di")).toBeVisible({ timeout: 15_000 });
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

const RING_ROOM = 7;

export type SpotOptions = { motion?: boolean; since?: number; rings?: number };

export const animationNow = (page: Page) => page.evaluate(() => performance.now());

const POLL_MS = 40;
const poll = () => ({ timeout: 5_000, intervals: [POLL_MS] });

export function spotProblems(page: Page, selectors: readonly string[], o: SpotOptions = {}): Promise<string[]> {
	return page.evaluate(
		({ selectors, motion, since, rings, RING_ROOM }) => {
			const problems: string[] = [];
			const halos = [...document.querySelectorAll<HTMLElement>(".rel-halo")];
			if (rings !== undefined && halos.length !== rings) problems.push(`${halos.length} rings, not ${rings}`);
			const sc = document.querySelector<HTMLElement>(".rel-sc")!;
			const axis = document.querySelector(".rel-axis")!.getBoundingClientRect().bottom;
			const sheetLeft = document.querySelector(".rel-sheet")?.getBoundingClientRect().left ?? Infinity;
			const targets: { name: string; vis: { l: number; t: number; r: number; b: number } }[] = [];
			for (const sel of selectors) {
				const els = [...document.querySelectorAll<HTMLElement>(sel)];
				if (!els.length) problems.push(`${sel}: not drawn`);
				for (const el of els) {
					const name = `${sel}${els.length > 1 ? `[${els.indexOf(el)}]` : ""}`;
					const scroller = el.closest<HTMLElement>(".sh-b") ?? (sc.contains(el) ? sc : null);
					const b = scroller?.getBoundingClientRect();
					const area = scroller
						? {
								l: b!.left + scroller.clientLeft,
								t: b!.top + scroller.clientTop,
								r: b!.left + scroller.clientLeft + scroller.clientWidth,
								b: b!.top + scroller.clientTop + scroller.clientHeight,
							}
						: { l: 0, t: 0, r: innerWidth, b: innerHeight };
					if (scroller === sc) {
						area.t = Math.max(area.t, axis);
						if (!el.closest(".rel-lab")) area.l += 280;
						area.r = Math.min(area.r, sheetLeft);
					}
					const r = el.getBoundingClientRect();
					const fits = (lo: number, hi: number, aLo: number, aHi: number) =>
						hi - lo <= aHi - aLo ? lo >= aLo - 1 && hi <= aHi + 1 : lo >= aLo - 1 && lo <= aLo + 40;
					if (!fits(r.left, r.right, area.l, area.r) || !fits(r.top, r.bottom, area.t, area.b))
						problems.push(
							`${name}: not in view (${[r.left, r.top, r.right, r.bottom].map(Math.round)} in ${[area.l, area.t, area.r, area.b].map(Math.round)})`,
						);
					const room = scroller ? RING_ROOM : 0;
					const inSheet = scroller && scroller !== sc ? room : 0;
					const vis = {
						l: Math.max(r.left, area.l + inSheet),
						t: Math.max(r.top, area.t + inSheet),
						r: Math.min(r.right, area.r - room),
						b: Math.min(r.bottom, area.b - room),
					};
					const hit = document.elementFromPoint((vis.l + vis.r) / 2, (vis.t + vis.b) / 2);
					if (!hit || !(hit === el || el.contains(hit) || hit.classList.contains("rel-halo")))
						problems.push(`${name}: covered by ${hit?.className.toString().slice(0, 40)}`);
					targets.push({ name, vis });
				}
			}
			const inside = (q: DOMRect, v: (typeof targets)[number]["vis"]) =>
				q.left <= v.l && q.top <= v.t && q.right >= v.r && q.bottom >= v.b;
			const watch = (globalThis as unknown as { __rings: RingWatch }).__rings;
			for (const t of targets) {
				const halo = halos.find((h) => inside(h.getBoundingClientRect(), t.vis));
				if (!halo) {
					problems.push(`${t.name}: no ring around its visible box`);
					continue;
				}
				const q = halo.getBoundingClientRect();
				const held = targets.filter((o) => inside(q, o.vis)).map((o) => o.vis);
				const u = {
					l: Math.min(...held.map((v) => v.l)),
					t: Math.min(...held.map((v) => v.t)),
					r: Math.max(...held.map((v) => v.r)),
					b: Math.max(...held.map((v) => v.b)),
				};
				const pads = [u.l - q.left, u.t - q.top, q.right - u.r, q.bottom - u.b];
				if (!pads.every((p) => p >= 2 && Math.abs(p - pads[0]) <= 1))
					problems.push(`${t.name}: its ring is uneven (${pads.map(Math.round)})`);
				const cs = getComputedStyle(halo);
				if (cs.backgroundColor !== "rgba(0, 0, 0, 0)")
					problems.push(`${t.name}: the ring is filled ${cs.backgroundColor}`);
				if (Number(cs.opacity) <= 0.3) problems.push(`${t.name}: the ring is faint (${cs.opacity})`);
				const pulsing = halo.getAnimations().some((a) => a.playState !== "finished");
				if (motion !== undefined && pulsing !== motion)
					problems.push(`${t.name}: ${motion ? "no pulse" : "a pulse runs under reduced motion"}`);
				if (since !== undefined && (watch.born.get(halo) ?? -1) < since)
					problems.push(`${t.name}: the ring is not from this press`);
			}
			return problems;
		},
		{ selectors, motion: o.motion, since: o.since, rings: o.rings, RING_ROOM },
	);
}

export async function expectSpotted(page: Page, selectors: readonly string[], o: SpotOptions = {}) {
	const want = { motion: true, ...o };
	await expect
		.poll(async () => [...(warnings.get(page) ?? []), ...(await spotProblems(page, selectors, want))], poll())
		.toEqual([]);
}

export async function expectRinged(page: Page, since: number, message: string) {
	const seen = new Set<string>();
	const end = Date.now() + poll().timeout;
	while (Date.now() < end) {
		const now = [
			...(warnings.get(page) ?? []),
			...(await haloProblems(page, since)),
			...(await outerScrolls(page)).map((o) => `scrolled ${o}`),
		];
		if (!now.length) return;
		for (const p of now) seen.add(p);
		await page.waitForTimeout(POLL_MS);
	}
	throw new Error(`${message}: never ringed cleanly; seen ${[...seen].join(" | ")}`);
}

function haloProblems(page: Page, since: number): Promise<string[]> {
	return page.evaluate((since) => {
		const halos = [...document.querySelectorAll<HTMLElement>(".rel-halo")];
		const watch = (globalThis as unknown as { __rings: RingWatch }).__rings;
		if (!halos.length) return [`no ring (since ${Math.round(since)}: ${watch.log.slice(-6).join(" ")})`];
		const sc = document.querySelector<HTMLElement>(".rel-sc")!;
		const b = sc.getBoundingClientRect();
		const top = document.querySelector(".rel-axis")!.getBoundingClientRect().bottom;
		const problems: string[] = [];
		for (const [i, h] of halos.entries()) {
			const r = h.getBoundingClientRect();
			const scroller = h.closest<HTMLElement>(".sh-b") ?? (sc.contains(h) ? sc : null);
			const area = !scroller
				? { left: 0, top: 0, right: innerWidth, bottom: innerHeight }
				: scroller === sc
					? {
							left: b.left,
							top,
							right: Math.min(
								b.left + sc.clientWidth,
								document.querySelector(".rel-sheet")?.getBoundingClientRect().left ?? Infinity,
							),
							bottom: b.top + sc.clientHeight,
						}
					: scroller.getBoundingClientRect();
			if (r.width < 20 || r.height < 20) problems.push(`ring ${i} is ${r.width}x${r.height}`);
			if (r.left < area.left - 12 || r.top < area.top - 12 || r.right > area.right + 12 || r.bottom > area.bottom + 12)
				problems.push(`ring ${i} is out of view`);
			const cs = getComputedStyle(h);
			if (cs.backgroundColor !== "rgba(0, 0, 0, 0)") problems.push(`ring ${i} is filled`);
			if (Number(cs.opacity) <= 0.3) problems.push(`ring ${i} is faint`);
			const under = document.elementFromPoint((r.left + r.right) / 2, (r.top + r.bottom) / 2);
			if (!under || (scroller && !scroller.contains(under)))
				problems.push(`ring ${i} is covered by ${under?.className}`);
			if (!h.getAnimations().some((a) => a.playState !== "finished")) problems.push(`ring ${i} does not pulse`);
			if ((watch.born.get(h) ?? -1) < since) problems.push(`ring ${i} is not from this press`);
		}
		return problems;
	}, since);
}

export function outerScrolls(page: Page): Promise<string[]> {
	return page.evaluate(() => {
		const moved: string[] = [];
		const se = document.scrollingElement!;
		if (se.scrollLeft || se.scrollTop) moved.push(`document ${se.scrollLeft},${se.scrollTop}`);
		for (let e = document.querySelector(".rel-sc")?.parentElement; e; e = e.parentElement)
			if (e.scrollLeft || e.scrollTop)
				moved.push(`${e.tagName}.${e.className.toString().slice(0, 30)} ${e.scrollLeft},${e.scrollTop}`);
		return moved;
	});
}
