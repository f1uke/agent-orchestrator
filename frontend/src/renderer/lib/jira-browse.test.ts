import { describe, expect, it } from "vitest";
import type { JiraIssueSummary } from "../hooks/useSessionJiraContext";
import {
	BACKLOG_LABEL,
	filterByAssignee,
	groupBySprint,
	hasUnassigned,
	UNASSIGNED,
	uniqueAssignees,
} from "./jira-browse";

function issue(over: Partial<JiraIssueSummary> & { key: string }): JiraIssueSummary {
	return { type: "Story", title: over.key, ...over };
}

const rows: JiraIssueSummary[] = [
	issue({ key: "DEMO-1", assignee: "Alex Rivera", sprint: { name: "Sprint 2026-15", state: "future" } }),
	issue({ key: "DEMO-2", assignee: "Sam Chen", sprint: { name: "Sprint 2026-14", state: "active" } }),
	issue({ key: "DEMO-3", assignee: "Alex Rivera", sprint: { name: "Sprint 2026-14", state: "active" } }),
	issue({ key: "DEMO-4" }), // no assignee, no sprint
];

describe("uniqueAssignees / hasUnassigned", () => {
	it("returns sorted unique assignees and detects unassigned", () => {
		expect(uniqueAssignees(rows)).toEqual(["Alex Rivera", "Sam Chen"]);
		expect(hasUnassigned(rows)).toBe(true);
		expect(hasUnassigned([rows[0], rows[1]])).toBe(false);
	});
});

describe("filterByAssignee", () => {
	it("passes everything through for the empty (all) filter", () => {
		expect(filterByAssignee(rows, "")).toHaveLength(4);
	});
	it("matches a specific assignee exactly", () => {
		expect(filterByAssignee(rows, "Alex Rivera").map((i) => i.key)).toEqual(["DEMO-1", "DEMO-3"]);
	});
	it("selects only unassigned issues for the UNASSIGNED sentinel", () => {
		expect(filterByAssignee(rows, UNASSIGNED).map((i) => i.key)).toEqual(["DEMO-4"]);
	});
});

describe("groupBySprint", () => {
	it("groups by sprint, sorts named sprints numerically, and puts the backlog last", () => {
		const groups = groupBySprint(rows);
		expect(groups.map((g) => g.name)).toEqual(["Sprint 2026-14", "Sprint 2026-15", BACKLOG_LABEL]);
		expect(groups[0].issues.map((i) => i.key)).toEqual(["DEMO-2", "DEMO-3"]);
		expect(groups[0].state).toBe("active");
		expect(groups[0].isBacklog).toBe(false);
		const backlog = groups[2];
		expect(backlog.isBacklog).toBe(true);
		expect(backlog.issues.map((i) => i.key)).toEqual(["DEMO-4"]);
	});

	it("omits the backlog group when every issue has a sprint", () => {
		const groups = groupBySprint([rows[0], rows[1]]);
		expect(groups.map((g) => g.name)).toEqual(["Sprint 2026-14", "Sprint 2026-15"]);
		expect(groups.some((g) => g.isBacklog)).toBe(false);
	});

	it("returns an empty array for no issues", () => {
		expect(groupBySprint([])).toEqual([]);
	});
});
