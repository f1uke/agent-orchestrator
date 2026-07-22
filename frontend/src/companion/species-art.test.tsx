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
			...PALETTES.flatMap((palette) => [palette.body, palette.shade]),
			...HATS.flatMap((hat) => [hat.fill, hat.trim]),
			...Object.values(PROP_COLOURS),
			PROCS_INK,
			// Sits on the character, never on the desktop, and is measured against what
			// it sits on instead: blush and iris in `species.test.ts`, and the lamp both
			// there and by the geometry test below.
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
