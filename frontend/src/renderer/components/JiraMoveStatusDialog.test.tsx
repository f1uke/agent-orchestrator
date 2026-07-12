import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { transitionsMock, moveMock, mutateMock } = vi.hoisted(() => ({
	transitionsMock: vi.fn(),
	moveMock: vi.fn(),
	mutateMock: vi.fn(),
}));
vi.mock("../hooks/useSessionJiraContext", () => ({
	useJiraTransitions: transitionsMock,
	useMoveJiraStatus: moveMock,
}));

import { JiraMoveStatusDialog, type MoveTarget } from "./JiraMoveStatusDialog";

const target: MoveTarget = {
	key: "DEMO-101",
	type: "Story",
	title: "Example story",
	status: "Ready for QA",
};

function setTransitions(over: Record<string, unknown> = {}) {
	transitionsMock.mockReturnValue({
		data: [
			{ id: "11", name: "Start Testing", to: "In Progress", toCategory: "indeterminate" },
			{ id: "21", name: "Abandoned", to: "Abandoned", toCategory: "done" },
		],
		isLoading: false,
		isError: false,
		error: null,
		...over,
	});
}

function setMove(over: Record<string, unknown> = {}) {
	moveMock.mockReturnValue({
		mutate: mutateMock,
		reset: vi.fn(),
		isPending: false,
		isError: false,
		error: null,
		...over,
	});
}

function renderDialog(onOpenChange = vi.fn(), issueKey?: string) {
	return render(
		<JiraMoveStatusDialog sessionId="s1" target={target} issueKey={issueKey} open={true} onOpenChange={onOpenChange} />,
	);
}

describe("JiraMoveStatusDialog", () => {
	beforeEach(() => {
		transitionsMock.mockReset();
		moveMock.mockReset();
		mutateMock.mockReset();
		setTransitions();
		setMove();
	});

	it("frames itself as the one write and lists live transitions", () => {
		renderDialog();
		expect(screen.getByText(/move jira status/i)).toBeTruthy();
		expect(screen.getByText(/no comment, no field edit/i)).toBeTruthy();
		expect(screen.getByText(/fetched live/i)).toBeTruthy();
		expect(screen.getByText("Start Testing")).toBeTruthy();
		expect(screen.getByText("Abandoned")).toBeTruthy();
		expect(screen.getByText("DEMO-101")).toBeTruthy();
	});

	it("enables the confirm only once a transition is picked, and shows current→next", () => {
		renderDialog();
		const send = screen.getByRole("button", { name: /move status/i });
		expect((send as HTMLButtonElement).disabled).toBe(true);
		expect(screen.getByText("Ready for QA")).toBeTruthy(); // current chip
		fireEvent.click(screen.getByText("Start Testing"));
		expect((send as HTMLButtonElement).disabled).toBe(false);
		expect(screen.getByText("In Progress")).toBeTruthy(); // next chip = the transition's target status
	});

	it("applies the chosen transition by id on confirm", () => {
		const onOpenChange = vi.fn();
		renderDialog(onOpenChange);
		fireEvent.click(screen.getByText("Abandoned"));
		fireEvent.click(screen.getByRole("button", { name: /move status/i }));
		expect(mutateMock).toHaveBeenCalledWith("21", expect.anything());
	});

	it("surfaces a transitions-fetch failure (e.g. missing token)", () => {
		setTransitions({ data: undefined, isLoading: false, isError: true, error: new Error("set JIRA_API_TOKEN") });
		renderDialog();
		expect(screen.getByText(/set JIRA_API_TOKEN/i)).toBeTruthy();
	});

	it("surfaces a Jira move rejection", () => {
		setMove({ isError: true, error: new Error("Jira rejected the transition (a validator)") });
		renderDialog();
		expect(screen.getByText(/rejected the transition/i)).toBeTruthy();
	});

	it("shows a loading placeholder while transitions load", () => {
		setTransitions({ data: undefined, isLoading: true });
		renderDialog();
		expect(screen.getByText(/loading transitions/i)).toBeTruthy();
	});

	it("shows an empty note when the issue has no transitions", () => {
		setTransitions({ data: [] });
		renderDialog();
		expect(screen.getByText(/no transitions are available/i)).toBeTruthy();
	});

	it("scopes the transitions + move to a subtask key when given", () => {
		renderDialog(vi.fn(), "DEMO-102");
		// The subtask key is threaded into both hooks so the write targets the subtask.
		expect(transitionsMock).toHaveBeenCalledWith("s1", true, "DEMO-102");
		expect(moveMock).toHaveBeenCalledWith("s1", "DEMO-102");
	});

	it("moves the bound issue (no subtask key) by default", () => {
		renderDialog();
		expect(transitionsMock).toHaveBeenCalledWith("s1", true, undefined);
		expect(moveMock).toHaveBeenCalledWith("s1", undefined);
	});
});
