import { describe, expect, it } from "vitest";
import type { RemoteControl, RemoteControlState } from "./chat-fold";
import { qrCode, remoteControlOn } from "./chat-remote";

const at = (state: RemoteControlState): RemoteControl => ({ state, url: "", detail: "" });

describe("remoteControlOn", () => {
	it.each([
		["off", false],
		["connecting", true],
		["connected", true],
		["reconnecting", true],
		["failed", false],
	] as const)("reads %s as %s", (state, on) => {
		expect(remoteControlOn(at(state))).toBe(on);
	});
});

describe("qrCode", () => {
	const url = "https://claude.ai/code/session_example";

	it("draws one square grid with the three corner finder patterns", () => {
		const { size, path } = qrCode(url);
		const dark = new Set<string>();
		for (const [, x, y, run] of path.matchAll(/M(\d+) (\d+)h(\d+)v1h-\d+z/g)) {
			for (let i = 0; i < Number(run); i++) dark.add(`${Number(x) + i},${y}`);
		}
		expect(size % 4).toBe(1);
		for (const [cx, cy] of [
			[0, 0],
			[size - 7, 0],
			[0, size - 7],
		]) {
			for (let i = 0; i < 7; i++) {
				expect(dark.has(`${cx + i},${cy}`)).toBe(true);
				expect(dark.has(`${cx + i},${cy + 6}`)).toBe(true);
				expect(dark.has(`${cx},${cy + i}`)).toBe(true);
				expect(dark.has(`${cx + 6},${cy + i}`)).toBe(true);
			}
			expect(dark.has(`${cx + 1},${cy + 1}`)).toBe(false);
		}
	});

	it("draws a different grid for a different link", () => {
		expect(qrCode(url).path).not.toBe(qrCode(`${url}2`).path);
	});
});
