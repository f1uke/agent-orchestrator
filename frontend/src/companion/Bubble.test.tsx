import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { BUBBLE_COARSE_TEXT, Bubble, looksLikeRawCommand } from "./Bubble";
import { PROCS_INK, PROP_COLOURS, contrastRatio, worstSeparation } from "./palette";

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
		// rim carries light ones — the same two channels the pets use.
		const { container } = render(<Bubble text="Running the test suite" />);
		const card = container.querySelector("[data-bubble]") as HTMLElement;

		expect(card.style.background).toBe(rgb(PROP_COLOURS.paper));
		expect(card.style.borderColor).toBe(rgb(PROCS_INK));
		expect(card.style.color).toBe(rgb(PROCS_INK));
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

		const opacity = (c: HTMLElement) => Number((c.querySelector("[data-bubble]") as HTMLElement).style.opacity || 1);
		expect(opacity(fading)).toBeLessThan(opacity(fresh));
		expect(opacity(fading)).toBeGreaterThan(0);
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

	it("wraps instead of cutting the sentence at the first line", () => {
		render(<Bubble text={LONG} />);
		const card = screen.getByText(LONG);

		expect(card.style.whiteSpace).not.toBe("nowrap");
	});

	it("stops at three lines, so a talkative Proc cannot grow a wall of text", () => {
		render(<Bubble text={LONG} />);
		const card = screen.getByText(LONG);

		expect(card.style.WebkitLineClamp).toBe("3");
		expect(card.style.overflow).toBe("hidden");
	});

	it("stays narrower than it is tall-capable, so it does not sprawl over the neighbour", () => {
		render(<Bubble text={LONG} />);

		expect(parseInt(screen.getByText(LONG).style.maxWidth, 10)).toBeLessThanOrEqual(200);
	});
});
