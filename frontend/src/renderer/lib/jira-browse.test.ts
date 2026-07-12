import { describe, expect, it } from "vitest";
import type { JiraIssueSummary } from "../hooks/useSessionJiraContext";
import {
	BACKLOG_LABEL,
	buildHierarchy,
	groupBySprint,
	groupRowsBySprint,
	hasUnassigned,
	missingParentKeys,
	uniqueAssignees,
} from "./jira-browse";

function issue(over: Partial<JiraIssueSummary> & { key: string }): JiraIssueSummary {
	return { type: "Story", title: over.key, ...over };
}

const rows: JiraIssueSummary[] = [
	issue({
		key: "DEMO-1",
		assignee: "Alex Rivera",
		assigneeAccountId: "acc-alex",
		sprint: { name: "Sprint 2026-15", state: "future" },
	}),
	issue({
		key: "DEMO-2",
		assignee: "Sam Chen",
		assigneeAccountId: "acc-sam",
		sprint: { name: "Sprint 2026-14", state: "active" },
	}),
	issue({
		key: "DEMO-3",
		assignee: "Alex Rivera",
		assigneeAccountId: "acc-alex",
		sprint: { name: "Sprint 2026-14", state: "active" },
	}),
	issue({ key: "DEMO-4" }), // no assignee, no sprint
];

describe("uniqueAssignees / hasUnassigned", () => {
	it("returns sorted unique assignee options (name + accountId) and detects unassigned", () => {
		// The dropdown needs the accountId so the filter can go server-side; the
		// list is deduped by name and sorted.
		expect(uniqueAssignees(rows)).toEqual([
			{ name: "Alex Rivera", accountId: "acc-alex" },
			{ name: "Sam Chen", accountId: "acc-sam" },
		]);
		expect(hasUnassigned(rows)).toBe(true);
		expect(hasUnassigned([rows[0], rows[1]])).toBe(false);
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

describe("buildHierarchy", () => {
	const parent = issue({ key: "DEMO-101", type: "Story", title: "Parent story" });
	const child1 = issue({ key: "DEMO-102", type: "Sub-task", parent: { key: "DEMO-101" } });
	const child2 = issue({ key: "DEMO-103", type: "Sub-task", parent: { key: "DEMO-101" } });
	const orphan = issue({ key: "DEMO-200", type: "Sub-task", parent: { key: "DEMO-999", title: "off-page" } });
	const standalone = issue({ key: "DEMO-3", type: "Bug" });

	it("nests subtasks under a present parent and preserves order", () => {
		const rows = buildHierarchy([parent, child1, child2, standalone]);
		expect(rows.map((r) => r.issue.key)).toEqual(["DEMO-101", "DEMO-3"]); // children are nested, not top-level
		expect(rows[0].children.map((c) => c.key)).toEqual(["DEMO-102", "DEMO-103"]);
		expect(rows[1].children).toEqual([]);
	});

	it("keeps a subtask whose parent is absent as a top-level orphan", () => {
		const rows = buildHierarchy([orphan, standalone]);
		expect(rows.map((r) => r.issue.key)).toEqual(["DEMO-200", "DEMO-3"]);
		expect(rows[0].children).toEqual([]); // orphan has no nested children of its own
	});

	it("marks context parents from the provided key set", () => {
		const rows = buildHierarchy([parent, child1], new Set(["DEMO-101"]));
		expect(rows[0].issue.key).toBe("DEMO-101");
		expect(rows[0].isContext).toBe(true);
		expect(rows[0].children.map((c) => c.key)).toEqual(["DEMO-102"]);
	});
});

describe("missingParentKeys", () => {
	it("returns sorted parent keys referenced but not present (dedup)", () => {
		const rows = [
			issue({ key: "DEMO-102", parent: { key: "DEMO-101" } }),
			issue({ key: "DEMO-103", parent: { key: "DEMO-101" } }), // dup parent
			issue({ key: "DEMO-104", parent: { key: "DEMO-050" } }),
			issue({ key: "DEMO-101" }), // this parent IS present → not missing
		];
		expect(missingParentKeys(rows)).toEqual(["DEMO-050"]);
	});

	it("is empty when every parent is present or there are no parents", () => {
		expect(missingParentKeys([issue({ key: "DEMO-1" }), issue({ key: "DEMO-2" })])).toEqual([]);
	});
});

describe("groupRowsBySprint", () => {
	it("groups hierarchy rows by the top-level issue's sprint; children ride along", () => {
		const rows = buildHierarchy([
			issue({ key: "DEMO-101", sprint: { name: "Sprint 2026-14", state: "active" } }),
			issue({ key: "DEMO-102", parent: { key: "DEMO-101" } }), // no sprint of its own, rides parent
			issue({ key: "DEMO-9", sprint: { name: "Sprint 2026-15", state: "future" } }),
		]);
		const groups = groupRowsBySprint(rows);
		expect(groups.map((g) => g.name)).toEqual(["Sprint 2026-14", "Sprint 2026-15"]);
		expect(groups[0].rows[0].issue.key).toBe("DEMO-101");
		expect(groups[0].rows[0].children.map((c) => c.key)).toEqual(["DEMO-102"]);
	});
});
