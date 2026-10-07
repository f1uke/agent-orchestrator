import { describe, expect, it } from "vitest";
import type { SimDevice } from "../hooks/useSimDevices";
import { cloneOwner, isWatchable, pickerGroups, resolveSelection, sessionDevices } from "./sim-devices";

const device = (udid: string, overrides: Partial<SimDevice> = {}): SimDevice => ({
	udid,
	name: udid,
	runtime: "iOS 26.3",
	runtimeIdentifier: "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
	state: "Shutdown",
	available: true,
	default: false,
	lease: { state: "unknown" },
	...overrides,
});

const clone = (udid: string, sessionId: string, label: string, overrides: Partial<SimDevice> = {}): SimDevice =>
	device(udid, {
		role: "clone",
		clone: {
			udid,
			sessionId,
			label,
			primary: label === "primary",
			base: "iPhone 17 Pro Max",
			name: `AO ${sessionId} ${label}`,
			createdAt: "2026-10-07T00:00:00Z",
		},
		...overrides,
	});

const base = (udid: string, overrides: Partial<SimDevice> = {}) => device(udid, { role: "base", ...overrides });

const udids = (devices: SimDevice[]) => devices.map((d) => d.udid);

describe("sessionDevices", () => {
	it("lists only this session's clones, primary first and the rest by label", () => {
		const devices = [
			clone("small", "s-1", "small"),
			clone("other", "s-2", "primary"),
			clone("advisor", "s-1", "advisor"),
			clone("primary", "s-1", "primary"),
			device("plain"),
			base("base"),
		];
		expect(udids(sessionDevices(devices, "s-1"))).toEqual(["primary", "advisor", "small"]);
	});

	it("is empty for a session AO cloned nothing for", () => {
		expect(sessionDevices([device("plain"), clone("other", "s-2", "primary")], "s-1")).toEqual([]);
	});
});

describe("isWatchable", () => {
	it("never offers a base, even one somebody booted", () => {
		expect(isWatchable(base("b", { state: "Booted" }))).toBe(false);
		expect(isWatchable(device("d", { state: "Booted" }))).toBe(true);
		expect(isWatchable(device("d"))).toBe(false);
	});
});

describe("resolveSelection", () => {
	const resolve = (devices: SimDevice[], selected: string | null, defaultUdid: string | null = null) =>
		resolveSelection({ defaultUdid, devices, looked: true, selected, sessionId: "s-1" });

	const fleet = [
		clone("primary", "s-1", "primary"),
		clone("se", "s-1", "iphone-se"),
		device("booted", { state: "Booted" }),
		base("base", { state: "Booted" }),
	];

	it("starts a session with devices of its own on its primary, even while it is shut down", () => {
		expect(resolve(fleet, null)).toBe("primary");
	});

	it("starts on the primary rather than the daemon's default", () => {
		expect(resolve(fleet, null, "booted")).toBe("primary");
	});

	it("keeps a shut-down device of this session selected, so it can be booted", () => {
		expect(resolve(fleet, "se")).toBe("se");
	});

	it("keeps another booted device the human picked", () => {
		expect(resolve(fleet, "booted")).toBe("booted");
	});

	it("goes back to the primary when the picked device is gone or off", () => {
		expect(resolve(fleet, "deleted")).toBe("primary");
		expect(resolve([...fleet.slice(0, 2), device("booted")], "booted")).toBe("primary");
	});

	it("never keeps a base selected", () => {
		expect(resolve(fleet, "base")).toBe("primary");
		expect(resolve([base("base", { state: "Booted" })], "base", "base")).toBeNull();
	});

	// A non-iOS session, or a daemon from before clones.
	it("falls back to the daemon's default for a session with no devices of its own", () => {
		const plain = [device("a", { state: "Booted" }), device("b", { state: "Booted" })];
		expect(resolve(plain, null, "a")).toBe("a");
		expect(resolve(plain, null, null)).toBeNull();
		expect(resolve(plain, "b", "a")).toBe("b");
		expect(resolve([device("a")], "a", "a")).toBeNull();
	});

	it("leaves the selection alone until the list has been read", () => {
		expect(resolveSelection({ defaultUdid: null, devices: [], looked: false, selected: "x", sessionId: "s-1" })).toBe(
			"x",
		);
	});
});

describe("pickerGroups", () => {
	it("puts every device in exactly one section", () => {
		const devices = [
			clone("se", "s-1", "iphone-se"),
			clone("primary", "s-1", "primary"),
			clone("theirs", "s-2", "primary", { state: "Booted" }),
			device("off"),
			base("base"),
		];
		const groups = pickerGroups(devices, "s-1");
		expect(udids(groups.own)).toEqual(["primary", "se"]);
		expect(udids(groups.booted)).toEqual(["theirs"]);
		expect(udids(groups.off)).toEqual(["off"]);
		expect(udids(groups.bases)).toEqual(["base"]);
	});
});

describe("cloneOwner", () => {
	it("names the session by its board name where it has one", () => {
		const theirs = {
			udid: "t",
			sessionId: "s-2",
			label: "iphone-se",
			primary: false,
			base: "iPhone SE (3rd generation)",
			name: "AO s-2 iphone-se",
			createdAt: "2026-10-07T00:00:00Z",
		};
		expect(cloneOwner(theirs)).toBe("s-2 · iphone-se");
		expect(cloneOwner(theirs, new Map([["s-2", "fix login"]]))).toBe("fix login · iphone-se");
	});
});
