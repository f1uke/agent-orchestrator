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

	it("offers `+ qa` on a solo task, and fires it without opening the card", async () => {
		const onAddRole = vi.fn();
		const onCardOpen = vi.fn();
		const dev = member("demo-9", { taskSize: "mechanical" });
		render(
			<div onClick={onCardOpen}>
				<CrewStrip
					task={{ dev, members: [dev], isCrew: false }}
					review="not run"
					onOpenMember={() => {}}
					onAddRole={onAddRole}
				/>
			</div>,
		);
		const add = document.querySelector('[data-crew-add="qa"]')!;
		expect(add).toBeInTheDocument();
		await userEvent.click(add);
		expect(onAddRole).toHaveBeenCalledTimes(1);
		expect(onCardOpen).not.toHaveBeenCalled();
	});

	// An affordance that can only fail is worse than no affordance: each of these
	// is a refusal the daemon would give, so the card never offers it.
	it.each([
		["the task already has a crew", () => crewTask()],
		[
			"the task is finished",
			() => {
				const dev = member("demo-9", { isTerminated: true, status: "terminated" });
				return { dev, members: [dev], isCrew: false };
			},
		],
		[
			"the task has not started",
			() => {
				const dev = member("demo-9", { status: "todo" });
				return { dev, members: [dev], isCrew: false };
			},
		],
		[
			"it is an orchestrator, not a task",
			() => {
				const dev = member("demo-orchestrator", { kind: "orchestrator" });
				return { dev, members: [dev], isCrew: false };
			},
		],
	])("does not offer `+ qa` when %s", (_name, build) => {
		render(<CrewStrip task={build()} review="not run" onOpenMember={() => {}} onAddRole={() => {}} />);
		expect(document.querySelector('[data-crew-add="qa"]')).toBeNull();
	});

	it("marks a finished member done rather than asleep", () => {
		render(<CrewStrip task={crewTask({ isTerminated: true })} review="changes" onOpenMember={() => {}} />);
		expect(document.querySelector('[data-crew-chip="qa"]')).toHaveAttribute("data-crew-chip-state", "done");
		expect(document.querySelector('[data-crew-gate="review"]')).toHaveAttribute("data-crew-gate-state", "changes");
	});

	it("says how the task gained its qa, so a card that changed shape explains itself", () => {
		render(
			<CrewStrip
				task={crewTask({ crew: { id: "demo-1", role: "qa", hasRun: true, joinReason: "review" } })}
				review="not run"
				onOpenMember={() => {}}
			/>,
		);
		const line = document.querySelector("[data-crew-join]");
		expect(line).toHaveAttribute("data-crew-join", "review");
		expect(line).toHaveTextContent("qa joined · dev asked for a review");
	});

	// THE WARNING THAT REPLACED THE TRIGGER, rendered where a human can act on it.
	// AO used to put a qa on a task the moment it saw the app being driven; it no
	// longer does, so a solo task that drove the app and never asked for one says
	// so, in the same slot, next to the `+ qa` button that fixes it.
	it("says when a solo task drove the app and nobody asked for a qa", () => {
		const dev = member("demo-9", { taskSize: "standard", runtimeTouch: "sim" });
		render(
			<CrewStrip
				task={{ dev, members: [dev], isCrew: false }}
				review="not run"
				onOpenMember={() => {}}
				onAddRole={() => {}}
			/>,
		);
		const line = document.querySelector("[data-crew-unreviewed]");
		expect(line).toHaveAttribute("data-crew-unreviewed", "sim");
		expect(line).toHaveTextContent("dev drove the simulator · no qa was asked for");
		// The control that answers it is on the same card.
		expect(document.querySelector('[data-crew-add="qa"]')).not.toBeNull();
	});

	it("says nothing about joining on a solo task, or when the reason was never recorded", () => {
		const dev = member("demo-9", { taskSize: "standard" });
		const { unmount } = render(
			<CrewStrip task={{ dev, members: [dev], isCrew: false }} review="not run" onOpenMember={() => {}} />,
		);
		expect(document.querySelector("[data-crew-join]")).toBeNull();
		unmount();

		render(<CrewStrip task={crewTask()} review="not run" onOpenMember={() => {}} />);
		expect(document.querySelector("[data-crew-join]")).toBeNull();
	});
});
