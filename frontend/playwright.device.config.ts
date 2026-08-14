import { defineConfig } from "@playwright/test";

/**
 * The device harness: the real Electron app on a throwaway AO, driving a real
 * iOS Simulator. Separate from `playwright.config.ts` because it is the
 * opposite kind of test - it needs a mac, Xcode, a booted simulator and a built
 * renderer, so it can never run in CI, and it launches Electron itself rather
 * than a dev server.
 *
 * Run it with `npm run test:device`. On a machine that cannot run it, every
 * case skips with the reason.
 */
export default defineConfig({
	testDir: "e2e-device",
	// One at a time: the machine has one simulator, and it has one finger.
	workers: 1,
	fullyParallel: false,
	// Building the daemon and launching the app costs most of this.
	timeout: 180_000,
	expect: { timeout: 20_000 },
	reporter: [["list"]],
});
