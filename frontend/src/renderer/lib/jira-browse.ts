import type { JiraIssueSummary } from "../hooks/useSessionJiraContext";

// Pure helpers for the Browse Jira view: deriving the assignee dropdown options +
// sprint grouping over the loaded result set. Assignee and type filtering itself
// is pushed into the server-side JQL (so a filtered set is complete, not pared
// from a capped page) — these helpers only shape what the UI renders. Kept here so
// the semantics stay unit-tested in one place.

/** Sentinel assignee value (UI + persistence) meaning "only unassigned issues". */
export const UNASSIGNED = "__unassigned__";

/** Server-side query token for the unassigned filter; buildJQL maps it to
 *  `assignee is EMPTY`. (A real accountId is never this word.) */
export const UNASSIGNED_QUERY = "unassigned";

/** Header label for issues that belong to no sprint. */
export const BACKLOG_LABEL = "No sprint";

/** One assignee dropdown option: the human display name plus the opaque Jira
 *  accountId the server-side filter needs. */
export type AssigneeOption = { name: string; accountId: string };

export type SprintGroup = {
	/** Sprint name, or BACKLOG_LABEL for the no-sprint group. */
	name: string;
	/** Sprint state when known (active | future | closed). */
	state?: string;
	/** True for the no-sprint catch-all group (always sorted last). */
	isBacklog: boolean;
	issues: JiraIssueSummary[];
};

function assigneeOf(issue: JiraIssueSummary): string {
	return (issue.assignee ?? "").trim();
}

/**
 * Sorted, unique assignee options (display name + accountId) present in the set,
 * for the dropdown. Derived from the UNFILTERED base fetch so the list stays
 * complete regardless of the active assignee filter; the accountId is what the
 * server-side JQL filter is keyed on. The first accountId seen for a name wins.
 */
export function uniqueAssignees(issues: JiraIssueSummary[]): AssigneeOption[] {
	const byName = new Map<string, string>(); // display name → accountId
	for (const issue of issues) {
		const name = assigneeOf(issue);
		if (name && !byName.has(name)) byName.set(name, (issue.assigneeAccountId ?? "").trim());
	}
	return [...byName.entries()]
		.map(([name, accountId]) => ({ name, accountId }))
		.sort((a, b) => a.name.localeCompare(b.name));
}

/** Whether any issue in the set is unassigned. */
export function hasUnassigned(issues: JiraIssueSummary[]): boolean {
	return issues.some((issue) => !assigneeOf(issue));
}

type SprintBucket<T> = { name: string; state?: string; isBacklog: boolean; items: T[] };

// groupSprint buckets items by a sprint accessor: named sprints sorted numeric-aware
// ("Sprint 2026-14" before "…-15"), the no-sprint backlog always last, order within
// a bucket preserved (the search's own ordering). Shared by the issue- and
// hierarchy-row grouping so the sort/backlog rules live in one place.
function groupSprint<T>(items: T[], sprintOf: (item: T) => JiraIssueSummary["sprint"]): SprintBucket<T>[] {
	const named = new Map<string, SprintBucket<T>>();
	const backlog: T[] = [];
	for (const item of items) {
		const sprint = sprintOf(item);
		const name = sprint?.name?.trim();
		if (!name) {
			backlog.push(item);
			continue;
		}
		let bucket = named.get(name);
		if (!bucket) {
			bucket = { name, state: sprint?.state, isBacklog: false, items: [] };
			named.set(name, bucket);
		}
		bucket.items.push(item);
	}
	const buckets = [...named.values()].sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
	if (backlog.length > 0) {
		buckets.push({ name: BACKLOG_LABEL, isBacklog: true, items: backlog });
	}
	return buckets;
}

/** Group issues by sprint name (see groupSprint for the ordering rules). */
export function groupBySprint(issues: JiraIssueSummary[]): SprintGroup[] {
	return groupSprint(issues, (i) => i.sprint).map((b) => ({
		name: b.name,
		state: b.state,
		isBacklog: b.isBacklog,
		issues: b.items,
	}));
}

/** One list row: a top-level issue plus the subtasks (of it) present in the set,
 *  nested beneath it — like the Jira backlog. */
export type HierarchyRow = {
	issue: JiraIssueSummary;
	children: JiraIssueSummary[];
	/** True when this row's issue is a parent pulled in only for CONTEXT (its own
	 *  assignee may differ from an active filter) — rendered dimmed, not a match. */
	isContext: boolean;
};

/**
 * Nest subtasks under their parent (Image #37, not a flat list). A subtask whose
 * parent is present in the set nests beneath it; a subtask whose parent is absent
 * stays top-level (an orphan, rendered with a subtask marker). `contextKeys` marks
 * parents pulled in only for context. Top-level order and child order are preserved.
 */
export function buildHierarchy(issues: JiraIssueSummary[], contextKeys?: ReadonlySet<string>): HierarchyRow[] {
	const present = new Set(issues.map((i) => i.key));
	const childrenOf = new Map<string, JiraIssueSummary[]>();
	const topLevel: JiraIssueSummary[] = [];
	for (const issue of issues) {
		const parentKey = issue.parent?.key;
		if (parentKey && parentKey !== issue.key && present.has(parentKey)) {
			const arr = childrenOf.get(parentKey);
			if (arr) arr.push(issue);
			else childrenOf.set(parentKey, [issue]);
		} else {
			topLevel.push(issue);
		}
	}
	return topLevel.map((issue) => ({
		issue,
		children: childrenOf.get(issue.key) ?? [],
		isContext: contextKeys?.has(issue.key) ?? false,
	}));
}

/** Parent keys referenced by the set's rows but not present in it — the parents to
 *  fetch for context so the hierarchy stays intact under an assignee filter. */
export function missingParentKeys(issues: JiraIssueSummary[]): string[] {
	const present = new Set(issues.map((i) => i.key));
	const missing = new Set<string>();
	for (const issue of issues) {
		const pk = issue.parent?.key;
		if (pk && !present.has(pk)) missing.add(pk);
	}
	return [...missing].sort();
}

export type SprintRowGroup = { name: string; state?: string; isBacklog: boolean; rows: HierarchyRow[] };

/** Group hierarchy rows by their top-level issue's sprint — children ride with their
 *  parent even across sprints — using the same ordering as groupBySprint. */
export function groupRowsBySprint(rows: HierarchyRow[]): SprintRowGroup[] {
	return groupSprint(rows, (r) => r.issue.sprint).map((b) => ({
		name: b.name,
		state: b.state,
		isBacklog: b.isBacklog,
		rows: b.items,
	}));
}
