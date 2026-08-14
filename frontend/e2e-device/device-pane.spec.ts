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
