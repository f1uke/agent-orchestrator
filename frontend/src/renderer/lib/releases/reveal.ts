export type Box = { left: number; top: number; right: number; bottom: number };

const PAD = 24;

function axisDelta(lo: number, hi: number, first: number, viewLo: number, viewHi: number): number {
	if (hi - lo <= viewHi - viewLo - 2 * PAD) return Math.round((lo + hi - viewLo - viewHi) / 2);
	return Math.round(first - viewLo - PAD);
}

export function union(boxes: readonly Box[]): Box {
	return {
		left: Math.min(...boxes.map((b) => b.left)),
		top: Math.min(...boxes.map((b) => b.top)),
		right: Math.max(...boxes.map((b) => b.right)),
		bottom: Math.max(...boxes.map((b) => b.bottom)),
	};
}

export function scrollDelta(
	targets: readonly [Box, ...Box[]],
	view: Box,
	o: { sticky?: boolean } = {},
): { dx: number; dy: number } {
	const all = union(targets);
	const [first] = targets;
	return {
		dx: o.sticky ? 0 : axisDelta(all.left, all.right, first.left, view.left, view.right),
		dy: axisDelta(all.top, all.bottom, first.top, view.top, view.bottom),
	};
}

export function intersect(a: Box, b: Box): Box | null {
	const r = {
		left: Math.max(a.left, b.left),
		top: Math.max(a.top, b.top),
		right: Math.min(a.right, b.right),
		bottom: Math.min(a.bottom, b.bottom),
	};
	return r.right > r.left && r.bottom > r.top ? r : null;
}

export function grow(b: Box, by: number): Box {
	return { left: b.left - by, top: b.top - by, right: b.right + by, bottom: b.bottom + by };
}

export function runs(boxes: readonly Box[]): number[][] {
	const order = boxes.map((_, i) => i).sort((a, b) => boxes[a].top - boxes[b].top);
	const out: number[][] = [];
	for (const i of order) {
		const b = boxes[i];
		const run = out.find((r) => {
			const last = boxes[r[r.length - 1]];
			return Math.abs(last.left - b.left) <= 1 && Math.abs(last.right - b.right) <= 1 && b.top - last.bottom <= 1;
		});
		if (run) run.push(i);
		else out.push([i]);
	}
	return out;
}
