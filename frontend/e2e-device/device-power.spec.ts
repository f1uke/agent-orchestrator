/**
 * Booting a simulator from the Device tab, and shutting it down again, against
 * a real device.
 *
 * ⚠ This is the only place any of it is proven. jsdom can check that a press
 * sends a request; it cannot boot a 4 GB virtual machine, wait tens of seconds
 * for SpringBoard, or watch frames start arriving from a device that did not
 * exist when the page rendered. A green unit suite says the wiring is shaped
 * right and nothing at all about whether booting works.
 *
 * ⚠ It boots exactly ONE extra device and shuts it down in the same run, even
 * when a case fails. This machine has hit a true OOM from three booted at once
 * - tooling started failing with `fork failed: resource temporarily
 * unavailable` - so a harness that leaked a booted device would be reproducing
 * the very bug the feature guards against.
 *
 * Run: `npm run test:device` (mac, Xcode, one booted simulator, and
 * `npm run package` for the renderer build). Everything missing is a skip.
 */
import { execFileSync } from "node:child_process";
import { expect, test } from "@playwright/test";
import { type Sandbox, focusWindow, openDevicePane, skipReason, startSandbox } from "./sandbox";

let sandbox: Sandbox;
/** The device this run boots, chosen once so the cleanup can always find it. */
let scratch: { udid: string; name: string } | null = null;
const skip = skipReason();

test.describe.configure({ mode: "serial" });

/** simctlDevices is the machine's own answer, independent of anything AO says. */
function simctlDevices(): { udid: string; name: string; state: string }[] {
	const raw = execFileSync("xcrun", ["simctl", "list", "devices", "--json"], { encoding: "utf8" });
	const parsed = JSON.parse(raw) as { devices: Record<string, { udid: string; name: string; state: string }[]> };
	return Object.values(parsed.devices).flat();
}

function simctlState(udid: string): string {
	return simctlDevices().find((d) => d.udid.toUpperCase() === udid.toUpperCase())?.state ?? "Unknown";
}

/** waitForState polls simctl, which is the ground truth a UI claim is checked against. */
async function waitForState(udid: string, want: string, timeoutMs: number): Promise<void> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const state = simctlState(udid);
		if (state === want) return;
		if (Date.now() > deadline) throw new Error(`${udid} is ${state} after ${timeoutMs}ms, want ${want}`);
		await new Promise((resolve) => setTimeout(resolve, 2_000));
	}
}

test.beforeAll(async () => {
	test.skip(skip !== null, skip ?? "");
	sandbox = await startSandbox();
	await openDevicePane(sandbox);

	// A device that is not the one the harness is already watching, and is not
	// running. Picked from simctl rather than from the pane so the pane's own
	// listing is still something to assert about rather than something to trust.
	const candidate = simctlDevices().find(
		(d) => d.state !== "Booted" && d.udid.toUpperCase() !== sandbox.udid.toUpperCase(),
	);
	test.skip(!candidate, "this machine has no second, shut-down simulator to boot");
	scratch = candidate ? { udid: candidate.udid, name: candidate.name } : null;
});

test.afterAll(async () => {
	// Never leave a device booted, whatever happened above.
	if (scratch && simctlState(scratch.udid) === "Booted") {
		try {
			execFileSync("xcrun", ["simctl", "shutdown", scratch.udid], { stdio: "ignore" });
		} catch {
			// Best effort: the assertions have already reported the real fault.
		}
	}
	await sandbox?.dispose();
});

test("the picker offers a device that is not booted, and says what booting costs", async () => {
	const { page } = sandbox;
	await focusWindow(sandbox);
	await page.getByRole("button", { name: "Simulator to watch" }).click();

	// The count and the cost are on the header before anything is pressed.
	await expect(page.getByTestId("sim-booted-count")).toContainText("1 booted");
	await expect(page.getByTestId("sim-booted-count")).toContainText("4 GB");

	// The shut-down device is listed at all - which is the whole gap this
	// closes, since the tab used to show only booted ones.
	await expect(page.getByTestId(`sim-device-${scratch?.udid}`)).toBeVisible();
});

test("booting an additional device warns first, then boots it and switches the tab to it", async () => {
	const { page } = sandbox;
	const row = page.getByTestId(`sim-device-${scratch?.udid}`);

	// ⚠ The memory guard, on the path that matters: one device is already up.
	await row.getByRole("button", { name: "Boot" }).click();
	await expect(page.getByTestId("sim-power-confirm")).toContainText(/already up/i);
	expect(simctlState(scratch?.udid ?? "")).not.toBe("Booted");

	await page.getByRole("button", { name: "Boot anyway" }).click();

	// It says it is working, with a real elapsed count rather than a bare
	// spinner - the thing the control must not be is silent.
	await expect(page.getByTestId("sim-power-running")).toBeVisible({ timeout: 15_000 });
	await expect(page.getByTestId("sim-power-running")).toContainText("2:00");

	// And the machine actually boots it. Two minutes is the daemon's own
	// deadline; this waits a little past it so a timeout here is a real
	// failure rather than a race with the timeout.
	await waitForState(scratch?.udid ?? "", "Booted", 140_000);

	// The tab follows the device it was asked to boot: choosing a shut-down
	// device means "show me this one".
	await expect(page.getByRole("button", { name: "Simulator to watch" })).toContainText(scratch?.name ?? "", {
		timeout: 30_000,
	});
	await expect(page.getByTestId("sim-freshness")).toContainText("live", { timeout: 60_000 });
});

test("the tab drives the device it just booted", async () => {
	const { page } = sandbox;
	await focusWindow(sandbox);

	// Nothing may touch the device until this session holds the lease - the
	// same rule `ao sim tap` keeps, and it applies to a device booted from here
	// exactly as it does to one booted in Xcode.
	await expect(page.getByRole("button", { name: /^home$/i })).toBeDisabled();
	await page.getByRole("button", { name: /claim to drive/i }).click();
	await page.getByRole("button", { name: /drive this device/i }).click();
	await expect(page.getByRole("button", { name: /^home$/i })).toBeEnabled({ timeout: 20_000 });

	await page.getByRole("button", { name: /^home$/i }).click();

	// The lease the pane took is on the NEW device, which is what makes this a
	// claim about the device it booted rather than about the other one.
	const raw = execFileSync("curl", ["-fsS", `${sandbox.api}/api/v1/sim/devices`], { encoding: "utf8" });
	const listing = JSON.parse(raw) as { devices: { udid: string; lease: { state: string; holder?: string } }[] };
	const held = listing.devices.find((d) => d.udid.toUpperCase() === (scratch?.udid ?? "").toUpperCase());
	expect(held?.lease.state).toBe("held");

	// An empty accessibility tree is this repo's own diagnosis for an app whose
	// main thread is blocked - so a tree with elements in it is the device
	// answering, not just the pane claiming it sent something.
	const tree = execFileSync(sandbox.aoBin, ["sim", "ax", "--udid", scratch?.udid ?? ""], {
		encoding: "utf8",
	});
	expect(tree.trim().length).toBeGreaterThan(0);
});

test("shutting the device down asks first, and actually powers it off", async () => {
	const { page } = sandbox;
	await focusWindow(sandbox);
	await page.getByRole("button", { name: "Simulator to watch" }).click();

	const row = page.getByTestId(`sim-device-${scratch?.udid}`);
	await row.getByRole("button", { name: "Shut down" }).click();
	await expect(page.getByTestId("sim-power-confirm")).toContainText(/lost/i);
	expect(simctlState(scratch?.udid ?? "")).toBe("Booted");

	await page.getByTestId("sim-power-confirm").getByRole("button", { name: "Shut down" }).click();
	await waitForState(scratch?.udid ?? "", "Shutdown", 60_000);

	// Back down to the one device this machine started with.
	await expect(page.getByTestId("sim-booted-count")).toContainText("1 booted", { timeout: 30_000 });
});
