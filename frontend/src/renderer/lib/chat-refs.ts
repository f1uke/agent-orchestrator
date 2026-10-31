export const IMAGE_EXT = ["png", "jpg", "jpeg", "gif", "webp", "svg"] as const;
export const DOC_EXT = ["md", "markdown", "html", "htm", "pdf", "json", "txt", "csv", "log", "diff", "patch"] as const;

const MAX_REFS = 64;
const MAX_REF_BYTES = 1024;

// A stem is required, so a bare ".png" or a directory ending in "/.png" is not a file.
const IMAGE_END = new RegExp(`[^/]\\.(?:${IMAGE_EXT.join("|")})$`, "i");
const NOT_LOCAL = /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i;
const PROSE_LEADING = /^[([{"'*_<]+/;
const PROSE_TRAILING = /[)\]}"'*_>.,;:!?]+$/;
const QUOTES = /^["']+|["']+$/g;

// One alternative per construct, so the earliest one in the text wins and a code span
// is never read again as prose. Prose is whatever lies between two matches.
const CONSTRUCT = new RegExp(
	[
		"(?:^|\\n)[ \\t]*(`{3,}|~{3,})[^\\n]*\\n([\\s\\S]*?)(?:\\n[ \\t]*\\1[`~]*[ \\t]*(?=\\n|$)|$)",
		"(`+)(.+?)\\3(?!`)",
		'!?\\[[^\\]\\n]*\\]\\(\\s*<?([^)\\s>]+)>?(?:\\s+"[^"]*")?\\s*\\)',
	].join("|"),
	"g",
);

function isImageRef(word: string): boolean {
	return IMAGE_END.test(word) && !NOT_LOCAL.test(word);
}

const split = (text: string) => text.split(/\s+/).filter((word) => word !== "");

const GLOB = /[*?[]/;

// A directory span such as `plans/x--screens/`: a later span may name files in it by a bare name or glob.
function directoryOf(code: string): string | undefined {
	const words = split(code).map((word) => word.replace(QUOTES, ""));
	if (words.length !== 1) return undefined;
	const [word] = words;
	return word.endsWith("/") && !NOT_LOCAL.test(word) && !GLOB.test(word) ? word : undefined;
}

// A code span is exact: its leading "*" belongs to a glob. Prose carries punctuation and emphasis.
// Under a directory span, a bare name or glob is also tried inside it; a glob with no extension is
// tried only there, since alone it could name anything. The daemon keeps only servable images.
function codeRefs(code: string, dir: string | undefined): string[] {
	const out: string[] = [];
	for (const word of split(code).map((w) => w.replace(QUOTES, ""))) {
		const bare = !word.includes("/") && !word.startsWith("~");
		if (isImageRef(word)) out.push(word);
		if (dir && bare && (isImageRef(word) || (GLOB.test(word) && !NOT_LOCAL.test(word)))) out.push(dir + word);
	}
	return out;
}

function proseRefs(prose: string): string[] {
	return split(prose)
		.map((word) => word.replace(PROSE_LEADING, "").replace(PROSE_TRAILING, ""))
		.filter((word) => isImageRef(word) && (word.includes("/") || word.startsWith("~/")));
}

/**
 * The image paths and globs a message names, in order of appearance and without
 * repeats, within the resolve endpoint's limits of 64 refs of 1024 bytes each.
 */
export function imageRefs(markdown: string): string[] {
	const found = new Set<string>();
	const add = (refs: string[]) => {
		for (const ref of refs) {
			if (found.size < MAX_REFS && new TextEncoder().encode(ref).length <= MAX_REF_BYTES) found.add(ref);
		}
	};
	let at = 0;
	let dir: string | undefined;
	for (const match of markdown.matchAll(CONSTRUCT)) {
		add(proseRefs(markdown.slice(at, match.index)));
		const [, , fenced, , inline, target] = match;
		if (fenced !== undefined) add(fenced.split("\n").flatMap((line) => codeRefs(line, undefined)));
		else if (inline !== undefined) {
			const named = directoryOf(inline);
			if (named) dir = named;
			else add(codeRefs(inline, dir));
		} else if (target !== undefined && isImageRef(target)) add([target]);
		at = match.index + match[0].length;
	}
	add(proseRefs(markdown.slice(at)));
	return [...found];
}

export const docRefs = (_markdown: string): string[] => [];

export const isDocPath = (_path: string): boolean => false;
