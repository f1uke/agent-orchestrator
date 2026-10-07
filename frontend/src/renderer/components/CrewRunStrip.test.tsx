import { render, screen, within } from "@testing-library/react";
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
	// the bracket - gets exactly the Summary tab it had before this existed.
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

	it("tags each run with the member that made it when the task has a crew", () => {
		render(
			<CrewRunStrip
				runs={[
					discarded("q1", { sessionId: "s1-qa", crewId: "s1", role: "qa", label: "maestro test" }),
					run({ id: "d1", endedAt: "2026-08-21T10:01:00Z", outcome: "trusted", result: "pass", role: "dev" }),
				]}
			/>,
		);
		const rows = screen.getAllByTestId("crew-run-row");
		expect(rows.map((row) => within(row).getByTestId("crew-run-role").textContent)).toEqual(["qa", "dev"]);
	});

	it("draws no member tag on a solo task's runs", () => {
		render(<CrewRunStrip runs={[run({ id: "a", endedAt: "2026-08-21T10:01:00Z", outcome: "trusted" })]} />);
		expect(screen.getByTestId("crew-run-row")).toBeInTheDocument();
		expect(screen.queryByTestId("crew-run-role")).not.toBeInTheDocument();
	});

	it("names the member whose runs keep being discarded, not the other one's quiet runs", () => {
		const qa = { sessionId: "s1-qa", role: "qa" as const };
		const devPass = (id: string) =>
			run({ id, endedAt: "2026-08-21T10:01:00Z", outcome: "trusted", result: "pass", role: "dev" });
		render(
			<CrewRunStrip
				runs={[discarded("q3", qa), devPass("d2"), discarded("q2", qa), devPass("d1"), discarded("q1", qa)]}
			/>,
		);
		expect(screen.getByText(/3 of qa's runs discarded in a row/)).toBeInTheDocument();
		expect(screen.getByText(/so qa gets a quiet tree/)).toBeInTheDocument();
	});

	it("shows an open run as running", () => {
		render(<CrewRunStrip runs={[run({ id: "a", kind: "build", label: "npm run build" })]} />);
		expect(screen.getByText("RUNNING")).toBeInTheDocument();
		expect(screen.getByText("Build · npm run build")).toBeInTheDocument();
	});
});
