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
		const name = "a really quite extraordinarily long session name";
		const { container } = render(<NameTag name={name} />);
		const chip = container.querySelector("[data-name-tag]") as HTMLElement;
		// The truncating box is the text itself, not the chip: the chip is a row that
		// may also carry the coordinator's crown, and the crown must never be the part
		// that gets ellipsized away.
		const text = [...chip.children].find((child) => child.textContent === name) as HTMLElement;

		expect(text.style.textOverflow).toBe("ellipsis");
		// Under the 155px crowding clearance, so it can never reach the Proc next door.
		expect(parseInt(chip.style.maxWidth, 10)).toBeLessThan(155);
	});
});

describe("PetTooltip", () => {
	it("answers the question the human actually asked — which session, which project", () => {
		render(<PetTooltip name="login rate limit" sessionId="demo-app-59" project="demo-app" status="working" />);

		// The `@` sigil, as a session ref is written everywhere else in the product.
		expect(screen.getByText("@demo-app-59")).toBeInTheDocument();
		expect(screen.getByText("demo-app")).toBeInTheDocument();
		expect(screen.getByText("login rate limit")).toBeInTheDocument();
	});

	it("does not double up the sigil if the feed already sent one", () => {
		render(<PetTooltip name="n" sessionId="@demo-app-59" project="p" status="working" />);

		expect(screen.getByText("@demo-app-59")).toBeInTheDocument();
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

describe("the tooltip's identifier lines", () => {
	// "agent-orchestrator-105" was being broken at a hyphen — "agent-" on one line,
	// "orchestrator-105" on the next — which reads as two different things. A
	// hyphen inside an identifier is not a place a reader expects a break; a space
	// inside a task name is.
	function lines(name = "smoke to testiny") {
		const { container } = render(
			<PetTooltip name={name} sessionId="agent-orchestrator-105" project="agent-orchestrator" status="working" />,
		);
		return [...container.querySelectorAll("[data-tooltip] > *")] as HTMLElement[];
	}

	it("keeps the session id on one line instead of splitting it at a hyphen", () => {
		const id = lines().find((line) => line.textContent === "@agent-orchestrator-105");

		expect(id?.style.whiteSpace).toBe("nowrap");
	});

	it("keeps the project name on one line for the same reason", () => {
		const project = lines().find((line) => line.textContent === "agent-orchestrator");

		expect(project?.style.whiteSpace).toBe("nowrap");
	});

	it("still lets a long task name wrap, because its spaces ARE break points", () => {
		const long = "rewrite the coupon search results ranking";
		const heading = lines(long).find((line) => line.textContent === long);

		expect(heading?.style.whiteSpace ?? "").not.toBe("nowrap");
	});
});

describe("the orchestrator's name chip", () => {
	// The human's call (2026-07-22): mark the coordinator on its LABEL, not on the
	// character. The Proc keeps the hat that says which character it is, and the
	// chip says what job it holds — two readings that stay separate.
	it("wears a crown", () => {
		const { container } = render(<NameTag name="orchestrating" lead />);

		expect(container.querySelector("[data-lead-crown]")).not.toBeNull();
	});

	it("does not put a crown on an ordinary worker", () => {
		const { container } = render(<NameTag name="login rate limit" />);

		expect(container.querySelector("[data-lead-crown]")).toBeNull();
	});

	it("is a different colour from its peers' chips", () => {
		const lead = render(<NameTag name="orchestrating" lead />).container.querySelector(
			"[data-name-tag]",
		) as HTMLElement;
		const worker = render(<NameTag name="login rate limit" />).container.querySelector(
			"[data-name-tag]",
		) as HTMLElement;

		expect(lead.style.background).not.toBe(worker.style.background);
	});

	it("keeps the ink rim, because the fill alone cannot carry a light wallpaper", () => {
		const lead = render(<NameTag name="orchestrating" lead />).container.querySelector(
			"[data-name-tag]",
		) as HTMLElement;

		expect(lead.style.borderColor.replace(/\s/g, "")).toBe("rgb(24,20,34)");
	});
});

describe("the project mark on the chip", () => {
	// The human could not tell which pet belonged to which project: the look is
	// assigned per SESSION, so it carried no project signal, and the only project
	// information on screen was inside a hover card you have to ask for.
	it("marks a Proc with its project", () => {
		const { container } = render(<NameTag name="login rate limit" project="demo-app" />);

		expect(container.querySelector("[data-project-mark]")).not.toBeNull();
	});

	it("gives two sessions on the same project the same mark", () => {
		const one = render(<NameTag name="a" project="demo-app" />).container;
		const two = render(<NameTag name="b" project="demo-app" />).container;

		expect(markOf(one)).toBe(markOf(two));
	});

	it("gives two projects different marks", () => {
		const app = render(<NameTag name="a" project="demo-app" />).container;
		const api = render(<NameTag name="a" project="demo-api" />).container;

		expect(markOf(app)).not.toBe(markOf(api));
	});

	it("shows no mark at all when the project is unknown", () => {
		const { container } = render(<NameTag name="login rate limit" />);

		expect(container.querySelector("[data-project-mark]")).toBeNull();
	});

	it("puts the mark AFTER the name and the crown BEFORE it", () => {
		// In front, the mark and the crown crowd each other and read as one cluttered
		// badge. The human's call once both were on the same chip.
		const { container } = render(<NameTag name="orchestrating" project="demo-app" lead />);
		const chip = container.querySelector("[data-name-tag]") as HTMLElement;
		const kids = [...chip.children];

		expect(kids.findIndex((k) => k.matches("[data-lead-crown]"))).toBe(0);
		expect(kids.findIndex((k) => k.matches("[data-project-mark]"))).toBe(kids.length - 1);
	});

	it("carries the ink rim, like every other mark out here", () => {
		const { container } = render(<NameTag name="a" project="demo-app" />);
		const path = container.querySelector("[data-project-mark] path");

		expect(path?.getAttribute("stroke")).toBe(PROCS_INK);
	});
});

function markOf(container: HTMLElement): string | null {
	return container.querySelector("[data-project-mark]")?.getAttribute("data-project-mark") ?? null;
}
