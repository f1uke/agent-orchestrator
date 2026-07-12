import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown, ChevronRight, Loader2, Search } from "lucide-react";
import { useEffect, useState } from "react";
import { JiraProjectPicker } from "./JiraProjectPicker";
import { NewTaskDialog } from "./NewTaskDialog";
import { useJiraSearch, type JiraIssueSummary, type JiraProject } from "../hooks/useSessionJiraContext";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { filterByAssignee, groupBySprint, hasUnassigned, UNASSIGNED, uniqueAssignees } from "../lib/jira-browse";
import { readBrowsePrefs, writeBrowsePrefs } from "../lib/jira-browse-prefs";
import { readLastJiraProject, writeLastJiraProject } from "../lib/jira-last-project";
import { cn } from "../lib/utils";

// Type filter chips (client-side, over the returned results). "Assigned to me"
// from the mockup is intentionally omitted — it needs current-user resolution the
// reused search endpoints don't provide. Matching is substring-on-type so it holds
// up across Jira's type-name variance (e.g. "Sub-task" / "Subtask").
const TYPE_FILTERS: { label: string; match: (type: string) => boolean }[] = [
	{ label: "All types", match: () => true },
	{ label: "Story", match: (t) => t.includes("story") },
	{ label: "Bug", match: (t) => t.includes("bug") },
	{ label: "Sub-task", match: (t) => t.includes("sub") },
	{ label: "Support", match: (t) => t.includes("support") || t.includes("service") },
];

/**
 * Browse Jira — the manual, project-first discovery surface (mockup 02, the last
 * slice of the Jira build). Pick a project from the real project list (remembered
 * across visits), search/pick a Story within it, and "Create session" hands off to
 * the New-task modal (mockup 03) pre-filled with the issue, creating a worker bound
 * to its key. Read-only + one manual create — nothing auto-imports onto the board.
 * Reuses the Slice-4 REST endpoints (`/jira/search`, `/jira/projects`); no new backend.
 */
export function BrowseJiraPage({ projectId }: { projectId: string }) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const [project, setProject] = useState<JiraProject | null>(() => readLastJiraProject());
	const [query, setQuery] = useState("");
	const [debounced, setDebounced] = useState("");
	const [filter, setFilter] = useState(0);
	const [createIssue, setCreateIssue] = useState<JiraIssueSummary | null>(null);
	// View prefs remembered across visits (grouping + assignee); last-project is
	// remembered separately (jira-last-project).
	const initialPrefs = readBrowsePrefs();
	const [groupSprints, setGroupSprints] = useState(initialPrefs.groupBySprint);
	const [assignee, setAssignee] = useState(initialPrefs.assignee);
	const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());

	// Debounce the free-text search so typing doesn't fan out a request per keystroke.
	useEffect(() => {
		const t = setTimeout(() => setDebounced(query), 250);
		return () => clearTimeout(t);
	}, [query]);

	// Persist grouping + assignee so the view returns as the user left it.
	useEffect(() => {
		writeBrowsePrefs({ groupBySprint: groupSprints, assignee });
	}, [groupSprints, assignee]);

	const projectKey = project?.key ?? "";
	// With a project scoped, the search fires even with no text (lists recent issues
	// in that project); typing narrows it. See useJiraSearch's enable rule.
	const { data, isFetching, isError, error } = useJiraSearch(debounced, projectKey, Boolean(projectKey));
	const allResults = data ?? [];

	// Assignee options come from the full loaded set (stable across type/grouping
	// changes). A remembered assignee that isn't present in this project falls back
	// to "all" without hiding everything — but stays in state so returning to a
	// project that has them re-applies the filter.
	const assignees = uniqueAssignees(allResults);
	const unassignedPresent = hasUnassigned(allResults);
	const assigneeValid = assignee === "" || (assignee === UNASSIGNED ? unassignedPresent : assignees.includes(assignee));
	const effectiveAssignee = assigneeValid ? assignee : "";

	const typeFiltered = allResults.filter((issue) => TYPE_FILTERS[filter].match((issue.type ?? "").toLowerCase()));
	const results = filterByAssignee(typeFiltered, effectiveAssignee);
	const groups = groupSprints ? groupBySprint(results) : [];

	const toggleCollapsed = (name: string) => {
		setCollapsed((prev) => {
			const next = new Set(prev);
			if (next.has(name)) next.delete(name);
			else next.add(name);
			return next;
		});
	};

	const selectProject = (next: JiraProject) => {
		setProject(next);
		writeLastJiraProject(next);
		setQuery("");
	};

	const handleCreated = async (sessionId: string) => {
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		void navigate({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId, sessionId } });
	};

	const handleQueued = async () => {
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		void navigate({ to: "/projects/$projectId", params: { projectId } });
	};

	const renderRow = (issue: JiraIssueSummary) => (
		<div key={issue.key} className="jira-browse__row">
			<span className={cn("jira-browse__sq", issueSquareClass(issue.type))} aria-hidden="true" />
			<span className="jira-browse__k">{issue.key}</span>
			<span className="jira-browse__t">{issue.title}</span>
			{issue.assignee ? <span className="jira-browse__assignee">{issue.assignee}</span> : null}
			{issue.status ? (
				<span className="jira-browse__st" style={browseStatusStyle(issue.statusCategory)}>
					{issue.status}
				</span>
			) : null}
			<button type="button" className="jira-browse__create" onClick={() => setCreateIssue(issue)}>
				Create session ▷
			</button>
		</div>
	);

	return (
		<div className="jira-browse">
			<header className="jira-browse__head">
				<h1 className="jira-browse__h1">
					Browse Jira <span className="jira-browse__manual">◈ MANUAL · YOU PICK</span>
				</h1>
				<p className="jira-browse__sub">
					Pick a project, then an issue, and start a worker. Your last project is remembered. Nothing is imported
					automatically.
				</p>
			</header>

			<div className="jira-browse__content">
				<div className="jira-browse__controls">
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
				</div>

				<div className="jira-browse__filters" role="group" aria-label="Filter and group issues">
					{TYPE_FILTERS.map((entry, index) => (
						<button
							key={entry.label}
							type="button"
							aria-pressed={index === filter}
							className={cn("jira-browse__chip", index === filter && "is-active")}
							onClick={() => setFilter(index)}
						>
							{entry.label}
						</button>
					))}
					<span className="jira-browse__filters-gap" aria-hidden="true" />
					<label className="jira-browse__assignee-filter">
						<span className="jira-browse__assignee-label">Assignee</span>
						<select
							value={effectiveAssignee}
							aria-label="Filter by assignee"
							onChange={(event) => setAssignee(event.target.value)}
							disabled={!projectKey}
						>
							<option value="">All assignees</option>
							{unassignedPresent ? <option value={UNASSIGNED}>Unassigned</option> : null}
							{assignees.map((name) => (
								<option key={name} value={name}>
									{name}
								</option>
							))}
						</select>
					</label>
					<button
						type="button"
						aria-pressed={groupSprints}
						className={cn("jira-browse__chip jira-browse__group-toggle", groupSprints && "is-active")}
						onClick={() => setGroupSprints((on) => !on)}
						title="Group issues into sprint sections like the Jira board"
					>
						Group by sprint
					</button>
				</div>

				{!projectKey ? (
					<div className="jira-browse__empty">Pick a project to browse its issues.</div>
				) : (
					<div className="jira-browse__list">
						<div className="jira-browse__lhead">
							<span className="jira-browse__live" aria-hidden="true" />
							MATCHING ISSUES
							<span className="jira-browse__n">
								{project?.name ? `${project.name} (${projectKey})` : projectKey} · {results.length} shown
							</span>
						</div>
						{isError ? (
							<p className="jira-browse__note jira-browse__note--err">
								{error instanceof Error ? error.message : "Couldn't search Jira."}
							</p>
						) : isFetching && allResults.length === 0 ? (
							<p className="jira-browse__note">Searching…</p>
						) : results.length === 0 ? (
							<p className="jira-browse__note">No issues match.</p>
						) : groupSprints ? (
							groups.map((group) => {
								const isCollapsed = collapsed.has(group.name);
								return (
									<div key={group.name} className="jira-browse__sprint">
										<button
											type="button"
											className="jira-browse__sprint-head"
											aria-expanded={!isCollapsed}
											onClick={() => toggleCollapsed(group.name)}
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
												· {group.issues.length} {group.issues.length === 1 ? "work item" : "work items"}
											</span>
										</button>
										{isCollapsed ? null : group.issues.map(renderRow)}
									</div>
								);
							})
						) : (
							results.map(renderRow)
						)}
					</div>
				)}

				<p className="jira-browse__manual-note">
					◈ <b>Manual by design</b> — pick a Story to start a worker tracked by its key. Subtasks show for context in
					the session's Summary; they aren't started on their own. Nothing auto-imports onto the board. Read-only search
					· one manual create.
				</p>
			</div>

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
	);
}

// issueSquareClass tints the leading square by issue type (bug red, sub-task
// purple, support blue; story/task fall through to the default green).
function issueSquareClass(type?: string): string {
	const t = (type ?? "").toLowerCase();
	if (t.includes("bug")) return "is-bug";
	if (t.includes("sub")) return "is-sub";
	if (t.includes("support") || t.includes("service")) return "is-support";
	return "";
}

// browseStatusStyle tints a row's status pill by Jira's status CATEGORY, matching
// the picker/inspector treatment (new → amber, indeterminate → accent, done → success).
function browseStatusStyle(category?: string): React.CSSProperties {
	const tone = category === "done" ? "var(--success)" : category === "indeterminate" ? "var(--accent)" : "var(--amber)";
	return {
		color: tone,
		background: `color-mix(in srgb, ${tone} 14%, transparent)`,
		borderColor: `color-mix(in srgb, ${tone} 42%, transparent)`,
	};
}
