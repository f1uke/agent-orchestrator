import { readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { DURATION, enterKeyframes, momentumDistance, moveKeys, releaseMotion, type Presence } from "./motion";
import { DEFAULT_LAYERS, parseReleasesSearch } from "./url";

const COMPONENTS = path.resolve(__dirname, "../../components/releases");
const FIX = "use Motion's x/y/scale values (transform: [.., 'none'] collapsed to scaleX(0) in d3)";
const OWN = "take transitions from releaseMotion() in lib/releases/motion.ts, the only place that times motion";

function sources(dir: string): string[] {
	return readdirSync(dir).flatMap((name) => {
		const p = path.join(dir, name);
		if (statSync(p).isDirectory()) return sources(p);
		return /\.(tsx?|css)$/.test(name) && !/\.test\./.test(name) ? [p] : [];
	});
}

/** Text of each `animate=`, `initial=`, `exit=` prop and `keyframes` value, braces balanced. */
function motionProps(src: string): string[] {
	const out: string[] = [];
	const re = /\b(?:(?:animate|initial|exit|keyframes)\s*[=:]|animate\(\s*[\w.]+\s*,)\s*\{/g;
	for (let m = re.exec(src); m; m = re.exec(src)) {
		let depth = 0;
		let i = m.index + m[0].length - 1;
		const from = i;
		for (; i < src.length; i++) {
			if (src[i] === "{") depth++;
			else if (src[i] === "}" && --depth === 0) break;
		}
		out.push(src.slice(from, i + 1));
	}
	return out;
}

function motionViolations(src: string): string[] {
	return motionProps(src).flatMap((p) => {
		const bad: string[] = [];
		if (/\btransform\s*:/.test(p)) bad.push(`transform in ${p.slice(0, 60)}`);
		if (/["']none["']/.test(p)) bad.push(`'none' keyframe in ${p.slice(0, 60)}`);
		return bad;
	});
}

function timingViolations(src: string): string[] {
	const code = src.replace(/\/\/.*$/gm, "");
	return [...code.matchAll(/\b(duration|ease|delay)\s*:|\bstagger\(/g)].map((m) => m[0]);
}

const ALL = [releaseMotion(false), releaseMotion(true)];
const presences = (s: ReturnType<typeof releaseMotion>): [string, Presence][] => [
	["unfold", s.unfold],
	["appear", s.appear],
	["sheet", s.sheet],
	...[0, 1, 4].map((i): [string, Presence] => [`label(${i})`, s.label(i)]),
];
const keysOf = (p: Presence) =>
	[...new Set([...Object.keys(p.initial || {}), ...Object.keys(p.animate), ...Object.keys(p.exit)])].sort();
const seconds = (t: { duration?: number; delay?: number }) => (t.duration ?? 0) + (t.delay ?? 0);

describe("releases motion", () => {
	it("reads keyframes passed to an imperative animate(el, ...) too", () => {
		expect(motionViolations(`animate(part, { transform: ["scaleX(0)", "none"] }, t)`)).toHaveLength(2);
		expect(motionViolations(`animate(el.scrollLeft, to, { ...mo.axis })`)).toEqual([]);
	});

	it("catches the d3 mistake in a sample", () => {
		expect(motionViolations(`<m.div animate={{ transform: ["scaleX(0.4)", "none"] }} />`)).toHaveLength(2);
		expect(motionViolations(`<m.div initial={{ opacity: 0, x: 16 }} animate={{ opacity: 1, x: 0 }} />`)).toEqual([]);
	});

	it(`components/releases animate only Motion values: ${FIX}`, () => {
		const files = sources(COMPONENTS);
		expect(files.length).toBeGreaterThan(0);
		const found = files.flatMap((f) =>
			motionViolations(readFileSync(f, "utf8")).map((v) => `${path.basename(f)}: ${v}`),
		);
		expect(found, FIX).toEqual([]);
	});

	it("catches a component that times its own animation", () => {
		expect(timingViolations(`animate(el, 0, { duration: 0.3, ease: [0, 0, 1, 1] })`)).toEqual(["duration:", "ease:"]);
		expect(timingViolations(`transition={{ delay: stagger(0.01) }}`)).toEqual(["delay:", "stagger("]);
		expect(timingViolations(`transition={mo.fold}`)).toEqual([]);
	});

	it(`components/releases never time motion themselves: ${OWN}`, () => {
		const found = sources(COMPONENTS)
			.filter((f) => f.endsWith(".tsx"))
			.flatMap((f) => timingViolations(readFileSync(f, "utf8")).map((v) => `${path.basename(f)}: ${v}`));
		expect(found, OWN).toEqual([]);
	});

	it("unfolds on height and opacity, appears and staggers labels on opacity only", () => {
		for (const s of ALL) {
			expect(keysOf(s.unfold)).toEqual(["height", "opacity"]);
			expect(keysOf(s.appear)).toEqual(["opacity"]);
			for (const i of [0, 1, 4]) expect(keysOf(s.label(i)), `label(${i}) has no offset`).toEqual(["opacity"]);
			expect(keysOf(s.sheet)).toEqual(s.reduced ? ["opacity"] : ["opacity", "x"]);
		}
		expect(releaseMotion(false).unfold.animate.height).toBe("auto");
		expect(releaseMotion(false).unfold.exit).toEqual({ height: 0, opacity: 0 });
	});

	it("settles every effect within DESIGN.md's medium duration, the last label included", () => {
		const s = releaseMotion(false);
		for (const [name, p] of presences(s)) expect(seconds(p.transition), name).toBeLessThanOrEqual(DURATION.medium);
		for (const [name, t] of Object.entries({ fold: s.fold, row: s.row, axis: s.axis, momentum: s.momentum }))
			expect(seconds(t), name).toBeLessThanOrEqual(DURATION.medium);
		expect(s.label(4).transition.delay).toBeGreaterThan(0);
		expect(s.label(1).transition.delay).toBeLessThan(s.label(4).transition.delay ?? 0);
		expect(s.unfold.transition).toEqual(s.fold);
	});

	it("makes every effect instant under reduced motion, with no starting frame", () => {
		const s = releaseMotion(true);
		for (const [name, p] of presences(s)) {
			expect(p.initial, `${name} starts at its end state`).toBe(false);
			expect(seconds(p.transition), name).toBe(0);
			expect(seconds(p.exitTransition ?? p.transition), `${name} exit`).toBe(0);
		}
		for (const [name, t] of Object.entries({ fold: s.fold, row: s.row, axis: s.axis, momentum: s.momentum }))
			expect(seconds(t), name).toBe(0);
	});

	it("turns a presence into from-to keyframes over its own keys, and none when it starts at its end", () => {
		expect(enterKeyframes(releaseMotion(false).label(2))).toEqual({ opacity: [0, 1] });
		expect(enterKeyframes(releaseMotion(true).label(2))).toBeNull();
	});

	it("gives a flick momentum and a slow release none", () => {
		expect(momentumDistance(0.1)).toBe(0);
		expect(momentumDistance(1)).toBeGreaterThan(0);
		expect(momentumDistance(-1)).toBeLessThan(0);
	});

	it("moves rows when apps, Only me, hotfixes or a layer change, and blocks also on a fold, never on focus or a refetch", () => {
		const url = parseReleasesSearch({});
		const folds = new Set(["di:21-2026"]);
		const base = moveKeys(url, DEFAULT_LAYERS, folds);
		expect(moveKeys(parseReleasesSearch({}), DEFAULT_LAYERS, new Set(folds))).toEqual(base);
		expect(moveKeys({ ...url, focus: "20-2026", sheet: { kind: "key" } }, DEFAULT_LAYERS, folds)).toEqual(base);
		const filters = [
			moveKeys({ ...url, apps: new Set(["di"]) }, DEFAULT_LAYERS, folds),
			moveKeys({ ...url, me: true }, DEFAULT_LAYERS, folds),
			moveKeys({ ...url, hotfixes: !url.hotfixes }, DEFAULT_LAYERS, folds),
			moveKeys(url, { ...DEFAULT_LAYERS, sprint: true }, folds),
		];
		for (const k of filters) {
			expect(k.rows).not.toBe(base.rows);
			expect(k.blocks).not.toBe(base.blocks);
		}
		expect(new Set(filters.map((k) => k.rows)).size).toBe(filters.length);
		for (const k of [
			moveKeys(url, DEFAULT_LAYERS, new Set([...folds, "di:llbs"])),
			moveKeys(url, DEFAULT_LAYERS, new Set()),
		]) {
			expect(k.rows, "a fold grows its own row, it does not FLIP the rows").toBe(base.rows);
			expect(k.blocks).not.toBe(base.blocks);
		}
		expect(moveKeys({ ...url, apps: new Set(["da", "di"]) }, DEFAULT_LAYERS, folds)).toEqual(
			moveKeys({ ...url, apps: new Set(["di", "da"]) }, DEFAULT_LAYERS, folds),
		);
	});
});
