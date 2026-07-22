import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { CenterPane } from "./CenterPane";

// The terminal body pulls in xterm/SSE machinery irrelevant to the toolbar under test.
vi.mock("./TerminalPane", () => ({ TerminalPane: () => <div>terminal body</div> }));
// The restart control pulls in react-query machinery irrelevant to the toolbar
// under test; stub it so we can assert only on where it is (and isn't) shown.
vi.mock("./RestartSessionButton", () => ({ RestartSessionButton: () => <div>restart control</div> }));
// Same for the kill control (react-query + router); stub to assert placement only.
vi.mock("./KillSessionButton", () => ({ KillSessionButton: () => <div>kill control</div> }));

const worker = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "ao/sess-1",
	status: "working",
	updatedAt: "2026-06-10T00:00:00Z",
	prs: [],
} satisfies WorkspaceSession;

describe("CenterPane toolbar session label", () => {
	it("shows the session display name for a worker", () => {
		render(<CenterPane session={worker} theme="dark" daemonReady />);
		expect(screen.getByText("do the thing")).toBeInTheDocument();
		expect(screen.queryByText("sess-1")).not.toBeInTheDocument();
	});

	it("shows 'Orchestrator' for an orchestrator session", () => {
		render(<CenterPane session={{ ...worker, id: "sess-orch", kind: "orchestrator" }} theme="dark" daemonReady />);
		expect(screen.getByText("Orchestrator")).toBeInTheDocument();
	});

	it("shows 'No session' when there is no session", () => {
		render(<CenterPane theme="dark" daemonReady />);
		expect(screen.getByText("No session")).toBeInTheDocument();
	});
});

describe("CenterPane restart control", () => {
	it("offers restart for an active session", () => {
		render(<CenterPane session={worker} theme="dark" daemonReady />);
		expect(screen.getByText("restart control")).toBeInTheDocument();
	});

	it("offers restart for an active orchestrator session", () => {
		render(<CenterPane session={{ ...worker, id: "sess-orch", kind: "orchestrator" }} theme="dark" daemonReady />);
		expect(screen.getByText("restart control")).toBeInTheDocument();
	});

	it("hides restart for a terminated session", () => {
		render(<CenterPane session={{ ...worker, status: "terminated" }} theme="dark" daemonReady />);
		expect(screen.queryByText("restart control")).not.toBeInTheDocument();
	});

	it("hides restart when there is no session", () => {
		render(<CenterPane theme="dark" daemonReady />);
		expect(screen.queryByText("restart control")).not.toBeInTheDocument();
	});

	it("hides restart on the reviewer terminal", () => {
		render(
			<CenterPane
				session={worker}
				theme="dark"
				daemonReady
				terminalTarget={{ kind: "reviewer", handleId: "h-1", harness: "claude-code" }}
			/>,
		);
		expect(screen.queryByText("restart control")).not.toBeInTheDocument();
	});
});

describe("CenterPane kill control", () => {
	it("offers kill for an active worker session", () => {
		render(<CenterPane session={worker} theme="dark" daemonReady />);
		expect(screen.getByText("kill control")).toBeInTheDocument();
	});

	it("hides kill for an orchestrator session (worker-only)", () => {
		render(<CenterPane session={{ ...worker, id: "sess-orch", kind: "orchestrator" }} theme="dark" daemonReady />);
		expect(screen.queryByText("kill control")).not.toBeInTheDocument();
	});

	it("hides kill for a terminated session (its Restore control takes over)", () => {
		render(<CenterPane session={{ ...worker, status: "terminated" }} theme="dark" daemonReady />);
		expect(screen.queryByText("kill control")).not.toBeInTheDocument();
	});

	it("hides kill for a merged/done session", () => {
		render(<CenterPane session={{ ...worker, status: "merged" }} theme="dark" daemonReady />);
		expect(screen.queryByText("kill control")).not.toBeInTheDocument();
	});

	it("hides kill when there is no session", () => {
		render(<CenterPane theme="dark" daemonReady />);
		expect(screen.queryByText("kill control")).not.toBeInTheDocument();
	});

	it("hides kill on the reviewer terminal", () => {
		render(
			<CenterPane
				session={worker}
				theme="dark"
				daemonReady
				terminalTarget={{ kind: "reviewer", handleId: "h-1", harness: "claude-code" }}
			/>,
		);
		expect(screen.queryByText("kill control")).not.toBeInTheDocument();
	});
});

describe("CenterPane split chrome", () => {
	it("single view: unchanged toolbar plus the provided split controls", () => {
		render(<CenterPane session={worker} theme="dark" daemonReady splitControls={<div>split entry</div>} />);
		expect(screen.getByText("TERMINAL")).toBeInTheDocument();
		expect(screen.getByText("split entry")).toBeInTheDocument();
		expect(screen.getByText("restart control")).toBeInTheDocument();
		expect(screen.queryByText("ao/sess-1")).not.toBeInTheDocument();
	});

	it("focused pane: pane header (glyph + branch, no eyebrow) with the full control set", () => {
		render(
			<CenterPane
				session={worker}
				theme="dark"
				daemonReady
				pane={{ focused: true }}
				splitControls={<div>split controls</div>}
			/>,
		);
		expect(screen.queryByText("TERMINAL")).not.toBeInTheDocument();
		expect(screen.getByText("ao/sess-1")).toBeInTheDocument();
		expect(screen.getByText("split controls")).toBeInTheDocument();
		expect(screen.getByText("restart control")).toBeInTheDocument();
		expect(screen.getByText("kill control")).toBeInTheDocument();
	});

	it("unfocused pane: dimmed slim toolbar — split controls only, no working controls", () => {
		render(
			<CenterPane
				session={worker}
				theme="dark"
				daemonReady
				pane={{ focused: false }}
				splitControls={<div>split controls</div>}
			/>,
		);
		expect(screen.getByText("split controls")).toBeInTheDocument();
		expect(screen.queryByText("restart control")).not.toBeInTheDocument();
		expect(screen.queryByText("kill control")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Increase terminal font size")).not.toBeInTheDocument();
		const toolbar = document.querySelector(".terminal-toolbar")!;
		expect(toolbar.className).toContain("opacity-60");
	});
});
