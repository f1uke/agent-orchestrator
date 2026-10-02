/**
 * The smallest set of edits that turns one text into another, for applying a
 * formatter's output to a live buffer.
 *
 * 🗝 Replacing the whole model with the formatted text would work and would be
 * wrong: the caret jumps to the end, the scroll position is lost, every
 * decoration and marker is reset, and the undo step contains the entire file.
 * gofmt typically touches a handful of lines; this finds those lines (Myers'
 * diff, line by line) and then trims each changed line down to the characters
 * that differ, so a caret on a re-indented line stays on its word.
 */

/** Replace `before.slice(start, end)` with `text`. Offsets are into `before`. */
export type TextEdit = { start: number; end: number; text: string };

/**
 * Beyond this many differing lines the diff stops looking for the minimal
 * script and replaces the changed region in one edit - correct, just coarser.
 * A formatter rewriting thousands of lines is rewriting the file anyway.
 */
const MAX_EDIT_DISTANCE = 4000;

export function minimalEdits(before: string, after: string): TextEdit[] {
	if (before === after) return [];
	const a = splitKeepingNewlines(before);
	const b = splitKeepingNewlines(after);

	// Common head and tail first: the usual formatter change is small and local,
	// and this keeps the diff below proportional to it rather than to the file.
	let head = 0;
	while (head < a.length && head < b.length && a[head] === b[head]) head++;
	let tail = 0;
	while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
	const midA = a.slice(head, a.length - tail);
	const midB = b.slice(head, b.length - tail);

	const offsets = lineOffsets(a);
	const hunks = diffLines(midA, midB) ?? [{ a0: 0, a1: midA.length, b0: 0, b1: midB.length }];
	const edits: TextEdit[] = [];
	for (const hunk of hunks) {
		const oldLines = midA.slice(hunk.a0, hunk.a1);
		const newLines = midB.slice(hunk.b0, hunk.b1);
		const start = offsets[head + hunk.a0];
		if (oldLines.length === newLines.length) {
			// Line for line: trim each pair to what actually differs.
			let at = start;
			for (let k = 0; k < oldLines.length; k++) {
				const edit = trimmed(at, oldLines[k], newLines[k]);
				if (edit) edits.push(edit);
				at += oldLines[k].length;
			}
			continue;
		}
		const edit = trimmed(start, oldLines.join(""), newLines.join(""));
		if (edit) edits.push(edit);
	}
	return edits;
}

/** Apply edits (non-overlapping, any order) to a text. */
export function applyEdits(text: string, edits: readonly TextEdit[]): string {
	const sorted = [...edits].sort((x, y) => y.start - x.start);
	let out = text;
	for (const edit of sorted) out = out.slice(0, edit.start) + edit.text + out.slice(edit.end);
	return out;
}

function splitKeepingNewlines(text: string): string[] {
	const lines: string[] = [];
	let from = 0;
	for (;;) {
		const nl = text.indexOf("\n", from);
		if (nl < 0) break;
		lines.push(text.slice(from, nl + 1));
		from = nl + 1;
	}
	if (from < text.length) lines.push(text.slice(from));
	return lines;
}

function lineOffsets(lines: readonly string[]): number[] {
	const offsets = [0];
	for (const line of lines) offsets.push(offsets[offsets.length - 1] + line.length);
	return offsets;
}

/** One edit for `oldText` at `start` → `newText`, without their common prefix and suffix. */
function trimmed(start: number, oldText: string, newText: string): TextEdit | null {
	if (oldText === newText) return null;
	let prefix = 0;
	const max = Math.min(oldText.length, newText.length);
	while (prefix < max && oldText[prefix] === newText[prefix]) prefix++;
	let suffix = 0;
	while (suffix < max - prefix && oldText[oldText.length - 1 - suffix] === newText[newText.length - 1 - suffix]) {
		suffix++;
	}
	return {
		start: start + prefix,
		end: start + oldText.length - suffix,
		text: newText.slice(prefix, newText.length - suffix),
	};
}

type Hunk = { a0: number; a1: number; b0: number; b1: number };

/**
 * Myers' O((N+M)·D) diff over lines, as hunks of `a[a0, a1)` replaced by
 * `b[b0, b1)`. Null when the edit distance passes `MAX_EDIT_DISTANCE`.
 */
function diffLines(a: readonly string[], b: readonly string[]): Hunk[] | null {
	const n = a.length;
	const m = b.length;
	const max = n + m;
	if (max === 0) return [];
	const offset = max;
	const v = new Int32Array(2 * max + 2).fill(-1);
	v[offset + 1] = 0;
	const trace: Int32Array[] = [];
	let found = false;
	for (let d = 0; d <= max && d <= MAX_EDIT_DISTANCE; d++) {
		trace.push(v.slice());
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
				found = true;
				break;
			}
		}
		if (found) break;
	}
	if (!found) return null;

	// Walk the trace back from (n, m), recording every non-diagonal step.
	type Step = { kind: "del" | "ins"; x: number; y: number };
	const steps: Step[] = [];
	let x = n;
	let y = m;
	for (let d = trace.length - 1; d > 0; d--) {
		const vd = trace[d];
		const k = x - y;
		const down = k === -d || (k !== d && vd[offset + k - 1] < vd[offset + k + 1]);
		const prevK = down ? k + 1 : k - 1;
		const prevX = vd[offset + prevK];
		const prevY = prevX - prevK;
		while (x > prevX && y > prevY) {
			x--;
			y--;
		}
		if (down) steps.push({ kind: "ins", x: prevX, y: prevY });
		else steps.push({ kind: "del", x: prevX, y: prevY });
		x = prevX;
		y = prevY;
	}
	steps.reverse();

	// Adjacent steps fold into one hunk.
	const hunks: Hunk[] = [];
	for (const step of steps) {
		const last = hunks[hunks.length - 1];
		const a1 = step.kind === "del" ? step.x + 1 : step.x;
		const b1 = step.kind === "ins" ? step.y + 1 : step.y;
		if (last && last.a1 === step.x && last.b1 === step.y) {
			last.a1 = a1;
			last.b1 = b1;
		} else {
			hunks.push({ a0: step.x, a1, b0: step.y, b1 });
		}
	}
	return hunks;
}
