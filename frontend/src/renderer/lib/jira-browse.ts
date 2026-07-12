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

/**
 * Group issues by sprint name. Named sprints are sorted by name (numeric-aware, so
 * "Sprint 2026-14" precedes "Sprint 2026-15"); the no-sprint "backlog" group is
 * always last. Order within a group is preserved (the search's own ordering).
 */
export function groupBySprint(issues: JiraIssueSummary[]): SprintGroup[] {
	const named = new Map<string, SprintGroup>();
	const backlog: JiraIssueSummary[] = [];
	for (const issue of issues) {
		const name = issue.sprint?.name?.trim();
		if (!name) {
			backlog.push(issue);
			continue;
		}
		let group = named.get(name);
		if (!group) {
			group = { name, state: issue.sprint?.state, isBacklog: false, issues: [] };
			named.set(name, group);
		}
		group.issues.push(issue);
	}
	const groups = [...named.values()].sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
	if (backlog.length > 0) {
		groups.push({ name: BACKLOG_LABEL, isBacklog: true, issues: backlog });
	}
	return groups;
}
