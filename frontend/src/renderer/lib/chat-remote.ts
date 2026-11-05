import { encode } from "uqr";
import type { RemoteControl, RemoteControlState } from "./chat-fold";

/** True while the bridge is on or coming up, so the menu offers to turn it off. A failed bridge is off: the menu offers a retry. */
export function remoteControlOn(remote: RemoteControl): boolean {
	return remote.state === "connecting" || remote.state === "connected" || remote.state === "reconnecting";
}

export const REMOTE_LABEL: Record<Exclude<RemoteControlState, "off">, string> = {
	connecting: "Remote connecting",
	connected: "Remote connected",
	reconnecting: "Remote reconnecting",
	failed: "Remote failed",
};

export type QrCode = { size: number; path: string };

/** The dark modules of a QR code for text, as one SVG path in a size x size grid of unit squares. */
export function qrCode(text: string): QrCode {
	const { size, data } = encode(text, { ecc: "M", border: 0 });
	let path = "";
	for (let y = 0; y < size; y++) {
		for (let x = 0; x < size; x++) {
			if (!data[y][x]) continue;
			let run = 1;
			while (x + run < size && data[y][x + run]) run++;
			path += `M${x} ${y}h${run}v1h-${run}z`;
			x += run;
		}
	}
	return { size, path };
}
