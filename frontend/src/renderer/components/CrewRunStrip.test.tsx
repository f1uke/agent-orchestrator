import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { CrewRunStrip } from "./CrewRunStrip";
import type { CrewRun } from "../lib/crew-run";

const base = {
	sessionId: "s1",
	projectId: "p",
	attempt: 1,
	detector: "live",
	genAtStart: 0,
	genAtEnd: 0,
	startedAt: "2026-08-21T10:00:00Z",
	createdAt: "2026-08-21T10:00:00Z",
	updatedAt: "2026-08-21T10:00:00Z",
	kind: "test",
};

const run = (over: Partial<CrewRun> & { id: string }): CrewRun => ({ ...base, ...over }) as CrewRun;

const discarded = (id: string, over: Partial<CrewRun> = {}) =>
	run({ id, endedAt: "2026-08-21T10:01:00Z", outcome: "discarded", ...over });

describe("CrewRunStrip", () => {
	// The "must not change" guarantee, on screen: a session that has never
	// bracketed a run - every solo session, and every project that does not use
	// the bracket - gets exactly the Tests tab it had before this existed.
	it("renders nothing at all when the session never bracketed a run", () => {
		const { container } = render(<CrewRunStrip runs={[]} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("shows a discarded run as DISCARDED and names what moved", () => {
		render(<CrewRunStrip runs={[discarded("a", { result: "pass", changedPaths: ["backend/app.go"] })]} />);
		expect(screen.getByText("DISCARDED")).toBeInTheDocument();
		expect(screen.getByText("backend/app.go")).toBeInTheDocument();
		// The pass it reported must not appear anywhere as a verdict.
		expect(screen.queryByText("PASSED")).not.toBeInTheDocument();
	});

	it("says a run nothing watched is uncertified, and why", () => {
		render(
			<CrewRunStrip
				runs={[
					run({
						id: "a",
						endedAt: "2026-08-21T10:01:00Z",
						outcome: "uncertified",
						result: "pass",
						detectorReason: "the daemon restarted while this run was open",
					}),
				]}
			/>,
		);
		expect(screen.getByText("UNCERTIFIED")).toBeInTheDocument();
		expect(screen.getByText(/the daemon restarted/)).toBeInTheDocument();
		expect(screen.queryByText("PASSED")).not.toBeInTheDocument();
	});

	it("keeps a trusted run's own verdict", () => {
		render(
			<CrewRunStrip runs={[run({ id: "a", endedAt: "2026-08-21T10:01:00Z", outcome: "trusted", result: "fail" })]} />,
		);
		expect(screen.getByText("FAILED")).toBeInTheDocument();
	});

	it("raises the escalation only once the automatic retry is spent", () => {
		const { rerender } = render(<CrewRunStrip runs={[discarded("b"), discarded("a")]} />);
		expect(screen.queryByText(/discarded in a row/)).not.toBeInTheDocument();

		rerender(<CrewRunStrip runs={[discarded("c"), discarded("b"), discarded("a")]} />);
		expect(screen.getByText(/3 runs discarded in a row/)).toBeInTheDocument();
		expect(screen.getByText(/Pause the other member/)).toBeInTheDocument();
	});

	it("shows an open run as running", () => {
		render(<CrewRunStrip runs={[run({ id: "a", kind: "build", label: "npm run build" })]} />);
		expect(screen.getByText("RUNNING")).toBeInTheDocument();
		expect(screen.getByText("Build · npm run build")).toBeInTheDocument();
	});
});
