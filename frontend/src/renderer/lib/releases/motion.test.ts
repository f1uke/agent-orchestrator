import { readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { DURATION, momentumDistance, sheetEnter } from "./motion";

const COMPONENTS = path.resolve(__dirname, "../../components/releases");
const FIX = "use Motion's x/y/scale values (transform: [.., 'none'] collapsed to scaleX(0) in d3)";

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
	const re = /\b(animate|initial|exit|keyframes)\s*[=:]\s*\{/g;
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

describe("releases motion", () => {
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

	it("keeps the sheet enter within DESIGN.md's medium duration, on Motion values", () => {
		expect(sheetEnter.transition.duration).toBeLessThanOrEqual(DURATION.medium);
		expect(Object.keys(sheetEnter.initial).sort()).toEqual(["opacity", "x"]);
	});

	it("gives a flick momentum and a slow release none", () => {
		expect(momentumDistance(0.1)).toBe(0);
		expect(momentumDistance(1)).toBeGreaterThan(0);
		expect(momentumDistance(-1)).toBeLessThan(0);
	});
});
