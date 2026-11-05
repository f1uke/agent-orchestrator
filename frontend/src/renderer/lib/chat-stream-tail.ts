const FENCE = /^\s*(```|~~~)/;
const TABLE_DELIMITER = /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$/;

export function settledPrefix(text: string): string {
	const lines = text.split("\n");
	if (lines.filter((line) => FENCE.test(line)).length % 2 === 1) return text;

	const blank = text.lastIndexOf("\n\n");
	const blockStart = blank < 0 ? 0 : blank + 2;
	const block = text.slice(blockStart);
	if (block.trimStart().startsWith("|") && !block.split("\n").some((line) => TABLE_DELIMITER.test(line))) {
		return text.slice(0, blockStart);
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
