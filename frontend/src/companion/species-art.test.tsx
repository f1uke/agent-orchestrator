import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { composeCast, HATS, PALETTES } from "./cast";
import { PROCS_INK, PROCS_LIGHT, PROCS_RIM_PX, PROP_COLOURS } from "./palette";
import { Procs, PROCS_VIEW } from "./Procs";
import { ALL_COMPANION_STATUSES, ALL_CORDS, sceneFor } from "./scene";
import type { SessionStatus } from "../renderer/types/workspace";
import { EAR_POSE, IRIS_BY_PALETTE, SPECIES, type SpeciesId } from "./species";
import { earTip, lampColour, SPECIES_ART, tellOrigin } from "./species-art";

// What the three new bodies have to prove, and it is the same list the Proc had to:
// they read differently in every state, they carry both wallpaper channels, they fit
// the hats, and they say what the LINK is doing without contradicting the cord.

function renderSpecies(species: SpeciesId, status: SessionStatus = "working", hatIndex = 0) {
	return render(
		<Procs cast={composeCast(PALETTES[0], HATS[hatIndex], species)} status={status} facing="front" walking={false} />,
	);
}

/**
 * Every colour something already measures against a WALLPAPER: the cast's own fills
 * and hats (swept via ALL_LOOKS) plus every named prop colour (swept directly), both
 * in `palette.test.ts`, plus the ink that is the second channel itself.
 */
function sweptColours(): Set<string> {
	return new Set([
		...PALETTES.flatMap((palette) => [palette.body, palette.shade]),
		...HATS.flatMap((hat) => [hat.fill, hat.trim]),
		...Object.values(PROP_COLOURS),
		PROCS_INK,
	]);
}

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

/** Everything drawn, as a comparable string. Two states that produce the same one are one state. */
function drawn(container: HTMLElement): string {
	return container.querySelector("svg")?.innerHTML ?? "";
}

describe("every character on the one rig", () => {
	it("keeps the same head box on every character, so all six hats still fit", () => {
		// The hats are authored in rig coordinates against ONE head: x 14…82, top y 6.
		// A species that moved any of those would wear all six of them wrong, and the
		// six hats are 6× the cast. Corner radius is the only part it may have.
		for (const species of SPECIES) {
			const head = SPECIES_ART[species.id].head;

			expect(head.x, species.id).toBe(14);
			expect(head.y, species.id).toBe(6);
			expect(head.width, species.id).toBe(68);
			expect(head.height, species.id).toBe(72);
		}
	});

	it("gives each character its own head silhouette", () => {
		const radii = SPECIES.map((species) => SPECIES_ART[species.id].head.rx);

		expect(new Set(radii).size).toBe(SPECIES.length);
	});

	it("draws the hat over the head and the species' own crown over the hat", () => {
		// Order is the whole reason ears work at all: a hat drawn last would swallow
		// them, and a Kitsu that is only a Kitsu with its hat off is not a character.
		const { container } = renderSpecies("kitsu");
		const nodes = [...container.querySelectorAll("*")];
		const head = nodes.indexOf(container.querySelector('[data-part="head"]')!);
		const hat = nodes.indexOf(container.querySelector("[data-hat-piece]")!);
		const ear = nodes.indexOf(container.querySelector("[data-ear]")!);

		expect(head).toBeLessThan(hat);
		expect(hat).toBeLessThan(ear);
	});

	it("wears every hat on every character without falling over", () => {
		for (const species of SPECIES) {
			for (let hat = 0; hat < HATS.length; hat++) {
				const { container, unmount } = renderSpecies(species.id, "working", hat);

				expect(container.querySelectorAll("[data-hat-piece]").length, `${species.id}/${HATS[hat].id}`).toBe(
					HATS[hat].pieces.length,
				);
				unmount();
			}
		}
	});

	it("carries the ink rim on every silhouette shape of every character", () => {
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

	it("exposes no colour to the wallpaper that has never been measured against one", () => {
		// ⚠ The gap this closes, raised by @agent-orchestrator-159 while merging: the
		// wallpaper sweep in `palette.test.ts` enumerates ALL_LOOKS and PROP_COLOURS, and
		// ALL_LOOKS is Procs. A new body that invented a fill of its own would face the
		// desktop having never been measured against one, and nothing would have said so.
		//
		// Rather than widen the sweep to 4× the looks, this asserts the stronger thing:
		// every colour these bodies put OUTSIDE themselves is already one of the colours
		// that sweep covers. Reach for a new one and this fails until it is named in
		// PROP_COLOURS, which is what puts it in the sweep.
		//
		// EVERY fill and stroke, not just the rimmed ones — a first pass only checked
		// `[data-rim]` shapes, and a deliberately-wrong ear lining sailed straight
		// through it because the lining sits inside the ear and carries no rim of its
		// own. The point is to catch a hand-typed hex ANYWHERE, so the net is total and
		// the allowed set is spelled out instead.
		const allowed = new Set([
			// Faces the wallpaper, and is swept against every one of them.
			...sweptColours(),
			// Sits on the character, never on the desktop, and is measured against what
			// it sits on instead: blush and iris in `species.test.ts`, and the lamp both
			// there and by the geometry tests below.
			...PALETTES.map((palette) => palette.blush),
			...Object.values(IRIS_BY_PALETTE),
			...ALL_CORDS.map(lampColour),
			PROCS_LIGHT,
			"none",
		]);

		for (const species of SPECIES) {
			for (const palette of PALETTES) {
				for (const status of ALL_COMPANION_STATUSES) {
					const { container, unmount } = render(
						<Procs cast={composeCast(palette, HATS[0], species.id)} status={status} facing="front" walking={false} />,
					);
					for (const node of container.querySelectorAll("svg *")) {
						for (const attribute of ["fill", "stroke"] as const) {
							const colour = node.getAttribute(attribute);

							if (colour) expect(allowed, `${species.id}/${palette.id}/${status} ${attribute}`).toContain(colour);
						}
					}
					unmount();
				}
			}
		}
	});

	it("only lets an on-body colour be used where something proves it stays on the body", () => {
		// ⚠ The hole @agent-orchestrator-159 found in the test above: its allowed set is
		// "colours that may be used ANYWHERE", not "colours that may be used HERE". Blush,
		// iris and lamp are exempt from the wallpaper sweep on the claim that they sit on
		// the character — and if a future body drew a blush-coloured tail tip past the
		// silhouette, the sweep test would still pass on a colour that genuinely faces the
		// desktop and has never been measured against one.
		//
		// It is not hypothetical: the ear LINING is blush, and an ear sticks out past the
		// head. What contains it is the ear's own rimmed outline, and until this test that
		// was an argument rather than a measurement.
		//
		// So each on-body colour gets a closed list of places it may appear, and each
		// place carries its own containment proof. A new shape reaching for one of these
		// colours fails here until somebody writes down why it is safe.
		// Only the colours that are ON THE BODY AND NOWHERE ELSE need placing. The lamp
		// is a blend, and at full glow and at zero it lands exactly on `spark` and
		// `quiet` — both swept prop colours used all over the scenery, so restricting
		// them here would ban the dust and the quiet dots from being themselves.
		const swept = sweptColours();
		const onBody = new Map<string, string>();
		for (const palette of PALETTES) onBody.set(palette.blush, "blush");
		for (const iris of Object.values(IRIS_BY_PALETTE)) onBody.set(iris, "iris");
		for (const cord of ALL_CORDS) onBody.set(lampColour(cord), "lamp");
		for (const colour of swept) onBody.delete(colour);
		const PERMITTED: Record<string, string> = {
			blush: "[data-blush], [data-ear-lining]",
			iris: "[data-anime-eye] *",
			lamp: "[data-core-lamp]",
		};

		for (const species of SPECIES) {
			for (const palette of PALETTES) {
				const { container, unmount } = render(
					<Procs cast={composeCast(palette, HATS[0], species.id)} status="working" facing="front" walking={false} />,
				);
				for (const node of container.querySelectorAll("svg *")) {
					for (const attribute of ["fill", "stroke"] as const) {
						const kind = onBody.get(node.getAttribute(attribute) ?? "");

						if (kind)
							expect(node.matches(PERMITTED[kind]), `${species.id}: stray ${kind} on <${node.tagName}>`).toBe(true);
					}
				}
				unmount();
			}
		}
	});

	it("proves each of those places actually contains what it draws", () => {
		// The blush ticks are held by the head's clip path — structural, not positional.
		const { container: kitsu, unmount } = renderSpecies("kitsu");

		for (const tick of kitsu.querySelectorAll("[data-blush]")) {
			expect(tick.closest("g[clip-path]"), "a blush tick outside the head clip").not.toBeNull();
		}

		// The ear LINING is the one that leaves the head, so it is measured against the
		// ear rather than the head: every vertex of the lining inside the ear's outline.
		const outer = triangle(kitsu.querySelector("[data-ear]")!);
		for (const vertex of triangle(kitsu.querySelector("[data-ear-lining]")!)) {
			expect(inside(vertex, outer), `ear lining vertex ${vertex} outside the ear`).toBe(true);
		}
		unmount();

		// The iris and its highlights never leave the head box the hats were cut for.
		for (const species of ["kitsu", "sprite", "unit"] as SpeciesId[]) {
			const { container, unmount: close } = renderSpecies(species);
			const head = SPECIES_ART[species].head;
			for (const part of container.querySelectorAll("[data-anime-eye] *")) {
				const cx = Number(part.getAttribute("cx"));
				const cy = Number(part.getAttribute("cy"));
				const rx = Number(part.getAttribute("rx") ?? part.getAttribute("r") ?? 0);
				const ry = Number(part.getAttribute("ry") ?? part.getAttribute("r") ?? 0);

				if (!Number.isFinite(cx) || !part.hasAttribute("cx")) continue;
				expect(cx - rx, `${species} eye`).toBeGreaterThanOrEqual(head.x);
				expect(cx + rx, `${species} eye`).toBeLessThanOrEqual(head.x + head.width);
				expect(cy - ry, `${species} eye`).toBeGreaterThanOrEqual(head.y);
				expect(cy + ry, `${species} eye`).toBeLessThanOrEqual(head.y + head.height);
			}
			close();
		}
	});

	it("keeps the Unit's lamp off the wallpaper entirely, and lets it be measured on the body", () => {
		// The one colour here that is NOT swept, deliberately: the lamp is a mix, and it
		// sits inside an ink bezel inside the body rect, so what it faces is the body —
		// the same argument that keeps the blush and the project marker out of the sweep.
		// This pins the geometry that argument rests on.
		const body = { left: 29, right: 67, top: 74, bottom: 104 };
		const { container } = renderSpecies("unit");
		const bezel = container.querySelector("[data-core-bezel]")!;
		const numbers = (bezel.getAttribute("d") ?? "").match(/-?\d+(\.\d+)?/g)!.map(Number);
		const xs = numbers.filter((_, index) => index % 2 === 0);
		const ys = numbers.filter((_, index) => index % 2 === 1);

		expect(Math.min(...xs)).toBeGreaterThanOrEqual(body.left);
		expect(Math.max(...xs)).toBeLessThanOrEqual(body.right);
		expect(Math.min(...ys)).toBeGreaterThanOrEqual(body.top);
		expect(Math.max(...ys)).toBeLessThanOrEqual(body.bottom);
	});

	it("writes every path in M/L/C/Z, so the mirror and the measuring tests can read it", () => {
		// `mirrorPathX` flips alternate numbers; an arc (7 params) or an H/V shorthand
		// silently breaks that pairing. Generated paths are the risk here — the wing
		// leaves and the cell lines are computed, not typed.
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

	it("renders all fifteen states on all four characters", () => {
		for (const species of SPECIES) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);

				expect(container.querySelector("svg"), `${species.id}/${status}`).not.toBeNull();
				unmount();
			}
		}
	});

	it("draws no two states alike, on any character", () => {
		// The rule this whole art exists to keep. It was pinned for the scene table;
		// it has to hold for the drawing too, because a species adds poses of its own
		// on top of the scene and could have collapsed a pair the scene kept apart.
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
});

describe("the tell, as drawn", () => {
	it("reports the cord the scene actually has, never a second opinion", () => {
		for (const species of ["kitsu", "sprite", "unit"] as SpeciesId[]) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species, status);
				const tell = container.querySelector('[data-slot="tell"]');

				expect(tell, `${species}/${status}`).not.toBeNull();
				expect(tell?.getAttribute("data-cord"), `${species}/${status}`).toBe(sceneFor(status).cord);
				unmount();
			}
		}
	});

	it("leaves the Proc's link to the cord alone", () => {
		const { container } = renderSpecies("proc");

		expect(container.querySelector('[data-slot="tell"]')).toBeNull();
	});

	it("never animates a node that a transform is positioning", () => {
		// A CSS transform keyframe REPLACES an element's SVG transform attribute rather
		// than composing with it, so a posed ear animated on the same node would snap
		// to the origin the moment the swing started.
		for (const species of SPECIES) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);
				for (const node of container.querySelectorAll("[style*='animation']")) {
					const style = node.getAttribute("style") ?? "";
					if (/procs-(swing|lamp|bob|walk|tug|zzz|spark|confetti)/.test(style)) {
						expect(node.getAttribute("transform"), `${species.id}/${status}`).toBeNull();
					}
				}
				unmount();
			}
		}
	});

	it("turns a tell about its own root, not about the corner of the drawn frame", () => {
		// An SVG element's transform-box is `view-box`, so a CSS transform-origin is
		// measured from the VIEW BOX's corner — which is at (-8, -24) here. Written as
		// the raw rig coordinate, every swing turned about a point 25 units up and left
		// of the ear's base, and the ears pumped instead of pivoting.
		expect(tellOrigin([30, 19.5])).toBe(`${30 - PROCS_VIEW.x}px ${19.5 - PROCS_VIEW.y}px`);
		expect(tellOrigin([0, 0])).toBe("8px 24px");
	});

	it("keeps a perked ear inside the drawn frame, in every state", () => {
		// The frame is CLIPPED — `overflow: hidden`, so an attached cord ends in a clean
		// cut instead of trailing a diagonal across the Proc next door. That makes this
		// a silent failure mode: an ear perked a few degrees too far is a character with
		// a flat top, and nothing anywhere reports it. `tugging` is the tightest pose,
		// at ~2 units of headroom.
		// Two units of daylight, not zero: "exactly touching the edge" is a clip on the
		// next tweak, and the rig is redrawn by hand.
		const margin = PROCS_RIM_PX / 2 + 2;
		for (const [cord, pose] of Object.entries(EAR_POSE)) {
			const tip = earTip(pose);

			expect(tip.y - margin, `${cord}: ear tip through the top of the frame`).toBeGreaterThan(PROCS_VIEW.y);
			expect(tip.x - margin, `${cord}: ear tip through the left of the frame`).toBeGreaterThan(PROCS_VIEW.x);
		}
	});

	it("never depends on an animation to make part of a character visible", () => {
		// Reduced motion switches every animation off. A pose whose base style hid it
		// would silently vanish for the group least able to notice it had.
		for (const species of SPECIES) {
			for (const status of ALL_COMPANION_STATUSES) {
				const { container, unmount } = renderSpecies(species.id, status);
				for (const node of container.querySelectorAll("[data-slot] *, [data-slot]")) {
					const opacity = (node as SVGElement).style?.opacity;

					if (opacity) expect(Number(opacity), `${species.id}/${status}`).toBeGreaterThan(0);
				}
				unmount();
			}
		}
	});
});
