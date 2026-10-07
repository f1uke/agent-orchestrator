import type { components } from "../../api/schema";
import type { SimDevice } from "../hooks/useSimDevices";
import type { SessionNames } from "./crew";

export type SimClone = components["schemas"]["SimCloneView"];

/** A device AO cloned for some session. */
export type CloneDevice = SimDevice & { clone: SimClone };

/**
 * A base is the template AO clones a session's devices from. It is never
 * booted, watched or driven from here: a booted base cannot be cloned, and the
 * daemon refuses to boot one.
 */
export function isBase(device: SimDevice): boolean {
	return device.role === "base";
}

export function isCloneOf(device: SimDevice, sessionId: string): device is CloneDevice {
	return device.clone?.sessionId === sessionId;
}

/** Up, and not a base: the only devices the live view may show. */
export function isWatchable(device: SimDevice): boolean {
	return device.state === "Booted" && !isBase(device);
}

/** The session's own devices, primary first, then the extra ones by label. */
export function sessionDevices(devices: readonly SimDevice[], sessionId: string): CloneDevice[] {
	return devices
		.filter((device): device is CloneDevice => isCloneOf(device, sessionId))
		.sort((a, b) => Number(b.clone.primary) - Number(a.clone.primary) || a.clone.label.localeCompare(b.clone.label));
}

/**
 * What to call a device. A clone's simctl name is AO's bookkeeping
 * ("AO ao-12 (iPhone 17 Pro Max)"); the model it was cloned from is what a
 * person recognises.
 */
export function deviceTitle(device: SimDevice): string {
	return device.clone?.base || device.name;
}

/** "<session> · <label>", naming the session by its board name where it has one. */
export function cloneOwner(clone: SimClone, names?: SessionNames): string {
	return `${names?.get(clone.sessionId) ?? clone.sessionId} · ${clone.label}`;
}

/**
 * Which device the panel has selected, given what it had and what the machine
 * now reports.
 *
 * A session with devices of its own keeps any of them selected whatever their
 * power state, so switching to a shut-down one is a way to boot it, and falls
 * back to its primary. Any other device stays selected only while it can be
 * watched. A session with no devices of its own (a non-iOS session, or an older
 * daemon) keeps the old rule: the daemon's default, or nothing.
 */
export function resolveSelection({
	defaultUdid,
	devices,
	looked,
	selected,
	sessionId,
}: {
	defaultUdid: string | null;
	devices: readonly SimDevice[];
	/** Whether the device list has been read at least once. */
	looked: boolean;
	selected: string | null;
	sessionId: string;
}): string | null {
	if (!looked) return selected;
	const own = sessionDevices(devices, sessionId);
	if (selected) {
		const current = devices.find((device) => device.udid === selected);
		if (current && (isCloneOf(current, sessionId) || isWatchable(current))) return selected;
	}
	if (own.length > 0) return own[0].udid;
	const fallback = devices.find((device) => device.udid === defaultUdid);
	return fallback && isWatchable(fallback) ? fallback.udid : null;
}

/** The picker's sections. Every device lands in exactly one. */
export type PickerGroups = {
	/** This session's own devices, in sessionDevices order. */
	own: CloneDevice[];
	booted: SimDevice[];
	off: SimDevice[];
	bases: SimDevice[];
};

export function pickerGroups(devices: readonly SimDevice[], sessionId: string): PickerGroups {
	const rest = devices.filter((device) => !isCloneOf(device, sessionId) && !isBase(device));
	return {
		own: sessionDevices(devices, sessionId),
		booted: rest.filter((device) => device.state === "Booted"),
		off: rest.filter((device) => device.state !== "Booted"),
		bases: devices.filter(isBase),
	};
}
