import path from "node:path";
import { describe, expect, it } from "vitest";
import { resolveOpenTargets } from "./open-in-targets";

const DIR = "/Users/dev/project";

describe("resolveOpenTargets", () => {
	it("reports no xcode target when the directory root has neither .xcworkspace nor .xcodeproj", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["README.md", "src", "package.json"],
			vscodeInstalled: true,
			xcodeInstalled: true,
		});
		expect(targets.xcode).toBeUndefined();
	});

	it("detects a top-level .xcodeproj when only a project is present", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcodeproj", "MyApp"],
			vscodeInstalled: false,
			xcodeInstalled: true,
		});
		expect(targets.xcode).toEqual({ name: "MyApp.xcodeproj", path: path.join(DIR, "MyApp.xcodeproj") });
	});

	it("detects a top-level .xcworkspace when only a workspace is present", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcworkspace", "Pods"],
			vscodeInstalled: false,
			xcodeInstalled: true,
		});
		expect(targets.xcode).toEqual({ name: "MyApp.xcworkspace", path: path.join(DIR, "MyApp.xcworkspace") });
	});

	it("prefers the .xcworkspace when both a workspace and a project exist", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcodeproj", "MyApp.xcworkspace"],
			vscodeInstalled: false,
			xcodeInstalled: true,
		});
		expect(targets.xcode?.name).toBe("MyApp.xcworkspace");
	});

	it("names the detected file in the target label", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["Weather.xcodeproj"],
			vscodeInstalled: false,
			xcodeInstalled: true,
		});
		expect(targets.xcode?.name).toBe("Weather.xcodeproj");
	});

	it("hides the xcode target when Xcode is not installed even though a project exists", () => {
		const targets = resolveOpenTargets({
			dir: DIR,
			entries: ["MyApp.xcworkspace"],
			vscodeInstalled: true,
			xcodeInstalled: false,
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
		});
		expect(targets.xcode).toBeUndefined();
	});

	it("reflects VS Code availability via hasVSCode", () => {
		const present = resolveOpenTargets({ dir: DIR, entries: [], vscodeInstalled: true, xcodeInstalled: false });
		expect(present.hasVSCode).toBe(true);

		const absent = resolveOpenTargets({ dir: DIR, entries: [], vscodeInstalled: false, xcodeInstalled: false });
		expect(absent.hasVSCode).toBe(false);
	});
});
