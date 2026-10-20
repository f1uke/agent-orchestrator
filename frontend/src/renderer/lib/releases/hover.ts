import { DW, LABEL_W } from "./layout";

export type HoverView = { clientX: number; scrollerLeft: number; canvasLeft: number; days: number };

/** The day column under the pointer, or null over the sticky label column or past either end of the timeline. */
export function hoverColumn(v: HoverView): number | null {
	if (v.clientX - v.scrollerLeft < LABEL_W) return null;
	const col = Math.floor((v.clientX - v.canvasLeft - LABEL_W) / DW);
	return col >= 0 && col < v.days ? col : null;
}
