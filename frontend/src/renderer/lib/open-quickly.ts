/**
 * Ranking for the ⌘⇧O "Open Quickly" palette — over FILES.
 *
 * Four rules, all four measured in the editor spike against Xcode's own Open
 * Quickly on a real iOS project (see `spike-in-app-editor-lsp--proposal.md` §8).
 * They are not preferences; each one fixes a result the spike observed being
 * wrong:
 *
 *  1. **A hit that starts the name beats one scattered through it.** Without it,
 *     `OG-Promotion-Hub 2.png` outranked `PromotionHubViewController.swift` for
 *     `"promohub"`. Enforced twice over, independently: the scorer pays a large
 *     bonus for matching at index 0 of the basename and for every contiguous
 *     character after it, and a basename match always outranks a match found
 *     only in the directory part of the path.
 *  2. **Assets are not code.** `.swift` / `.go` / `.m` weigh up, `.png` /
 *     `.json` / `.plist` weigh down.
 *  3. **Generated output is demoted by PATH SHAPE**, not by root prefix — the
 *     index answers with paths from the tree that was built, so `DerivedData/…`
 *     and `node_modules/…` look exactly like source until you read the segments.
 *  4. **Dedupe**, so one file cannot occupy two rows via two spellings of its
 *     path.
 *
 * Ranking lives in the renderer, not the daemon, on purpose. Results are derived
 * synchronously from `(index, query)`, which makes "the palette shows the
 * previous query's results" structurally impossible rather than merely guarded
 * against — the failure the spike hit on the symbol side. Slice 5's symbol
 * ranking is meant to reuse the scorer here rather than grow a second one.
 */

/** One ranked file, ready to render. */
export type FileMatch = {
	/** Normalised, workspace-relative, slash-separated. */
	path: string;
	score: number;
	/**
	 * Indices into `path` of the characters the query matched, ascending. The
	 * palette underlines these; they are also the cheapest proof that the scorer
	 * matched what a reader thinks it matched.
	 */
	positions: number[];
};

// Scoring weights. Tuned against the spike's fixture (see open-quickly.test.ts),
// on one scale so they can be compared by eye: a structural win is worth more
// than a file-kind nudge, and a file-kind nudge is worth more than length.
const MATCH = 2; // any matched character
const CONSECUTIVE = 6; // matched immediately after the previous match
const BOUNDARY = 8; // matched at a word start: after a separator, or a camel hump
const NAME_START = 16; // matched at index 0 of the basename
const CASE_MATCH = 1; // smart case: the query's own capitals matched exactly
const GAP = 1; // per character skipped inside the match
const LEAD_GAP = 1; // per character skipped before the match begins

/**
 * A basename match and a directory-only match are different KINDS of answer, not
 * two points on one scale, so they are separated by more than any bonus can
 * bridge. Typing `promohub` means "the file called that", and a directory that
 * happens to contain those letters must never come first.
 *
 * The price, measured on this repo: because the separation is absolute, an
 * arbitrarily SCATTERED name match still beats a perfect directory match — for
 * `"queries"`, `migrate_unique_version_test.go` lands above
 * `storage/sqlite/queries/*.sql`, whose directory is spelled exactly that. That
 * is rule 1 working as specified rather than a bug, and softening the tier into
 * a bonus would put the spike's own regression back on the table, so it stays
 * absolute until someone decides otherwise with a case in hand.
 */
const NAME_TIER = 1000;

const EXACT_NAME = 100; // basename === query
const EXACT_STEM = 60; // basename without its extension === query

const KIND_CODE = 30;
const KIND_DATA = -10;
const KIND_ASSET = -60;

const GENERATED_DIR = -80;
const GENERATED_FILE = -60;
const GENERATED_FLOOR = -120; // the two never stack past this

/** Longer paths lose ties; small enough that it only ever breaks one. */
const LENGTH_TIEBREAK = 0.05;

/** Beyond this the O(n·m) scorer is skipped for a cheap greedy walk. */
const MAX_SCORED_LENGTH = 400;

const CODE_EXT = new Set([
	"swift",
	"m",
	"mm",
	"h",
	"hpp",
	"c",
	"cc",
	"cpp",
	"go",
	"rs",
	"java",
	"kt",
	"kts",
	"scala",
	"cs",
	"ts",
	"tsx",
	"js",
	"jsx",
	"mjs",
	"cjs",
	"vue",
	"svelte",
	"py",
	"rb",
	"php",
	"ex",
	"exs",
	"erl",
	"hs",
	"lua",
	"pl",
	"sh",
	"bash",
	"zsh",
	"fish",
	"sql",
	"proto",
	"graphql",
	"gradle",
	"cmake",
]);

const ASSET_EXT = new Set([
	"png",
	"jpg",
	"jpeg",
	"gif",
	"webp",
	"bmp",
	"tiff",
	"ico",
	"icns",
	"svg",
	"pdf",
	"psd",
	"sketch",
	"mp3",
	"mp4",
	"mov",
	"wav",
	"aiff",
	"ttf",
	"otf",
	"woff",
	"woff2",
	"eot",
	"zip",
	"gz",
	"tar",
	"car",
	"nib",
	"storyboardc",
	"bin",
	"dat",
	"pack",
	"map",
]);

const DATA_EXT = new Set([
	"json",
	"yaml",
	"yml",
	"toml",
	"ini",
	"cfg",
	"conf",
	"plist",
	"xml",
	"lock",
	"sum",
	"csv",
	"tsv",
	"xcconfig",
	"pbxproj",
	"xcworkspacedata",
	"xcscheme",
	"strings",
	"log",
	"snap",
]);

/**
 * Directory names that mean "a machine wrote this". Matched as whole path
 * SEGMENTS, case-insensitively — a file called `build.gradle` is not build
 * output. Deliberately excludes the truly ambiguous ones (`bin`, `out`, `obj`,
 * `src`): a false demotion is cheap but pointless, and these names carry real
 * source in enough repositories to not be worth it.
 */
const GENERATED_DIRS = new Set([
	"deriveddata",
	"node_modules",
	"pods",
	"carthage",
	".build",
	"build",
	"dist",
	"vendor",
	"target",
	".next",
	".nuxt",
	".svelte-kit",
	".venv",
	"venv",
	"__pycache__",
	".pytest_cache",
	"coverage",
	".gradle",
	".yarn",
	".turbo",
	".parcel-cache",
	"bower_components",
	"generated",
	"gen",
	".terraform",
]);

/** Filename infixes that mean the same thing (`routeTree.gen.ts`, `api.pb.go`). */
const GENERATED_FILE_MARKS = [".gen.", ".pb.", ".g.", ".generated.", "_generated.", ".min.", ".designer."];

/**
 * Collapse the spellings of one path to a single key, so rule 4 (dedupe) can be
 * a Set rather than a scan. `./a//b.go` and `a/b.go` are the same file and must
 * not both take a row.
 */
export function normalizeIndexPath(path: string): string {
	let p = path.replace(/\\/g, "/");
	while (p.startsWith("./")) p = p.slice(2);
	p = p.replace(/\/{2,}/g, "/");
	if (p.length > 1 && p.endsWith("/")) p = p.slice(0, -1);
	return p;
}

function basenameStart(path: string): number {
	return path.lastIndexOf("/") + 1;
}

function extensionOf(basename: string): string {
	const dot = basename.lastIndexOf(".");
	// A leading dot is the whole name (`.gitignore`), not an extension.
	if (dot <= 0) return "";
	return basename.slice(dot + 1).toLowerCase();
}

/** Rule 2: what kind of file this is, as a score adjustment. */
function kindWeight(basename: string): number {
	const ext = extensionOf(basename);
	if (ext === "") return 0;
	if (CODE_EXT.has(ext)) return KIND_CODE;
	if (ASSET_EXT.has(ext)) return KIND_ASSET;
	if (DATA_EXT.has(ext)) return KIND_DATA;
	return 0;
}

/** Rule 3: how much of this path's SHAPE says "generated". */
function generatedPenalty(path: string): number {
	let penalty = 0;
	const cut = basenameStart(path);
	const dirs = path.slice(0, cut === 0 ? 0 : cut - 1);
	if (dirs !== "") {
		for (const segment of dirs.split("/")) {
			if (GENERATED_DIRS.has(segment.toLowerCase())) {
				penalty += GENERATED_DIR;
				break; // one verdict per path; nesting does not make it more generated
			}
		}
	}
	const basename = path.slice(cut).toLowerCase();
	if (GENERATED_FILE_MARKS.some((mark) => basename.includes(mark))) penalty += GENERATED_FILE;
	return Math.max(penalty, GENERATED_FLOOR);
}

/**
 * The query as the scorer sees it: whitespace dropped, so `promo hub` and
 * `promohub` are the same search. Case is KEPT — capitals the user typed are a
 * signal (smart case), never a filter.
 */
export function normalizeQuery(query: string): string {
	return query.replace(/\s+/g, "");
}

function isBoundary(text: string, index: number): boolean {
	if (index === 0) return true;
	const prev = text.charCodeAt(index - 1);
	const cur = text.charCodeAt(index);
	const isAlnum = (c: number) => (c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122);
	if (!isAlnum(prev)) return true; // after `/`, `-`, `_`, `.`, space
	const prevLower = prev >= 97 && prev <= 122;
	const prevDigit = prev >= 48 && prev <= 57;
	const curUpper = cur >= 65 && cur <= 90;
	return (prevLower || prevDigit) && curUpper; // camel hump
}

/** Fast reject: can `needle` be read out of `hay` in order at all? */
function subsequenceEnd(hay: string, needle: string): number {
	let j = 0;
	for (let i = 0; i < hay.length && j < needle.length; i++) {
		if (hay.charCodeAt(i) === needle.charCodeAt(j)) j++;
		if (j === needle.length) return i;
	}
	return j === needle.length ? hay.length - 1 : -1;
}

type Scored = { score: number; positions: number[] };

/**
 * Best-alignment scorer: for every way `query` can be read out of `text` in
 * order, the highest-scoring one. O(text · query) with a running prefix maximum,
 * which the linear gap penalty makes possible — `best[k] − GAP·(j−k−1)` is
 * `(best[k] + GAP·k) − GAP·(j−1)`, so the max over all k is carried forward
 * instead of rescanned.
 *
 * Greedy left-to-right would answer this wrong on exactly the case that matters:
 * for `promohub` it takes the first `h` it can reach and never discovers that
 * the `H` of `PromotionHub` is worth a word-boundary bonus.
 */
function scoreAlignment(text: string, lowerText: string, query: string, lowerQuery: string): Scored | null {
	const n = lowerText.length;
	const m = lowerQuery.length;
	if (m === 0 || n === 0 || m > n) return null;

	// best[j]: score of the best alignment of query[0..i] whose last character
	// matched at text[j]. from[i][j]: which text index the previous query
	// character matched at, for the walk-back.
	let best = new Float64Array(n).fill(Number.NEGATIVE_INFINITY);
	const from: Int32Array[] = [];

	for (let j = 0; j < n; j++) {
		if (lowerText.charCodeAt(j) !== lowerQuery.charCodeAt(0)) continue;
		let s = MATCH - LEAD_GAP * j;
		if (j === 0) s += NAME_START;
		else if (isBoundary(text, j)) s += BOUNDARY;
		if (text.charCodeAt(j) === query.charCodeAt(0)) s += CASE_MATCH;
		best[j] = s;
	}

	for (let i = 1; i < m; i++) {
		const next = new Float64Array(n).fill(Number.NEGATIVE_INFINITY);
		const back = new Int32Array(n).fill(-1);
		// runningBest carries max_k(best[k] + GAP·k) for every k seen so far.
		let runningBest = Number.NEGATIVE_INFINITY;
		let runningAt = -1;
		for (let j = 1; j < n; j++) {
			const k = j - 1;
			if (best[k] > Number.NEGATIVE_INFINITY) {
				const shifted = best[k] + GAP * k;
				if (shifted > runningBest) {
					runningBest = shifted;
					runningAt = k;
				}
			}
			if (lowerText.charCodeAt(j) !== lowerQuery.charCodeAt(i)) continue;

			let base = MATCH;
			if (isBoundary(text, j)) base += BOUNDARY;
			if (text.charCodeAt(j) === query.charCodeAt(i)) base += CASE_MATCH;

			// Contiguous with the previous match — scored on its own because the
			// bonus depends on WHICH k, not just on the best one.
			let bestScore = Number.NEGATIVE_INFINITY;
			let bestFrom = -1;
			if (best[j - 1] > Number.NEGATIVE_INFINITY) {
				bestScore = best[j - 1] + base + CONSECUTIVE;
				bestFrom = j - 1;
			}
			if (runningBest > Number.NEGATIVE_INFINITY) {
				const gapped = runningBest - GAP * (j - 1) + base;
				if (gapped > bestScore) {
					bestScore = gapped;
					bestFrom = runningAt;
				}
			}
			if (bestFrom >= 0) {
				next[j] = bestScore;
				back[j] = bestFrom;
			}
		}
		from.push(back);
		best = next;
	}

	let endAt = -1;
	let endScore = Number.NEGATIVE_INFINITY;
	for (let j = 0; j < n; j++) {
		if (best[j] > endScore) {
			endScore = best[j];
			endAt = j;
		}
	}
	if (endAt < 0 || endScore === Number.NEGATIVE_INFINITY) return null;

	const positions = new Array<number>(m);
	let at = endAt;
	for (let i = m - 1; i >= 0; i--) {
		positions[i] = at;
		if (i > 0) at = from[i - 1][at];
	}
	return { score: endScore, positions };
}

/** The long-path escape hatch: a plain greedy walk, scored flat. */
function greedyAlignment(lowerText: string, lowerQuery: string): Scored | null {
	const positions: number[] = [];
	let j = 0;
	for (let i = 0; i < lowerText.length && j < lowerQuery.length; i++) {
		if (lowerText.charCodeAt(i) === lowerQuery.charCodeAt(j)) {
			positions.push(i);
			j++;
		}
	}
	if (j < lowerQuery.length) return null;
	return { score: MATCH * lowerQuery.length, positions };
}

/**
 * Score one path against a query, or null if the query cannot be read out of it.
 * Exported for the ranking test; the palette calls `rankFiles`.
 */
export function scoreFile(path: string, query: string, lowerQuery: string): FileMatch | null {
	const lowerPath = path.toLowerCase();
	if (subsequenceEnd(lowerPath, lowerQuery) < 0) return null;

	const cut = basenameStart(path);
	const basename = path.slice(cut);
	const lowerBasename = lowerPath.slice(cut);

	let scored: Scored | null = null;
	let offset = 0;
	let tier = 0;
	if (subsequenceEnd(lowerBasename, lowerQuery) >= 0) {
		// Rule 1, the structural half: the name matched, so this outranks every
		// path-only hit no matter how the two score against each other.
		scored =
			lowerBasename.length > MAX_SCORED_LENGTH
				? greedyAlignment(lowerBasename, lowerQuery)
				: scoreAlignment(basename, lowerBasename, query, lowerQuery);
		offset = cut;
		tier = NAME_TIER;
	} else {
		scored =
			lowerPath.length > MAX_SCORED_LENGTH
				? greedyAlignment(lowerPath, lowerQuery)
				: scoreAlignment(path, lowerPath, query, lowerQuery);
	}
	if (!scored) return null;

	let score = tier + scored.score + kindWeight(basename) + generatedPenalty(path);
	score -= path.length * LENGTH_TIEBREAK;
	if (tier > 0) {
		if (lowerBasename === lowerQuery) score += EXACT_NAME;
		else {
			const dot = lowerBasename.lastIndexOf(".");
			if (dot > 0 && lowerBasename.slice(0, dot) === lowerQuery) score += EXACT_STEM;
		}
	}

	return { path, score, positions: offset === 0 ? scored.positions : scored.positions.map((p) => p + offset) };
}

/**
 * Rank an index of workspace-relative paths against a query.
 *
 * Pure and synchronous by design: the palette recomputes this from the CURRENT
 * query on every render, so there is no request in flight that could answer a
 * keystroke with a previous query's results.
 *
 * An empty query returns nothing rather than the whole tree — a list nobody
 * asked for is not a useful default, and the palette says "type to search"
 * instead of pretending the first N files are an answer.
 */
export function rankFiles(paths: readonly string[], query: string, limit = 50): FileMatch[] {
	const trimmed = normalizeQuery(query);
	if (trimmed === "") return [];
	const lowerQuery = trimmed.toLowerCase();

	const seen = new Set<string>();
	const matches: FileMatch[] = [];
	for (const raw of paths) {
		const path = normalizeIndexPath(raw);
		if (path === "" || seen.has(path)) continue; // rule 4
		seen.add(path);
		const match = scoreFile(path, trimmed, lowerQuery);
		if (match) matches.push(match);
	}

	// Ties break on the path so the order is deterministic: a list that reshuffles
	// between two identical queries is a list you cannot learn.
	matches.sort((a, b) => b.score - a.score || (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
	return matches.slice(0, limit);
}
