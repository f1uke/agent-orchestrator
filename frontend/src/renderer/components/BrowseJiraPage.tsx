import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown, ChevronRight, Loader2, Plus, Search, Send } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { JiraIssueDetail } from "./JiraIssueDetail";
import { JiraProjectPicker } from "./JiraProjectPicker";
import { NewTaskDialog } from "./NewTaskDialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { SimpleTooltip, TooltipProvider } from "./ui/tooltip";
import {
	type JiraIssueSummary,
	type JiraProject,
	useJiraMyself,
	useJiraSearch,
	useJiraTreeContext,
} from "../hooks/useSessionJiraContext";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import {
	buildTree,
	countTreeNodes,
	emptyResultHint,
	groupTreeBySprint,
	hasUnassigned,
	isEpicIssue,
	type TreeNode,
	UNASSIGNED,
	UNASSIGNED_QUERY,
	uniqueAssignees,
} from "../lib/jira-browse";
import { readBrowsePrefs, readCollapsedNodes, writeBrowsePrefs, writeCollapsedNodes } from "../lib/jira-browse-prefs";
import { readLastJiraProject, writeLastJiraProject } from "../lib/jira-last-project";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { findProjectOrchestrator } from "../types/workspace";
import { cn } from "../lib/utils";

// Type filter chips. Each carries the JQL issue-type name(s) pushed into the
// server-side search (All types = no clause), so a type filter is complete rather
// than pared from a capped page; multiple names cover Jira's type-name variance
// (e.g. "Sub-task" / "Subtask").
const TYPE_FILTERS: { label: string; jql: string[]; tip: string }[] = [
	{ label: "All types", jql: [], tip: "Show every issue type" },
	{ label: "Story", jql: ["Story"], tip: "Show only Stories" },
	{ label: "Bug", jql: ["Bug"], tip: "Show only Bugs" },
	{ label: "Sub-task", jql: ["Sub-task", "Subtask"], tip: "Show only Sub-tasks" },
	{ label: "Support", jql: ["Support", "Service Request"], tip: "Show only Support requests" },
];

const TOAST_MS = 3200;

// Radix Select reserves the empty string (it marks the placeholder), so the
// "no assignee filter" case travels through the listbox under a sentinel and is
// mapped back to "" — the value the search/prefs layer already understands.
const ALL_ASSIGNEES = "__all__";

/**
 * Browse Jira — the manual, project-first discovery surface. Pick a project
 * (remembered across visits), browse its issues nested as an Epic→Story→Sub-task
 * tree (a card's real subtasks are fetched even when they don't match the filter,
 * dimmed as context), highlight your own rows, and act per row: open the read-only
 * detail drawer, start a session, or hand the issue(s) to the project's Orchestrator.
 * Display-only apart from Move-status (in the detail drawer); nothing auto-imports.
 */
export function BrowseJiraPage({ projectId }: { projectId: string }) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	// The project's LIVE orchestrator session (undefined when none is running), the
	// target of "Send to Orchestrator" (Fix 4).
	const orchestrator = findProjectOrchestrator(workspaceQuery.data ?? [], projectId);

	const [project, setProject] = useState<JiraProject | null>(() => readLastJiraProject());
	const [query, setQuery] = useState("");
	const [debounced, setDebounced] = useState("");
	const [filter, setFilter] = useState(0);
	const [createIssue, setCreateIssue] = useState<JiraIssueSummary | null>(null);
	// The key of the issue open in the read-only detail drawer, or null.
	const [detailKey, setDetailKey] = useState<string | null>(null);
	// View prefs remembered across visits.
	const initialPrefs = readBrowsePrefs();
	const [groupSprints, setGroupSprints] = useState(initialPrefs.groupBySprint);
	const [assignee, setAssignee] = useState(initialPrefs.assignee);
	const [hideDone, setHideDone] = useState(initialPrefs.hideDone);
	const [activeSprintOnly, setActiveSprintOnly] = useState(initialPrefs.activeSprintOnly);
	const [advancedMode, setAdvancedMode] = useState(initialPrefs.advancedMode);
	const [advancedJql, setAdvancedJql] = useState(initialPrefs.advancedJql);
	// Sprint-section collapse (by group name, in-memory) + tree-node collapse (by
	// issue key, persisted — Fix 2).
	const [collapsedSprints, setCollapsedSprints] = useState<Set<string>>(() => new Set());
	const [collapsedNodes, setCollapsedNodes] = useState<Set<string>>(() => readCollapsedNodes());
	// Multi-select for batch "Send to Orchestrator" (Fix 4).
	const [selected, setSelected] = useState<Set<string>>(() => new Set());
	const [toast, setToast] = useState<string | null>(null);

	// Debounce the free-text search so typing doesn't fan out a request per keystroke.
	useEffect(() => {
		const t = setTimeout(() => setDebounced(query), 250);
		return () => clearTimeout(t);
	}, [query]);

	// Persist the view prefs + collapse state so the view returns as the user left it.
	useEffect(() => {
		writeBrowsePrefs({ groupBySprint: groupSprints, assignee, hideDone, activeSprintOnly, advancedMode, advancedJql });
	}, [groupSprints, assignee, hideDone, activeSprintOnly, advancedMode, advancedJql]);
	useEffect(() => {
		writeCollapsedNodes(collapsedNodes);
	}, [collapsedNodes]);

	// Auto-dismiss the transient toast.
	useEffect(() => {
		if (!toast) return;
		const t = setTimeout(() => setToast(null), TOAST_MS);
		return () => clearTimeout(t);
	}, [toast]);

	const projectKey = project?.key ?? "";
	const selectedTypes = TYPE_FILTERS[filter].jql;
	// In advanced mode the raw JQL drives the search verbatim and the structured
	// filters are hidden.
	const structuredEnabled = Boolean(projectKey) && !advancedMode;

	// The authenticated Jira account, to highlight the viewer's own rows (Fix 3).
	const me = useJiraMyself(Boolean(projectKey) || advancedMode);
	const myAccountId = me.data?.accountId ?? "";

	// Base fetch (structured mode): project + text + hide-done/active-sprint, NO
	// assignee/type — the source for the assignee dropdown, kept complete across the
	// assignee filter. Shares its request with the results when no assignee/type is set.
	const base = useJiraSearch(debounced, projectKey, structuredEnabled, {
		hideDone,
		activeSprint: activeSprintOnly,
	});
	const baseResults = base.data ?? [];

	const assignees = uniqueAssignees(baseResults);
	const unassignedPresent = hasUnassigned(baseResults);
	const assigneeValid =
		assignee === "" || (assignee === UNASSIGNED ? unassignedPresent : assignees.some((a) => a.name === assignee));
	const effectiveAssignee = assigneeValid ? assignee : "";
	const assigneeQuery =
		effectiveAssignee === ""
			? ""
			: effectiveAssignee === UNASSIGNED
				? UNASSIGNED_QUERY
				: (assignees.find((a) => a.name === effectiveAssignee)?.accountId ?? "");

	// Results fetch (the direct matches).
	const resultsQuery = useJiraSearch(
		advancedMode ? "" : debounced,
		advancedMode ? "" : projectKey,
		advancedMode || Boolean(projectKey),
		advancedMode
			? { jql: advancedJql }
			: { assignee: assigneeQuery, types: selectedTypes, hideDone, activeSprint: activeSprintOnly },
	);
	const results = resultsQuery.data ?? [];

	// Tree-context (Fix 2): the ancestors + descendants of the matches — a card's own
	// (unmatched) subtasks and the parents above them — so the list nests the full
	// Epic→Story→Sub-task tree, not just what matched. Descendants respect hide-done /
	// active-sprint; ancestors are always shown (dimmed). Skipped in advanced mode.
	const treeContext = useJiraTreeContext(results, {
		hideDone,
		activeSprint: activeSprintOnly,
		enabled: !advancedMode && Boolean(projectKey),
	});
	const matchedKeys = new Set(results.map((r) => r.key));
	const contextIssues = advancedMode ? [] : (treeContext.data ?? []).filter((p) => !matchedKeys.has(p.key));
	// Direct matches emphasized; context (ancestors/descendants outside the filter) dimmed.
	const union = [...results, ...contextIssues];
	const tree = buildTree(union, matchedKeys);
	// Grouped by sprint: unwrap epics so stories still group by their OWN sprint
	// (preserving #83) with subtasks nested; the full 3-level tree with epic headers
	// shows when grouping is off. A leaf epic (no children) stays as its own row.
	const groupingRoots = tree.flatMap((n) => (isEpicIssue(n.issue) && n.children.length > 0 ? n.children : [n]));
	const treeGroups = groupSprints ? groupTreeBySprint(groupingRoots) : null;

	// A key → issue map so a selection of keys can be turned back into the issues the
	// Orchestrator message describes.
	const issuesByKey = new Map<string, JiraIssueSummary>();
	for (const it of union) if (!issuesByKey.has(it.key)) issuesByKey.set(it.key, it);

	const isFetching = base.isFetching || resultsQuery.isFetching || treeContext.isFetching;
	const isError = resultsQuery.isError;
	const error = resultsQuery.error;

	// POST a message into the project's orchestrator session — the same daemon path
	// `ao send` uses. The orchestrator (an AI session) reads it and decides whether to
	// spawn; Browse Jira never spawns workers directly.
	const send = useMutation({
		mutationFn: async (message: string) => {
			if (!orchestrator) throw new Error("no-orchestrator");
			const { error: sendErr } = await apiClient.POST("/api/v1/sessions/{sessionId}/send", {
				params: { path: { sessionId: orchestrator.id } },
				body: { message },
			});
			if (sendErr) throw new Error(apiErrorMessage(sendErr, "Couldn't reach the Orchestrator"));
		},
	});

	const toggleSprint = (name: string) =>
		setCollapsedSprints((prev) => {
			const next = new Set(prev);
			if (next.has(name)) next.delete(name);
			else next.add(name);
			return next;
		});

	const toggleNode = (key: string) =>
		setCollapsedNodes((prev) => {
			const next = new Set(prev);
			if (next.has(key)) next.delete(key);
			else next.add(key);
			return next;
		});

	const toggleSelected = (key: string) =>
		setSelected((prev) => {
			const next = new Set(prev);
			if (next.has(key)) next.delete(key);
			else next.add(key);
			return next;
		});

	const sendToOrchestrator = (keys: string[]) => {
		const issues = keys.map((k) => issuesByKey.get(k)).filter((it): it is JiraIssueSummary => Boolean(it));
		if (issues.length === 0) return;
		if (!orchestrator) {
			setToast("Start this project's Orchestrator first, then send.");
			return;
		}
		send.mutate(buildOrchestratorMessage(issues), {
			onSuccess: () => {
				setSelected(new Set());
				setToast(`Sent ${issues.length} issue${issues.length === 1 ? "" : "s"} to the Orchestrator.`);
			},
			onError: (e) =>
				setToast(
					e instanceof Error && e.message !== "no-orchestrator" ? e.message : "Couldn't send to the Orchestrator.",
				),
		});
	};

	const selectProject = (next: JiraProject) => {
		setProject(next);
		writeLastJiraProject(next);
		setQuery("");
		setSelected(new Set());
	};

	const handleCreated = async (sessionId: string) => {
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		void navigate({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId, sessionId } });
	};

	const handleQueued = async () => {
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		void navigate({ to: "/projects/$projectId", params: { projectId } });
	};

	// A collapse/expand twisty for a node with children, or an aligning spacer.
	const renderTwisty = (node: TreeNode, hasChildren: boolean, isCollapsed: boolean): ReactNode =>
		hasChildren ? (
			<SimpleTooltip label={isCollapsed ? "Expand subtasks" : "Collapse subtasks"}>
				<button
					type="button"
					className="jira-browse__twisty"
					aria-label={isCollapsed ? `Expand ${node.issue.key}` : `Collapse ${node.issue.key}`}
					aria-expanded={!isCollapsed}
					onClick={(event) => {
						event.stopPropagation();
						toggleNode(node.issue.key);
					}}
				>
					{isCollapsed ? (
						<ChevronRight className="size-3.5" aria-hidden="true" />
					) : (
						<ChevronDown className="size-3.5" aria-hidden="true" />
					)}
				</button>
			</SimpleTooltip>
		) : (
			<span className="jira-browse__twisty jira-browse__twisty--spacer" aria-hidden="true" />
		);

	// An Epic row: a context-only group header (Fix 5) — no status pill, no create/+,
	// no Send-to-Orchestrator, not batch-selectable. Its children nest beneath it.
	const renderEpicRow = (node: TreeNode, depth: number, hasChildren: boolean, isCollapsed: boolean): ReactNode => {
		const issue = node.issue;
		const childCount = countTreeNodes(node.children);
		return (
			<div
				role="button"
				tabIndex={0}
				aria-label={`Open ${issue.key}`}
				className={cn("jira-browse__row jira-browse__row--epic", node.isContext && "jira-browse__row--context")}
				style={{ paddingLeft: 14 + depth * 22 }}
				onClick={() => setDetailKey(issue.key)}
				onKeyDown={(event) => {
					if (event.key === "Enter" || event.key === " ") {
						event.preventDefault();
						setDetailKey(issue.key);
					}
				}}
			>
				{renderTwisty(node, hasChildren, isCollapsed)}
				<span className="jira-browse__epic-badge" aria-hidden="true">
					EPIC
				</span>
				<span className="jira-browse__k">{issue.key}</span>
				<span className="jira-browse__t">{issue.title}</span>
				{childCount > 0 ? (
					<span className="jira-browse__sprint-count">
						· {childCount} {childCount === 1 ? "item" : "items"}
					</span>
				) : null}
			</div>
		);
	};

	// A startable issue row: opens the detail drawer on click; carries the select
	// checkbox, "You" highlight (Fix 3), Send-to-Orchestrator + "+" actions (Fix 4).
	const renderIssueRow = (node: TreeNode, depth: number, hasChildren: boolean, isCollapsed: boolean): ReactNode => {
		const issue = node.issue;
		const isMe = Boolean(myAccountId) && issue.assigneeAccountId === myAccountId;
		const isSel = selected.has(issue.key);
		return (
			<div
				role="button"
				tabIndex={0}
				aria-label={`Open ${issue.key}`}
				className={cn(
					"jira-browse__row",
					node.isContext && "jira-browse__row--context",
					isMe && "jira-browse__row--me",
					isSel && "jira-browse__row--selected",
				)}
				style={{ paddingLeft: 14 + depth * 22 }}
				onClick={() => setDetailKey(issue.key)}
				onKeyDown={(event) => {
					if (event.key === "Enter" || event.key === " ") {
						event.preventDefault();
						setDetailKey(issue.key);
					}
				}}
			>
				{renderTwisty(node, hasChildren, isCollapsed)}
				<input
					type="checkbox"
					className="jira-browse__check"
					checked={isSel}
					aria-label={`Select ${issue.key} for Send to Orchestrator`}
					onClick={(event) => event.stopPropagation()}
					onChange={(event) => {
						event.stopPropagation();
						toggleSelected(issue.key);
					}}
				/>
				<span className={cn("jira-browse__sq", issueSquareClass(issue.type))} aria-hidden="true" />
				<span className="jira-browse__k">{issue.key}</span>
				<span className="jira-browse__t">{issue.title}</span>
				{node.isContext ? <span className="jira-browse__context-tag">context</span> : null}
				{isMe ? (
					<span className="jira-browse__you" title={issue.assignee || "Assigned to you"}>
						You
					</span>
				) : issue.assignee ? (
					<span className="jira-browse__assignee">{issue.assignee}</span>
				) : null}
				{issue.status ? (
					<span className="jira-browse__st" style={browseStatusStyle(issue.statusCategory)}>
						{issue.status}
					</span>
				) : null}
				<SimpleTooltip
					label={orchestrator ? "Send this issue to the Orchestrator" : "Start this project's Orchestrator first"}
				>
					{/* Wrapped in a span so the tooltip still shows when the button is
					    disabled (no live Orchestrator) — disabled buttons swallow hover. */}
					<span className="jira-browse__tipwrap">
						<button
							type="button"
							className="jira-browse__act jira-browse__act--send"
							aria-label={`Send ${issue.key} to the Orchestrator`}
							disabled={!orchestrator || send.isPending}
							onClick={(event) => {
								event.stopPropagation();
								sendToOrchestrator([issue.key]);
							}}
						>
							<Send className="size-3.5" aria-hidden="true" />
							<span className="jira-browse__act-label">Send</span>
						</button>
					</span>
				</SimpleTooltip>
				<SimpleTooltip label="Start a session for this issue">
					<button
						type="button"
						className="jira-browse__act jira-browse__act--add"
						aria-label={`Create a session for ${issue.key}`}
						onClick={(event) => {
							event.stopPropagation();
							setCreateIssue(issue);
						}}
					>
						<Plus className="size-3.5" aria-hidden="true" />
					</button>
				</SimpleTooltip>
			</div>
		);
	};

	// Render a node and (unless collapsed) its children. `depth` is the RENDER depth
	// (0 at each list/section root), so grouped mode can promote stories out of epics
	// and still indent correctly.
	const renderNode = (node: TreeNode, depth: number): ReactNode => {
		const hasChildren = node.children.length > 0;
		const isCollapsed = collapsedNodes.has(node.issue.key);
		return (
			<div key={node.issue.key} className="jira-browse__treenode">
				{isEpicIssue(node.issue)
					? renderEpicRow(node, depth, hasChildren, isCollapsed)
					: renderIssueRow(node, depth, hasChildren, isCollapsed)}
				{hasChildren && !isCollapsed ? node.children.map((child) => renderNode(child, depth + 1)) : null}
			</div>
		);
	};

	return (
		<TooltipProvider delayDuration={0}>
			<div className="jira-browse">
				<header className="jira-browse__head">
					<h1 className="jira-browse__h1">
						Browse Jira <span className="jira-browse__manual">◈ MANUAL · YOU PICK</span>
					</h1>
					<p className="jira-browse__sub">
						Pick a project, then an issue, and start a worker — or hand issues to the Orchestrator. Your last project is
						remembered. Nothing is imported automatically.
					</p>
				</header>

				<div className="jira-browse__content">
					<div className="jira-browse__controls">
						{advancedMode ? (
							<div className="jira-browse__search jira-browse__jql">
								<span className="jira-browse__jql-tag" aria-hidden="true">
									JQL
								</span>
								<input
									value={advancedJql}
									placeholder="project = PROJ AND assignee = currentUser() ORDER BY updated DESC"
									autoComplete="off"
									autoCapitalize="none"
									spellCheck={false}
									aria-label="Advanced JQL query"
									onChange={(event) => setAdvancedJql(event.target.value)}
								/>
								{isFetching ? <Loader2 className="jira-browse__spin size-3.5 animate-spin" aria-hidden="true" /> : null}
							</div>
						) : (
							<>
								<JiraProjectPicker value={project} onSelect={selectProject} lastUsedKey={project?.key} />
								<div className="jira-browse__search">
									<Search className="jira-browse__mag size-3.5" aria-hidden="true" />
									<input
										value={query}
										disabled={!projectKey}
										placeholder={projectKey ? `Search issues in ${projectKey}…` : "Pick a project first"}
										autoComplete="off"
										autoCapitalize="none"
										spellCheck={false}
										aria-label="Search issues"
										onChange={(event) => setQuery(event.target.value)}
									/>
									{isFetching && projectKey ? (
										<Loader2 className="jira-browse__spin size-3.5 animate-spin" aria-hidden="true" />
									) : null}
								</div>
							</>
						)}
					</div>

					<div className="jira-browse__filters" role="group" aria-label="Filter and group issues">
						{advancedMode ? (
							<>
								<span className="jira-browse__advanced-note">
									Advanced JQL drives the search — the structured filters are off.
								</span>
								<span className="jira-browse__filters-gap" aria-hidden="true" />
								<SimpleTooltip label="Return to the structured filters">
									<button type="button" className="jira-browse__chip" onClick={() => setAdvancedMode(false)}>
										← Back to filters
									</button>
								</SimpleTooltip>
							</>
						) : (
							<>
								{TYPE_FILTERS.map((entry, index) => (
									<SimpleTooltip key={entry.label} label={entry.tip}>
										<button
											type="button"
											aria-pressed={index === filter}
											className={cn("jira-browse__chip", index === filter && "is-active")}
											onClick={() => setFilter(index)}
										>
											{entry.label}
										</button>
									</SimpleTooltip>
								))}
								<span className="jira-browse__filters-gap" aria-hidden="true" />
								<span className="jira-browse__assignee-filter">
									<span className="jira-browse__assignee-label">Assignee</span>
									<Select
										value={effectiveAssignee === "" ? ALL_ASSIGNEES : effectiveAssignee}
										onValueChange={(v) => setAssignee(v === ALL_ASSIGNEES ? "" : v)}
										disabled={!projectKey}
									>
										<SelectTrigger className="jira-browse__assignee-trigger" aria-label="Filter by assignee">
											<SelectValue />
										</SelectTrigger>
										<SelectContent className="jira-browse__assignee-list">
											<SelectItem value={ALL_ASSIGNEES}>All assignees</SelectItem>
											{unassignedPresent ? <SelectItem value={UNASSIGNED}>Unassigned</SelectItem> : null}
											{assignees.map((a) => (
												<SelectItem key={a.name} value={a.name}>
													{a.name}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								</span>
								<SimpleTooltip label="Hide issues that are Done">
									<button
										type="button"
										aria-pressed={hideDone}
										className={cn("jira-browse__chip", hideDone && "is-active")}
										onClick={() => setHideDone((on) => !on)}
									>
										Hide done
									</button>
								</SimpleTooltip>
								<SimpleTooltip label="Show only issues in an open sprint">
									<button
										type="button"
										aria-pressed={activeSprintOnly}
										className={cn("jira-browse__chip", activeSprintOnly && "is-active")}
										onClick={() => setActiveSprintOnly((on) => !on)}
									>
										Active sprint
									</button>
								</SimpleTooltip>
							</>
						)}
						<SimpleTooltip label="Group issues into sprint sections">
							<button
								type="button"
								aria-pressed={groupSprints}
								className={cn("jira-browse__chip jira-browse__group-toggle", groupSprints && "is-active")}
								onClick={() => setGroupSprints((on) => !on)}
							>
								Group by sprint
							</button>
						</SimpleTooltip>
						{advancedMode ? null : (
							<SimpleTooltip label="Search with raw JQL instead of the filters">
								<button type="button" className="jira-browse__chip" onClick={() => setAdvancedMode(true)}>
									Advanced JQL
								</button>
							</SimpleTooltip>
						)}
					</div>

					{selected.size > 0 ? (
						<div className="jira-browse__batchbar" role="group" aria-label="Batch actions">
							<span className="jira-browse__batchbar-n">{selected.size} selected</span>
							<SimpleTooltip
								label={
									orchestrator
										? `Send ${selected.size} selected to the Orchestrator`
										: "Start this project's Orchestrator first"
								}
							>
								<span className="jira-browse__tipwrap">
									<button
										type="button"
										className="jira-browse__batchbar-send"
										disabled={!orchestrator || send.isPending}
										onClick={() => sendToOrchestrator([...selected])}
										aria-label={`Send ${selected.size} selected to the Orchestrator`}
									>
										<Send className="size-3.5" aria-hidden="true" />
										{send.isPending ? "Sending…" : "Send"}
									</button>
								</span>
							</SimpleTooltip>
							<SimpleTooltip label="Clear the selection">
								<button type="button" className="jira-browse__batchbar-clear" onClick={() => setSelected(new Set())}>
									Clear
								</button>
							</SimpleTooltip>
							{!orchestrator ? (
								<span className="jira-browse__batchbar-warn">Start this project's Orchestrator first</span>
							) : null}
						</div>
					) : null}

					{toast ? (
						<div className="jira-browse__toast" role="status">
							{toast}
						</div>
					) : null}

					{!advancedMode && !projectKey ? (
						<div className="jira-browse__empty">Pick a project to browse its issues.</div>
					) : (
						<div className="jira-browse__list">
							<div className="jira-browse__lhead">
								<span className="jira-browse__live" aria-hidden="true" />
								MATCHING ISSUES
								<span className="jira-browse__n">
									{advancedMode ? "Advanced JQL" : project?.name ? `${project.name} (${projectKey})` : projectKey} ·{" "}
									{results.length} shown
								</span>
							</div>
							{isError ? (
								<p className="jira-browse__note jira-browse__note--err">
									{error instanceof Error ? error.message : "Couldn't search Jira."}
								</p>
							) : advancedMode && advancedJql.trim().length === 0 ? (
								<p className="jira-browse__note">Type a JQL query to search.</p>
							) : isFetching && results.length === 0 ? (
								<p className="jira-browse__note">Searching…</p>
							) : results.length === 0 ? (
								<p className="jira-browse__note">
									{advancedMode
										? "No issues match."
										: emptyResultHint({
												text: debounced,
												projectKey,
												filtersActive:
													effectiveAssignee !== "" || selectedTypes.length > 0 || hideDone || activeSprintOnly,
											})}
								</p>
							) : treeGroups ? (
								treeGroups.map((group) => {
									const isCollapsed = collapsedSprints.has(group.name);
									const count = countTreeNodes(group.nodes);
									return (
										<div key={group.name} className="jira-browse__sprint">
											<SimpleTooltip label={isCollapsed ? "Expand this sprint" : "Collapse this sprint"}>
												<button
													type="button"
													className="jira-browse__sprint-head"
													aria-expanded={!isCollapsed}
													onClick={() => toggleSprint(group.name)}
												>
													{isCollapsed ? (
														<ChevronRight className="size-3.5" aria-hidden="true" />
													) : (
														<ChevronDown className="size-3.5" aria-hidden="true" />
													)}
													<span className="jira-browse__sprint-name">{group.name}</span>
													{group.state === "active" && !group.isBacklog ? (
														<span className="jira-browse__sprint-active">active</span>
													) : null}
													<span className="jira-browse__sprint-count">
														· {count} {count === 1 ? "work item" : "work items"}
													</span>
												</button>
											</SimpleTooltip>
											{isCollapsed ? null : group.nodes.map((node) => renderNode(node, 0))}
										</div>
									);
								})
							) : (
								tree.map((node) => renderNode(node, 0))
							)}
						</div>
					)}

					<p className="jira-browse__manual-note">
						◈ <b>Manual by design</b> — open a card for detail, start a worker with <b>+</b>, or{" "}
						<b>Send to Orchestrator</b> to let it decide. Epics are context-only group headers; subtasks nest under
						their story. Nothing auto-imports. Read-only search · Move-status is the only write.
					</p>
				</div>

				<JiraIssueDetail
					issueKey={detailKey}
					open={Boolean(detailKey)}
					onOpenChange={(open) => {
						if (!open) setDetailKey(null);
					}}
					onCreateSession={(issue) => {
						setDetailKey(null);
						setCreateIssue(issue);
					}}
				/>

				<NewTaskDialog
					open={Boolean(createIssue)}
					projectId={projectId}
					initialIssue={createIssue}
					onCreated={(sessionId) => void handleCreated(sessionId)}
					onQueued={() => void handleQueued()}
					onOpenChange={(open) => {
						if (!open) setCreateIssue(null);
					}}
				/>
			</div>
		</TooltipProvider>
	);
}

// buildOrchestratorMessage frames the selected issues as a clear, actionable request
// for the orchestrator session (which decides whether to spawn). Carries key + summary
// + status + type per issue. The trim below is about keeping the ask readable for a
// human, not about the daemon's send cap (128 KiB) — a long selection would fit.
function buildOrchestratorMessage(issues: JiraIssueSummary[]): string {
	const lines = issues.map(
		(i) => `- ${i.key} (${i.title ?? ""})${i.status ? ` [${i.status}]` : ""}${i.type ? ` · ${i.type}` : ""}`,
	);
	const header =
		issues.length === 1
			? "Please start work on this Jira issue:"
			: `Please start work on these ${issues.length} Jira issues:`;
	const footer =
		"\n\nEach is a jira:<KEY> task — decide whether to spawn a worker for each (I'm not spawning directly).";
	let body = `${header}\n${lines.join("\n")}${footer}`;
	if (body.length > 3900) body = `${body.slice(0, 3860)}\n… (list truncated)${footer}`;
	return body;
}

// issueSquareClass tints the leading square by issue type (bug red, sub-task purple,
// support blue; story/task fall through to the default green).
function issueSquareClass(type?: string): string {
	const t = (type ?? "").toLowerCase();
	if (t.includes("bug")) return "is-bug";
	if (t.includes("sub")) return "is-sub";
	if (t.includes("support") || t.includes("service")) return "is-support";
	return "";
}

// browseStatusStyle tints a row's status pill by Jira's status CATEGORY, matching the
// picker/inspector treatment (new → amber, indeterminate → accent, done → success).
function browseStatusStyle(category?: string): React.CSSProperties {
	const tone = category === "done" ? "var(--success)" : category === "indeterminate" ? "var(--accent)" : "var(--amber)";
	return {
		color: tone,
		background: `color-mix(in srgb, ${tone} 14%, transparent)`,
		borderColor: `color-mix(in srgb, ${tone} 42%, transparent)`,
	};
}
