import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CrewSwitcher } from "./CrewSwitcher";
import type { Task } from "../lib/crew";
import type { WorkspaceSession } from "../types/workspace";

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "dev-1",
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "do the thing",
		provider: "claude-code",
		kind: "worker",
		branch: "feature/thing",
		status: "working",
		updatedAt: "2026-08-22T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function soloTask(overrides: Partial<WorkspaceSession> = {}): Task {
	const dev = session(overrides);
	return { dev, members: [dev], isCrew: false };
}

function crewTask(devOverrides: Partial<WorkspaceSession> = {}, qaOverrides: Partial<WorkspaceSession> = {}): Task {
	const dev = session({ crew: { id: "dev-1", role: "dev", hasRun: true }, ...devOverrides });
	const qa = session({
		id: "dev-1-qa",
		crew: { id: "dev-1", role: "qa", hasRun: true },
		...qaOverrides,
	});
	return { dev, qa, members: [dev, qa], isCrew: true };
}

describe("CrewSwitcher — a solo task pays for one thing only", () => {
	it("draws `+ qa` and nothing else: no chips, no divider, no review pip", () => {
		render(
			<CrewSwitcher
				activeSessionId="dev-1"
				onAddRole={vi.fn()}
				onOpenMember={vi.fn()}
				review="not run"
				task={soloTask()}
			/>,
		);

		expect(document.querySelector("[data-crew-switcher-add='qa']")).not.toBeNull();
		expect(document.querySelectorAll("[data-crew-switcher-chip]")).toHaveLength(0);
		expect(document.querySelector("[data-crew-switcher-gate='review']")).toBeNull();
	});

	it("renders NOTHING at all when the task cannot gain a member", () => {
		// A finished task: attaching would hand a new agent a worktree that is
		// about to be reclaimed, so the daemon refuses — and an affordance that can
		// only fail is worse than no affordance.
		const { container } = render(
			<CrewSwitcher
				activeSessionId="dev-1"
				onAddRole={vi.fn()}
				onOpenMember={vi.fn()}
				review="not run"
				task={soloTask({ status: "merged" })}
			/>,
		);

		expect(container).toBeEmptyDOMElement();
	});

	it("renders nothing when the caller offers no way to attach at all", () => {
		const { container } = render(
			<CrewSwitcher activeSessionId="dev-1" onOpenMember={vi.fn()} review="not run" task={soloTask()} />,
		);

		expect(container).toBeEmptyDOMElement();
	});
});

describe("CrewSwitcher — a crew task", () => {
	it("draws one chip per member and marks the routed one, with review as a PIP and never a chip", () => {
		render(<CrewSwitcher activeSessionId="dev-1-qa" onOpenMember={vi.fn()} review="approved" task={crewTask()} />);

		const chips = [...document.querySelectorAll("[data-crew-switcher-chip]")];
		expect(chips.map((chip) => chip.getAttribute("data-crew-switcher-chip"))).toEqual(["dev", "qa"]);
		expect(chips.map((chip) => chip.getAttribute("data-crew-switcher-chip-active"))).toEqual(["false", "true"]);
		// The gate is a pip, not a chip: it must never be counted among the members.
		expect(document.querySelector("[data-crew-switcher-gate='review']")).toHaveAttribute(
			"data-crew-switcher-gate-state",
			"approved",
		);
		expect(screen.getByText("approved")).toBeInTheDocument();
	});

	it("offers no `+ qa`: the seat is taken, and the database refuses a second one", () => {
		render(
			<CrewSwitcher
				activeSessionId="dev-1"
				onAddRole={vi.fn()}
				onOpenMember={vi.fn()}
				review="not run"
				task={crewTask()}
			/>,
		);

		expect(document.querySelector("[data-crew-switcher-add='qa']")).toBeNull();
	});

	it("opens the other member when its chip is clicked", async () => {
		const onOpenMember = vi.fn();
		render(<CrewSwitcher activeSessionId="dev-1" onOpenMember={onOpenMember} review="not run" task={crewTask()} />);

		await userEvent.click(document.querySelector("[data-crew-switcher-chip='qa']") as HTMLElement);

		expect(onOpenMember).toHaveBeenCalledWith(expect.objectContaining({ id: "dev-1-qa" }));
	});
});

describe("CrewSwitcher — the device pip cannot shift the strip", () => {
	// The lease moves between two agents mid-task. A pip that appeared and
	// vanished would re-measure the chip every time a device changed hands, which
	// is the "layout must not shift as lists grow" lesson in its smallest form.
	function pips(): Element[] {
		return [...document.querySelectorAll("[data-device-pip]")];
	}

	it("reserves the SAME box on a chip that holds a device and one that does not", () => {
		const { rerender } = render(
			<CrewSwitcher
				activeSessionId="dev-1"
				deviceHolders={new Set<string>()}
				onOpenMember={vi.fn()}
				review="not run"
				showDevicePip
				task={crewTask()}
			/>,
		);
		// Both chips carry the pip's BOX; on a chip with no lease it is hidden by
		// ink alone (`invisible`), which keeps the box, where `hidden` would not.
		expect(pips()).toHaveLength(2);
		expect(pips().every((pip) => pip.classList.contains("invisible"))).toBe(true);
		const free = pips().map((pip) => pip.className.toString().replace(" invisible", ""));

		rerender(
			<CrewSwitcher
				activeSessionId="dev-1"
				deviceHolders={new Set(["dev-1-qa"])}
				onOpenMember={vi.fn()}
				review="not run"
				showDevicePip
				task={crewTask()}
			/>,
		);

		// Same number of pips, same classes minus `invisible` — nothing was added
		// or removed, so no chip can have changed width.
		expect(pips()).toHaveLength(2);
		expect(pips().map((pip) => pip.className.toString().replace(" invisible", ""))).toEqual(free);
		expect(document.querySelector("[data-crew-switcher-chip='qa']")).toHaveAttribute(
			"data-crew-switcher-chip-device",
			"held",
		);
		expect(document.querySelector("[data-crew-switcher-chip='dev']")).toHaveAttribute(
			"data-crew-switcher-chip-device",
			"free",
		);
	});

	it("reserves nothing on a project with no simulator — there is no lease to land there", () => {
		render(<CrewSwitcher activeSessionId="dev-1" onOpenMember={vi.fn()} review="not run" task={crewTask()} />);

		// One glyph per chip: the status glyph, and no reserved pip beside it.
		for (const chip of document.querySelectorAll("[data-crew-switcher-chip]")) {
			expect(chip.querySelectorAll("svg")).toHaveLength(1);
		}
	});
});
