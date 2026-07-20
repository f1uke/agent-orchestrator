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

// ── 3-level tree (Epic → Story/Task → Sub-task) ────────────────────────────────
// #92 nested only filter-matched issues, so a card's own (unmatched) subtasks were
// invisible in the list. The tree below nests the FULL Epic→Story→Sub-task chain
// (fetched level-by-level, see useJiraTreeContext), capped at 3 levels like the Jira
// backlog. Issues pulled in for context (outside the active filter) are marked so the
// UI can dim them; matched issues stay emphasized.

/** Max rendered tree depth: Epic (0) → Story/Task (1) → Sub-task (2). */
export const MAX_TREE_DEPTH = 3;

/** A node in the Browse Jira issue tree. */
export type TreeNode = {
	issue: JiraIssueSummary;
	children: TreeNode[];
	/** 0-based depth (0 = top level). Never exceeds MAX_TREE_DEPTH - 1. */
	depth: number;
	/** True when the issue was pulled in only for CONTEXT — an ancestor/descendant
	 *  outside the active filter — so the UI dims it (it is not a direct match). */
	isContext: boolean;
};

/** True when the issue is an Epic (a container/group header, per Fix 5 — no status
 *  pill, no start/create actions). */
export function isEpicIssue(issue: JiraIssueSummary): boolean {
	return (issue.type ?? "").trim().toLowerCase() === "epic";
}

/**
 * Build the Epic→Story→Sub-task tree from a flat issue set (the matched results plus
 * the ancestors/descendants fetched for context). Nodes nest by the `parent` chain;
 * a node whose parent is absent from the set is a root. `matchedKeys` are the direct
 * search matches — every other node is context (dimmed). Depth is capped at
 * MAX_TREE_DEPTH so we never render deeper than the 3 Jira hierarchy levels. Root and
 * child order follow input order; a cycle guard keeps a malformed parent chain safe.
 */
export function buildTree(issues: JiraIssueSummary[], matchedKeys: ReadonlySet<string>): TreeNode[] {
	const byKey = new Map<string, JiraIssueSummary>();
	for (const issue of issues) if (!byKey.has(issue.key)) byKey.set(issue.key, issue);

	const childrenOf = new Map<string, JiraIssueSummary[]>();
	const roots: JiraIssueSummary[] = [];
	for (const issue of byKey.values()) {
		const parentKey = issue.parent?.key;
		if (parentKey && parentKey !== issue.key && byKey.has(parentKey)) {
			const arr = childrenOf.get(parentKey);
			if (arr) arr.push(issue);
			else childrenOf.set(parentKey, [issue]);
		} else {
			roots.push(issue);
		}
	}

	const build = (issue: JiraIssueSummary, depth: number, seen: Set<string>): TreeNode => {
		seen.add(issue.key);
		// Stop at the depth cap, and guard against a cyclic parent chain.
		const kids = depth + 1 < MAX_TREE_DEPTH ? (childrenOf.get(issue.key) ?? []).filter((c) => !seen.has(c.key)) : [];
		return {
			issue,
			depth,
			isContext: !matchedKeys.has(issue.key),
			children: kids.map((c) => build(c, depth + 1, seen)),
		};
	};
	return roots.map((r) => build(r, 0, new Set()));
}

/** Count a tree node plus all its descendants (for the sprint-section work-item count). */
export function countTreeNodes(nodes: TreeNode[]): number {
	return nodes.reduce((n, node) => n + 1 + countTreeNodes(node.children), 0);
}

export type SprintTreeGroup = { name: string; state?: string; isBacklog: boolean; nodes: TreeNode[] };

/** Group top-level tree nodes by their sprint (children ride with their root), using
 *  the same ordering as groupBySprint. */
export function groupTreeBySprint(nodes: TreeNode[]): SprintTreeGroup[] {
	return groupSprint(nodes, (n) => n.issue.sprint).map((b) => ({
		name: b.name,
		state: b.state,
		isBacklog: b.isBacklog,
		nodes: b.items,
	}));
}

// ── Batch tree-context fetch (descendants + ancestors) ─────────────────────────
// #92 only nested filter-MATCHED issues, so a card's unmatched subtasks were
// invisible. collectTreeContext fetches the surrounding Epic→Story→Sub-task tree:
// DESCENDANTS (children then grandchildren, ≤2 steps for the 3-level cap) and
// ANCESTORS (parent then grandparent up to the Epic). Each step is ONE batched JQL
// (`parent in (…)` / `key in (…)`, chunked to bound the query length) — not N+1.
// Descendants respect hide-done / active-sprint (a hidden-done child stays hidden);
// ancestors do NOT (an Epic may be done / in another sprint but still heads the tree).
// A seen-set dedups and guards cycles.

/** Split keys into chunks so a single `parent in (…)`/`key in (…)` clause never
 *  grows past Jira's JQL length budget on a wide level. */
function chunk<T>(items: T[], size: number): T[][] {
	const out: T[][] = [];
	for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size));
	return out;
}

const TREE_JQL_BATCH = 50;

/** Parent keys referenced by the issues that aren't already seen — the next ascent
 *  level to fetch. */
function unseenParentKeys(issues: JiraIssueSummary[], seen: ReadonlySet<string>): string[] {
	const out = new Set<string>();
	for (const issue of issues) {
		const pk = issue.parent?.key;
		if (pk && !seen.has(pk)) out.add(pk);
	}
	return [...out].sort();
}

async function fetchByKeysInBatches(
	keys: string[],
	clause: (keyList: string) => string,
	order: string,
	fetchByJql: (jql: string) => Promise<JiraIssueSummary[]>,
): Promise<JiraIssueSummary[]> {
	const batches = await Promise.all(
		chunk(keys, TREE_JQL_BATCH).map((batch) => fetchByJql(`${clause(batch.join(", "))} ${order}`)),
	);
	return batches.flat();
}

/**
 * Fetch the issues surrounding `roots` in the Epic→Story→Sub-task tree — the
 * ancestors + descendants #92 left out — using batched JQL. `fetchByJql` runs one
 * raw-JQL search (injected so this stays unit-testable). Returns only the CONTEXT
 * issues (never the roots). Descent applies the hide-done/active-sprint clauses;
 * ascent does not. Bounded to MAX_TREE_DEPTH-1 steps each way.
 */
export async function collectTreeContext(
	roots: JiraIssueSummary[],
	opts: { hideDone?: boolean; activeSprint?: boolean },
	fetchByJql: (jql: string) => Promise<JiraIssueSummary[]>,
): Promise<JiraIssueSummary[]> {
	const seen = new Set(roots.map((r) => r.key));
	const context: JiraIssueSummary[] = [];
	const descentFilter =
		(opts.hideDone ? " AND statusCategory != Done" : "") + (opts.activeSprint ? " AND sprint in openSprints()" : "");

	// DESCENT: children, then grandchildren (respect the toggles).
	let frontier = roots.map((r) => r.key);
	for (let step = 0; step < MAX_TREE_DEPTH - 1 && frontier.length > 0; step += 1) {
		const rows = await fetchByKeysInBatches(
			frontier,
			(keyList) => `parent in (${keyList})${descentFilter}`,
			"ORDER BY created ASC",
			fetchByJql,
		);
		const fresh = rows.filter((r) => !seen.has(r.key));
		if (fresh.length === 0) break;
		fresh.forEach((r) => seen.add(r.key));
		context.push(...fresh);
		frontier = fresh.map((r) => r.key);
	}

	// ASCENT: parents up to the Epic (no toggle filter — a context ancestor always shows).
	let pending: JiraIssueSummary[] = [...roots, ...context];
	for (let step = 0; step < MAX_TREE_DEPTH - 1; step += 1) {
		const missing = unseenParentKeys(pending, seen);
		if (missing.length === 0) break;
		const rows = await fetchByKeysInBatches(
			missing,
			(keyList) => `key in (${keyList})`,
			"ORDER BY updated DESC",
			fetchByJql,
		);
		const fresh = rows.filter((r) => !seen.has(r.key));
		if (fresh.length === 0) break;
		fresh.forEach((r) => seen.add(r.key));
		context.push(...fresh);
		pending = fresh;
	}

	return context;
}

/** Shape of a search that came back empty, for explaining WHY. */
export interface EmptyResultContext {
	/** The search box text, as sent. */
	text: string;
	/** The selected project key, or "" when browsing without one. */
	projectKey: string;
	/** Whether any narrowing filter (assignee/type/hide-done/sprint) is active. */
	filtersActive: boolean;
}

/** A full Jira key typed on its own (PROJECT-123). Mirrors the server's fullKeyRE. */
const FULL_KEY = /^[A-Z][A-Z0-9]+-\d+$/;
/** Just the number half of a key — resolvable only with a project selected. */
const BARE_NUMBER = /^\d+$/;

/**
 * Explains an empty result the way the query was ACTUALLY run, so the human is not
 * left guessing. Deliberately limited to what the server-side classification can
 * support (see buildJQL): a key lookup either found the issue or it does not exist,
 * while free text is prefix-matched per word and cannot match mid-word. Nothing
 * here is inferred beyond that.
 */
export function emptyResultHint({ text, projectKey, filtersActive }: EmptyResultContext): string {
	const trimmed = text.trim();
	const upper = trimmed.toUpperCase();
	const key = FULL_KEY.test(upper)
		? upper
		: projectKey && BARE_NUMBER.test(trimmed)
			? `${projectKey.toUpperCase()}-${trimmed}`
			: "";
	if (key) return `No issue ${key} found.`;
	if (trimmed) {
		return `No issues match "${trimmed}". Words match from the start, so try the beginning of a word.`;
	}
	return filtersActive ? "No issues match. Filters are narrowing this list." : "No issues match.";
}
