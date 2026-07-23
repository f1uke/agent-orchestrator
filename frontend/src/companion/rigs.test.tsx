import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { SessionStatus } from "../renderer/types/workspace";
import { composeCast, HATS, PALETTES } from "./cast";
import { PROCS_INK, PROCS_LIGHT, PROCS_RIM_PX, PROP_COLOURS } from "./palette";
import { Procs, PROCS_VIEW } from "./Procs";
import { ALL_COMPANION_STATUSES, ALL_CORDS, sceneFor } from "./scene";
import { LIMB_POSE, SPECIES, speciesById, speciesWears, type SpeciesId } from "./species";
import { blinkPhase, pivoted, tellGlow, tellOrigin } from "./rigs";

// What five new BODIES have to prove, and it is the same list the Proc had to: they
// read differently in every state, they carry both wallpaper channels, they say what
// the LINK is doing without contradicting the cord, and they keep every colour they
// draw somewhere something has measured it.

function renderSpecies(species: SpeciesId, status: SessionStatus = "working", hatIndex = 0, palette = 0) {
	return render(
		<Procs
			cast={composeCast(PALETTES[palette], HATS[hatIndex], species)}
			status={status}
			facing="front"
			walking={false}
		/>,
	);
}

/**
 * Every colour something already measures against a WALLPAPER: the cast's own fills and
 * hats (swept via ALL_LOOKS) plus every named prop colour (swept directly), both in
 * `palette.test.ts`, plus the ink that is the second channel itself.
 */
function sweptColours(): Set<string> {
	return new Set([
		...PALETTES.flatMap((palette) => [palette.body, palette.shade]),
		...HATS.flatMap((hat) => [hat.fill, hat.trim]),
		...Object.values(PROP_COLOURS),
		PROCS_INK,
	]);
}

/** Everything drawn, as a comparable string. Two states that produce the same one are one state. */
function drawn(container: HTMLElement): string {
	return container.querySelector("svg")?.innerHTML ?? "";
}

describe("six creatures, one set of machinery", () => {
	it("draws every creature in every state without falling over", () => {
		for (const species of SPECIES) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);

				expect(container.querySelector("svg"), `${species.id}/${status}`).not.toBeNull();
				unmount();
			}
		}
	});

	it("draws no two states alike, on any creature", () => {
		// The rule this whole art exists to keep. It was pinned for the scene table; it has
		// to hold for the drawing too, because a rig adds poses of its own on top of the
		// scene and could collapse a pair the scene kept apart.
		for (const species of SPECIES) {
			const seen = new Map<string, string>();
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);
				const markup = drawn(container);
				const clash = seen.get(markup);

				expect(clash, `${species.id}: ${status} is drawn exactly like ${clash}`).toBeUndefined();
				seen.set(markup, status);
				unmount();
			}
		}
	});

	it("gives every creature a silhouette of its own", () => {
		// Six bodies that rendered the same markup would be the complaint this whole
		// direction exists to answer: recolouring one body is not a second character.
		const shapes = new Set(
			SPECIES.map((species) => {
				const { container, unmount } = renderSpecies(species.id);
				const markup = drawn(container);
				unmount();
				return markup;
			}),
		);

		expect(shapes.size).toBe(SPECIES.length);
	});

	it("carries the ink rim on every silhouette shape of every creature", () => {
		for (const species of SPECIES) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);
				for (const shape of container.querySelectorAll("[data-rim]")) {
					expect(shape.getAttribute("stroke"), `${species.id}/${status}`).toBe(PROCS_INK);
					expect(shape.getAttribute("stroke-width"), `${species.id}/${status}`).toBe(String(PROCS_RIM_PX));
				}
				unmount();
			}
		}
	});

	it("writes every path in M/L/C/Z, so the mirror and the measuring tests can read it", () => {
		// `mirrorPathX` flips alternate numbers; an arc (7 params) or an H/V shorthand
		// silently breaks that pairing. The generated paths are the risk — the mouths and
		// the eyes are computed, not typed.
		for (const species of SPECIES) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);
				for (const path of container.querySelectorAll("path")) {
					const d = path.getAttribute("d") ?? "";

					expect(d.replace(/[-\d.,\s]/g, ""), `${species.id}/${status}: ${d}`).toMatch(/^[MLCZ]*$/);
				}
				unmount();
			}
		}
	});
});

describe("what each creature can wear", () => {
	it("puts a hat on the three with a head for one, and on nobody else", () => {
		// A picker that offered a hat to a toadstool would offer a choice with no effect,
		// which is worse than not offering it: the user picks, nothing changes, and the
		// feature looks broken. `speciesWears` is the data the library reads; this is the
		// drawing obeying it.
		for (const species of SPECIES) {
			const { container, unmount } = renderSpecies(species.id);
			const worn = container.querySelectorAll("[data-hat-piece]").length;

			expect(worn > 0, `${species.id}`).toBe(speciesWears(species.id, "hat"));
			unmount();
		}
	});

	it("wears every one of the six hats, on every creature that wears any", () => {
		for (const species of SPECIES.filter((entry) => entry.axes.includes("hat"))) {
			for (let hat = 0; hat < HATS.length; hat++) {
				const { container, unmount } = renderSpecies(species.id, "working", hat);

				expect(container.querySelectorAll("[data-hat-piece]").length, `${species.id}/${HATS[hat].id}`).toBe(
					HATS[hat].pieces.length,
				);
				unmount();
			}
		}
	});

	it("tints every creature with the colour it was given", () => {
		for (const species of SPECIES) {
			const a = renderSpecies(species.id, "working", 0, 0);
			const b = renderSpecies(species.id, "working", 0, 3);

			expect(drawn(a.container), species.id).not.toBe(drawn(b.container));
			a.unmount();
			b.unmount();
		}
	});
});

describe("how each creature gets about", () => {
	it("steps a leg strip only where there are legs, and only while it is moving", () => {
		for (const species of SPECIES) {
			const moving = render(
				<Procs cast={composeCast(PALETTES[0], HATS[0], species.id)} status="working" facing="front" walking />,
			);
			const still = renderSpecies(species.id);
			const walks = speciesById(species.id).locomotion === "walk";

			expect(Boolean(moving.container.querySelector("[data-walk-strip]")), species.id).toBe(walks);
			expect(still.container.querySelector("[data-walk-strip]")?.getAttribute("style") ?? "", species.id).not.toContain(
				"steps",
			);
			moving.unmount();
			still.unmount();
		}
	});

	it("sits the cat down when it stops, and stands it side-on when it walks", () => {
		// The whole answer to three failed cats: one figure cannot be both, because a
		// head-on face on a side-on body is the children's-drawing convention and reads
		// exactly like one. Two poses, one viewpoint each — and a cat that sits down when
		// it stops is what a cat does anyway.
		const sitting = renderSpecies("cat");
		const walking = render(
			<Procs cast={composeCast(PALETTES[0], HATS[0], "cat")} status="working" facing="front" walking />,
		);

		// Sitting is head-on: two eyes. Walking is a profile: one.
		expect(sitting.container.querySelectorAll("[data-eye]").length).toBe(2);
		expect(walking.container.querySelectorAll("[data-eye]").length).toBe(1);
		expect(drawn(sitting.container)).not.toBe(drawn(walking.container));
		sitting.unmount();
		walking.unmount();
	});

	it("moves the cat's lead to whichever end its tail is at", () => {
		// Sitting, the tail curls round its right side; walking, it turns side-on and the
		// tail is at the BACK, which is the other end of the animal. One anchor for both
		// would have the lead growing out of its face half the time.
		const sitting = renderSpecies("cat");
		const walking = render(
			<Procs cast={composeCast(PALETTES[0], HATS[0], "cat")} status="working" facing="front" walking />,
		);
		const from = (container: HTMLElement) =>
			(container.querySelector('[data-core="cord"]')?.getAttribute("d") ?? "").match(/^M(-?[\d.]+)/)?.[1];

		expect(Number(from(sitting.container))).toBeGreaterThan(60);
		expect(Number(from(walking.container))).toBeLessThan(30);
		sitting.unmount();
		walking.unmount();
	});

	it("runs each creature's own cycle when it sets off", () => {
		const expected: Record<string, string> = { walk: "procs-walk", float: "procs-float", hop: "procs-hop" };
		for (const species of SPECIES) {
			const { container, unmount } = render(
				<Procs cast={composeCast(PALETTES[0], HATS[0], species.id)} status="working" facing="front" walking />,
			);
			const styles = [...container.querySelectorAll("[style*='animation']")]
				.map((node) => node.getAttribute("style") ?? "")
				.join(" ");

			expect(styles, species.id).toContain(expected[speciesById(species.id).locomotion]);
			unmount();
		}
	});

	it("puts every pet at its own point in the blink cycle", () => {
		// ⚠ Measured on the real band, where eight pets shut their eyes together and it
		// read as the screen refreshing rather than as eight animals. They all mount at
		// the same instant, so the only thing that can separate them is the phase — and
		// React hands out SEQUENTIAL ids, which is exactly the input a weak hash fails on.
		// A plain rolling hash of `:r0:`…`:r7:` came out one millisecond apart.
		const cycle = 4300;
		const phases = Array.from({ length: 8 }, (_, i) => blinkPhase(`:r${i}:`, cycle)).sort((a, b) => a - b);
		const gaps = phases.slice(1).map((phase, i) => phase - phases[i]);

		expect(Math.min(...gaps), `phases: ${phases.join(", ")}`).toBeGreaterThan(cycle * 0.01);
		expect(Math.max(...phases) - Math.min(...phases)).toBeGreaterThan(cycle * 0.5);
	});

	it("gives one pet the same phase every time, so its eye can finish closing", () => {
		// Re-rolled per render, the animation restarts on every tick and the eye never
		// gets far enough down the keyframes to shut at all.
		expect(blinkPhase(":r4:", 4300)).toBe(blinkPhase(":r4:", 4300));
	});

	it("keeps a ghost hovering even when it is standing still", () => {
		// The one creature that animates at rest, and it has to: a ghost that stopped
		// bobbing would be a ghost resting on the floor. Its locomotion is its posture.
		const { container } = renderSpecies("ghost");
		const styles = [...container.querySelectorAll("[style*='animation']")]
			.map((node) => node.getAttribute("style") ?? "")
			.join(" ");

		expect(styles).toContain("procs-float");
	});
});

describe("the tell, as drawn", () => {
	it("reports the cord the scene actually has, never a second opinion", () => {
		for (const species of SPECIES.filter((entry) => entry.id !== "proc")) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);
				const tell = container.querySelector('[data-slot="tell"]');

				expect(tell, `${species.id}/${status}`).not.toBeNull();
				expect(tell?.getAttribute("data-cord"), `${species.id}/${status}`).toBe(sceneFor(status).cord);
				unmount();
			}
		}
	});

	it("starts each creature's lead where its own body says it does", () => {
		for (const species of SPECIES) {
			const { container, unmount } = renderSpecies(species.id);
			const d = container.querySelector('[data-core="cord"]')?.getAttribute("d") ?? "";

			expect(d, species.id).toMatch(new RegExp(`^M${species.cordFrom[0]} ${species.cordFrom[1]}\\b`));
			unmount();
		}
	});

	it("leaves the Proc's link to the cord alone", () => {
		const { container } = renderSpecies("proc");

		expect(container.querySelector('[data-slot="tell"]')).toBeNull();
	});

	it("never animates a node that a transform is positioning", () => {
		// A CSS transform keyframe REPLACES an element's SVG transform attribute rather
		// than composing with it, so a posed ear animated on the same node would snap to
		// the origin the moment the swing started.
		for (const species of SPECIES) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);
				for (const node of container.querySelectorAll("[style*='animation']")) {
					const style = node.getAttribute("style") ?? "";
					if (/procs-(swing|lamp|hop|float|bob|walk|tug|zzz|spark|confetti)/.test(style)) {
						expect(node.getAttribute("transform"), `${species.id}/${status}`).toBeNull();
					}
				}
				unmount();
			}
		}
	});

	it("turns a tell about its own root, not about the corner of the drawn frame", () => {
		// An SVG element's transform-box is `view-box`, so a CSS transform-origin is
		// measured from the VIEW BOX's corner — which is at (-8, -24) here. Written as the
		// raw rig coordinate, every swing turns about a point 25 units up and left of the
		// part's base and it pumps instead of pivoting.
		expect(tellOrigin([30, 19.5])).toBe(`${30 - PROCS_VIEW.x}px ${19.5 - PROCS_VIEW.y}px`);
		expect(tellOrigin([0, 0])).toBe("8px 24px");
	});

	it("keeps every swung part inside the drawn frame, in every state", () => {
		// The frame is CLIPPED — `overflow: hidden`, so an attached cord ends in a clean
		// cut instead of trailing a diagonal across the pet next door. That makes this a
		// silent failure: a cat's ear perked a few degrees too far is a cat with a flat
		// head, and nothing anywhere reports it.
		const margin = PROCS_RIM_PX / 2 + 2;
		for (const species of SPECIES.filter((entry) => entry.id !== "proc")) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);
				const tell = container.querySelector('[data-slot="tell"]');
				// The SWUNG parts only. A glowing tell has no pose to apply and its "out"
				// slash is drawn in place, so running a limb rotation over it measures a
				// shape that never rotates and fails on a body that never moves.
				for (const swung of tell?.querySelectorAll("[data-ear], [data-sleeve], [data-crest]") ?? []) {
					for (const [x, y] of posedPoints(swung, sceneFor(status).cord)) {
						expect(y - margin, `${species.id}/${status}: through the top`).toBeGreaterThan(PROCS_VIEW.y);
						expect(x - margin, `${species.id}/${status}: through the left`).toBeGreaterThan(PROCS_VIEW.x);
					}
				}
				unmount();
			}
		}
	});
});

/**
 * Every point of a swung part, with its pose applied.
 *
 * Read from the DOM rather than from a hard-coded copy of the shape, deliberately: a
 * containment test that carried its own idea of where the ear is would pass for ever
 * after somebody redrew it, which is worse than having no test at all.
 */
function posedPoints(node: Element, cord: (typeof ALL_CORDS)[number]): Array<[number, number]> {
	const pose = LIMB_POSE[cord];
	const numbers = (node.getAttribute("d") ?? "").match(/-?\d+(\.\d+)?/g)?.map(Number) ?? [];
	const radians = (pose.angle * Math.PI) / 180;
	const out: Array<[number, number]> = [];
	// The pivot is the closest thing the test can know without duplicating the rig: read
	// it back off the transform the rig actually wrote.
	const written = node.closest("g[transform]")?.getAttribute("transform") ?? "";
	const root = written.match(/translate\((-?[\d.]+) (-?[\d.]+)\)/);
	const [px, py] = root ? [Number(root[1]), Number(root[2])] : [0, 0];
	for (let i = 0; i + 1 < numbers.length; i += 2) {
		const dx = numbers[i] - px;
		const dy = numbers[i + 1] - py;
		out.push([
			px + (dx * Math.cos(radians) - dy * Math.sin(radians)) * pose.scale,
			py + (dx * Math.sin(radians) + dy * Math.cos(radians)) * pose.scale,
		]);
	}
	return out;
}

describe("colour, everywhere these bodies put it", () => {
	it("exposes no colour to the wallpaper that has never been measured against one", () => {
		// ⚠ The gap @agent-orchestrator-159 found on the previous attempt: the wallpaper
		// sweep in `palette.test.ts` enumerates ALL_LOOKS, and ALL_LOOKS is Procs. A new
		// body that invented a fill of its own would face the desktop having never been
		// measured against one, and nothing would have said so.
		//
		// EVERY fill and stroke, not just the rimmed ones — checking only `[data-rim]`
		// misses anything that sits inside another shape and carries no rim of its own,
		// which is most of a face. The net is total and the allowed set is spelled out.
		const allowed = new Set([
			...sweptColours(),
			// Sits on the creature, never on the desktop, and is measured against what it
			// sits on instead — see the placement test below, which is what makes that
			// claim true rather than assumed.
			...PALETTES.map((palette) => palette.blush),
			...ALL_CORDS.map(tellGlow),
			PROCS_LIGHT,
			"none",
		]);

		for (const species of SPECIES) {
			for (const palette of PALETTES.keys()) {
				for (const status of ALL_COMPANION_STATUSES) {
					const { container, unmount } = renderSpecies(species.id, status, 0, palette);
					for (const node of container.querySelectorAll("svg *")) {
						for (const attribute of ["fill", "stroke"] as const) {
							const colour = node.getAttribute(attribute);

							if (colour) expect(allowed, `${species.id}/${status} ${attribute}`).toContain(colour);
						}
					}
					unmount();
				}
			}
		}
	});

	it("only lets an on-body colour be used where something proves it stays on the body", () => {
		// The other half, and the one an allowed SET cannot give: blush and glow are
		// exempt from the wallpaper sweep on the claim that they sit on the creature. A
		// body that drew a blush-coloured tail tip past its own silhouette would still
		// pass the test above while facing the desktop with an unmeasured colour.
		//
		// So each on-body colour gets a closed list of places it may appear. A new shape
		// reaching for one fails here until somebody writes down why it is safe.
		const swept = sweptColours();
		const onBody = new Map<string, string>();
		for (const palette of PALETTES) onBody.set(palette.blush, "blush");
		for (const cord of ALL_CORDS) onBody.set(tellGlow(cord), "glow");
		// A glow at full and at zero lands exactly on `spark` and `quiet`, which are swept
		// prop colours the scenery uses everywhere. Restricting those would ban the dust
		// from being dust.
		for (const colour of swept) onBody.delete(colour);
		const PERMITTED: Record<string, string> = {
			blush: "[data-blush], [data-ear-lining]",
			glow: "[data-nucleus], [data-spot]",
		};

		for (const species of SPECIES) {
			for (const palette of PALETTES.keys()) {
				const { container, unmount } = renderSpecies(species.id, "working", 0, palette);
				for (const node of container.querySelectorAll("svg *")) {
					for (const attribute of ["fill", "stroke"] as const) {
						const kind = onBody.get(node.getAttribute(attribute) ?? "");

						if (kind) {
							expect(node.matches(PERMITTED[kind]), `${species.id}: stray ${kind} on <${node.tagName}>`).toBe(true);
						}
					}
				}
				unmount();
			}
		}
	});

	it("proves each of those places contains what it draws", () => {
		// Blush is held by a clip path — structural, so it holds wherever the cheek moves.
		for (const species of SPECIES) {
			const { container, unmount } = renderSpecies(species.id);
			for (const tick of container.querySelectorAll("[data-blush]")) {
				expect(tick.closest("g[clip-path]"), `${species.id}: blush outside a clip`).not.toBeNull();
			}
			unmount();
		}

		// A cat's ear LINING is the one that leaves the head, so it is measured against the
		// ear: every vertex of the lining inside the ear's own outline. Both poses, because
		// the cat has two ears drawn two different ways and only one of them is on screen
		// at a time.
		for (const pose of [false, true]) {
			const { container: cat, unmount: close } = render(
				<Procs cast={composeCast(PALETTES[0], HATS[0], "cat")} status="working" facing="front" walking={pose} />,
			);
			const lining = cat.querySelector("[data-ear-lining]")!;
			const outer = triangle(lining.parentElement!.querySelector("[data-ear]")!);
			for (const vertex of triangle(lining)) {
				expect(inside(vertex, outer), `${pose ? "walking" : "sitting"}: lining vertex ${vertex} outside the ear`).toBe(
					true,
				);
			}
			close();
		}

		// A glow sits inside the body it is mounted in.
		for (const [species, box] of [
			["slime", { left: 15, right: 81, top: 52, bottom: 116 }],
			["toadstool", { left: 4, right: 92, top: 12, bottom: 66 }],
		] as const) {
			const { container, unmount } = renderSpecies(species);
			for (const lit of container.querySelectorAll("[data-nucleus], [data-spot]")) {
				const cx = Number(lit.getAttribute("cx"));
				const cy = Number(lit.getAttribute("cy"));
				const rx = Number(lit.getAttribute("rx"));
				const ry = Number(lit.getAttribute("ry"));

				expect(cx - rx, species).toBeGreaterThanOrEqual(box.left);
				expect(cx + rx, species).toBeLessThanOrEqual(box.right);
				expect(cy - ry, species).toBeGreaterThanOrEqual(box.top);
				expect(cy + ry, species).toBeLessThanOrEqual(box.bottom);
			}
			unmount();
		}
	});
});

/** The three vertices of an M/L/L/Z triangle, in rig coordinates. */
function triangle(node: Element): Array<[number, number]> {
	const numbers = (node.getAttribute("d") ?? "").match(/-?\d+(\.\d+)?/g)!.map(Number);
	return [
		[numbers[0], numbers[1]],
		[numbers[2], numbers[3]],
		[numbers[4], numbers[5]],
	];
}

/** Point in triangle, by the sign of the three edge cross-products. */
function inside(point: [number, number], corners: Array<[number, number]>): boolean {
	const side = (a: [number, number], b: [number, number]) =>
		(b[0] - a[0]) * (point[1] - a[1]) - (b[1] - a[1]) * (point[0] - a[0]);
	const signs = [side(corners[0], corners[1]), side(corners[1], corners[2]), side(corners[2], corners[0])];
	return signs.every((value) => value >= 0) || signs.every((value) => value <= 0);
}

describe("pivoting", () => {
	it("turns and shrinks about the point given, leaving it where it was", () => {
		expect(pivoted([10, 20], 90, 1)).toContain("translate(10 20) rotate(90) scale(1) translate(-10 -20)");
	});
});
