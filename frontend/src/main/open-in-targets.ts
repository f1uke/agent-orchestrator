import path from "node:path";

/** A detected Xcode workspace/project at a directory root. */
export type XcodeTarget = {
	/** The file name, e.g. "MyApp.xcworkspace" — used verbatim as the menu label. */
	name: string;
	/** Absolute path to open with `open <path>` (Xcode is the default handler). */
	path: string;
};

/** A detected Gradle project root to open in Android Studio. */
export type AndroidTarget = {
	/** The directory basename (worktree name, or "android" for the subdir layout). */
	name: string;
	/** Absolute path of the Gradle root directory to open with `open -a "Android Studio" <path>`. */
	path: string;
};

/**
 * The conventional subdirectory that holds the Gradle root in cross-platform
 * layouts (React Native, Flutter, Capacitor) where the repo root is not itself a
 * Gradle project.
 */
export const ANDROID_SUBDIR = "android";

// The file names that mark a directory as a Gradle project. `settings.gradle[.kts]`
// specifically marks a Gradle *root* (the settings file declares the build), so it
// is preferred over a bare `build.gradle` module file when choosing what to open.
const GRADLE_SETTINGS_MARKERS = ["settings.gradle", "settings.gradle.kts"];
const GRADLE_MARKERS = [...GRADLE_SETTINGS_MARKERS, "build.gradle", "build.gradle.kts"];

/**
 * The "Open in…" targets that vary by machine/project. Terminal and Finder are
 * always offered by the menu, so they are not represented here; only the
 * conditionally-shown items are: VS Code (installed?), an Xcode workspace/project
 * (detected at the directory root and Xcode installed?), and an Android Studio /
 * Gradle project (detected at the root or `android/` subdir and Android Studio
 * installed?). The Xcode and Android targets are independent — an iOS worktree
 * shows only Xcode, an Android worktree only Android Studio.
 */
export type OpenInTargets = {
	hasVSCode: boolean;
	xcode?: XcodeTarget;
	android?: AndroidTarget;
};

export type ResolveOpenTargetsInput = {
	/** Absolute path of the directory being inspected. */
	dir: string;
	/** Top-level entry names in {@link dir}. No recursion — root listing only. */
	entries: string[];
	/**
	 * Entry names in the `android/` subdir of {@link dir}, when that subdir exists;
	 * `undefined` when there is no `android/` subdir. Lets detection reach the
	 * Gradle root of React Native / Flutter / Capacitor layouts without recursing
	 * the whole tree.
	 */
	androidSubdirEntries?: string[];
	/** Whether the Visual Studio Code app is installed. */
	vscodeInstalled: boolean;
	/** Whether the Xcode app is installed. */
	xcodeInstalled: boolean;
	/** Whether the Android Studio app is installed. */
	androidStudioInstalled: boolean;
};

/**
 * Pure mapping from a directory's root listing plus installed-app facts to the
 * conditional "Open in…" targets. Kept side-effect free (the fs listing and app
 * probes happen in the caller) so it is fully unit-testable.
 *
 * An Xcode target is offered only when Xcode is installed AND a `.xcworkspace`
 * or `.xcodeproj` sits at the directory root; a `.xcworkspace` wins over a
 * `.xcodeproj` when both are present. An Android target is offered only when
 * Android Studio is installed AND a Gradle marker is found at the root or in the
 * `android/` subdir (see {@link detectAndroidTarget}).
 */
export function resolveOpenTargets({
	dir,
	entries,
	androidSubdirEntries,
	vscodeInstalled,
	xcodeInstalled,
	androidStudioInstalled,
}: ResolveOpenTargetsInput): OpenInTargets {
	return {
		hasVSCode: vscodeInstalled,
		xcode: xcodeInstalled ? detectXcodeTarget(dir, entries) : undefined,
		android: androidStudioInstalled ? detectAndroidTarget(dir, entries, androidSubdirEntries) : undefined,
	};
}

/** Whether a file name marks a directory as a Gradle project (Groovy or Kotlin DSL). */
export function isGradleMarker(name: string): boolean {
	return GRADLE_MARKERS.includes(name);
}

/**
 * Resolve the Gradle project root to open, preferring the directory that declares
 * `settings.gradle[.kts]` (the true Gradle root) over one that merely has a
 * `build.gradle` module file. Candidates are the worktree root and its `android/`
 * subdir, in that order, so a native Android repo opens its root while an
 * RN/Flutter/Capacitor app opens `android/`.
 */
function detectAndroidTarget(
	dir: string,
	entries: string[],
	androidSubdirEntries: string[] | undefined,
): AndroidTarget | undefined {
	// Candidate Gradle roots, in preference order: the worktree root, then `android/`.
	const candidates: Array<{ target: AndroidTarget; entries: string[] }> = [
		{ target: { name: path.basename(dir), path: dir }, entries },
	];
	if (androidSubdirEntries) {
		candidates.push({
			target: { name: ANDROID_SUBDIR, path: path.join(dir, ANDROID_SUBDIR) },
			entries: androidSubdirEntries,
		});
	}
	const withSettings = candidates.find((c) => c.entries.some((e) => GRADLE_SETTINGS_MARKERS.includes(e)));
	const chosen = withSettings ?? candidates.find((c) => c.entries.some(isGradleMarker));
	return chosen?.target;
}

/**
 * Whether a `.app` bundle name is an Xcode install. Matches the canonical
 * `Xcode.app` plus versioned / side-by-side bundles like `Xcode-26.3.0.app` or
 * `Xcode-beta.app` (common when several Xcode versions are installed), while
 * excluding lookalikes such as `Xcodes.app` (the Xcode version-manager app).
 */
export function isXcodeAppName(name: string): boolean {
	return name === "Xcode.app" || /^Xcode-.+\.app$/.test(name);
}

function detectXcodeTarget(dir: string, entries: string[]): XcodeTarget | undefined {
	// Sort so a directory with multiple candidates resolves deterministically.
	const workspaces = entries.filter((entry) => entry.endsWith(".xcworkspace")).sort();
	const projects = entries.filter((entry) => entry.endsWith(".xcodeproj")).sort();
	const name = workspaces[0] ?? projects[0];
	if (!name) return undefined;
	return { name, path: path.join(dir, name) };
}
