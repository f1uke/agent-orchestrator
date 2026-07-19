import path from "node:path";
import { describe, expect, it } from "vitest";
import { isGradleMarker, isXcodeAppName, resolveOpenTargets } from "./open-in-targets";

const DIR = "/Users/dev/project";

describe("resolveOpenTargets", () => {
	it("reports no xcode target when the directory root has neither .xcworkspace nor .xcodeproj", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["README.md", "src", "package.json"],
			vscodeInstalled: true,
			xcodeInstalled: true,
			androidStudioInstalled: false,
		});
		expect(targets.xcode).toBeUndefined();
	});

	it("detects a top-level .xcodeproj when only a project is present", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcodeproj", "MyApp"],
			vscodeInstalled: false,
			xcodeInstalled: true,
			androidStudioInstalled: false,
		});
		expect(targets.xcode).toEqual({ name: "MyApp.xcodeproj", path: path.join(DIR, "MyApp.xcodeproj") });
	});

	it("detects a top-level .xcworkspace when only a workspace is present", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcworkspace", "Pods"],
			vscodeInstalled: false,
			xcodeInstalled: true,
			androidStudioInstalled: false,
		});
		expect(targets.xcode).toEqual({ name: "MyApp.xcworkspace", path: path.join(DIR, "MyApp.xcworkspace") });
	});

	it("prefers the .xcworkspace when both a workspace and a project exist", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcodeproj", "MyApp.xcworkspace"],
			vscodeInstalled: false,
			xcodeInstalled: true,
			androidStudioInstalled: false,
		});
		expect(targets.xcode?.name).toBe("MyApp.xcworkspace");
	});

	it("names the detected file in the target label", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["Weather.xcodeproj"],
			vscodeInstalled: false,
			xcodeInstalled: true,
			androidStudioInstalled: false,
		});
		expect(targets.xcode?.name).toBe("Weather.xcodeproj");
	});

	it("hides the xcode target when Xcode is not installed even though a project exists", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcworkspace"],
			vscodeInstalled: true,
			xcodeInstalled: false,
			androidStudioInstalled: false,
		});
		expect(targets.xcode).toBeUndefined();
	});

	it("does not recurse into subdirectories (only the root listing is considered)", () => {
		// A nested "ios/App.xcodeproj" arrives as the top-level entry "ios", not as
		// a matching file — resolveOpenTargets never sees the nested project.
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["ios", "android", "README.md"],
			vscodeInstalled: false,
			xcodeInstalled: true,
			androidStudioInstalled: false,
		});
		expect(targets.xcode).toBeUndefined();
	});

	it("reflects VS Code availability via hasVSCode", () => {
		const present = resolveOpenTargets({
			dir: DIR,
			entries: [],
			vscodeInstalled: true,
			xcodeInstalled: false,
			androidStudioInstalled: false,
		});
		expect(present.hasVSCode).toBe(true);

		const absent = resolveOpenTargets({
			dir: DIR,
			entries: [],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: false,
		});
		expect(absent.hasVSCode).toBe(false);
	});
});

describe("resolveOpenTargets — Android Studio", () => {
	it("reports no android target when no Gradle marker is present at root or in android/", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["README.md", "src", "package.json"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: true,
		});
		expect(targets.android).toBeUndefined();
	});

	it("opens the worktree root when it declares settings.gradle (native Android project)", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["settings.gradle", "build.gradle", "app", "gradlew"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: true,
		});
		expect(targets.android).toEqual({ name: path.basename(DIR), path: DIR });
	});

	it("opens the root for a Kotlin-DSL project (settings.gradle.kts)", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["settings.gradle.kts", "build.gradle.kts", "app"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: true,
		});
		expect(targets.android?.path).toBe(DIR);
	});

	it("opens the root when only a build.gradle is present (no settings)", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["build.gradle", "src"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: true,
		});
		expect(targets.android?.path).toBe(DIR);
	});

	it("opens the android/ subdir when the Gradle root lives there (React Native / Flutter layout)", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["package.json", "ios", "android", "src"],
			androidSubdirEntries: ["settings.gradle", "build.gradle", "app", "gradlew"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: true,
		});
		expect(targets.android).toEqual({ name: "android", path: path.join(DIR, "android") });
	});

	it("prefers the directory declaring settings.gradle over one with only a build.gradle module file", () => {
		// Root has a stray build.gradle but the real Gradle root (settings.gradle) is android/.
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["build.gradle", "android", "package.json"],
			androidSubdirEntries: ["settings.gradle", "build.gradle"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: true,
		});
		expect(targets.android?.path).toBe(path.join(DIR, "android"));
	});

	it("prefers the root when both root and android/ declare settings.gradle", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["settings.gradle", "android"],
			androidSubdirEntries: ["settings.gradle"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: true,
		});
		expect(targets.android?.path).toBe(DIR);
	});

	it("ignores an android/ subdir that has no Gradle marker", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["android", "src"],
			androidSubdirEntries: ["MainActivity.kt", "res"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: true,
		});
		expect(targets.android).toBeUndefined();
	});

	it("hides the android target when Android Studio is not installed even though a Gradle project exists", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["settings.gradle", "build.gradle"],
			vscodeInstalled: false,
			xcodeInstalled: false,
			androidStudioInstalled: false,
		});
		expect(targets.android).toBeUndefined();
	});

	it("is independent of the Xcode target (an iOS-only worktree shows no android target)", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcodeproj"],
			vscodeInstalled: false,
			xcodeInstalled: true,
			androidStudioInstalled: true,
		});
		expect(targets.xcode).toBeDefined();
		expect(targets.android).toBeUndefined();
	});
});

describe("isGradleMarker", () => {
	it("matches the four Gradle project markers (Groovy and Kotlin DSL)", () => {
		expect(isGradleMarker("settings.gradle")).toBe(true);
		expect(isGradleMarker("settings.gradle.kts")).toBe(true);
		expect(isGradleMarker("build.gradle")).toBe(true);
		expect(isGradleMarker("build.gradle.kts")).toBe(true);
	});

	it("rejects non-marker Gradle files and lookalikes", () => {
		expect(isGradleMarker("gradle.properties")).toBe(false);
		expect(isGradleMarker("gradlew")).toBe(false);
		expect(isGradleMarker("build.gradlex")).toBe(false);
		expect(isGradleMarker("settings.gradle.bak")).toBe(false);
		expect(isGradleMarker("mybuild.gradle")).toBe(false);
	});
});

describe("isXcodeAppName", () => {
	it("matches the canonical Xcode.app", () => {
		expect(isXcodeAppName("Xcode.app")).toBe(true);
	});

	it("matches versioned and side-by-side bundles (multiple Xcodes installed)", () => {
		expect(isXcodeAppName("Xcode-26.3.0.app")).toBe(true);
		expect(isXcodeAppName("Xcode-beta.app")).toBe(true);
	});

	it("rejects the Xcodes version-manager app and other lookalikes", () => {
		expect(isXcodeAppName("Xcodes.app")).toBe(false);
		expect(isXcodeAppName("NotXcode.app")).toBe(false);
		expect(isXcodeAppName("Xcode.txt")).toBe(false);
		expect(isXcodeAppName("Xcode")).toBe(false);
	});
});
