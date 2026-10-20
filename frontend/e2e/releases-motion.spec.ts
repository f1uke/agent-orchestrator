import { expect, test, type Page } from "@playwright/test";
import { NARROW, halfLabels, labelOverlaps, layer, open, settle } from "./releases-kit";

const SHOTS = process.env.AO_RELEASES_SHOTS;

const WATCHED = [
	".rel-app",
	".rel-sprow",
	".rel-bar",
	".rel-cg",
	".rel-llb",
	".rel-cg .crows",
	".rel-app > .rel-lab > *",
	".rel-wires circle",
].join(", ");

type Box = { id: string; x: number; y: number; w: number; h: number; opacity: number; transform: string };

function boxes(page: Page): Promise<Box[]> {
	return page.evaluate((sel) => {
		const c = document.querySelector(".rel-canvas")!.getBoundingClientRect();
		return [...document.querySelectorAll<HTMLElement>(sel)].map((e, i) => {
			const r = e.getBoundingClientRect();
			const cs = getComputedStyle(e);
			const id =
				e.dataset.testid ??
				e.dataset.app ??
				e.dataset.board ??
				e.dataset.dest ??
				e.dataset.branch ??
				`${e.className}#${i}`;
			return {
				id: `${e.tagName}.${id}`,
				x: Math.round(r.x - c.x),
				y: Math.round(r.y - c.y),
				w: Math.round(r.width),
				h: Math.round(r.height),
				opacity: Number(cs.opacity),
				transform: cs.transform,
			};
		});
	}, WATCHED);
}

async function startFrames(page: Page) {
	await page.evaluate((sel) => {
		const w = window as unknown as { __frames: Box[][]; __covered: string[]; __stop: boolean };
		w.__frames = [];
		w.__covered = [];
		w.__stop = false;
		const before = new Set(document.querySelectorAll(".rel-row > .rel-lab"));
		const tick = () => {
			if (w.__stop) return;
			for (const lab of document.querySelectorAll<HTMLElement>(".rel-row > .rel-lab")) {
				if (!before.has(lab)) continue;
				const r = lab.getBoundingClientRect();
				for (const [x, y] of [
					[r.left + 12, r.top + 6],
					[r.left + 12, r.bottom - 6],
				]) {
					if (y < 0 || y > innerHeight || r.height < 12) continue;
					const hit = document.elementFromPoint(x, y);
					if (hit && !lab.contains(hit) && !hit.closest(".rel-axis, .rel-sheet, .rel-top, .rel-strip"))
						w.__covered.push(`${lab.dataset.testid ?? lab.className} by ${hit.className}`);
				}
			}
			const c = document.querySelector(".rel-canvas")!.getBoundingClientRect();
			w.__frames.push(
				[...document.querySelectorAll<HTMLElement>(sel)].map((e, i) => {
					const r = e.getBoundingClientRect();
					const cs = getComputedStyle(e);
					return {
						id: `${e.tagName}.${e.dataset.testid ?? e.dataset.app ?? e.dataset.board ?? e.dataset.dest ?? `${e.className}#${i}`}`,
						x: Math.round(r.x - c.x),
						y: Math.round(r.y - c.y),
						w: Math.round(r.width),
						h: Math.round(r.height),
						opacity: Number(cs.opacity),
						transform: cs.transform,
					};
				}),
			);
			requestAnimationFrame(tick);
		};
		requestAnimationFrame(tick);
	}, WATCHED);
}

async function stopFrames(page: Page): Promise<{ frames: Box[][]; covered: string[] }> {
	await page.waitForTimeout(400);
	return page.evaluate(() => {
		const w = window as unknown as { __frames: Box[][]; __covered: string[]; __stop: boolean };
		w.__stop = true;
		return { frames: w.__frames, covered: [...new Set(w.__covered)] };
	});
}

const key = (b: Box) => `${b.id} ${b.x},${b.y} ${b.w}x${b.h} ${b.opacity.toFixed(2)} ${b.transform}`;

function frameViolations(frames: Box[][]): string[] {
	const bad = new Set<string>();
	for (const f of frames)
		for (const b of f) {
			if (b.id.includes("bar-") && (b.transform.startsWith("matrix(0,") || b.w === 0)) bad.add(`${b.id} collapsed`);
			if (b.id.startsWith("DIV.label-") || /^(DIV|SPAN)\.(nm|ver|why|meta|meline)/.test(b.id))
				if (b.transform !== "none") bad.add(`${b.id} label moved: ${b.transform}`);
		}
	return [...bad];
}

async function expectSameAsColdLoad(page: Page) {
	await settle(page);
	const now = (await boxes(page)).map(key);
	await page.reload();
	await expect(page.getByTestId("label-di")).toBeVisible();
	await settle(page);
	expect(now).toEqual((await boxes(page)).map(key));
	expect(await halfLabels(page)).toEqual([]);
	expect((await labelOverlaps(page)).overlaps).toEqual([]);
}

for (const reduced of [false, true]) {
	test.describe(reduced ? "releases motion, reduced" : "releases motion", () => {
		test.use({ viewport: NARROW });

		test.beforeEach(async ({ page }) => {
			await page.emulateMedia({ reducedMotion: reduced ? "reduce" : "no-preference" });
		});

		async function act(page: Page, click: () => Promise<void>, done: () => Promise<void>, grows?: string) {
			await settle(page);
			const before = (await boxes(page)).map(key).join("\n");
			await startFrames(page);
			await click();
			await done();
			const { frames, covered } = await stopFrames(page);
			expect(frameViolations(frames)).toEqual([]);
			expect(covered, "no row covers a label while it moves").toEqual([]);
			const shapes = new Set(frames.map((f) => f.map(key).join("\n")).filter((f) => f !== before));
			if (reduced) expect(shapes.size, "reduced motion jumps straight to the end").toBeLessThanOrEqual(1);
			else expect(shapes.size, "the change animates").toBeGreaterThan(2);
			if (grows) {
				const heights = new Set(frames.map((f) => f.find((b) => b.id === grows)?.h));
				if (reduced) expect(heights.size, `${grows} resizes in one step`).toBeLessThanOrEqual(2);
				else expect(heights.size, `${grows} grows or shrinks over several frames`).toBeGreaterThan(3);
			}
		}

		test("a card group unfolds and folds back", async ({ page }) => {
			await open(page);
			const group = page.getByTestId("cards-di:21-2026");
			const rows = group.locator(".crows .crow");
			await act(
				page,
				() => group.locator(".csum").click(),
				() => expect(rows).toHaveCount(7),
				"DIV.di",
			);
			if (reduced) await expect(group.locator(".crows")).toHaveCSS("opacity", "1");
			const fit = await group
				.locator(".crows")
				.evaluate((e) => [
					e.clientHeight,
					e.scrollHeight,
					(e.lastElementChild as HTMLElement).getBoundingClientRect().bottom - e.getBoundingClientRect().bottom,
				]);
			expect(fit[0], "the unfolded box shows every row").toBe(fit[1]);
			expect(fit[2]).toBeLessThanOrEqual(0);
			await expectSameAsColdLoad(page);
			if (SHOTS) await page.screenshot({ path: `${SHOTS}/motion-cards-${reduced ? "reduced" : "on"}.png` });
			await act(
				page,
				() => group.locator(".csum").click(),
				() => expect(rows).toHaveCount(0),
				"DIV.di",
			);
			await expectSameAsColdLoad(page);
		});

		test("the long-lived line unfolds and folds back, with the bars below moving with it", async ({ page }) => {
			await open(page);
			const sum = page.locator('[data-testid="llb-summary"][data-app="di"]').locator(".ls[aria-expanded]");
			const bar = page.getByTestId("bar-di-21-2026");
			const y0 = (await bar.boundingBox())!.y;
			const lanes = page.locator('.rel-app[data-app="di"]').getByTestId("llb-lane");
			await act(
				page,
				() => sum.click(),
				() => expect(lanes).toHaveCount(4),
				"DIV.di",
			);
			if (reduced) expect((await bar.boundingBox())!.y).toBe(y0 + 4 * 24 + 2);
			await expectSameAsColdLoad(page);
			expect((await bar.boundingBox())!.y).toBe(y0 + 4 * 24 + 2);
			await act(
				page,
				() => sum.click(),
				() => expect(lanes).toHaveCount(0),
				"DIV.di",
			);
			await expectSameAsColdLoad(page);
			expect((await bar.boundingBox())!.y).toBe(y0);
		});

		test("a sprint destination unfolds and folds back", async ({ page }) => {
			await open(page, "?layers=sprint,llb,cards");
			const demo = page.getByTestId("sprint-row-DEMO");
			const blocks = demo.getByTestId("sprint-dest-block");
			await act(
				page,
				() => demo.locator(".spsrc .csum").click(),
				() => expect(blocks).toHaveCount(6),
				"DIV.sprint-row-DEMO",
			);
			const t21 = demo.locator('[data-dest="t:21-2026"]');
			const below = demo.locator('[data-dest="l:demo-wallet"]');
			const y0 = (await below.boundingBox())!.y;
			await act(
				page,
				() => t21.locator(".dsum").click(),
				() => expect(t21.locator(".crows .crow[data-key]")).toHaveCount(3),
				"DIV.sprint-row-DEMO",
			);
			await expectSameAsColdLoad(page);
			expect((await below.boundingBox())!.y).toBe(y0 + 20 + 3 * 24 + 4);
			if (SHOTS) await page.screenshot({ path: `${SHOTS}/motion-sprint-${reduced ? "reduced" : "on"}.png` });
			await act(
				page,
				() => t21.locator(".dsum").click(),
				() => expect(t21.locator(".crow[data-key]")).toHaveCount(0),
				"DIV.sprint-row-DEMO",
			);
			await expectSameAsColdLoad(page);
			expect((await below.boundingBox())!.y).toBe(y0);
		});

		test("the app filter, Only me and a layer move the rows that stay into their new place", async ({ page }) => {
			await open(page);
			await layer(page, "Sprint cards").click();
			await expect(page.locator("[data-board]")).toHaveCount(3);
			const apps = page.getByRole("group", { name: "Apps" });
			const order = () => page.locator(".rel-app").evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.app));
			await act(
				page,
				() => apps.getByRole("button").nth(1).click(),
				() => expect(page.locator(".rel-app")).toHaveCount(3),
			);
			expect(await order()).toEqual(["di", "pi", "pa"]);
			await expectSameAsColdLoad(page);
			await act(
				page,
				() => apps.getByRole("button").nth(1).click(),
				() => expect(page.locator(".rel-app")).toHaveCount(4),
			);
			expect(await order()).toEqual(["di", "da", "pi", "pa"]);
			await expectSameAsColdLoad(page);
			await act(
				page,
				() => page.getByTestId("only-me").click(),
				() => expect(page.getByTestId("only-me")).toHaveAttribute("aria-pressed", "true"),
			);
			await expectSameAsColdLoad(page);
			await act(
				page,
				() => layer(page, "Sprint cards").click(),
				() => expect(page.locator("[data-board]")).toHaveCount(0),
			);
			await expectSameAsColdLoad(page);
		});

		test("a focus switch leaves every label part fully opaque, faded on opacity only", async ({ page }) => {
			await open(page);
			const focus = page.getByRole("group", { name: "Train in focus" });
			await act(
				page,
				() => focus.getByRole("button", { name: "#20" }).click(),
				() => expect(page.getByTestId("label-di")).toContainText("2.3.0"),
			);
			await settle(page);
			const parts = await page
				.locator(".rel-app > .rel-lab > *")
				.evaluateAll((els) => els.map((e) => [getComputedStyle(e).opacity, getComputedStyle(e).transform]));
			expect(parts.length).toBeGreaterThan(8);
			for (const p of parts) expect(p).toEqual(["1", "none"]);
		});

		test("pan and an unfold together leave no clipped label", async ({ page }) => {
			await open(page);
			await page.locator('[data-testid="llb-summary"][data-app="di"]').locator(".ls[aria-expanded]").click();
			await page.getByTestId("cards-di:21-2026").locator(".csum").click();
			const sc = page.getByTestId("release-timeline");
			const box = (await sc.boundingBox())!;
			await page.mouse.move(box.x + 900, box.y + 300);
			await page.mouse.down();
			await page.mouse.move(box.x + 400, box.y + 300, { steps: 12 });
			await page.mouse.up();
			await settle(page);
			expect(await halfLabels(page)).toEqual([]);
			expect((await labelOverlaps(page)).overlaps).toEqual([]);
		});
	});
}
