import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CrewStrip } from "./CrewStrip";
import type { Task } from "../lib/crew";
import type { WorkspaceSession } from "../types/workspace";

function member(id: string, over: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id,
		workspaceId: "demo",
		workspaceName: "Demo",
		title: id,
		provider: "claude-code",
		kind: "worker",
		branch: "feature/x",
		status: "working",
		updatedAt: "2026-08-21T00:00:00Z",
		prs: [],
		...over,
	};
}

function crewTask(qaOver: Partial<WorkspaceSession> = {}): Task {
	const dev = member("demo-1", { crew: { id: "demo-1", role: "dev", hasRun: true } });
	const qa = member("demo-2", {
		isSuspended: true,
		status: "idle",
		crew: { id: "demo-1", role: "qa", hasRun: false },
		...qaOver,
	});
	return { dev, qa, members: [dev, qa], isCrew: true };
}

describe("CrewStrip", () => {
	it("draws one chip per member and a review gate, and never a chip for a role that is not there", () => {
		render(<CrewStrip task={crewTask()} review="not run" onOpenMember={() => {}} />);
		expect(document.querySelectorAll("[data-crew-chip]")).toHaveLength(2);
		expect(document.querySelector('[data-crew-chip="dev"]')).toHaveAttribute("data-crew-chip-state", "working");
		expect(document.querySelector('[data-crew-chip="qa"]')).toHaveAttribute("data-crew-chip-state", "asleep");
		expect(document.querySelector('[data-crew-gate="review"]')).toHaveAttribute("data-crew-gate-state", "not run");
	});

	it("shows a solo task as a solo task, with no empty seats", () => {
		const dev = member("demo-9", { taskSize: "mechanical" });
		render(<CrewStrip task={{ dev, members: [dev], isCrew: false }} review="not run" onOpenMember={() => {}} />);
		expect(document.querySelectorAll("[data-crew-chip]")).toHaveLength(0);
		expect(document.querySelector("[data-crew-solo]")).toHaveAttribute("data-crew-solo", "mechanical");
		expect(screen.getByText("mechanical")).toBeInTheDocument();
	});

	it("opens the member a chip names, without opening the card underneath it", async () => {
		const onOpenMember = vi.fn();
		const onCardOpen = vi.fn();
		render(
			<div onClick={onCardOpen}>
				<CrewStrip task={crewTask()} review="approved" onOpenMember={onOpenMember} />
			</div>,
		);
		await userEvent.click(document.querySelector('[data-crew-chip="qa"]')!);
		expect(onOpenMember).toHaveBeenCalledWith(expect.objectContaining({ id: "demo-2" }));
		expect(onCardOpen).not.toHaveBeenCalled();
	});

	it("marks a finished member done rather than asleep", () => {
		render(<CrewStrip task={crewTask({ isTerminated: true })} review="changes" onOpenMember={() => {}} />);
		expect(document.querySelector('[data-crew-chip="qa"]')).toHaveAttribute("data-crew-chip-state", "done");
		expect(document.querySelector('[data-crew-gate="review"]')).toHaveAttribute("data-crew-gate-state", "changes");
	});
});
