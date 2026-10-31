export const IMAGE_EXT = ["png", "jpg", "jpeg", "gif", "webp", "svg"] as const;
export const DOC_EXT = ["md", "markdown", "html", "htm", "pdf", "json", "txt", "csv", "log", "diff", "patch"] as const;

const MAX_REFS = 64;
const MAX_REF_BYTES = 1024;

// A stem is required, so a bare ".png" or a directory ending in "/.png" is not a file.
const endsWith = (extensions: readonly string[]) => new RegExp(`[^/]\\.(?:${extensions.join("|")})$`, "i");
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

/**
 * What counts as a ref. `globs` lets a glob stand for several files: one card per match suits images
 * in a thumbnail row, but would fill the chat with cards for documents.
 */
type Kind = { ends: RegExp; globs: boolean };

const IMAGES: Kind = { ends: endsWith(IMAGE_EXT), globs: true };
const DOCUMENTS: Kind = { ends: endsWith(DOC_EXT), globs: false };

function isRef(word: string, kind: Kind): boolean {
	return kind.ends.test(word) && !NOT_LOCAL.test(word) && (kind.globs || !GLOB.test(word));
}

const GLOB = /[*?[]/;

const split = (text: string) => text.split(/\s+/).filter((word) => word !== "");

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
function codeRefs(code: string, dir: string | undefined, kind: Kind): string[] {
	const out: string[] = [];
	for (const word of split(code).map((w) => w.replace(QUOTES, ""))) {
		const bare = !word.includes("/") && !word.startsWith("~");
		if (isRef(word, kind)) out.push(word);
		if (dir && bare && (isRef(word, kind) || (kind.globs && GLOB.test(word) && !NOT_LOCAL.test(word)))) {
			out.push(dir + word);
		}
	}
	return out;
}

function proseRefs(prose: string, kind: Kind): string[] {
	return split(prose)
		.map((word) => word.replace(PROSE_LEADING, "").replace(PROSE_TRAILING, ""))
		.filter((word) => isRef(word, kind) && (word.includes("/") || word.startsWith("~/")));
}

/**
 * The image paths and globs a message names, in order of appearance and without
 * repeats, within the resolve endpoint's limits of 64 refs of 1024 bytes each.
 */
export const imageRefs = (markdown: string): string[] => refsOf(markdown, IMAGES);

/** The document paths a message names, found the way imageRefs finds images. A glob is not a document ref. */
export const docRefs = (markdown: string): string[] => refsOf(markdown, DOCUMENTS);

/** Whether a path the model wrote names a document, so a Write of `main.go` is never sent to be resolved. */
export const isDocPath = (path: string): boolean => isRef(path, DOCUMENTS);

/** Whether a path names a document or an image: the files a Write row may offer to open. */
export const isShowablePath = (path: string): boolean => isRef(path, DOCUMENTS) || isRef(path, IMAGES);

function refsOf(markdown: string, kind: Kind): string[] {
	const found = new Set<string>();
	const add = (refs: string[]) => {
		for (const ref of refs) {
			if (found.size < MAX_REFS && new TextEncoder().encode(ref).length <= MAX_REF_BYTES) found.add(ref);
		}
	};
	let at = 0;
	let dir: string | undefined;
	for (const match of markdown.matchAll(CONSTRUCT)) {
		add(proseRefs(markdown.slice(at, match.index), kind));
		const [, , fenced, , inline, target] = match;
		if (fenced !== undefined) add(fenced.split("\n").flatMap((line) => codeRefs(line, undefined, kind)));
		else if (inline !== undefined) {
			const named = directoryOf(inline);
			if (named) dir = named;
			else add(codeRefs(inline, dir, kind));
		} else if (target !== undefined && isRef(target, kind)) add([target]);
		at = match.index + match[0].length;
	}
	add(proseRefs(markdown.slice(at), kind));
	return [...found];
}
