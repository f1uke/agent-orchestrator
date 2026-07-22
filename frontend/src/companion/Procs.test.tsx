import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { WALK_CYCLE_MS } from "./behaviour";
import { CAST, castForSession } from "./cast";
import { PROCS_INK, PROCS_RIM_PX } from "./palette";
import { ALL_COMPANION_STATUSES, sceneFor } from "./scene";
import { Procs } from "./Procs";

const CURLY = CAST[0];

function renderProcs(overrides: Partial<React.ComponentProps<typeof Procs>> = {}) {
	return render(<Procs cast={CURLY} status="pr_open" facing="front" walking={false} {...overrides} />);
}

/** Horizontal extent of everything drawn inside an element, in rig coordinates. */
function extentX(root: Element): { min: number; max: number } {
	let min = Number.POSITIVE_INFINITY;
	let max = Number.NEGATIVE_INFINITY;
	const see = (from: number, to: number) => {
		min = Math.min(min, from);
		max = Math.max(max, to);
	};
	for (const node of [root, ...root.querySelectorAll("*")]) {
		const num = (name: string) => Number(node.getAttribute(name));
		if (node.tagName === "rect") see(num("x"), num("x") + num("width"));
		if (node.tagName === "circle") see(num("cx") - num("r"), num("cx") + num("r"));
		if (node.tagName === "ellipse") see(num("cx") - num("rx"), num("cx") + num("rx"));
		if (node.tagName === "path") {
			const numbers = (node.getAttribute("d") ?? "").match(/-?\d+(\.\d+)?/g)?.map(Number) ?? [];
			for (let i = 0; i < numbers.length; i += 2) see(numbers[i], numbers[i]);
		}
	}
	return { min, max };
}

describe("the character", () => {
	it("says who it is and what it is doing, for assistive tech and for tests", () => {
		renderProcs({ cast: CAST[2], status: "ci_failed" });

		expect(screen.getByRole("img", { name: /brack/i })).toHaveAttribute("aria-label", expect.stringMatching(/ci/i));
	});

	it("wears its own character's hat", () => {
		for (const member of CAST) {
			const { container, unmount } = renderProcs({ cast: member });
			const worn = [...container.querySelectorAll("[data-hat-piece]")].map((p) => p.getAttribute("d"));

			expect(worn, member.name).toEqual(member.hat.map((piece) => piece.d));
			unmount();
		}
	});

	it("draws the hat over the head, so no Proc is bald", () => {
		const { container } = renderProcs();
		const nodes = [...container.querySelectorAll("*")];
		const head = nodes.indexOf(container.querySelector('[data-part="head"]')!);
		const hat = nodes.indexOf(container.querySelector("[data-hat-piece]")!);

		expect(hat).toBeGreaterThan(head);
	});

	it("wears its own character's colour", () => {
		const { container } = renderProcs({ cast: CAST[3] });
		const head = container.querySelector('[data-part="head"]');

		expect(head?.getAttribute("fill")).toBe(CAST[3].body);
	});

	it("looks different from the next character in both silhouette and colour", () => {
		const a = renderProcs({ cast: CAST[0] });
		const b = renderProcs({ cast: CAST[1] });

		const ear = (c: HTMLElement) => c.querySelector("[data-hat-piece]")?.getAttribute("d");
		const head = (c: HTMLElement) => c.querySelector('[data-part="head"]')?.getAttribute("fill");
		expect(ear(a.container)).not.toBe(ear(b.container));
		expect(head(a.container)).not.toBe(head(b.container));
	});

	it("carries the ink rim on every silhouette shape, art not CSS filter", () => {
		const { container } = renderProcs({ status: "working" });
		const rimmed = container.querySelectorAll("[data-rim]");

		expect(rimmed.length).toBeGreaterThan(0);
		for (const shape of rimmed) {
			expect(shape.getAttribute("stroke")).toBe(PROCS_INK);
			expect(shape.getAttribute("stroke-width")).toBe(String(PROCS_RIM_PX));
		}
	});

	it("mirrors on X to turn around, and faces you head-on when summoned", () => {
		const { container: left } = renderProcs({ facing: "left", walking: true });
		const { container: front } = renderProcs({ facing: "front" });

		expect(left.querySelector("svg")?.getAttribute("style")).toContain("scaleX(-1)");
		expect(front.querySelector("svg")?.getAttribute("style") ?? "").not.toContain("scaleX(-1)");
	});

	it("runs the four-beat walk strip only while it is walking", () => {
		const { container: walking } = renderProcs({ walking: true });
		const { container: standing } = renderProcs({ walking: false });

		expect(walking.querySelector("[data-walk-strip]")?.getAttribute("style")).toContain("steps(4, end)");
		expect(walking.querySelector("[data-walk-strip]")?.getAttribute("style")).toContain(`${WALK_CYCLE_MS}ms`);
		expect(standing.querySelector("[data-walk-strip]")?.getAttribute("style") ?? "").not.toContain("steps");
	});

	it("never lets the two legs collapse onto each other in a frame", () => {
		const { container } = renderProcs({ walking: true });

		for (const pose of container.querySelectorAll("[data-walk-pose]")) {
			const legs = [...pose.querySelectorAll("rect")].map((r) => ({
				from: Number(r.getAttribute("x")),
				to: Number(r.getAttribute("x")) + Number(r.getAttribute("width")),
			}));

			expect(legs).toHaveLength(2);
			expect(legs[0].to).toBeLessThanOrEqual(legs[1].from);
		}
	});
});

describe("the scene", () => {
	it("draws a ground only for the states that have one", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			const { container, unmount } = renderProcs({ status });
			const ground = container.querySelector('[data-slot="ground"]');

			expect(Boolean(ground), status).toBe(sceneFor(status).ground !== "none");
			unmount();
		}
	});

	it("draws a held prop only for the states that hold one", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			const { container, unmount } = renderProcs({ status });

			expect(Boolean(container.querySelector('[data-slot="held"]')), status).toBe(sceneFor(status).held !== "none");
			unmount();
		}
	});

	it("draws an emit layer only for the states that emit", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			const { container, unmount } = renderProcs({ status });

			expect(Boolean(container.querySelector('[data-slot="emit"]')), status).toBe(sceneFor(status).emit !== "none");
			unmount();
		}
	});

	it("keeps the cord on the RIGHT and the held prop on the LEFT, so they cannot double-encode", () => {
		const { container } = renderProcs({ status: "needs_input" });
		const held = container.querySelector('[data-slot="held"]')!;
		const cord = container.querySelector('[data-core="cord"]')!;

		// x=48 is the figure's centre line.
		expect(extentX(held).max).toBeLessThan(48);
		expect(extentX(cord).min).toBeGreaterThan(48);
	});

	it("keeps the ground beside the Proc rather than across its face", () => {
		// The design hit this: a desk drawn behind a Proc crosses its face, because a
		// Proc is nearly all head. The ground lives clear of the figure's box.
		const { container } = renderProcs({ status: "working" });

		expect(extentX(container.querySelector('[data-slot="ground"]')!).min).toBeGreaterThan(67);
	});

	it("plugs the cord into the ground when the scene has one", () => {
		// This is what marries the two layers instead of letting them compete: the
		// desk is where it works AND what it is plugged into.
		const { container } = renderProcs({ status: "working" });

		expect(container.querySelector('[data-plug="ground"]')).not.toBeNull();
	});

	it("shows an unplugged cord as genuinely unplugged", () => {
		// Caught by rendering the contact sheet: `no_signal`, `terminated` and
		// `unknown` came out indistinguishable, because an "attached" cord with no
		// ground was drawn ending in a plug lying on the floor — which is precisely
		// what unplugged looks like. Three states, one picture, no information.
		const { container } = renderProcs({ status: "terminated" });
		const plug = container.querySelector('[data-plug="loose"]');

		expect(plug).not.toBeNull();
		// Lying on its side on the floor, not standing in a socket.
		expect(plug?.closest("g")?.getAttribute("transform")).toMatch(/rotate/);
	});

	it("runs an attached cord off to something, with no plug lying about", () => {
		const { container } = renderProcs({ status: "pr_open" });
		const cord = container.querySelector('[data-core="cord"]')!;
		const points = (cord.getAttribute("d") ?? "").match(/-?\d+(\.\d+)? -?\d+(\.\d+)?/g) ?? [];
		const lowest = Math.max(...points.map((p) => Number(p.split(" ")[1])));

		expect(container.querySelector("[data-plug]")).toBeNull();
		// It leaves through the FLOOR — the frame's bottom edge is y=132 — which is
		// what "attached to something off screen" looks like for a cable. Running it
		// level across the frame instead read as a long tail.
		expect(lowest).toBeGreaterThanOrEqual(132);
		// And it stays short: a lead, not a leash.
		expect(extentX(cord).max).toBeLessThan(96);
	});

	it("keeps the three quiet states telling themselves apart", () => {
		const looks = ["no_signal", "terminated", "unknown"].map((status) => {
			const { container, unmount } = renderProcs({ status: status as never });
			const look = [
				container.querySelector('[data-slot="emit"]')?.getAttribute("data-emit") ?? "none",
				container.querySelector('[data-slot="cord"]')?.getAttribute("data-cord"),
				container.querySelector("[data-plug]")?.getAttribute("data-plug") ?? "none",
			].join("/");
			unmount();
			return look;
		});

		expect(new Set(looks).size).toBe(3);
	});

	it("puts data pips on the cord only while the session is actually working", () => {
		const { container: working } = renderProcs({ status: "working" });
		const { container: open } = renderProcs({ status: "pr_open" });

		expect(working.querySelectorAll("[data-pip]").length).toBeGreaterThan(0);
		expect(open.querySelectorAll("[data-pip]").length).toBe(0);
	});

	it("gives every prop the ink rim too — a prop is on the wallpaper like the Proc is", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			const { container, unmount } = renderProcs({ status });
			for (const slot of ["ground", "held", "emit"]) {
				const group = container.querySelector(`[data-slot="${slot}"]`);
				if (!group) continue;
				const shapes = group.querySelectorAll("rect, circle, ellipse, path");
				const rimmed = group.querySelectorAll("[data-rim], [data-casing]");

				expect(shapes.length, `${status}/${slot}`).toBeGreaterThan(0);
				expect(rimmed.length, `${status}/${slot}`).toBeGreaterThan(0);
			}
			unmount();
		}
	});

	it("draws every path with absolute M/L/C only", () => {
		// Not style policing. `mirrorPathX` mirrors the ears by flipping alternate
		// numbers, and the same alternating read is how anything can measure where a
		// prop actually sits. An arc (7 params) or an H/V shorthand silently breaks
		// that pairing — which is exactly how a sign that lives at x≤30 measured as
		// reaching x=89 and hid a side-of-the-body check.
		for (const status of ALL_COMPANION_STATUSES) {
			const { container, unmount } = renderProcs({ status });
			for (const path of container.querySelectorAll("path")) {
				const d = path.getAttribute("d") ?? "";

				expect(d.replace(/[-\d.,\s]/g, ""), `${status}: ${d}`).toMatch(/^[MLCZ]*$/);
			}
			unmount();
		}
	});

	it("never puts a transform animation on the same node that a transform positions", () => {
		// A CSS `transform` keyframe REPLACES an element's SVG `transform` attribute
		// rather than composing with it, so a sparkle positioned by `translate(88 62)`
		// and animated by `transform: scale(…)` snaps to the origin the moment the
		// animation starts. Position and motion must live on separate groups.
		for (const status of ALL_COMPANION_STATUSES) {
			const { container, unmount } = renderProcs({ status });
			for (const node of container.querySelectorAll("[style*='animation']")) {
				const style = node.getAttribute("style") ?? "";
				const animatesTransform = /procs-(zzz|spark|confetti|tug|bob|walk)/.test(style);

				if (animatesTransform) expect(node.getAttribute("transform"), `${status}`).toBeNull();
			}
			unmount();
		}
	});

	it("never depends on an animation to make a prop visible", () => {
		// Reduced motion switches every animation off. Several scene keyframes START at
		// opacity 0 (zzz rising, confetti falling), which is fine — with the animation
		// gone the element falls back to its BASE style. But if a base style ever set
		// opacity to 0, that state would silently vanish for anyone who asked the OS to
		// reduce motion, which is the one group least able to notice it had.
		for (const status of ALL_COMPANION_STATUSES) {
			const { container, unmount } = renderProcs({ status });
			for (const node of container.querySelectorAll("[data-slot] *, [data-slot]")) {
				const opacity = (node as SVGElement).style?.opacity;

				if (opacity) expect(Number(opacity), `${status}`).toBeGreaterThan(0);
			}
			unmount();
		}
	});

	it("renders every one of the fifteen states without falling over", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			const { container, unmount } = renderProcs({ status });

			expect(container.querySelector("svg"), status).not.toBeNull();
			unmount();
		}
	});
});

describe("a roster of Procs", () => {
	it("is visibly varied — the all-identical look was the bug", () => {
		const refs = ["ao-1", "ao-2", "ao-3", "ao-4", "ao-5", "ao-6", "ao-7", "ao-8"];
		const looks = refs.map((ref) => {
			const member = castForSession(ref);
			return `${member.body}/${member.hat.map((piece) => piece.d).join("")}`;
		});

		expect(new Set(looks).size).toBeGreaterThanOrEqual(4);
	});
});
