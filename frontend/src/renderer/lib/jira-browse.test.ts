import { describe, expect, it } from "vitest";
import type { JiraIssueSummary } from "../hooks/useSessionJiraContext";
import {
	BACKLOG_LABEL,
	buildTree,
	collectTreeContext,
	countTreeNodes,
	emptyResultHint,
	groupBySprint,
	groupTreeBySprint,
	hasUnassigned,
	isEpicIssue,
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

describe("isEpicIssue", () => {
	it("detects Epics by type, case-insensitively", () => {
		expect(isEpicIssue(issue({ key: "E-1", type: "Epic" }))).toBe(true);
		expect(isEpicIssue(issue({ key: "E-2", type: "epic" }))).toBe(true);
		expect(isEpicIssue(issue({ key: "S-1", type: "Story" }))).toBe(false);
		expect(isEpicIssue(issue({ key: "X-1", type: undefined }))).toBe(false);
	});
});

describe("buildTree", () => {
	const epic = issue({ key: "E-1", type: "Epic", title: "Epic" });
	const story = issue({ key: "S-1", type: "Story", parent: { key: "E-1" } });
	const sub = issue({ key: "T-1", type: "Sub-task", parent: { key: "S-1" } });
	const standalone = issue({ key: "B-1", type: "Bug" });

	it("nests the 3-level Epic → Story → Sub-task chain with correct depths", () => {
		const tree = buildTree([epic, story, sub], new Set(["E-1", "S-1", "T-1"]));
		expect(tree.map((n) => n.issue.key)).toEqual(["E-1"]); // only the epic is a root
		expect(tree[0].depth).toBe(0);
		expect(tree[0].children.map((c) => c.issue.key)).toEqual(["S-1"]);
		expect(tree[0].children[0].depth).toBe(1);
		expect(tree[0].children[0].children.map((c) => c.issue.key)).toEqual(["T-1"]);
		expect(tree[0].children[0].children[0].depth).toBe(2);
	});

	it("caps depth at 3 levels — a 4th-level descendant is not nested", () => {
		const deep = issue({ key: "D-1", type: "Sub-task", parent: { key: "T-1" } }); // child of the sub-task
		const tree = buildTree([epic, story, sub, deep], new Set(["E-1", "S-1", "T-1", "D-1"]));
		const subNode = tree[0].children[0].children[0];
		expect(subNode.issue.key).toBe("T-1");
		expect(subNode.children).toEqual([]); // D-1 dropped at the depth cap
	});

	it("marks non-matched nodes as context (dimmed) and matched as not", () => {
		// Only the sub-task matched; its ancestors are context.
		const tree = buildTree([epic, story, sub], new Set(["T-1"]));
		expect(tree[0].isContext).toBe(true); // epic
		expect(tree[0].children[0].isContext).toBe(true); // story
		expect(tree[0].children[0].children[0].isContext).toBe(false); // the match
	});

	it("keeps an issue whose parent is absent as a root, and preserves order", () => {
		const orphanSub = issue({ key: "T-9", type: "Sub-task", parent: { key: "S-999" } });
		const tree = buildTree([orphanSub, standalone], new Set(["T-9", "B-1"]));
		expect(tree.map((n) => n.issue.key)).toEqual(["T-9", "B-1"]);
		expect(tree[0].children).toEqual([]);
	});

	it("guards against a cyclic parent chain (terminates, no duplication)", () => {
		const a = issue({ key: "A", parent: { key: "B" } });
		const b = issue({ key: "B", parent: { key: "A" } });
		// A pure cycle has no root (both parents are present), so the subtree is
		// dropped — the point is it TERMINATES and never duplicates a node.
		const tree = buildTree([a, b], new Set(["A", "B"]));
		expect(countTreeNodes(tree)).toBe(0);
	});
});

describe("countTreeNodes / groupTreeBySprint", () => {
	it("counts a node plus all its descendants", () => {
		const tree = buildTree(
			[
				issue({ key: "E-1", type: "Epic" }),
				issue({ key: "S-1", type: "Story", parent: { key: "E-1" } }),
				issue({ key: "T-1", type: "Sub-task", parent: { key: "S-1" } }),
			],
			new Set(["E-1", "S-1", "T-1"]),
		);
		expect(countTreeNodes(tree)).toBe(3);
	});

	it("groups top-level tree nodes by their sprint; children ride along", () => {
		const tree = buildTree(
			[
				issue({ key: "DEMO-101", sprint: { name: "Sprint 2026-14", state: "active" } }),
				issue({ key: "DEMO-102", parent: { key: "DEMO-101" } }), // rides parent's sprint
				issue({ key: "DEMO-9", sprint: { name: "Sprint 2026-15", state: "future" } }),
			],
			new Set(["DEMO-101", "DEMO-102", "DEMO-9"]),
		);
		const groups = groupTreeBySprint(tree);
		expect(groups.map((g) => g.name)).toEqual(["Sprint 2026-14", "Sprint 2026-15"]);
		expect(groups[0].nodes[0].issue.key).toBe("DEMO-101");
		expect(groups[0].nodes[0].children.map((c) => c.issue.key)).toEqual(["DEMO-102"]);
	});
});

describe("collectTreeContext", () => {
	// A fixture graph: Epic E-1 → Story S-1 → Sub-task T-1 (+ a done sub-task T-2).
	const graph: Record<string, JiraIssueSummary> = {
		"E-1": issue({ key: "E-1", type: "Epic" }),
		"S-1": issue({ key: "S-1", type: "Story", parent: { key: "E-1" } }),
		"T-1": issue({ key: "T-1", type: "Sub-task", parent: { key: "S-1" }, statusCategory: "new" }),
		"T-2": issue({ key: "T-2", type: "Sub-task", parent: { key: "S-1" }, statusCategory: "done" }),
	};

	// A fake fetcher that resolves `parent in (...)` (descent) and `key in (...)`
	// (ascent) against the graph, honoring a `statusCategory != Done` clause.
	function fakeFetch(jqls: string[]): (jql: string) => Promise<JiraIssueSummary[]> {
		return async (jql: string) => {
			jqls.push(jql);
			const hideDone = jql.includes("statusCategory != Done");
			const inList = (clause: string) => {
				const m = jql.match(new RegExp(`${clause} \\(([^)]*)\\)`));
				return m ? new Set(m[1].split(",").map((k) => k.trim())) : null;
			};
			const parents = inList("parent in");
			const keys = inList("key in");
			return Object.values(graph).filter((it) => {
				if (hideDone && it.statusCategory === "done") return false;
				if (parents) return Boolean(it.parent?.key && parents.has(it.parent.key));
				if (keys) return keys.has(it.key);
				return false;
			});
		};
	}

	it("descends to a matched card's subtasks and ascends to its epic", async () => {
		const jqls: string[] = [];
		const context = await collectTreeContext([graph["S-1"]], {}, fakeFetch(jqls));
		const keys = context.map((c) => c.key).sort();
		expect(keys).toEqual(["E-1", "T-1", "T-2"]); // subtasks (down) + epic (up); not S-1 itself
		// Batched, not N+1: one descent query + one ascent query.
		expect(jqls.some((q) => q.includes("parent in (S-1)"))).toBe(true);
		expect(jqls.some((q) => q.includes("key in (E-1)"))).toBe(true);
	});

	it("respects hide-done on descendants (a done subtask is not fetched)", async () => {
		const jqls: string[] = [];
		const context = await collectTreeContext([graph["S-1"]], { hideDone: true }, fakeFetch(jqls));
		expect(context.map((c) => c.key)).toContain("T-1");
		expect(context.map((c) => c.key)).not.toContain("T-2"); // done → excluded
		expect(jqls.some((q) => q.includes("parent in (S-1) AND statusCategory != Done"))).toBe(true);
	});
});

describe("emptyResultHint", () => {
	it("says nothing beyond the basics when there is no query and no filters", () => {
		expect(emptyResultHint({ text: "", projectKey: "PROJ", filtersActive: false })).toBe("No issues match.");
	});

	it("explains that a key lookup found nothing when a number was resolved to a key", () => {
		// The backend turns a bare number + selected project into `key = "PROJ-9999"`,
		// so the honest report is that the key does not exist, not that prose missed.
		expect(emptyResultHint({ text: "9999", projectKey: "PROJ", filtersActive: false })).toBe(
			"No issue PROJ-9999 found.",
		);
	});

	it("explains a full key lookup the same way", () => {
		expect(emptyResultHint({ text: "demo-4", projectKey: "PROJ", filtersActive: false })).toBe(
			"No issue DEMO-4 found.",
		);
	});

	it("says free text is matched from the start of each word", () => {
		expect(emptyResultHint({ text: "upon", projectKey: "PROJ", filtersActive: false })).toBe(
			'No issues match "upon". Words match from the start, so try the beginning of a word.',
		);
	});

	it("mentions active filters as a narrowing cause", () => {
		expect(emptyResultHint({ text: "", projectKey: "PROJ", filtersActive: true })).toBe(
			"No issues match. Filters are narrowing this list.",
		);
	});

	it("does not claim a key lookup when a number has no project to resolve against", () => {
		expect(emptyResultHint({ text: "9999", projectKey: "", filtersActive: false })).toBe(
			'No issues match "9999". Words match from the start, so try the beginning of a word.',
		);
	});
});
