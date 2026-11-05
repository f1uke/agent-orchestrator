const FENCE = /^\s*(```|~~~)/;
const TABLE_DELIMITER = /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$/;

// The part of a streaming reply that renders as its final shape. A half-typed
// table header, bold, code span or link would show its raw markers for a few
// words and then jump, so the tail that opens one waits for the next delta.
export function settledPrefix(text: string): string {
	const lines = text.split("\n");
	if (lines.filter((line) => FENCE.test(line)).length % 2 === 1) return text;

	const blockStart = text.lastIndexOf("\n\n") + 1;
	const block = text.slice(blockStart === 0 ? 0 : blockStart + 1);
	const blockLines = block.split("\n");
	if (block.trimStart().startsWith("|") && !blockLines.some((line) => TABLE_DELIMITER.test(line))) {
		return text.slice(0, blockStart === 0 ? 0 : blockStart + 1);
	}

	const lineStart = text.lastIndexOf("\n") + 1;
	const cut = openInlineAt(text.slice(lineStart));
	return cut === undefined ? text : text.slice(0, lineStart + cut);
}

function openInlineAt(line: string): number | undefined {
	const cuts: number[] = [];
	const code = [...line.matchAll(/`/g)].map((m) => m.index);
	if (code.length % 2 === 1) cuts.push(code.at(-1)!);
	const prose = code.length % 2 === 1 ? line.slice(0, code.at(-1)) : line;
	const bold = [...prose.matchAll(/\*\*/g)].map((m) => m.index);
	if (bold.length % 2 === 1) cuts.push(bold.at(-1)!);
	const link = prose.lastIndexOf("[");
	if (link >= 0) {
		const rest = prose.slice(link);
		const close = rest.indexOf("]");
		const opensUrl = close >= 0 && rest[close + 1] === "(";
		if (close < 0 || (opensUrl && !rest.includes(")", close))) cuts.push(link);
	}
	return cuts.length > 0 ? Math.min(...cuts) : undefined;
}
