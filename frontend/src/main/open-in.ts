import { spawn } from "node:child_process";
import { existsSync, readdirSync } from "node:fs";
import { readdir } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { ANDROID_SUBDIR, isXcodeAppName, type OpenInTargets, resolveOpenTargets } from "./open-in-targets";

// The macOS locations a .app bundle can live. Detection is a cheap existsSync
// against these rather than a Spotlight (`mdfind`) query, which can be slow and
// is disabled on some machines.
const APP_DIRS = ["/Applications", path.join(os.homedir(), "Applications"), "/System/Applications"];

/** Whether an app bundle named `<appName>.app` exists in a standard location. */
export function isAppInstalled(appName: string): boolean {
	return APP_DIRS.some((dir) => existsSync(path.join(dir, `${appName}.app`)));
}

/**
 * Whether Xcode is installed. Unlike {@link isAppInstalled}, this scans the app
 * directories rather than probing a fixed `Xcode.app`, so versioned/side-by-side
 * bundles like `Xcode-26.3.0.app` or `Xcode-beta.app` are recognized too.
 */
export function isXcodeInstalled(): boolean {
	return APP_DIRS.some((dir) => {
		try {
			return readdirSync(dir).some(isXcodeAppName);
		} catch {
			return false;
		}
	});
}

/**
 * Inspect a directory's root and installed apps to decide which conditional
 * "Open in…" items to show. Never throws: a missing/unreadable dir or a
 * non-darwin host yields empty targets so the caller simply shows fewer items.
 */
export async function detectOpenTargets(dir: string): Promise<OpenInTargets> {
	if (process.platform !== "darwin" || !dir) return { hasVSCode: false };
	let entries: string[];
	try {
		entries = await readdir(dir);
	} catch {
		return { hasVSCode: false };
	}
	// Cross-platform mobile layouts (React Native, Flutter, Capacitor) keep the
	// Gradle root in an `android/` subdir, so list it too when present. Best-effort:
	// an unreadable/absent subdir yields undefined and detection falls back to root.
	const androidSubdirEntries = entries.includes(ANDROID_SUBDIR)
		? await readdir(path.join(dir, ANDROID_SUBDIR)).catch(() => undefined)
		: undefined;
	return resolveOpenTargets({
		dir,
		entries,
		androidSubdirEntries,
		vscodeInstalled: isAppInstalled("Visual Studio Code"),
		xcodeInstalled: isXcodeInstalled(),
		androidStudioInstalled: isAppInstalled("Android Studio"),
	});
}

// Launch via `open`, resolving on a clean exit and rejecting otherwise so the
// renderer can surface a toast. Rejection (not a throw) keeps a failed launch
// from crashing the main process.
function runOpen(args: string[]): Promise<void> {
	return new Promise((resolve, reject) => {
		let child: ReturnType<typeof spawn>;
		try {
			child = spawn("open", args);
		} catch (error) {
			reject(error instanceof Error ? error : new Error(String(error)));
			return;
		}
		child.once("error", reject);
		child.once("exit", (code) => {
			if (code === 0) resolve();
			else reject(new Error(`open exited with code ${code ?? "unknown"}`));
		});
	});
}

/**
 * Open `dir` in a terminal: Ghostty when installed, else macOS Terminal.app.
 * No-op off darwin (the feature is macOS-only).
 */
export function openInTerminal(dir: string): Promise<void> {
	if (process.platform !== "darwin") return Promise.resolve();
	const app = isAppInstalled("Ghostty") ? "Ghostty" : "Terminal";
	return runOpen(["-a", app, dir]);
}

/** Open `dir` in Visual Studio Code. No-op off darwin. */
export function openInEditor(dir: string): Promise<void> {
	if (process.platform !== "darwin") return Promise.resolve();
	return runOpen(["-a", "Visual Studio Code", dir]);
}

/**
 * Open an Xcode workspace/project path with its default handler (Xcode). No-op
 * off darwin.
 */
export function openInXcode(targetPath: string): Promise<void> {
	if (process.platform !== "darwin") return Promise.resolve();
	return runOpen([targetPath]);
}

/**
 * Open a Gradle project directory in Android Studio. Unlike Xcode, Android Studio
 * opens a folder (the Gradle root), not a single project file. No-op off darwin.
 */
export function openInAndroidStudio(dir: string): Promise<void> {
	if (process.platform !== "darwin") return Promise.resolve();
	return runOpen(["-a", "Android Studio", dir]);
}
