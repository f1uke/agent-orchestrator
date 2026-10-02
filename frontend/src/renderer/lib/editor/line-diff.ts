/**
 * One changed run between two line arrays: `a[a0, a1)` was replaced by
 * `b[b0, b1)`. Half-open and 0-based. An empty `a` range is an insertion, an
 * empty `b` range a deletion.
 */
export type LineRun = { a0: number; a1: number; b0: number; b1: number };

/**
 * Beyond this many differing lines the search stops looking for the minimal
 * script and reports the whole changed middle as ONE run - correct, just
 * coarser. Two texts that far apart are a rewrite, and the run that says so is
 * the honest answer at a bounded cost.
 */
export const MAX_EDIT_DISTANCE = 2000;

/**
 * The changed runs that turn `a` into `b`, line by line (Myers' O((N+M)·D)
 * diff), in order.
 *
 * Built to run on every pause in typing, so its cost follows the EDIT, not the
 * file: the common head and tail are peeled off with plain string compares
 * before anything else, which for a keystroke leaves a middle of one line; only
 * that middle is interned and searched. The search keeps one snapshot of its
 * frontier per edit step, each only as wide as that step can reach, so memory is
 * O(D²) rather than the O(D·(N+M)) of copying the whole frontier every step.
 */
export function diffLines(a: readonly string[], b: readonly string[], maxDistance = MAX_EDIT_DISTANCE): LineRun[] {
	let head = 0;
	while (head < a.length && head < b.length && a[head] === b[head]) head++;
	let tail = 0;
	while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
	const n = a.length - head - tail;
	const m = b.length - head - tail;
	if (n === 0 && m === 0) return [];
	if (n === 0 || m === 0) return [{ a0: head, a1: head + n, b0: head, b1: head + m }];

	// Interned to small integers, so the inner loop compares numbers.
	const ids = new Map<string, number>();
	const intern = (line: string) => {
		let id = ids.get(line);
		if (id === undefined) {
			id = ids.size;
			ids.set(line, id);
		}
		return id;
	};
	const x0 = new Int32Array(n);
	for (let i = 0; i < n; i++) x0[i] = intern(a[head + i]);
	const y0 = new Int32Array(m);
	for (let j = 0; j < m; j++) y0[j] = intern(b[head + j]);

	const runs = myers(x0, y0, maxDistance);
	if (!runs) return [{ a0: head, a1: head + n, b0: head, b1: head + m }];
	for (const run of runs) {
		run.a0 += head;
		run.a1 += head;
		run.b0 += head;
		run.b1 += head;
	}
	return runs;
}

/** Myers over interned lines. Null when the edit distance passes `maxDistance`. */
function myers(a: Int32Array, b: Int32Array, maxDistance: number): LineRun[] | null {
	const n = a.length;
	const m = b.length;
	const limit = Math.min(n + m, maxDistance);
	const offset = limit + 1;
	const v = new Int32Array(2 * limit + 3);
	v[offset + 1] = 0;
	// snapshots[d][k + d] is the furthest x reached on diagonal k after step d.
	const snapshots: Int32Array[] = [];
	let found = -1;
	for (let d = 0; d <= limit && found < 0; d++) {
		for (let k = -d; k <= d; k += 2) {
			const down = k === -d || (k !== d && v[offset + k - 1] < v[offset + k + 1]);
			let x = down ? v[offset + k + 1] : v[offset + k - 1] + 1;
			let y = x - k;
			while (x < n && y < m && a[x] === b[y]) {
				x++;
				y++;
			}
			v[offset + k] = x;
			if (x >= n && y >= m) {
				found = d;
				break;
			}
		}
		snapshots.push(v.slice(offset - d, offset + d + 1));
	}
	if (found < 0) return null;

	// Walk back from (n, m), one edit step at a time.
	type Step = { kind: "del" | "ins"; x: number; y: number };
	const steps: Step[] = [];
	let x = n;
	let y = m;
	for (let d = found; d > 0; d--) {
		const prev = snapshots[d - 1];
		const at = (k: number) => prev[k + d - 1];
		const k = x - y;
		const down = k === -d || (k !== d && at(k - 1) < at(k + 1));
		const prevK = down ? k + 1 : k - 1;
		const prevX = at(prevK);
		const prevY = prevX - prevK;
		steps.push(down ? { kind: "ins", x: prevX, y: prevY } : { kind: "del", x: prevX, y: prevY });
		x = prevX;
		y = prevY;
	}
	steps.reverse();

	// Adjacent steps fold into one run.
	const runs: LineRun[] = [];
	for (const step of steps) {
		const last = runs[runs.length - 1];
		const a1 = step.kind === "del" ? step.x + 1 : step.x;
		const b1 = step.kind === "ins" ? step.y + 1 : step.y;
		if (last && last.a1 === step.x && last.b1 === step.y) {
			last.a1 = a1;
			last.b1 = b1;
		} else {
			runs.push({ a0: step.x, a1, b0: step.y, b1 });
		}
	}
	return runs;
}
