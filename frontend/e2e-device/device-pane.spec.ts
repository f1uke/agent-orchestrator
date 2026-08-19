/**
 * The Device tab, played the way a human plays it: a real Electron build, real
 * pointer input, a real simulator on the other end.
 *
 * Every case here is one an ordinary unit test cannot make: each depends on the
 * page being laid out and hit-tested, or on what actually reached the device.
 * The pane's rules that do not need a device - what is offered while the lease
 * is elsewhere, what the picker refuses - are tested in
 * `src/renderer/components/SimulatorPanel.test.tsx`, where they cost nothing.
 *
 * Run: `npm run test:device` (mac, Xcode, one booted simulator, and
 * `npm run package` for the renderer build). Everything missing is a skip.
 */
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import {
	type Sandbox,
	deviceState,
	dragThrough,
	focusWindow,
	openDevicePane,
	skipReason,
	startSandbox,
} from "./sandbox";

let sandbox: Sandbox;
const skip = skipReason();

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
	test.skip(skip !== null, skip ?? "");
	sandbox = await startSandbox();
	await openDevicePane(sandbox);
});

test.afterAll(async () => {
	await sandbox?.dispose();
});

/** post talks to the sandbox daemon the way the pane does. */
function post(sandbox: Sandbox, path: string, body: unknown): { status: number; code?: string } {
	const out = execFileSync(
		"curl",
		[
			"-sS",
			"-o",
			"-",
			"-w",
			"\n%{http_code}",
			"-X",
			"POST",
			`${sandbox.api}${path}`,
			"-H",
			"content-type: application/json",
			"-d",
			JSON.stringify(body),
		],
		{ encoding: "utf8" },
	);
	const lines = out.trim().split("\n");
	const status = Number(lines.pop());
	let code: string | undefined;
	try {
		code = (JSON.parse(lines.join("\n")) as { code?: string }).code;
	} catch {
		code = undefined;
	}
	return { status, code };
}

// Claiming is the first thing a person does - nothing can be touched until the
// lease is held - and it used to cost two presses: open a menu, then pick the
// item in it.
test("one press claims the device, and driving only appears after it", async () => {
	const claim = sandbox.page.getByRole("button", { name: /claim to drive|take over from/i });
	await expect(claim).toBeVisible();
	await expect(sandbox.page.getByRole("button", { name: /drive this device/i })).toHaveCount(0);

	await claim.click();

	await expect(sandbox.page.getByRole("button", { name: /drive this device/i })).toBeVisible();
	await expect(claim).toHaveCount(0);
	expect((await deviceState(sandbox)).lease).toMatchObject({ state: "held", holder: sandbox.sessionId });
});

// The complaint this pane was rebuilt for: a scroll arrived only once the
// finger came up, as one flick. While the pointer is down there has to be a
// touch on the device - which the daemon knows, and the picture does not.
test("a drag is on the device while the finger is still down", async () => {
	// Driving is opt-in and is what lets a pointer reach the device at all, so
	// it is a precondition of this case rather than part of what it proves.
	const drive = sandbox.page.getByRole("button", { name: /drive this device/i });
	await drive.click();
	await expect(drive).toHaveAttribute("aria-pressed", "true");

	const route = [0.75, 0.7, 0.65, 0.6, 0.55, 0.5, 0.45].map((y) => ({ x: 0.5, y }));
	// A move only lands while a drag is in flight; with none, the daemon says so.
	let midDrag: { status: number; code?: string } = { status: 0 };
	await dragThrough(sandbox, route, {
		whileDown: () => {
			midDrag = post(sandbox, `/api/v1/sessions/${sandbox.sessionId}/sim-devices/${sandbox.udid}/gesture`, {
				kind: "drag-move",
				x: 0.5,
				y: 0.4,
			});
		},
	});
	await sandbox.page.waitForTimeout(500);
	const afterRelease = post(sandbox, `/api/v1/sessions/${sandbox.sessionId}/sim-devices/${sandbox.udid}/gesture`, {
		kind: "drag-move",
		x: 0.5,
		y: 0.4,
	});

	expect(
		midDrag.status,
		`the finger was not down on the device while the pointer was (${midDrag.code ?? "no code"})`,
	).toBe(200);
	expect(afterRelease.code, "the touch outlived the pointer").toBe("SIM_DRAG_ENDED");
});

// The rule that keeps this pane cheap: a stream nobody is looking at is a
// process nobody needed. Two CPU-burning pollers have shipped here before.
test("nothing is captured once the tab is not the one on screen", async () => {
	const capturing = () => {
		try {
			execFileSync("pgrep", ["-f", `ao-device-harness-${process.pid}.*capture.mjs`], { stdio: "ignore" });
			return true;
		} catch {
			return false;
		}
	};
	expect(capturing(), "nothing was capturing while the Device tab was open").toBe(true);

	await sandbox.page.getByRole("tab", { name: "Summary", exact: true }).click();
	await expect.poll(capturing, { timeout: 15_000, message: "capture kept running with nobody looking" }).toBe(false);

	await sandbox.page.getByRole("tab", { name: "Device", exact: true }).click();
	await expect.poll(capturing, { timeout: 20_000, message: "the stream did not come back" }).toBe(true);
});

// The body drawn around the screen is the device's own, read from the artwork
// Xcode ships - a guessed one was visibly wrong twice.
test("the body around the screen is the device's own proportions", async () => {
	const frame = (await deviceState(sandbox)).frame;
	test.skip(!frame, "this machine has no chrome artwork for this device, so the pane draws no body");

	const drawn = await sandbox.page.evaluate(() => {
		const canvas = document.querySelector("[data-testid=sim-canvas]");
		const body = canvas?.parentElement;
		if (!canvas || !body) return null;
		const screen = canvas.getBoundingClientRect();
		const outer = body.getBoundingClientRect();
		return { bezel: (outer.width - screen.width) / 2, screenWidth: screen.width };
	});
	expect(drawn).not.toBeNull();
	// Within a pixel: the drawn body is rounded to whole pixels.
	expect(Math.abs(drawn!.bezel - drawn!.screenWidth * frame!.thickness)).toBeLessThanOrEqual(1.5);
});

// --- recording ---------------------------------------------------------------

/**
 * ⚠ The requirement, and it is a hard one: the device screen's dimensions must
 * be IDENTICAL with zero recordings, with one, and with fifty. A screen that
 * resizes as a side effect of unrelated activity is what made this pane feel
 * bad before, and "the screen is too small" is the complaint that produced the
 * 84.6% measurement it now has to hold.
 *
 * jsdom cannot see this at all - it has no layout - so it has to be measured
 * here, and it is measured rather than eyeballed so this goes red if somebody
 * later makes the list push the screen.
 */
async function deviceBox(sandbox: Sandbox) {
	return sandbox.page.evaluate(() => {
		const canvas = document.querySelector("[data-testid=sim-canvas]");
		if (!canvas) return null;
		const box = canvas.getBoundingClientRect();
		return { width: Math.round(box.width), height: Math.round(box.height), top: Math.round(box.top) };
	});
}

/** Writes flows straight into the session's own directory, as a recording would. */
function seedRecordings(sandbox: Sandbox, count: number): void {
	const dir = path.join(sandbox.dataDir, "sim", sandbox.sessionId, "flows");
	mkdirSync(dir, { recursive: true });
	for (let i = 0; i < count; i += 1) {
		const stamp = `20260818-0400${String(i).padStart(2, "0")}.000Z`;
		const body = [
			"appId: ${APP_ID}",
			"---",
			"# recorded by ao sim at t, device d (r)",
			"# 3 step(s), 1 needing review",
			'- tapOn: "Home"',
			"",
		].join("\n");
		writeFileSync(path.join(dir, `seeded-flow-${i}-${stamp}.yaml`), body);
	}
}

function clearRecordings(sandbox: Sandbox): void {
	rmSync(path.join(sandbox.dataDir, "sim", sandbox.sessionId, "flows"), { recursive: true, force: true });
}

test("the device screen is exactly the same size with no recordings, one, and fifty", async () => {
	clearRecordings(sandbox);
	await sandbox.page.getByTestId("sim-recordings-trigger").click();
	await sandbox.page.keyboard.press("Escape");
	await expect.poll(() => sandbox.page.getByTestId("sim-recordings-count").textContent()).toBe("0");
	const empty = await deviceBox(sandbox);
	expect(empty, "no device screen to measure").not.toBeNull();

	seedRecordings(sandbox, 1);
	await expect
		.poll(() => sandbox.page.getByTestId("sim-recordings-count").textContent(), { timeout: 15_000 })
		.toBe("1");
	expect(await deviceBox(sandbox), "one recording moved the screen").toEqual(empty);

	seedRecordings(sandbox, 50);
	await expect
		.poll(() => sandbox.page.getByTestId("sim-recordings-count").textContent(), { timeout: 15_000 })
		.toBe("50");
	expect(await deviceBox(sandbox), "fifty recordings moved the screen").toEqual(empty);

	// And with the list actually open and scrolling, which is the case a
	// disclosure would have failed.
	await sandbox.page.getByTestId("sim-recordings-trigger").click();
	const list = sandbox.page.getByTestId("sim-recordings-list");
	await expect(list).toBeVisible();
	expect(
		await list.evaluate((el) => el.scrollHeight > el.clientHeight),
		"fifty rows should overflow the list's own bounded height",
	).toBe(true);
	expect(await deviceBox(sandbox), "opening the list moved the screen").toEqual(empty);

	await sandbox.page.keyboard.press("Escape");
	clearRecordings(sandbox);
});

// A control can render, pass every unit test and still be invisible or
// unhittable - this repo has shipped exactly that. So: is the thing at the
// button's own centre point the button?
test("the record button is actually visible and actually hittable", async () => {
	const button = sandbox.page.getByTestId("sim-record-toggle");
	await expect(button).toBeVisible();

	const hit = await button.evaluate((el) => {
		const box = el.getBoundingClientRect();
		const at = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
		const style = getComputedStyle(el);
		return {
			ownsItsCentre: el === at || el.contains(at),
			width: box.width,
			height: box.height,
			visibility: style.visibility,
			opacity: style.opacity,
		};
	});
	expect(hit.ownsItsCentre, "something else is on top of the record button").toBe(true);
	expect(hit.width).toBeGreaterThan(24);
	expect(hit.height).toBeGreaterThan(20);
	expect(hit.visibility).toBe("visible");
	expect(Number(hit.opacity)).toBeGreaterThan(0.5);
});

// The whole loop, through the real routes: record, drag by hand, watch the
// count move, stop, and find the flow on disk with the counts it claimed.
test("recording captures a hand drag, and stop writes the flow it reports", async () => {
	clearRecordings(sandbox);
	const drive = sandbox.page.getByRole("button", { name: /drive this device/i });
	if ((await drive.getAttribute("aria-pressed")) !== "true") await drive.click();
	await expect(drive).toHaveAttribute("aria-pressed", "true");

	const button = sandbox.page.getByTestId("sim-record-toggle");
	await expect(button).toBeEnabled();
	await button.click();
	await expect(button).toHaveAttribute("aria-pressed", "true");
	await expect(sandbox.page.getByTestId("sim-record-count")).toHaveText("0");

	await dragThrough(
		sandbox,
		[0.5, 0.45, 0.4, 0.35].map((y) => ({ x: 0.5, y })),
	);

	// 🗝 The count has to MOVE. A recorder that is not wired to the daemon
	// leaves it at zero while start and stop both succeed, and this is the
	// assertion that catches that.
	await expect
		.poll(() => sandbox.page.getByTestId("sim-record-count").textContent(), {
			timeout: 15_000,
			message: "the step count never moved - the recorder captured nothing",
		})
		.not.toBe("0");

	await button.click();
	const summary = sandbox.page.getByTestId("sim-stop-summary");
	await expect(summary).toBeVisible();
	const reported = await summary.textContent();

	const dir = path.join(sandbox.dataDir, "sim", sandbox.sessionId, "flows");
	const written = readdirSync(dir).filter((f) => f.endsWith(".yaml"));
	expect(written, "stop reported a flow that is not on disk").toHaveLength(1);
	const body = readFileSync(path.join(dir, written[0]), "utf8");
	expect(body).toContain("appId: ${APP_ID}");
	// What the summary claims and what the file says are the same numbers.
	const counts = body.match(/# (\d+) step\(s\), (\d+) needing review/);
	expect(counts, `the flow states no counts:\n${body}`).not.toBeNull();
	expect(reported).toContain(`${counts![1]} step`);

	await expect(button).toHaveAttribute("aria-pressed", "false");
});

/**
 * Skips a case that can only mean anything on a guest which reads US ASCII key
 * presses as the characters they were sent as.
 *
 * ⚠ The guest follows the MAC'S OWN input source, so these cases quietly depend
 * on the language the developer happens to be typing in. On a Mac set to Thai
 * the very same ASCII goes through the guest pasteboard instead - correctly,
 * but batched, several seconds slower, and with iOS's smart-insert space
 * turning "aXb" into "a X b". Without this they fail with a confusing number or
 * a confusing string rather than saying which machine they are on.
 */
function skipUnlessGuestTakesUSKeys(sandbox: Sandbox) {
	const keyboard = JSON.parse(
		execFileSync("curl", ["-sS", `${sandbox.api}/api/v1/sim/devices/${sandbox.udid}/keyboard`], {
			encoding: "utf8",
		}),
	) as { sendsUSASCII?: boolean; mode?: string };
	test.skip(
		keyboard.sendsUSASCII !== true,
		`this Mac's input source makes the guest ${keyboard.mode ?? "unknown"}, which routes even ASCII through the pasteboard`,
	);
}

/**
 * 🗝 A character has to appear about as fast as it does when typing into
 * Simulator.app, because a person types by watching characters land.
 *
 * Measured before this guard existed: one character took 1164-1181 ms to reach
 * the device - a 250 ms wait for the human to pause, then ~935 ms for the
 * daemon to ask the guest which keyboard input mode it was in, in front of
 * every keystroke - and a burst arrived all at once when they stopped typing.
 * After: 8-14 ms, of which the device itself is 3-6 ms.
 *
 * ⚠ jsdom can measure none of this: it has no daemon, no guest and no clock
 * that means anything. The bound here is deliberately far above what was
 * measured and far below what the bug cost, so it catches the probe coming back
 * onto the keystroke path without failing on an unlucky second.
 */
test("a character reaches the device without waiting for a pause", async () => {
	// First line on purpose: a skipped case must leave the device exactly as it
	// found it. Claiming and driving before deciding to skip churns state that
	// later timing cases in this serial file measure.
	skipUnlessGuestTakesUSKeys(sandbox);
	await readyToDrive(sandbox);
	await openSpotlightField(sandbox);
	const canvas = sandbox.page.getByTestId("sim-canvas");
	await canvas.focus();
	for (let i = 0; i < 20; i += 1) await sandbox.page.keyboard.press("Backspace");
	await sandbox.page.waitForTimeout(1500);

	skipUnlessGuestTakesUSKeys(sandbox);

	const seen: { at: number; kind: string }[] = [];
	const onFinished = (request: import("@playwright/test").Request) => {
		if (!request.url().includes("/gesture")) return;
		let kind = "";
		try {
			kind = (JSON.parse(request.postData() ?? "{}") as { kind?: string }).kind ?? "";
		} catch {
			kind = "";
		}
		const timing = request.timing();
		seen.push({ at: timing.startTime + Math.max(0, timing.responseEnd), kind });
	};
	sandbox.page.on("requestfinished", onFinished);

	const pressed = Date.now();
	await sandbox.page.keyboard.press("a");
	await expect.poll(() => seen.filter((s) => s.kind === "type").length, { timeout: 10_000 }).toBe(1);
	sandbox.page.off("requestfinished", onFinished);

	const echo = seen.filter((s) => s.kind === "type")[0].at - pressed;
	expect(echo, `a character took ${Math.round(echo)} ms to reach the device`).toBeLessThan(400);
});

/**
 * The pane says when something typed has not arrived yet - and saying it must
 * not move the screen.
 *
 * ⚠ Only measurable here. jsdom has no layout, so it cannot see a row growing
 * by an icon and pushing the device screen down; and the state itself is only
 * reachable through a send that genuinely takes time. Thai is that send: no US
 * keyboard key can produce those runes, so they go through the guest pasteboard
 * whatever the input mode is, measured at ~2.9 s.
 */
test("saying that typing is on its way does not move the screen", async () => {
	await readyToDrive(sandbox);
	await openSpotlightField(sandbox);
	const canvas = sandbox.page.getByTestId("sim-canvas");
	await canvas.focus();
	for (let i = 0; i < 20; i += 1) await sandbox.page.keyboard.press("Backspace");
	await sandbox.page.waitForTimeout(1500);

	const slot = sandbox.page.getByTestId("sim-typing-waiting");
	await expect(slot, "the slot must always be in the row, so nothing moves when it fills").toBeAttached();
	const before = await canvas.boundingBox();

	// ⚠ Neither `press` nor `type` delivers a Thai rune: one wants a named key,
	// the other falls back to insertText, which fires no keydown at all. A Thai
	// Mac sends a real keydown carrying the rune.
	const cdp = await sandbox.app.context().newCDPSession(sandbox.page);
	for (const ch of "สวัสดี") {
		await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: ch, text: ch });
		await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: ch });
		await sandbox.page.waitForTimeout(60);
	}

	await expect(slot, "the human was left watching nothing happen").not.toBeEmpty({ timeout: 10_000 });
	// Actually visible, and actually where it says it is - not clipped to
	// nothing or painted under something else.
	const box = await slot.boundingBox();
	expect(box, "the indicator has no box at all").not.toBeNull();
	expect(box!.width, "the indicator is clipped to nothing").toBeGreaterThan(4);
	expect(box!.height).toBeGreaterThan(4);
	const onTop = await sandbox.page.evaluate(() => {
		const el = document.querySelector('[data-testid="sim-typing-waiting"]');
		if (!el) return false;
		const r = el.getBoundingClientRect();
		const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
		return el.contains(hit) || hit === el;
	});
	expect(onTop, "something is painted over the indicator").toBe(true);

	// The screen has not moved by a pixel while it is showing.
	const during = await canvas.boundingBox();
	expect(during).toEqual(before);

	// And it stops saying so once the text has landed.
	await expect(slot).toBeEmpty({ timeout: 20_000 });
	expect(await canvas.boundingBox()).toEqual(before);
});

/**
 * A recorded step is what the human did, not how the pane chunked it.
 *
 * Typing is sent a character at a time on a guest that reads US ASCII
 * faithfully - that is what makes it immediate - so without coalescing, one
 * word would record five steps and emit five `inputText` lines.
 */
test("a word typed while recording is one step and one inputText", async () => {
	skipUnlessGuestTakesUSKeys(sandbox);
	clearRecordings(sandbox);
	await readyToDrive(sandbox);
	await openSpotlightField(sandbox);

	const button = sandbox.page.getByTestId("sim-record-toggle");
	await expect(button).toBeEnabled();
	await button.click();
	await expect(button).toHaveAttribute("aria-pressed", "true");

	const canvas = sandbox.page.getByTestId("sim-canvas");
	await canvas.focus();
	await sandbox.page.keyboard.type("hello");
	await expect
		.poll(() => sandbox.page.getByTestId("sim-record-count").textContent(), { timeout: 15_000 })
		.not.toBe("0");
	// The whole word is one step, however many requests carried it.
	await expect(sandbox.page.getByTestId("sim-record-count")).toHaveText("1");

	await button.click();
	await expect(button).toHaveAttribute("aria-pressed", "false");

	const dir = path.join(sandbox.dataDir, "sim", sandbox.sessionId, "flows");
	// ⚠ Polled, and deliberately NOT gated on the stop summary being visible:
	// an earlier case in this file leaves its own summary on screen, so waiting
	// for one proves nothing about THIS stop and would pass before the flow was
	// written. The file is the evidence - the directory was cleared at the top
	// of this case, so exactly one flow in it can only be this recording's. On
	// a failure it prints what is actually there, so "no flow" and "a flow
	// somewhere else" are distinguishable without a second run.
	await expect
		.poll(() => (existsSync(dir) ? readdirSync(dir).filter((f) => f.endsWith(".yaml")).length : -1), {
			timeout: 10_000,
			message: `no flow appeared in ${dir} (session dir holds: ${
				existsSync(path.dirname(dir)) ? readdirSync(path.dirname(dir)).join(", ") : "nothing"
			})`,
		})
		.toBe(1);
	const written = readdirSync(dir).filter((f) => f.endsWith(".yaml"));
	const body = readFileSync(path.join(dir, written[0]), "utf8");
	expect(body).toContain('- inputText: "hello"');
	expect(
		(body.match(/- inputText:/g) ?? []).length,
		`one word must not become one inputText per character:\n${body}`,
	).toBe(1);
});

// One recording per session, not one per surface: what a terminal starts is
// what the tab shows.
test("a recording started outside the tab shows up in it", async () => {
	const started = post(sandbox, `/api/v1/sessions/${sandbox.sessionId}/sim-recordings/${sandbox.udid}`, {});
	expect(started.status, "could not start a recording the way the CLI does").toBe(200);

	const button = sandbox.page.getByTestId("sim-record-toggle");
	await expect(button).toHaveAttribute("aria-pressed", "true", { timeout: 15_000 });

	await button.click();
	await expect(button).toHaveAttribute("aria-pressed", "false");
});

/**
 * The narrow cases, where a control that grows by one character costs the
 * device a whole line of height: the toolbar row wraps, and the row is above
 * the screen's own flex space.
 *
 * 960px is the app's minimum window width. Both the recording state changing
 * and the count climbing are checked, because those are the two things that
 * change while somebody is looking at the screen rather than at the toolbar.
 */
test("nothing about recording resizes the screen at the minimum window width", async () => {
	clearRecordings(sandbox);
	await sandbox.app.evaluate(({ BrowserWindow }) => {
		BrowserWindow.getAllWindows()[0]?.setSize(960, 800);
	});
	await sandbox.page.waitForTimeout(500);

	const drive = sandbox.page.getByRole("button", { name: /drive this device/i });
	if ((await drive.getAttribute("aria-pressed")) !== "true") await drive.click();

	const button = sandbox.page.getByTestId("sim-record-toggle");
	const idle = await deviceBox(sandbox);
	expect(idle).not.toBeNull();

	await button.click();
	await expect(button).toHaveAttribute("aria-pressed", "true");
	expect(await deviceBox(sandbox), "turning recording on resized the screen").toEqual(idle);

	// And with a count wide enough to have widened an unconstrained slot.
	await dragThrough(
		sandbox,
		[0.5, 0.45, 0.4].map((y) => ({ x: 0.5, y })),
	);
	await expect
		.poll(() => sandbox.page.getByTestId("sim-record-count").textContent(), { timeout: 15_000 })
		.not.toBe("0");
	expect(await deviceBox(sandbox), "a climbing step count resized the screen").toEqual(idle);

	await button.click();
	await sandbox.page.getByTestId("sim-stop-summary").waitFor();

	// The list has to fit inside the window at this width, not spill out of it.
	await sandbox.page.getByTestId("sim-recordings-trigger").click();
	const popover = sandbox.page.getByTestId("sim-recordings-popover");
	await expect(popover).toBeVisible();
	const fits = await popover.evaluate((el) => {
		const box = el.getBoundingClientRect();
		return { left: box.left, right: box.right, width: window.innerWidth };
	});
	expect(fits.left).toBeGreaterThanOrEqual(0);
	expect(fits.right).toBeLessThanOrEqual(fits.width);
	await sandbox.page.keyboard.press("Escape");

	clearRecordings(sandbox);
});

// --- recording must not change how the tab behaves ---------------------------

/**
 * ⚠ The regression this pins, which shipped in #208 and the human hit at once:
 * with a recording open, driving the tab got slow and drag-to-scroll stopped
 * working ENTIRELY.
 *
 * One cause, two symptoms. The recorder read the accessibility tree between
 * the finger going down and the touch reaching the device; the bridge
 * serializes reads and touches, so that read (~0.5 s here, ~1.5 s on a real
 * app) was time the finger spent in the air. And because the drag stream sends
 * one request at a time and coalesces moves, a ~0.5 s stall on `drag-begin`
 * meant every intermediate move was collapsed and then dropped by the end:
 * the device received a touch-down and a touch-up with NO motion between them,
 * which is not a scroll at any speed.
 *
 * Measured, before: tap 45 ms -> 616 ms, and a drag went from 9 gesture
 * requests to 2. After: identical either way.
 *
 * jsdom can see none of this. The request COUNT is the structurally checkable
 * half of it and is what makes the scroll failure visible here; the
 * milliseconds are in the record.
 */
function countGestures(sandbox: Sandbox): { stop: () => { kinds: string[]; slowest: number } } {
	const seen: { kind: string; ms: number }[] = [];
	const onFinished = (request: import("@playwright/test").Request) => {
		if (!request.url().includes("/gesture")) return;
		const timing = request.timing();
		let kind = "";
		try {
			kind = (JSON.parse(request.postData() ?? "{}") as { kind?: string }).kind ?? "";
		} catch {
			kind = "";
		}
		seen.push({ kind, ms: Math.max(0, timing.responseEnd - timing.requestStart) });
	};
	sandbox.page.on("requestfinished", onFinished);
	return {
		stop: () => {
			sandbox.page.off("requestfinished", onFinished);
			return { kinds: seen.map((s) => s.kind), slowest: Math.max(0, ...seen.map((s) => s.ms)) };
		},
	};
}

async function dragForScroll(sandbox: Sandbox) {
	await dragThrough(
		sandbox,
		[0.75, 0.7, 0.65, 0.6, 0.55, 0.5, 0.45].map((y) => ({ x: 0.5, y })),
	);
	await sandbox.page.waitForTimeout(800);
}

test("a recording does not change how a drag reaches the device", async () => {
	clearRecordings(sandbox);
	const drive = sandbox.page.getByRole("button", { name: /drive this device/i });
	if ((await drive.getAttribute("aria-pressed")) !== "true") await drive.click();
	await expect(drive).toHaveAttribute("aria-pressed", "true");

	const plain = countGestures(sandbox);
	await dragForScroll(sandbox);
	const withoutRecording = plain.stop();
	const moves = withoutRecording.kinds.filter((k) => k === "drag-move").length;
	expect(moves, "the baseline drag delivered no motion, so this test cannot mean anything").toBeGreaterThan(2);

	const button = sandbox.page.getByTestId("sim-record-toggle");
	await button.click();
	await expect(button).toHaveAttribute("aria-pressed", "true");

	const recorded = countGestures(sandbox);
	await dragForScroll(sandbox);
	const withRecording = recorded.stop();

	// The shape of the gesture must be identical. Before the fix this was
	// ["drag-begin", "drag-end"] - every move swallowed.
	expect(withRecording.kinds, "recording changed the drag the device received").toEqual(withoutRecording.kinds);
	// And nothing on the path may block for anything like an accessibility
	// read. The threshold is far above the ~2 ms these take and far below the
	// ~500 ms a read costs, so it fails on the regression and not on a slow
	// machine.
	expect(withRecording.slowest, "a gesture blocked long enough to have taken a screen read").toBeLessThan(250);

	await button.click();
	await sandbox.page.getByTestId("sim-stop-summary").waitFor();
});

// Speed must not have been bought with the drag end coordinates #208 fixed.
// Both properties, proved by the same drag.
test("a drag recorded at full speed still records where it really ended", async () => {
	clearRecordings(sandbox);
	const drive = sandbox.page.getByRole("button", { name: /drive this device/i });
	if ((await drive.getAttribute("aria-pressed")) !== "true") await drive.click();

	const button = sandbox.page.getByTestId("sim-record-toggle");
	await button.click();
	await expect(button).toHaveAttribute("aria-pressed", "true");

	const counted = countGestures(sandbox);
	await dragThrough(
		sandbox,
		[0.8, 0.7, 0.6, 0.5, 0.4].map((y) => ({ x: 0.5, y })),
	);
	await sandbox.page.waitForTimeout(800);
	const during = counted.stop();
	expect(during.slowest, "the gesture path blocked").toBeLessThan(250);

	await button.click();
	await sandbox.page.getByTestId("sim-stop-summary").waitFor();

	const dir = path.join(sandbox.dataDir, "sim", sandbox.sessionId, "flows");
	const written = readdirSync(dir).filter((f) => f.endsWith(".yaml"));
	expect(written).toHaveLength(1);
	const body = readFileSync(path.join(dir, written[0]), "utf8");
	const swipe = body.match(/- swipe: \{start: "(\d+)%,(\d+)%", end: "(\d+)%,(\d+)%"\}/);
	expect(swipe, `no swipe in the recorded flow:\n${body}`).not.toBeNull();
	const [, , startY, , endY] = swipe!;
	// It went up the screen, and the end is where the finger left - not the
	// middle of the screen, which is what the fallback used to record.
	expect(Number(endY), "the drag was recorded as ending where it started").toBeLessThan(Number(startY));
	expect(Number(endY)).toBeLessThan(50);
	clearRecordings(sandbox);
});

// --- the human's own keyboard ------------------------------------------------

/**
 * Typing was the one thing this tab did not forward. You could tap a field,
 * watch the caret appear, type - and nothing arrived, so the only way in was
 * `ao sim type` from a terminal while looking at the tab.
 *
 * ⚠ jsdom can see none of this: not whether a key reached the device, and not
 * focus as a browser resolves it. What is pinned here is delivery and scoping
 * on a real device; the character-by-character rules are in
 * src/renderer/hooks/useDeviceKeyboard.test.ts, where they cost nothing.
 */
async function deviceField(sandbox: Sandbox): Promise<string> {
	const tree = execFileSync(sandbox.aoBin, ["sim", "ax", "--udid", sandbox.udid], { encoding: "utf8" });
	const line = tree.split("\n").find((l) => l.includes('TextField "'));
	const value = line?.match(/TextField "([^"]*)"/)?.[1] ?? "";
	// Spotlight shows its placeholder when the field is empty.
	return value === "Search" ? "" : value.trim();
}

/**
 * Claims the device if it is not already this session's, and turns driving on.
 *
 * It also puts the window back to a comfortable size and in front, because an
 * earlier case in this file deliberately shrinks it to the minimum width and
 * nothing captures while the window is in the background - so a case that
 * needs to drive has to set that up rather than inherit whatever the last one
 * left.
 */
async function readyToDrive(sandbox: Sandbox) {
	await sandbox.app.evaluate(({ BrowserWindow }) => {
		BrowserWindow.getAllWindows()[0]?.setSize(1280, 900);
	});
	await focusWindow(sandbox);
	await sandbox.page.waitForTimeout(500);
	const claim = sandbox.page.getByRole("button", { name: /claim to drive|take over from/i });
	if (await claim.isVisible().catch(() => false)) await claim.click();
	const drive = sandbox.page.getByRole("button", { name: /drive this device/i });
	await expect(drive).toBeVisible({ timeout: 20_000 });
	if ((await drive.getAttribute("aria-pressed")) !== "true") await drive.click();
	await expect(drive).toHaveAttribute("aria-pressed", "true");
}

/**
 * Opens Spotlight and puts the caret in its search field.
 *
 * ⚠ It CHECKS that Spotlight actually opened rather than assuming it. These
 * cases run in series against a device that other cases have been driving, so
 * whatever app is in front when this starts is not fixed - and a pull-down that
 * lands on the wrong screen leaves the following keystrokes going nowhere,
 * which reads as "typing does not reach the device" when the truth is that no
 * field was ever focused.
 */
async function openSpotlightField(sandbox: Sandbox) {
	for (let attempt = 0; attempt < 3; attempt += 1) {
		await pullSpotlightDown(sandbox);
		if (await hasTextField(sandbox)) return;
	}
	throw new Error("Spotlight never opened, so there was no field to type into");
}

/** Whether the device is showing a text field at all. */
async function hasTextField(sandbox: Sandbox): Promise<boolean> {
	const tree = execFileSync(sandbox.aoBin, ["sim", "ax", "--udid", sandbox.udid], { encoding: "utf8" });
	return tree.includes('TextField "');
}

async function pullSpotlightDown(sandbox: Sandbox) {
	// exact, because a recorded flow's path contains "home" and the copy button
	// carries that whole path in its accessible name - which is right for the
	// copy button and ambiguous for this locator.
	//
	// Disabled while a gesture is still in flight, so wait for the pane to be
	// idle rather than racing it.
	const home = sandbox.page.getByRole("button", { name: "Home", exact: true });
	await expect(home).toBeEnabled({ timeout: 20_000 });
	await home.click();
	await sandbox.page.waitForTimeout(1500);
	const box = await sandbox.page.getByTestId("sim-canvas").boundingBox();
	if (!box) throw new Error("no canvas");
	await sandbox.page.mouse.move(box.x + box.width * 0.5, box.y + box.height * 0.35);
	await sandbox.page.mouse.down();
	for (const y of [0.45, 0.55, 0.65, 0.75]) {
		await sandbox.page.mouse.move(box.x + box.width * 0.5, box.y + box.height * y);
		await sandbox.page.waitForTimeout(16);
	}
	await sandbox.page.mouse.up();
	await sandbox.page.waitForTimeout(2000);
	await sandbox.page.mouse.move(box.x + box.width * 0.5, box.y + box.height * 0.936);
	await sandbox.page.mouse.down();
	await sandbox.page.mouse.up();
	await sandbox.page.waitForTimeout(1500);
}

test("typing on the human's own keyboard reaches the device", async () => {
	skipUnlessGuestTakesUSKeys(sandbox);
	await readyToDrive(sandbox);
	await openSpotlightField(sandbox);
	const canvas = sandbox.page.getByTestId("sim-canvas");
	await canvas.focus();
	// Clear whatever a previous run left, using the key under test.
	for (let i = 0; i < 20; i += 1) await sandbox.page.keyboard.press("Backspace");
	await sandbox.page.waitForTimeout(2500);

	await sandbox.page.keyboard.type("abc");
	await sandbox.page.waitForTimeout(2500);
	expect(await deviceField(sandbox), "what was typed did not reach the field").toBe("abc");

	// Backspace, then the arrows - a caret move is the only thing that can put
	// a character in the MIDDLE of what was already there.
	await sandbox.page.keyboard.press("Backspace");
	await sandbox.page.waitForTimeout(2000);
	expect(await deviceField(sandbox)).toBe("ab");
	await sandbox.page.keyboard.press("ArrowLeft");
	await sandbox.page.waitForTimeout(800);
	await sandbox.page.keyboard.type("X");
	await sandbox.page.waitForTimeout(2500);
	expect(await deviceField(sandbox), "the arrow key did not move the caret").toBe("aXb");
});

// ⚠ The rule that keeps the rest of AO usable: keys reach the device only when
// the device surface has focus. jsdom cannot judge focus the way a browser
// does, so this is checked where focus is real.
test("keys do not reach the device unless the device surface has focus", async () => {
	await readyToDrive(sandbox);
	await openSpotlightField(sandbox);
	const canvas = sandbox.page.getByTestId("sim-canvas");
	await canvas.focus();
	for (let i = 0; i < 20; i += 1) await sandbox.page.keyboard.press("Backspace");
	await sandbox.page.waitForTimeout(2500);
	const before = await deviceField(sandbox);

	// Escape is the way out, and is never sent on.
	await sandbox.page.keyboard.press("Escape");
	await expect(canvas).not.toBeFocused();
	await sandbox.page.keyboard.type("nope");
	await sandbox.page.waitForTimeout(2500);
	expect(await deviceField(sandbox), "keys reached the device with the surface unfocused").toBe(before);
});

/**
 * ⚠ The case the human kept hitting, and the one the earlier regression test
 * could not see.
 *
 * That test drags on the springboard, where the application root element spans
 * the whole screen - so something always resolves under the finger and the
 * recorder always takes its fast path. The failure needs a frontmost app whose
 * accessibility tree cannot be read at all, and there is no shortage of those:
 * on this device Safari and Settings both publish NOTHING.
 *
 * When that happened, the recorder read the screen on the gesture path, the
 * bridge serialized that read ahead of the touch, and the drag stream - which
 * kept only the newest unsent position - collapsed the whole scroll into a
 * touch-down and a touch-up. Measured with a human driving the pane: 11 of 26
 * drags arrived as two requests and no motion, every one of them with a begin
 * of 181-526 ms.
 */
test("a drag still carries its motion on a screen nothing can be read from", async () => {
	await readyToDrive(sandbox);
	clearRecordings(sandbox);

	// Safari, brought to the front on whatever it was already showing: up,
	// frontmost, and publishing no accessibility elements. Launching an app is
	// not booting a device - nothing here boots, shuts down or erases anything.
	execFileSync("xcrun", ["simctl", "launch", sandbox.udid, "com.apple.mobilesafari"]);
	await sandbox.page.waitForTimeout(5000);

	const record = sandbox.page.getByTestId("sim-record-toggle");
	if ((await record.getAttribute("aria-pressed")) !== "true") await record.click();
	await expect(record).toHaveAttribute("aria-pressed", "true");

	const counted = countGestures(sandbox);
	await dragForScroll(sandbox);
	const seen = counted.stop();

	const moves = seen.kinds.filter((k) => k === "drag-move").length;
	expect(moves, `the drag reached the device as ${seen.kinds.join(", ")} - its motion was swallowed`).toBeGreaterThan(
		2,
	);
	expect(seen.kinds[0]).toBe("drag-begin");
	expect(seen.kinds[seen.kinds.length - 1]).toBe("drag-end");
	// And nothing on the path blocked for anything like an accessibility read.
	expect(seen.slowest, "a gesture blocked long enough to have taken a screen read").toBeLessThan(250);

	await record.click();
	await sandbox.page.getByTestId("sim-stop-summary").waitFor();

	// The flow must be honest about what it could not describe, rather than
	// looking complete.
	const dir = path.join(sandbox.dataDir, "sim", sandbox.sessionId, "flows");
	const body = readFileSync(path.join(dir, readdirSync(dir).filter((f) => f.endsWith(".yaml"))[0]), "utf8");
	expect(body, `a swipe nothing could be described from must say so:\n${body}`).toContain("# REVIEW:");
	expect(body).toMatch(/- swipe: \{start: "\d+%,\d+%", end: "\d+%,\d+%"\}/);
	const counts = body.match(/# (\d+) step\(s\), (\d+) needing review/);
	expect(counts).not.toBeNull();
	const markers = body.split("\n").filter((l) => l.startsWith("# REVIEW:")).length;
	expect(markers, "the banner counts steps it never marked").toBe(Number(counts![2]));

	await sandbox.page.getByRole("button", { name: "Home", exact: true }).click();
	await sandbox.page.waitForTimeout(1000);
	clearRecordings(sandbox);
});

// ⚠ These two go LAST on purpose. They take the lease away and give it back,
// and #209's recorder keeps a per-device screen that a lease change discards -
// so the first recorded gesture after them pays the fallback read, which is
// exactly what the drag-timing case above measures. Ordering is load-bearing
// in a serial file that drives one real device.
/**
 * 🗝 The bug somebody lost real working time to.
 *
 * AO's leases last ten minutes, so anybody working longer than that loses one
 * and takes it back. Driving never came back with it: the daemon said the lease
 * was theirs, the pill said live, and every press vanished in silence, so they
 * had to ask another person what was wrong.
 *
 * ⚠ Only measurable here. jsdom has no daemon to hold a lease, no lease to
 * lapse, and no device to refuse a gesture - the pane's own unit tests can pin
 * the state machine, but "the press reached the device" is this.
 */
test("a lease that lapses and comes back can drive again", async () => {
	await readyToDrive(sandbox);
	const drive = sandbox.page.getByRole("button", { name: /drive this device/i });
	await expect(drive).toHaveAttribute("aria-pressed", "true");

	const lease = (method: "POST" | "DELETE") => {
		const url =
			method === "POST"
				? `${sandbox.api}/api/v1/sessions/${sandbox.sessionId}/sim-leases`
				: `${sandbox.api}/api/v1/sessions/${sandbox.sessionId}/sim-leases/${sandbox.udid}`;
		const args = ["-sS", "-o", "/dev/null", "-w", "%{http_code}", "-X", method, url];
		if (method === "POST") {
			args.push("-H", "content-type: application/json", "-d", JSON.stringify({ udid: sandbox.udid }));
		}
		expect(Number(execFileSync("curl", args, { encoding: "utf8" }).trim())).toBe(200);
	};

	// The lease lapses and is taken again by this same session - nobody else
	// ever held it, so nothing moved the screen underneath.
	lease("DELETE");
	await expect(sandbox.page.getByRole("button", { name: /claim to drive/i })).toBeVisible({ timeout: 20_000 });
	lease("POST");
	await expect(drive).toBeVisible({ timeout: 20_000 });

	// Driving is back without being re-armed by hand...
	await expect(drive, "the lease came back and driving did not").toHaveAttribute("aria-pressed", "true", {
		timeout: 20_000,
	});

	// ...and a press actually reaches the device again, which is the thing the
	// human could not do.
	const seen: string[] = [];
	const onRequest = (request: import("@playwright/test").Request) => {
		if (request.url().includes("/gesture")) seen.push(request.url());
	};
	sandbox.page.on("request", onRequest);
	const box = await sandbox.page.getByTestId("sim-canvas").boundingBox();
	if (!box) throw new Error("no canvas");
	await sandbox.page.mouse.move(box.x + box.width * 0.5, box.y + box.height * 0.5);
	await sandbox.page.mouse.down();
	await sandbox.page.waitForTimeout(200);
	await sandbox.page.mouse.up();
	await expect.poll(() => seen.length, { timeout: 10_000 }).toBeGreaterThan(0);
	sandbox.page.off("request", onRequest);
});

/**
 * A press that cannot reach the device must say so where the human is pressing.
 *
 * ⚠ Checked in a browser because the failure was silence: `onPointerDown` used
 * to open with `if (!canDrive) return`, which renders nothing, breaks nothing,
 * and passes every test that only asserts what WAS sent.
 */
test("a press that cannot reach the device says why, on screen", async () => {
	await readyToDrive(sandbox);
	// Give the device back, so the pane is watching something it may not touch.
	execFileSync("curl", [
		"-sS",
		"-o",
		"/dev/null",
		"-X",
		"DELETE",
		`${sandbox.api}/api/v1/sessions/${sandbox.sessionId}/sim-leases/${sandbox.udid}`,
	]);
	await expect(sandbox.page.getByRole("button", { name: /claim to drive/i })).toBeVisible({ timeout: 20_000 });

	const box = await sandbox.page.getByTestId("sim-canvas").boundingBox();
	if (!box) throw new Error("no canvas");
	await sandbox.page.mouse.move(box.x + box.width * 0.5, box.y + box.height * 0.5);
	await sandbox.page.mouse.down();
	await sandbox.page.waitForTimeout(150);
	await sandbox.page.mouse.up();

	// The words are in the page, and really painted rather than clipped away.
	const said = sandbox.page.getByText(/not holding this device/i);
	await expect(said, "a press was dropped without saying why").toBeVisible({ timeout: 10_000 });
	const reason = await said.boundingBox();
	expect(reason?.width ?? 0).toBeGreaterThan(20);

	// ⚠ Put the lease back. These cases run in series against one device, and a
	// later one starts a recording - which needs a live lease and is refused
	// with a 409 without it. A case that takes something away owes it back.
	expect(post(sandbox, `/api/v1/sessions/${sandbox.sessionId}/sim-leases`, { udid: sandbox.udid }).status).toBe(200);
	await expect(sandbox.page.getByRole("button", { name: /drive this device/i })).toBeVisible({ timeout: 20_000 });
});
