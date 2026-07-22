import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { NameTag, PetTooltip } from "./NameTag";
import { PROCS_INK, PROP_COLOURS, contrastRatio, worstSeparation } from "./palette";

function rgb(hex: string): string {
	const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
	return `rgb(${r}, ${g}, ${b})`;
}

describe("NameTag", () => {
	it("says which session this Proc is", () => {
		render(<NameTag name="fix gl note render" />);

		expect(screen.getByText("fix gl note render")).toBeInTheDocument();
	});

	it("is a self-contained chip, because it sits on a wallpaper like the Proc does", () => {
		const { container } = render(<NameTag name="fix gl note render" />);
		const chip = container.querySelector("[data-name-tag]") as HTMLElement;

		expect(chip.style.background).toBe(rgb(PROP_COLOURS.paper));
		expect(chip.style.borderColor).toBe(rgb(PROCS_INK));
		expect(chip.style.color).toBe(rgb(PROCS_INK));
	});

	it("clears the wallpaper floor through both channels", () => {
		expect(worstSeparation(PROP_COLOURS.paper)).toBeGreaterThanOrEqual(3);
		expect(contrastRatio(PROCS_INK, PROP_COLOURS.paper)).toBeGreaterThanOrEqual(4.5);
	});

	it("shows nothing rather than an empty chip when a session has no name", () => {
		const { container } = render(<NameTag name="" />);

		expect(container.querySelector("[data-name-tag]")).toBeNull();
	});

	it("truncates a long name instead of stretching across its neighbours", () => {
		const { container } = render(<NameTag name={"a really quite extraordinarily long session name"} />);
		const chip = container.querySelector("[data-name-tag]") as HTMLElement;

		expect(chip.style.textOverflow).toBe("ellipsis");
		// Under the 155px crowding clearance, so it can never reach the Proc next door.
		expect(parseInt(chip.style.maxWidth, 10)).toBeLessThan(155);
	});
});

describe("PetTooltip", () => {
	it("answers the question the human actually asked — which session, which project", () => {
		render(
			<PetTooltip
				name="fix gl note render"
				sessionId="agent-orchestrator-154"
				project="agent-orchestrator"
				status="working"
			/>,
		);

		expect(screen.getByText("agent-orchestrator-154")).toBeInTheDocument();
		expect(screen.getByText("agent-orchestrator")).toBeInTheDocument();
	});

	it("says the status in words, not as an enum", () => {
		render(<PetTooltip name="n" sessionId="s" project="p" status="needs_input" />);

		expect(screen.getByText("Waiting for you to answer")).toBeInTheDocument();
		expect(screen.queryByText("needs_input")).not.toBeInTheDocument();
	});

	it("is the same self-contained card the bubble is", () => {
		const { container } = render(<PetTooltip name="n" sessionId="s" project="p" status="working" />);
		const card = container.querySelector("[data-tooltip]") as HTMLElement;

		expect(card.style.background).toBe(rgb(PROP_COLOURS.paper));
		expect(card.style.borderColor).toBe(rgb(PROCS_INK));
	});

	it("keeps its secondary text readable against its own fill", () => {
		expect(contrastRatio(PROP_COLOURS.bubbleMuted, PROP_COLOURS.paper)).toBeGreaterThanOrEqual(4.5);
	});
});

describe("what a name IS", () => {
	it("is not squeezed to the width of the Proc it sits under", () => {
		// The chip lives in a container the width of the FIGURE (93px), which was
		// truncating almost every real name. It may be wider than the Proc — it just
		// may not be wide enough to reach the neighbour it would be mistaken for.
		const { container } = render(<NameTag name="feature/parser-rewrite" />);
		const chip = container.querySelector("[data-name-tag]") as HTMLElement;

		expect(parseInt(chip.style.maxWidth, 10)).toBeGreaterThan(93);
		expect(parseInt(chip.style.maxWidth, 10)).toBeLessThan(155);
	});
});
