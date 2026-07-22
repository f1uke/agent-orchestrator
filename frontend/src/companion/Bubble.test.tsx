import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { BUBBLE_COARSE_TEXT, Bubble, looksLikeRawCommand } from "./Bubble";
import { PROCS_INK, PROCS_RIM_PX, PROP_COLOURS, contrastRatio, worstSeparation } from "./palette";

/** jsdom reports colours as `rgb(...)`, so compare like for like. */
function rgb(hex: string): string {
	const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
	return `rgb(${r}, ${g}, ${b})`;
}

describe("Bubble", () => {
	it("says the one line the agent wrote about itself", () => {
		render(<Bubble text="Running the test suite" />);

		expect(screen.getByText("Running the test suite")).toBeInTheDocument();
	});

	it("is a self-contained card, not app chrome", () => {
		// It floats on the user's wallpaper, so it cannot borrow a theme token for its
		// background any more than a Proc can. Fill carries dark wallpapers, the ink
		// rim carries light ones — the same two channels the pets use. Both live on
		// the OUTLINE, which is one path around the card and its tail together.
		const { container } = render(<Bubble text="Running the test suite" />);
		const outline = container.querySelector("[data-bubble-tail] path") as SVGPathElement;
		const card = container.querySelector("[data-bubble]") as HTMLElement;

		expect(outline.getAttribute("fill")).toBe(PROP_COLOURS.paper);
		expect(outline.getAttribute("stroke")).toBe(PROCS_INK);
		expect(outline.getAttribute("stroke-width")).toBe(String(PROCS_RIM_PX));
		expect(card.style.color).toBe(rgb(PROCS_INK));
	});

	it("draws the card and its tail as ONE path, so the two can never fail to meet", () => {
		// They used to be a CSS border and a separate SVG wedge with a paper-coloured
		// strip laid over the seam. It never joined cleanly and never could: a CSS
		// border and an SVG stroke round their sub-pixels differently.
		const { container } = render(<Bubble text="Running the test suite" />);

		expect(container.querySelectorAll("[data-bubble-tail] path")).toHaveLength(1);
		expect(container.querySelector("[data-bubble]")?.getAttribute("style")).toContain("transparent");
	});

	it("clears the wallpaper floor with both channels, like everything else on the desktop", () => {
		expect(worstSeparation(PROP_COLOURS.paper)).toBeGreaterThanOrEqual(3);
	});

	it("keeps its own text readable against its own fill, never against a desktop we do not control", () => {
		expect(contrastRatio(PROCS_INK, PROP_COLOURS.paper)).toBeGreaterThanOrEqual(4.5);
		expect(contrastRatio(PROP_COLOURS.bubbleMuted, PROP_COLOURS.paper)).toBeGreaterThanOrEqual(4.5);
		expect(contrastRatio(PROP_COLOURS.bubbleAlert, PROP_COLOURS.paper)).toBeGreaterThanOrEqual(4.5);
	});

	it("dims as its claim ages, because a weaker claim should look weaker", () => {
		const { container: fresh } = render(<Bubble text="Running the test suite" decay="fresh" />);
		const { container: fading } = render(<Bubble text="Running the test suite" decay="fading" />);

		const opacity = (c: HTMLElement) =>
			Number((c.querySelector("[data-bubble-text]") as HTMLElement).style.opacity || 1);
		expect(opacity(fading)).toBeLessThan(opacity(fresh));
		expect(opacity(fading)).toBeGreaterThan(0);
	});

	it("fades only the WORDS, never the card under them", () => {
		// A see-through card is a card whose legibility depends on a desktop we do not
		// control, which is the one thing this whole palette exists to avoid.
		const { container } = render(<Bubble text="Running the test suite" decay="settled" />);
		const card = container.querySelector("[data-bubble]") as HTMLElement;
		const tail = container.querySelector("[data-bubble-tail]") as HTMLElement;

		expect(card.style.opacity === "" || card.style.opacity === "1").toBe(true);
		expect(tail.style.opacity === "" || tail.style.opacity === "1").toBe(true);
	});

	it("collapses to the coarsest still-true thing rather than keep asserting a stale one", () => {
		// A Proc still saying "Running the test suite" ten minutes after the run ended
		// is lying. Settled drops the detail and keeps only what is still true.
		render(<Bubble text="Running the test suite" decay="settled" />);

		expect(screen.queryByText("Running the test suite")).not.toBeInTheDocument();
		expect(screen.getByText(BUBBLE_COARSE_TEXT)).toBeInTheDocument();
	});

	it("says nothing at all when there is nothing to say", () => {
		// No "unsupported" badge, no greyed placeholder, no empty bubble. A Proc
		// without a bubble is just a Proc.
		const { container } = render(<Bubble text="" />);

		expect(container.querySelector("[data-bubble]")).toBeNull();
	});

	it("marks a genuine block as an alert, and an inferred quiet as merely settled", () => {
		// StatusReason splits real from inferred. We do not cry wolf on a guess.
		const { container: real } = render(<Bubble text="Waiting for you" tone="alert" />);
		const { container: guess } = render(<Bubble text="Quiet for 12 minutes" tone="normal" decay="settled" />);

		expect((real.querySelector("[data-bubble]") as HTMLElement).style.color).toBe(rgb(PROP_COLOURS.bubbleAlert));
		expect((guess.querySelector("[data-bubble]") as HTMLElement).style.color).not.toBe(rgb(PROP_COLOURS.bubbleAlert));
	});

	it("points at the Proc it belongs to", () => {
		const { container } = render(<Bubble text="Running the test suite" />);

		expect(container.querySelector("[data-bubble-tail]")).not.toBeNull();
	});
});

describe("looksLikeRawCommand", () => {
	// The feed whitelists fields before anything is emitted, and that is the real
	// guard. This is the last-mile one: a bubble is the only part of the product that
	// puts agent-derived text on screen, so if a raw command ever reaches it, it must
	// not be the thing that shows a path, a host or a token to the room.
	it("passes the one-line sentences the model writes about itself", () => {
		for (const text of [
			"Running the test suite",
			"About to update the changelog",
			"Reading workspace.ts",
			"Waiting for you",
			"Quiet for 12 minutes",
		]) {
			expect(looksLikeRawCommand(text), text).toBe(false);
		}
	});

	it("catches text that is plainly a shell command", () => {
		for (const text of [
			"npm run test -- --watch",
			"cd /Users/someone/secret && ./deploy.sh",
			"cat ~/.aws/credentials",
			"curl -H 'Authorization: Bearer sk-abc123' https://api.example.com",
			"git push origin main | tee log.txt",
		]) {
			expect(looksLikeRawCommand(text), text).toBe(true);
		}
	});

	it("shows the coarse line instead of a command that slipped through", () => {
		render(<Bubble text="cat ~/.aws/credentials" />);

		expect(screen.queryByText(/credentials/)).not.toBeInTheDocument();
		expect(screen.getByText(BUBBLE_COARSE_TEXT)).toBeInTheDocument();
	});
});

describe("how much a bubble is allowed to say", () => {
	// One line cut at ~30 characters threw away most of what the agent was actually
	// doing — which is the only thing the bubble is for. It wraps to three lines and
	// truncates there, so a real sentence survives.
	const LONG = "Rewriting the coupon search ranking so expired offers stop being promoted";

	const cardOf = (container: HTMLElement) => container.querySelector("[data-bubble]") as HTMLElement;

	it("wraps instead of cutting the sentence at the first line", () => {
		const { container } = render(<Bubble text={LONG} />);
		const card = cardOf(container);

		expect(card.style.whiteSpace).not.toBe("nowrap");
	});

	it("stops at three lines, so a talkative Proc cannot grow a wall of text", () => {
		const { container } = render(<Bubble text={LONG} />);

		expect(cardOf(container).style.webkitLineClamp).toBe("3");
		expect(cardOf(container).style.overflow).toBe("hidden");
	});

	it("stays narrower than it is tall-capable, so it does not sprawl over the neighbour", () => {
		const { container } = render(<Bubble text={LONG} />);

		expect(parseInt(cardOf(container).style.maxWidth, 10)).toBeLessThanOrEqual(200);
	});
});
