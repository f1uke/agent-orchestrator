/**
 * A synthetic file index shaped like a real large iOS app.
 *
 * The numbers are not invented: they were measured (read-only, `git ls-files`)
 * from the 7k-file iOS project the slowdown was reported against, and this
 * generator reproduces that shape rather than the project itself — 6,940 paths,
 * ~1,970 directories, a median of 4 files per directory, one dominant top-level
 * module holding ~97% of the tree, and a depth histogram peaking at 7 segments
 * and reaching 10. Reproducing the SHAPE matters more than the names: what the
 * Files rail pays for is the number of rows and how deep they nest.
 *
 * Deterministic (seeded), so a before/after measurement compares like with like.
 */

/** mulberry32 — small, fast, and stable across runs and machines. */
function rng(seed: number): () => number {
	let a = seed >>> 0;
	return () => {
		a = (a + 0x6d2b79f5) >>> 0;
		let t = Math.imul(a ^ (a >>> 15), 1 | a);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const FEATURES = [
	"Onboarding",
	"Portfolio",
	"Trading",
	"Wallet",
	"Profile",
	"Notifications",
	"Search",
	"Settings",
	"Discover",
	"Rewards",
	"Support",
	"Insights",
	"Watchlist",
	"Transfers",
	"Statements",
	"Security",
	"Referral",
	"Feed",
	"Chat",
	"Documents",
];
const LAYERS = ["Presentation", "Domain", "Data", "Common", "Resources"];
const PARTS = [
	"View",
	"ViewModel",
	"Coordinator",
	"Service",
	"Repository",
	"Model",
	"Mapper",
	"Cell",
	"Router",
	"Assembly",
];
const EXTS = ["swift", "swift", "swift", "png", "json", "xib"];

/** Generates `count` slash-separated paths with the measured shape. */
export function largeWorkspacePaths(count = 6940, seed = 7055): string[] {
	const rand = rng(seed);
	const pick = <T>(xs: readonly T[]) => xs[Math.floor(rand() * xs.length)];
	const paths: string[] = [];
	const push = (p: string) => {
		if (paths.length < count) paths.push(p);
	};

	// The few small top-level siblings the real tree carries alongside its one
	// dominant module — they are what stops the root from being a single row.
	for (const [dir, n] of [
		["FNCore", 82],
		["Vendor.xcframework", 72],
		["deployment", 4],
		["ci_scripts", 3],
	] as const) {
		for (let i = 0; i < n; i++) push(`${dir}/${pick(LAYERS)}/${pick(PARTS)}${i}.${pick(EXTS)}`);
	}
	for (const f of ["README.md", "Podfile", "Gemfile", ".swiftlint.yml", "Makefile"]) push(f);

	// The dominant module. Depth grows with a decaying probability, which is what
	// produces a histogram peaking mid-tree rather than a uniform one.
	let feature = 0;
	while (paths.length < count) {
		const segs = ["App", `Features`, FEATURES[feature % FEATURES.length], pick(LAYERS)];
		feature++;
		let depth = 0;
		while (rand() < 0.72 - depth * 0.12 && depth < 5) {
			segs.push(`${pick(PARTS)}${Math.floor(rand() * 12)}`);
			depth++;
		}
		const dir = segs.join("/");
		const fileCount = 2 + Math.floor(rand() * 4);
		for (let i = 0; i < fileCount; i++) push(`${dir}/${pick(PARTS)}${feature}${i}.${pick(EXTS)}`);
	}
	return Array.from(new Set(paths)).sort();
}
