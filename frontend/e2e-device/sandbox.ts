/**
 * A throwaway AO - its own daemon, its own data, its own Electron profile - with
 * the real app on top of it, so an agent can play the Device tab the way a human
 * does: press the buttons, drag on the screen, and watch a real simulator answer.
 *
 * Why this exists: `ao sim tap|drag|...` drives the *device*, and that is most of
 * what an agent needs. What it cannot do is press anything in the *app* - and a
 * pane's own behaviour (does one press claim, does a drag follow the finger, does
 * capture stop when nobody is looking) is only true in a browser that lays it out
 * and hit-tests it. jsdom cannot see clipping or overlap, and a synthetic
 * PointerEvent is refused by setPointerCapture, so a check written that way
 * measures nothing at all. Playwright drives the real Electron build with the
 * browser's own input path, which is the only thing that proves it.
 *
 * Two rules this file keeps, because breaking either is worse than having no
 * harness at all:
 *
 *   - It never touches the human's AO. Its own HOME, data dir, run file and
 *     port; the app attaches to the sandbox daemon and nothing else.
 *   - It never fights the human for the device. The lease that stops two agents
 *     driving one simulator lives in a daemon's own database, so a second daemon
 *     cannot see the first one's leases - and the machine has one simulator with
 *     one finger. So this refuses to start while the live AO holds a lease.
 *
 * Requires a mac with Xcode, a booted simulator, and a built renderer
 * (`npm run package`). Anything missing is a skip with the reason, never a
 * failure: there is nothing to test on a machine that cannot run it.
 */
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import path from "node:path";
import { type ElectronApplication, type Page, _electron as electron } from "playwright";

/** Where the renderer build lands. Without it there is no app to launch. */
const RENDERER_BUILD = path.resolve(__dirname, "..", ".vite", "build", "main.js");
const FRONTEND_DIR = path.resolve(__dirname, "..");
const BACKEND_DIR = path.resolve(__dirname, "..", "..", "backend");

export type Sandbox = {
	app: ElectronApplication;
	page: Page;
	projectId: string;
	sessionId: string;
	/** The sandbox daemon, for asserting what actually reached the device. */
	api: string;
	udid: string;
	/** The sandbox daemon's data directory, which is where recorded flows land. */
	dataDir: string;
	/** The ao binary this harness built, for read-only checks against the device. */
	aoBin: string;
	dispose: () => Promise<void>;
};

/**
 * skipReason is why this machine cannot run the harness, or null when it can.
 * Checked before anything is started so a skip costs nothing.
 */
export function skipReason(): string | null {
	if (process.platform !== "darwin") return "an iOS Simulator needs a mac";
	if (!existsSync(RENDERER_BUILD)) return `no renderer build at ${RENDERER_BUILD} - run \`npm run package\` first`;
	const target = targetUDID();
	if (target.udid === null) return target.reason;
	const busy = liveLeaseHolder(target.udid);
	if (busy) return `the live AO holds this simulator (@${busy}) - two daemons cannot arbitrate one device`;
	return null;
}

/** bootedUDIDs asks simctl directly: the same read-only listing `ao sim list` does. */
function bootedUDIDs(): string[] {
	try {
		const raw = execFileSync("xcrun", ["simctl", "list", "devices", "--json"], { encoding: "utf8" });
		const parsed = JSON.parse(raw) as { devices: Record<string, { udid: string; state: string }[]> };
		const booted: string[] = [];
		for (const devices of Object.values(parsed.devices)) {
			for (const device of devices) if (device.state === "Booted") booted.push(device.udid);
		}
		return booted;
	} catch {
		return [];
	}
}

/**
 * Which simulator this harness may drive.
 *
 * ⚠ It never picks one when several are booted. This harness performs real
 * drags on a real device, and on a machine where a person also has their own
 * work running in a second simulator, "whichever simctl listed first" is a
 * coin flip between a scratch device and somebody's actual app. That is the
 * same refusal `ao sim shot` and the daemon already make when several are
 * booted, for the same reason - the device is never guessed - and it is a
 * stronger reason here, because this one touches it.
 *
 * Pin the device with AO_DEVICE_TEST_UDID to run against a chosen one.
 */
function targetUDID(): { udid: string | null; reason: string | null } {
	const pinned = (process.env.AO_DEVICE_TEST_UDID ?? "").trim();
	const booted = bootedUDIDs();
	if (pinned) {
		if (!booted.some((udid) => udid.toLowerCase() === pinned.toLowerCase())) {
			return { udid: null, reason: `AO_DEVICE_TEST_UDID names ${pinned}, which is not booted` };
		}
		return { udid: booted.find((udid) => udid.toLowerCase() === pinned.toLowerCase()) ?? null, reason: null };
	}
	if (booted.length === 0) return { udid: null, reason: "no simulator is booted (this harness never boots one)" };
	if (booted.length > 1) {
		return {
			udid: null,
			reason:
				`${booted.length} simulators are booted and this harness drives the device it is given - ` +
				"set AO_DEVICE_TEST_UDID to the one it may touch",
		};
	}
	return { udid: booted[0], reason: null };
}

/**
 * liveLeaseHolder is the session driving a simulator on the human's own AO, if
 * one is. The sandbox daemon cannot see that lease, so this is the only thing
 * standing between an agent's drag and a human's.
 */
function liveLeaseHolder(udid: string): string | null {
	try {
		const runFile = process.env.AO_RUN_FILE ?? path.join(homedir(), ".ao", "running.json");
		const { port } = JSON.parse(readFileSync(runFile, "utf8")) as { port: number };
		const raw = execFileSync("curl", ["-fsS", `http://127.0.0.1:${port}/api/v1/sim/devices`], { encoding: "utf8" });
		const parsed = JSON.parse(raw) as { devices: { udid: string; lease?: { state: string; holder?: string } }[] };
		for (const device of parsed.devices) {
			// ⚠ Only the device this harness is about to DRIVE. It used to refuse
			// whenever the live AO held any lease at all, which on a machine with
			// two simulators booted means a human working on one of them blocks
			// every test against the other - and the two do not collide: the
			// lease is per device, and this harness only ever touches the one it
			// was given. The guard is still the only thing standing between an
			// agent's drag and a human's, so it stays exact rather than eager.
			if (device.udid.toLowerCase() !== udid.toLowerCase()) continue;
			if (device.lease?.state === "held") return device.lease.holder ?? "another session";
		}
	} catch {
		// No live AO, or it cannot list devices: nothing to collide with.
		return null;
	}
	return null;
}

/**
 * startSandbox builds the daemon from this checkout, brings up a private AO with
 * one project and one TODO session (a session row is all the pane needs - no
 * agent is ever launched), and opens the real app on it.
 */
export async function startSandbox(): Promise<Sandbox> {
	const root = path.join(tmpdir(), `ao-device-harness-${process.pid}`);
	rmSync(root, { recursive: true, force: true });
	mkdirSync(path.join(root, "repo"), { recursive: true });
	mkdirSync(path.join(root, "home"), { recursive: true });

	const repo = path.join(root, "repo");
	execFileSync("git", ["init", "-q", "-b", "main"], { cwd: repo });
	execFileSync("git", ["commit", "-q", "--allow-empty", "-m", "sandbox"], {
		cwd: repo,
		env: {
			...process.env,
			GIT_AUTHOR_NAME: "AO",
			GIT_AUTHOR_EMAIL: "ao@example.com",
			GIT_COMMITTER_NAME: "AO",
			GIT_COMMITTER_EMAIL: "ao@example.com",
		},
	});

	// Built from this checkout rather than taken from PATH: the daemon under
	// test has to be the one whose code this run is about.
	const ao = path.join(root, "ao");
	execFileSync("go", ["build", "-o", ao, "./cmd/ao"], { cwd: BACKEND_DIR, stdio: "inherit" });

	const port = 3100 + (process.pid % 400);
	const env = {
		...process.env,
		HOME: path.join(root, "home"),
		AO_PORT: String(port),
		AO_DATA_DIR: path.join(root, "home", ".ao", "data"),
		AO_RUN_FILE: path.join(root, "home", ".ao", "running.json"),
		AO_DAEMON_COMMAND: `${ao} daemon`,
	};
	const api = `http://127.0.0.1:${port}`;
	const cli = (...args: string[]) => execFileSync(ao, args, { env, encoding: "utf8" });

	// The app spawns the daemon itself (AO_DAEMON_COMMAND), so the project and
	// session are seeded after it is up.
	const app = await electron.launch({
		executablePath: path.join(FRONTEND_DIR, "node_modules", ".bin", "electron"),
		args: [FRONTEND_DIR],
		env,
		timeout: 120_000,
	});
	const page = await app.firstWindow();
	await page.waitForLoadState("domcontentloaded");
	await waitFor(async () => {
		try {
			execFileSync("curl", ["-fsS", `${api}/healthz`], { stdio: "ignore" });
			return true;
		} catch {
			return false;
		}
	}, "the sandbox daemon never came up");

	const projectId = "devicecheck";
	cli("project", "add", "--path", repo, "--id", projectId);
	// The Device tab is opt-in per project, exactly as it is for a real one.
	cli("project", "set-config", projectId, "--ios-simulator");
	// --todo stages the session without a branch, a worktree, a tmux or an
	// agent: the pane needs a session to hang a lease on, not a running one.
	cli(
		"spawn",
		"--project",
		projectId,
		"--todo",
		"--from",
		"main",
		"--harness",
		"claude-code",
		"--name",
		"device check",
		"--prompt",
		"device harness",
	);
	const sessionId = `${projectId}-1`;

	// The window booted against an AO with no projects in it. Reload so it comes
	// up knowing the one just seeded, rather than waiting on a refetch.
	await page.reload();
	await page.waitForLoadState("domcontentloaded");

	const udid = targetUDID().udid;
	if (!udid) throw new Error("the simulator stopped being booted mid-setup");

	return {
		app,
		page,
		projectId,
		sessionId,
		api,
		udid,
		dataDir: env.AO_DATA_DIR,
		aoBin: ao,
		dispose: async () => {
			await app.close().catch(() => {});
			rmSync(root, { recursive: true, force: true });
		},
	};
}

async function waitFor(check: () => Promise<boolean>, whenNot: string, timeoutMs = 60_000): Promise<void> {
	const deadline = Date.now() + timeoutMs;
	while (Date.now() < deadline) {
		if (await check()) return;
		await new Promise((resolve) => setTimeout(resolve, 250));
	}
	throw new Error(whenNot);
}

/**
 * openDevicePane navigates to the session and opens its Device tab.
 *
 * The hash is set more than once on purpose: the router owns the URL, and a
 * hash written before it has mounted is simply overwritten. Asking again until
 * the tab is there is what makes this independent of how long the shell takes
 * to come up on the day.
 */
export async function openDevicePane(sandbox: Sandbox): Promise<void> {
	const { page, projectId, sessionId } = sandbox;
	const hash = `#/projects/${projectId}/sessions/${sessionId}`;
	const device = page.getByRole("tab", { name: "Device", exact: true });
	await waitFor(async () => {
		await page.evaluate((to) => {
			if (location.hash !== to) location.hash = to;
		}, hash);
		return device.isVisible();
	}, "the session never opened with a Device tab - is the project's iOS config set?");
	await device.click();

	// With more than one simulator booted the pane refuses to choose - the same
	// refusal `ao sim shot` makes - so the harness makes the choice it was given
	// explicitly, rather than the pane guessing on its behalf.
	if (!(await page.getByTestId("sim-canvas").isVisible())) {
		const name = deviceName(sandbox);
		await page.getByRole("button", { name: "Simulator to watch" }).click();
		// The picker is a popover of rows rather than a Select of options, since
		// it now also boots and shuts devices down. A booted device is chosen by
		// its "Watch <name>" row; the trailing $ keeps "iPhone 17 Pro" off
		// "iPhone 17 Pro Max".
		await page.getByRole("button", { name: new RegExp(`^Watch ${escapeRegExp(name)}$`) }).click();
	}

	await page.getByTestId("sim-canvas").waitFor({ state: "visible", timeout: 60_000 });

	// Capture stops when nobody can SEE the pane - the rule that keeps it off
	// the CPU - and a window this harness launched can start life behind
	// whatever else is on the screen. The window is brought forward, and the
	// wait is on frames actually arriving rather than on the canvas existing;
	// input needs focus even where capture no longer does.
	await focusWindow(sandbox);
	await waitFor(
		async () => ((await page.getByTestId("sim-freshness").textContent()) ?? "").includes("live"),
		"no frames arrived - is the window in the background, or the simulator busy?",
	);
}

/** deviceName is what the picker calls the device this harness may drive. */
function deviceName(sandbox: Sandbox): string {
	const raw = execFileSync("curl", ["-fsS", `${sandbox.api}/api/v1/sim/devices`], { encoding: "utf8" });
	const parsed = JSON.parse(raw) as { devices: { udid: string; name: string }[] };
	const found = parsed.devices.find((device) => device.udid.toUpperCase() === sandbox.udid.toUpperCase());
	if (!found) throw new Error(`the daemon does not list ${sandbox.udid}`);
	return found.name;
}

function escapeRegExp(value: string): string {
	return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * focusWindow puts the app in front, which is what makes its input path work.
 *
 * Not what makes it capture: a blurred window keeps streaming on purpose. But a
 * pointer or a key belongs to whichever window has focus, so anything that
 * presses something asks for it first.
 */
export async function focusWindow(sandbox: Sandbox): Promise<void> {
	await sandbox.page.bringToFront();
	await sandbox.app.evaluate(({ BrowserWindow }) => {
		BrowserWindow.getAllWindows()[0]?.focus();
	});
}

/** screenBox is where the device's screen is drawn, in window coordinates. */
export async function screenBox(sandbox: Sandbox): Promise<{ x: number; y: number; width: number; height: number }> {
	const box = await sandbox.page.getByTestId("sim-canvas").boundingBox();
	if (!box) throw new Error("the screen is not on display");
	return box;
}

/** at maps a point on the device (0..1 of its screen) to a window coordinate. */
export async function at(sandbox: Sandbox, x: number, y: number): Promise<{ x: number; y: number }> {
	const box = await screenBox(sandbox);
	return { x: box.x + box.width * x, y: box.y + box.height * y };
}

/**
 * dragThrough holds the pointer down and moves it through a route, the way a
 * human scrolls - which is not the same gesture as a swipe, and is the one that
 * used to arrive only after the finger came up.
 */
export async function dragThrough(
	sandbox: Sandbox,
	route: { x: number; y: number }[],
	options: { stepMs?: number; whileDown?: () => void | Promise<void> } = {},
): Promise<void> {
	const { stepMs = 40, whileDown } = options;
	const first = await at(sandbox, route[0].x, route[0].y);
	await sandbox.page.mouse.move(first.x, first.y);
	await sandbox.page.mouse.down();
	for (const point of route.slice(1)) {
		const to = await at(sandbox, point.x, point.y);
		await sandbox.page.mouse.move(to.x, to.y);
		await sandbox.page.waitForTimeout(stepMs);
	}
	// Whatever has to be true *while the finger is down* is asked here: after
	// the release there is nothing left to see.
	await whileDown?.();
	await sandbox.page.mouse.up();
}

/** deviceState is what the sandbox daemon says about the device right now. */
export async function deviceState(
	sandbox: Sandbox,
): Promise<{ lease?: { state: string; holder?: string }; frame?: { thickness: number; radius: number } }> {
	const raw = execFileSync("curl", ["-fsS", `${sandbox.api}/api/v1/sim/devices`], { encoding: "utf8" });
	const parsed = JSON.parse(raw) as {
		devices: {
			udid: string;
			lease?: { state: string; holder?: string };
			frame?: { thickness: number; radius: number };
		}[];
	};
	return parsed.devices.find((device) => device.udid.toUpperCase() === sandbox.udid.toUpperCase()) ?? {};
}
