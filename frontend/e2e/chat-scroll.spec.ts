import { expect, type Page, test } from "@playwright/test";

// What the stream looked like in each animation frame: the on-screen rows by key and where
// they sat. The last sample of a frame comes from a ResizeObserver made after the list's own
// (on the first row of each new stream), so it runs after the list has repositioned its rows:
// what was painted.
type Row = { key: string; top: number; bottom: number };
type Sample = { frame: number; session: string; visible: boolean; scrollTop: number; fromEnd: number; rows: Row[] };

function recordFrames() {
	const w = window as unknown as { __frames: Sample[]; __mark: number };
	w.__frames = [];
	w.__mark = 0;
	let frame = 0;
	let rowObserver: ResizeObserver | null = null;
	let observed: Element | null = null;
	const sample = () => {
		const sc = document.querySelector<HTMLElement>('[data-testid="chat-stream"]');
		if (!sc) return;
		const box = sc.getBoundingClientRect();
		const rows = [...sc.querySelectorAll<HTMLElement>(":scope > .chat-col > .chat-row")]
			.map((el) => {
				const r = el.getBoundingClientRect();
				return {
					key: el.dataset.key ?? "",
					index: Number(el.dataset.index),
					top: r.top - box.top,
					bottom: r.bottom - box.top,
				};
			})
			.filter((r) => r.bottom > 0 && r.top < box.height)
			.sort((a, b) => a.index - b.index)
			.map(({ key, top, bottom }) => ({ key, top, bottom }));
		const last = w.__frames.at(-1);
		const session = sc.closest<HTMLElement>('[data-testid="chat-pane"]')?.dataset.session ?? "";
		const next = {
			frame,
			session,
			visible: getComputedStyle(sc).visibility !== "hidden",
			scrollTop: sc.scrollTop,
			fromEnd: sc.scrollHeight - sc.scrollTop - sc.clientHeight,
			rows,
		};
		if (last?.frame === frame) w.__frames[w.__frames.length - 1] = next;
		else w.__frames.push(next);
	};
	new MutationObserver((records) => {
		for (const record of records)
			record.addedNodes.forEach((node) => {
				if (!(node instanceof HTMLElement) || !node.classList.contains("chat-row")) return;
				const owner = node.closest('[data-testid="chat-stream"]');
				if (owner !== observed) {
					rowObserver?.disconnect();
					rowObserver = new ResizeObserver(sample);
					observed = owner;
				}
				rowObserver?.observe(node);
			});
	}).observe(document, { childList: true, subtree: true });
	const tick = () => {
		frame += 1;
		sample();
		requestAnimationFrame(tick);
	};
	requestAnimationFrame(tick);
}

async function framesSince(page: Page, mark: number): Promise<Sample[]> {
	return page.evaluate((m) => (window as unknown as { __frames: Sample[] }).__frames.filter((s) => s.frame >= m), mark);
}

async function markFrames(page: Page): Promise<number> {
	return page.evaluate(() => {
		const w = window as unknown as { __frames: Sample[] };
		return (w.__frames.at(-1)?.frame ?? 0) + 1;
	});
}

/** The largest overlap between two adjacent on-screen rows in any painted frame. */
function maxOverlap(frames: Sample[]): number {
	let max = 0;
	for (const s of frames)
		for (let i = 1; i < s.rows.length; i++) max = Math.max(max, s.rows[i - 1].bottom - s.rows[i].top);
	return max;
}

/** The largest upward move of a row still on screen from one painted frame to the next. */
function maxMoveUp(frames: Sample[]): number {
	let max = 0;
	for (let i = 1; i < frames.length; i++) {
		const before = new Map(frames[i - 1].rows.map((r) => [r.key, r.top]));
		for (const row of frames[i].rows) {
			const top = before.get(row.key);
			if (top !== undefined) max = Math.max(max, top - row.top);
		}
	}
	return max;
}

/** The largest move in either direction of a row still on screen from one painted frame to the next. */
function maxMove(frames: Sample[]): number {
	let max = 0;
	for (let i = 1; i < frames.length; i++) {
		const before = new Map(frames[i - 1].rows.map((r) => [r.key, r.top]));
		for (const row of frames[i].rows) {
			const top = before.get(row.key);
			if (top !== undefined) max = Math.max(max, Math.abs(top - row.top));
		}
	}
	return max;
}

const stream = (page: Page) => page.getByTestId("chat-stream");
const fromEnd = (page: Page) => stream(page).evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);
const go = (page: Page, id: string) =>
	page.evaluate((id) => {
		location.hash = `#/projects/ao-demo/sessions/${id}`;
	}, id);

async function expectAtEnd(page: Page, id: string) {
	await expect(page.locator(`[data-testid="chat-pane"][data-session="${id}"] [data-connection="live"]`)).toBeVisible();
	await expect
		.poll(() => fromEnd(page), { message: `${id} lands on the newest row`, timeout: 4000 })
		.toBeLessThanOrEqual(2);
}

async function wheelOverStream(page: Page, steps: number, deltaY: number, everyMs: number) {
	const box = await stream(page).boundingBox();
	if (!box) throw new Error("no chat stream");
	await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
	for (let i = 0; i < steps; i++) {
		await page.mouse.wheel(0, deltaY);
		await page.waitForTimeout(everyMs);
	}
}

async function open(page: Page, id: string) {
	await page.goto(`/#/projects/ao-demo/sessions/${id}`);
	await expect(page.locator(`[data-testid="chat-pane"][data-session="${id}"] [data-connection="live"]`)).toBeVisible();
}

/**
 * The user drags the scrollbar to the bottom until it stays there (rows measured on the way can
 * grow the list under them), so these tests do not depend on where opening landed.
 */
async function scrollToEnd(page: Page) {
	await expect
		.poll(async () => {
			await stream(page).evaluate((el) => el.scrollTo(0, el.scrollHeight));
			await page.waitForTimeout(150);
			return fromEnd(page);
		})
		.toBeLessThanOrEqual(2);
}

async function openStreamingTall(page: Page) {
	await open(page, "demo-chat-tall");
	await scrollToEnd(page);
	await expect(stream(page).locator("[data-streaming]")).toBeVisible();
	await scrollToEnd(page);
}

test.describe("chat scroll", () => {
	test.use({ viewport: { width: 1920, height: 1080 }, colorScheme: "dark" });
	test.beforeEach(async ({ page }) => {
		await page.addInitScript(recordFrames);
	});

	test("opening a session, and switching while its log still loads, lands on the newest row", async ({ page }) => {
		await page.goto("/#/projects/ao-demo/sessions/demo-chat-tall");
		await expectAtEnd(page, "demo-chat-tall");

		const hops: Array<[string, string | null]> = [
			["demo-chat", null],
			["demo-chat-tall", null],
			["demo-chat", "demo-chat-tall"],
			["demo-chat-long", "demo-chat-tall"],
			["demo-chat-tall", "demo-chat"],
			["demo-chat-tall", null],
		];
		for (const [first, then] of hops) {
			await go(page, first);
			if (then) {
				await page.waitForTimeout(60);
				await go(page, then);
			}
			await expectAtEnd(page, then ?? first);
		}
	});

	test("a switched-to session paints its first frame at the newest row, with no scroll on the way", async ({
		page,
	}) => {
		await page.goto("/#/projects/ao-demo/sessions/demo-chat");
		await expectAtEnd(page, "demo-chat");
		for (const id of ["demo-chat-tall", "demo-chat", "demo-chat-long", "demo-chat-tall"]) {
			const mark = await markFrames(page);
			await go(page, id);
			await expectAtEnd(page, id);
			await page.waitForTimeout(300);
			const painted = (await framesSince(page, mark)).filter((s) => s.session === id && s.visible && s.rows.length > 0);
			expect(painted.length, `${id} painted frames`).toBeGreaterThan(0);
			const away = painted.filter((s) => s.fromEnd > 2).map((s) => `frame ${s.frame}: ${Math.round(s.fromEnd)}px`);
			expect(away, `${id}: frames painted above the newest row`).toEqual([]);
		}
	});

	test("a small scroll up while a reply streams is not pulled back to the end", async ({ page }) => {
		await openStreamingTall(page);
		const mark = await markFrames(page);
		await wheelOverStream(page, 40, -6, 16);
		await page.waitForTimeout(300);

		const frames = await framesSince(page, mark);
		expect(maxMoveUp(frames), "no row moves up against the scroll").toBeLessThanOrEqual(2);
		expect(await fromEnd(page), "the user left the end").toBeGreaterThan(100);
	});

	test("scrolling up through rows not yet measured paints no overlap and no jump", async ({ page }) => {
		await openStreamingTall(page);
		const mark = await markFrames(page);
		await wheelOverStream(page, 30, -80, 50);
		await page.waitForTimeout(300);

		const frames = await framesSince(page, mark);
		expect(maxOverlap(frames), "no painted frame overlaps two rows").toBeLessThanOrEqual(1);
		expect(maxMoveUp(frames), "no row moves up against the scroll").toBeLessThanOrEqual(2);
	});

	test("scrolled up, the view holds still while new turns arrive below", async ({ page }) => {
		await openStreamingTall(page);
		await wheelOverStream(page, 10, -150, 30);
		await page.waitForTimeout(300);
		const mark = await markFrames(page);
		await page.waitForTimeout(2000);

		const frames = await framesSince(page, mark);
		expect(maxMove(frames), "no row moves while the user is still").toBeLessThanOrEqual(1);
		expect(maxOverlap(frames), "no painted frame overlaps two rows").toBeLessThanOrEqual(1);
	});

	test("at the end, every painted frame shows the newest line while text streams", async ({ page }) => {
		await openStreamingTall(page);
		const mark = await markFrames(page);
		await page.waitForTimeout(1500);

		const frames = await framesSince(page, mark);
		const behind = frames.filter((s) => s.fromEnd > 2).map((s) => `frame ${s.frame}: ${Math.round(s.fromEnd)}px`);
		expect(behind, "frames left above the end").toEqual([]);
	});

	test("opening groups paints no overlapping rows", async ({ page }) => {
		await open(page, "demo-chat-long");
		const heads = stream(page).locator('[data-turn] .chat-group__head[aria-expanded="false"]');
		const mark = await markFrames(page);
		for (let i = 0; i < 2; i++) {
			await heads.last().click();
			await page.waitForTimeout(250);
			const groups = stream(page).locator('[data-group] .chat-group__head[aria-expanded="false"]');
			if ((await groups.count()) > 0) {
				await groups.last().click();
				await page.waitForTimeout(250);
			}
		}
		expect(maxOverlap(await framesSince(page, mark)), "no painted frame overlaps two rows").toBeLessThanOrEqual(1);
	});

	test("jump to latest appears when new content arrives below, and resumes following", async ({ page }) => {
		await openStreamingTall(page);
		const jump = page.getByRole("button", { name: "Jump to latest" });
		await expect(jump).toHaveCount(0);

		await wheelOverStream(page, 10, -150, 30);
		await expect(jump).toBeVisible();
		const glide = await markFrames(page);
		await jump.click();
		await expect.poll(() => fromEnd(page)).toBeLessThanOrEqual(2);
		await expect(jump).toHaveCount(0);
		const gliding = await framesSince(page, glide);
		expect(gliding.filter((s) => s.fromEnd > 2).length, "the jump glides over several frames").toBeGreaterThan(2);
		expect(maxOverlap(gliding), "no painted frame overlaps two rows").toBeLessThanOrEqual(1);

		const mark = await markFrames(page);
		await page.waitForTimeout(1000);
		const behind = (await framesSince(page, mark)).filter((s) => s.fromEnd > 2);
		expect(behind, "keeps following after the jump").toEqual([]);
	});

	test("a rail jump glides the turn's message to the top, paints no overlap, and stops following", async ({ page }) => {
		await openStreamingTall(page);
		const rail = page.getByRole("navigation", { name: "Turns" });
		const tick = rail.getByRole("button", { name: /^Turn 10:/ });
		const mark = await markFrames(page);
		await tick.click();
		const target = stream(page).locator(".chat-row", { hasText: "Step 10: run the checks" });
		await expect(target).toBeVisible();
		await page.waitForTimeout(1500);

		const key = await target.getAttribute("data-key");
		const frames = await framesSince(page, mark);
		const landed = frames.findIndex((s) => s.rows.some((r) => r.key === key));
		expect(landed, "the turn's message was painted").toBeGreaterThanOrEqual(0);
		const tops = frames.slice(landed).map((s) => s.rows.find((r) => r.key === key)?.top ?? NaN);
		expect(new Set(tops).size, "the message was painted at several positions on its way up").toBeGreaterThan(2);
		const rest = tops.findIndex((top, i) => tops.slice(i).every((t) => t === top));
		expect(tops[rest], "the message comes to rest at the top").toBeLessThanOrEqual(40);
		expect(maxMoveUp(frames.slice(landed)), "the glide only moves rows down toward the message").toBeLessThanOrEqual(1);
		expect(maxOverlap(frames), "no painted frame overlaps two rows").toBeLessThanOrEqual(1);
		await expect(tick).toHaveAttribute("aria-current", "true");
		expect(await fromEnd(page), "the jump left the end, and new turns below did not pull it back").toBeGreaterThan(100);
	});

	test("a wheel during a glide stops it and leaves the user where they scrolled", async ({ page }) => {
		await openStreamingTall(page);
		await wheelOverStream(page, 10, -150, 30);
		await page.getByRole("button", { name: "Jump to latest" }).click();
		await page.waitForTimeout(60);
		await wheelOverStream(page, 1, -200, 0);
		const mark = await markFrames(page);
		const stopped = await fromEnd(page);
		await page.waitForTimeout(600);
		expect(stopped, "the glide had not reached the end").toBeGreaterThan(100);
		expect(maxMove(await framesSince(page, mark)), "nothing moves once the user took over").toBeLessThanOrEqual(1);
	});

	test("with reduced motion, a rail jump lands in one frame", async ({ page }) => {
		await page.emulateMedia({ reducedMotion: "reduce" });
		await openStreamingTall(page);
		const tick = page.getByRole("navigation", { name: "Turns" }).getByRole("button", { name: /^Turn 10:/ });
		const mark = await markFrames(page);
		await tick.click();
		const target = stream(page).locator(".chat-row", { hasText: "Step 10: run the checks" });
		await expect(target).toBeVisible();
		await page.waitForTimeout(600);
		const key = await target.getAttribute("data-key");
		const tops = (await framesSince(page, mark)).flatMap((s) => s.rows.filter((r) => r.key === key).map((r) => r.top));
		expect(new Set(tops).size, `one landing position, saw ${[...new Set(tops)].join(", ")}`).toBe(1);
		expect(tops[0], "the message sits at the top").toBeLessThanOrEqual(40);
	});

	test("the rail's current tick follows the scroll", async ({ page }) => {
		await openStreamingTall(page);
		const rail = page.getByRole("navigation", { name: "Turns" });
		const current = rail.locator('[aria-current="true"]');
		const atEnd = await current.getAttribute("aria-label");
		await rail.getByRole("button", { name: /^Turn 5:/ }).click();
		await expect(current).toHaveAttribute("aria-label", /^Turn 5:/);
		await wheelOverStream(page, 3, 400, 50);
		await expect(current).not.toHaveAttribute("aria-label", /^Turn 5:/);
		expect(await current.getAttribute("aria-label")).not.toBe(atEnd);
	});

	test("scrolled up with nothing new below, there is no jump button", async ({ page }) => {
		await open(page, "demo-chat");
		await expect(stream(page).locator("[data-streaming]")).toContainText("So far 31 of 50 passed with no failures.");
		await scrollToEnd(page);
		await wheelOverStream(page, 6, -150, 30);
		await page.waitForTimeout(500);
		expect(await fromEnd(page)).toBeGreaterThan(100);
		await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
	});
});
