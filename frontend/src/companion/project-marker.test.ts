import { describe, expect, it } from "vitest";
import { PROCS_INK, PROP_COLOURS, PROJECT_MARKER_COLOURS, contrastRatio, worstSeparation } from "./palette";
import { MARKER_SHAPES, markerForProject, markerPath } from "./project-marker";

const PROJECTS = ["agent-orchestrator", "demo-app", "demo-api", "finnomena-web", "starlight", "e-coupon"];

describe("the per-project marker", () => {
	// The human looked at a full overlay and could not tell which pet belonged to
	// which project. The look is assigned per SESSION, so it carried no project
	// signal at all; the only project information on screen was inside a hover card.
	it("always gives a project the same mark, so it can be learnt", () => {
		for (const project of PROJECTS) {
			const first = markerForProject(project);
			for (let i = 0; i < 20; i++) expect(markerForProject(project)).toEqual(first);
		}
	});

	it("gives different projects different marks", () => {
		const marks = PROJECTS.map((project) => markerForProject(project).id);

		expect(new Set(marks).size).toBe(PROJECTS.length);
	});

	it("tells apart project names that share a long prefix", () => {
		// Exactly the ones a person sees side by side on one machine, and exactly
		// where a weak hash fails.
		const family = ["agent-orchestrator", "agent-orchestrator-web", "agent-orchestrator-api"];
		const marks = family.map((project) => markerForProject(project).id);

		expect(new Set(marks).size).toBe(family.length);
	});

	it("ignores surrounding whitespace, so one project is not two marks", () => {
		expect(markerForProject(" demo-app ")).toEqual(markerForProject("demo-app"));
	});

	it("still gives a mark for a project with no name at all", () => {
		expect(MARKER_SHAPES).toContain(markerForProject("").shape);
	});

	it("varies SHAPE as well as colour, so it survives a greyscale screenshot", () => {
		// Colour alone is one channel: it fails on a greyscale capture, at 10px, and
		// for anyone with a colour-vision difference.
		const shapes = new Set<string>();
		const fills = new Set<string>();
		for (let i = 0; i < 400; i++) {
			const mark = markerForProject(`project-${i}`);
			shapes.add(mark.shape);
			fills.add(mark.fill);
		}

		expect(shapes.size).toBe(MARKER_SHAPES.length);
		expect(fills.size).toBe(PROJECT_MARKER_COLOURS.length);
	});

	it("picks shape and colour independently, so the two axes multiply", () => {
		const pairs = new Set<string>();
		for (let i = 0; i < 900; i++) pairs.add(markerForProject(`project-${i}`).id);

		expect(pairs.size).toBe(MARKER_SHAPES.length * PROJECT_MARKER_COLOURS.length);
	});

	it("draws every shape as an absolute path in the same 12-unit box", () => {
		for (const shape of MARKER_SHAPES) {
			const d = markerPath(shape);
			const numbers = (d.match(/-?\d+(\.\d+)?/g) ?? []).map(Number);

			expect(d, shape).toMatch(/^M/);
			expect(Math.min(...numbers), shape).toBeGreaterThanOrEqual(0);
			expect(Math.max(...numbers), shape).toBeLessThanOrEqual(12);
		}
	});
});

describe("the marker's colours", () => {
	it("reads on the worker's paper chip AND on the coordinator's gold one", () => {
		// The chip is paper for a worker and gold for the coordinator. A mark that
		// needs to be told which one it is on is a mark with a bug waiting in it.
		for (const colour of PROJECT_MARKER_COLOURS) {
			expect(contrastRatio(colour, PROP_COLOURS.paper), colour).toBeGreaterThanOrEqual(3);
			expect(contrastRatio(colour, PROP_COLOURS.lead), colour).toBeGreaterThanOrEqual(1.6);
		}
	});

	it("leaves facing the wallpaper to the CHIP, which is the thing that faces it", () => {
		// The marker is drawn inside the name chip, so the chip is what the desktop
		// sees. Sweeping the marker as well would force it into the same
		// mid-luminance band the Procs live in — where it measures under 2:1 against
		// the chip's own near-white fill, i.e. a mark you cannot see, in the name of a
		// wallpaper it never touches.
		expect(worstSeparation(PROP_COLOURS.paper)).toBeGreaterThanOrEqual(3);
		expect(worstSeparation(PROP_COLOURS.lead)).toBeGreaterThanOrEqual(3);
	});

	it("is never mistakable for the coordinator's gold, which answers a different question", () => {
		// The crown says "this one coordinates". The marker says "this one is on that
		// project". A gold-ish marker beside a gold crown reads as a second crown.
		for (const colour of PROJECT_MARKER_COLOURS) {
			expect(contrastRatio(colour, PROP_COLOURS.lead), colour).toBeGreaterThanOrEqual(1.6);
		}
	});

	it("keeps every marker colour distinguishable from the others", () => {
		for (const a of PROJECT_MARKER_COLOURS) {
			for (const b of PROJECT_MARKER_COLOURS) {
				if (a === b) continue;
				expect(distance(a, b), `${a} vs ${b}`).toBeGreaterThan(80);
			}
		}
	});

	it("carries the ink rim's contrast, because that is the marker's second channel", () => {
		for (const colour of PROJECT_MARKER_COLOURS) {
			expect(contrastRatio(PROCS_INK, colour), colour).toBeGreaterThanOrEqual(2.5);
		}
	});
});

/** Plain RGB distance. Enough to catch two colours nobody could tell apart. */
function distance(a: string, b: string): number {
	const rgb = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
	const [ar, ag, ab] = rgb(a);
	const [br, bg, bb] = rgb(b);
	return Math.hypot(ar - br, ag - bg, ab - bb);
}
