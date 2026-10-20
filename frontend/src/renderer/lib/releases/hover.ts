import { DW, LABEL_W } from "./layout";

export type HoverView = { clientX: number; scrollerLeft: number; canvasLeft: number; days: number };

export function hoverColumn(v: HoverView): number | null {
	if (v.clientX - v.scrollerLeft < LABEL_W) return null;
	const col = Math.floor((v.clientX - v.canvasLeft - LABEL_W) / DW);
	return col >= 0 && col < v.days ? col : null;
}
