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
import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { type Sandbox, deviceState, dragThrough, openDevicePane, skipReason, startSandbox } from "./sandbox";

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
