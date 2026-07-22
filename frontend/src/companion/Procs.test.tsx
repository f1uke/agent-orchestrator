import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { WALK_CYCLE_MS } from "./behaviour";
import { PROCS_BODY_SHADE, PROCS_INK, PROCS_RIM_PX } from "./palette";
import { Procs } from "./Procs";

describe("Procs", () => {
	it("names itself for assistive tech instead of being an anonymous blob", () => {
		render(<Procs facing="front" walking={false} />);

		expect(screen.getByRole("img", { name: /proc/i })).toBeInTheDocument();
	});

	it("carries the ink rim as a real stroke in the art, not a CSS filter", () => {
		const { container } = render(<Procs facing="front" walking={false} />);
		const silhouette = container.querySelectorAll("[data-rim]");

		expect(silhouette.length).toBeGreaterThan(0);
		for (const shape of silhouette) {
			expect(shape.getAttribute("stroke")).toBe(PROCS_INK);
			expect(shape.getAttribute("stroke-width")).toBe(String(PROCS_RIM_PX));
		}
		expect(container.querySelector("svg")?.getAttribute("style") ?? "").not.toContain("filter");
	});

	it("runs the four-beat walk strip only while it is walking", () => {
		const { container: walking } = render(<Procs facing="right" walking />);
		const { container: standing } = render(<Procs facing="right" walking={false} />);

		const strip = walking.querySelector("[data-walk-strip]");
		expect(strip?.getAttribute("style")).toContain("steps(4, end)");
		expect(strip?.getAttribute("style")).toContain(`${WALK_CYCLE_MS}ms`);
		expect(standing.querySelector("[data-walk-strip]")?.getAttribute("style") ?? "").not.toContain("steps");
	});

	it("holds four genuinely different leg poses, so the strip is a walk and not a nudge", () => {
		const { container } = render(<Procs facing="right" walking />);
		const poses = [...container.querySelectorAll("[data-walk-pose]")].map((g) => g.innerHTML);

		expect(poses).toHaveLength(4);
		expect(new Set(poses).size).toBe(4);
	});

	it("never lets the two legs collapse onto each other in a frame", () => {
		// Caught by measuring the rendered strip: a ±6 swing about legs 12 apart put
		// BOTH legs on the same x in the third beat, so that frame drew one leg and a
		// walking Proc flickered a leg away four times a second. A front-facing sprite
		// must not cross its legs — this asserts the left leg stays left of the right.
		const { container } = render(<Procs facing="right" walking />);

		for (const pose of container.querySelectorAll("[data-walk-pose]")) {
			const legs = [...pose.querySelectorAll("rect")].map((r) => ({
				from: Number(r.getAttribute("x")),
				to: Number(r.getAttribute("x")) + Number(r.getAttribute("width")),
			}));

			expect(legs).toHaveLength(2);
			expect(legs[0].to).toBeLessThanOrEqual(legs[1].from);
		}
	});

	it("mirrors on X to turn around, and faces you head-on when summoned", () => {
		const { container: left } = render(<Procs facing="left" walking />);
		const { container: right } = render(<Procs facing="right" walking />);
		const { container: front } = render(<Procs facing="front" walking={false} />);

		expect(left.querySelector("svg")?.getAttribute("style")).toContain("scaleX(-1)");
		expect(right.querySelector("svg")?.getAttribute("style") ?? "").not.toContain("scaleX(-1)");
		expect(front.querySelector("svg")?.getAttribute("style") ?? "").not.toContain("scaleX(-1)");
	});

	it("gives the ears and the cord both channels, not ink alone", () => {
		// Caught by rendering: drawn in flat ink, the bracket ears and the cord
		// vanished completely on a dark wallpaper — the exact single-channel failure
		// the rim rule exists to prevent, on the two parts that carry the character's
		// identity. Each is now an ink casing with a body-coloured core on top.
		const { container } = render(<Procs facing="front" walking={false} />);

		for (const part of ["ear-left", "ear-right", "cord"]) {
			const casing = container.querySelector(`[data-casing="${part}"]`);
			const core = container.querySelector(`[data-core="${part}"]`);

			expect(casing?.getAttribute("stroke")).toBe(PROCS_INK);
			expect(core?.getAttribute("stroke")).toBe(PROCS_BODY_SHADE);
			expect(casing?.getAttribute("d")).toBe(core?.getAttribute("d"));
			expect(Number(casing?.getAttribute("stroke-width"))).toBeCloseTo(
				Number(core?.getAttribute("stroke-width")) + 2 * PROCS_RIM_PX,
				5,
			);
		}
	});

	it("keeps the cord on the right, where it cannot be confused with a held prop", () => {
		const { container } = render(<Procs facing="front" walking={false} />);
		const cord = container.querySelector('[data-core="cord"]');

		// Every point of the cord path lives right of the body's centre line (x=48),
		// because held props sit LEFT: the link and the task must not double-encode.
		const points = (cord?.getAttribute("d") ?? "").match(/-?\d+(\.\d+)? -?\d+(\.\d+)?/g) ?? [];
		expect(points.length).toBeGreaterThan(0);
		for (const point of points) {
			expect(Number(point.split(" ")[0])).toBeGreaterThan(48);
		}
	});

	it("keeps the blush inside the head silhouette", () => {
		// The clip is the safety net; this is the actual rule. Blush drawn out to the
		// head's edge gets sliced flat by the clip and reads as a smudge rather than a
		// cheek — the same defect the design hit, arriving from the other direction.
		const { container } = render(<Procs facing="front" walking={false} />);
		const head = container.querySelector('[data-rim][rx="26"]')!;
		const headX = Number(head.getAttribute("x"));
		const headY = Number(head.getAttribute("y"));
		const headW = Number(head.getAttribute("width"));
		const headH = Number(head.getAttribute("height"));
		const radius = Number(head.getAttribute("rx"));
		const blushes = [...container.querySelectorAll("[data-blush]")];

		expect(blushes.length).toBe(2);
		for (const blush of blushes) {
			expect(blush.getAttribute("clip-path")).toMatch(/procs-head/);
			const cx = Number(blush.getAttribute("cx"));
			const cy = Number(blush.getAttribute("cy"));
			const rx = Number(blush.getAttribute("rx"));
			// Half-width of the rounded head at the blush's height.
			const intoCorner = Math.max(0, Math.abs(cy - (headY + headH / 2)) - (headH / 2 - radius));
			const halfWidth = headW / 2 - (radius - Math.sqrt(Math.max(0, radius ** 2 - intoCorner ** 2)));
			const centre = headX + headW / 2;

			expect(cx - rx).toBeGreaterThan(centre - halfWidth);
			expect(cx + rx).toBeLessThan(centre + halfWidth);
		}
	});
});
