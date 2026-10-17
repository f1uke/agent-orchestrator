export type Box = { left: number; top: number; right: number; bottom: number };

const PAD = 24;

function axisDelta(lo: number, hi: number, viewLo: number, viewHi: number): number {
	if (lo >= viewLo && hi <= viewHi) return 0;
	const toStart = lo - viewLo - PAD;
	if (lo < viewLo || hi - lo > viewHi - viewLo - 2 * PAD) return toStart;
	return Math.min(hi - viewHi + PAD, toStart);
}

export function scrollDelta(target: Box, view: Box, o: { sticky?: boolean } = {}): { dx: number; dy: number } {
	return {
		dx: o.sticky ? 0 : axisDelta(target.left, target.right, view.left, view.right),
		dy: axisDelta(target.top, target.bottom, view.top, view.bottom),
	};
}
