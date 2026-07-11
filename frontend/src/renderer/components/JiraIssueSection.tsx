import { ArrowUpRight } from "lucide-react";
import { useSessionJiraContext, type JiraIssue, type JiraSubtask } from "../hooks/useSessionJiraContext";
import { JiraAdf } from "./JiraAdf";

/**
 * The JIRA ISSUE section rendered at the top of the Summary tab for a session
 * bound to a Jira key. Display-only: the status pill shows the live status (its
 * Move-status action is wired in a later slice); subtasks are shown for context.
 * Renders nothing when the session is not Jira-linked.
 */
export function JiraIssueSection({ sessionId, linked }: { sessionId: string; linked: boolean }) {
	const query = useSessionJiraContext(sessionId, linked);
	if (!linked) return null;

	const data = query.data;
	// While the first fetch is in flight, show a quiet placeholder so the section
	// does not pop in abruptly.
	if (!data && query.isLoading) {
		return (
			<section className="jira-section">
				<JiraEyebrow />
				<div className="jira-card jira-card--lead">
					<p className="inspector-empty">Loading Jira issue…</p>
				</div>
			</section>
		);
	}
	if (!data || !data.linked) return null;

	if (!data.issue) {
		return (
			<section className="jira-section">
				<JiraEyebrow />
				<div className="jira-card jira-card--lead">
					<p className="jira-fetch-error">{data.fetchError || "Couldn't load the Jira issue."}</p>
				</div>
			</section>
		);
	}

	return (
		<section className="jira-section">
			<JiraEyebrow />
			<IssueLead issue={data.issue} />
			{data.issue.description && data.issue.description.length > 0 ? (
				<div className="jira-card">
					<p className="jira-sect-label">Description</p>
					<JiraAdf nodes={data.issue.description} />
				</div>
			) : null}
			{data.issue.subtasks && data.issue.subtasks.length > 0 ? (
				<div className="jira-card">
					<p className="jira-sect-label">Subtasks · {data.issue.subtasks.length}</p>
					{data.issue.subtasks.map((s) => (
						<SubtaskRow key={s.key} subtask={s} />
					))}
				</div>
			) : null}
		</section>
	);
}

function JiraEyebrow() {
	return (
		<div className="jira-eyebrow">
			<span>◈ Jira issue</span>
			<span className="jira-eyebrow__line" />
		</div>
	);
}

function IssueLead({ issue }: { issue: JiraIssue }) {
	return (
		<div className="jira-card jira-card--lead">
			<div className="jira-head">
				<span className="jira-key">{issue.key}</span>
				{issue.type ? (
					<span className="jira-type">
						<span className="jira-type__sq" />
						{issue.type}
					</span>
				) : null}
				{issue.status ? (
					<span className="jira-status" style={statusPillStyle(issue.statusCategory)}>
						{issue.status}
					</span>
				) : null}
			</div>
			{issue.title ? <div className="jira-title">{issue.title}</div> : null}
			{issue.url ? (
				<a className="jira-openlink" href={issue.url} target="_blank" rel="noopener noreferrer">
					Open in Jira
					<ArrowUpRight className="jira-openlink__icon" aria-hidden="true" />
				</a>
			) : null}
			<dl className="jira-meta">
				{issue.assignee ? <MetaRow k="Assignee" v={issue.assignee} /> : null}
				{issue.reporter ? <MetaRow k="Reporter" v={issue.reporter} /> : null}
				{issue.priority ? (
					<MetaRow
						k="Priority"
						v={
							<span className="jira-priority">
								<span className="jira-priority__dot" style={{ background: priorityTone(issue.priority) }} />
								{issue.priority}
							</span>
						}
					/>
				) : null}
				{issue.sprint ? (
					<MetaRow
						k="Sprint"
						v={
							<span className="jira-sprint">
								{issue.sprint.name}
								{issue.sprint.state ? (
									<span className="jira-sprint__badge" data-state={issue.sprint.state}>
										{issue.sprint.state}
									</span>
								) : null}
								{formatSprintDates(issue.sprint.startDate, issue.sprint.endDate) ? (
									<span className="jira-sprint__dates">
										{formatSprintDates(issue.sprint.startDate, issue.sprint.endDate)}
									</span>
								) : null}
							</span>
						}
					/>
				) : null}
			</dl>
		</div>
	);
}

function SubtaskRow({ subtask }: { subtask: JiraSubtask }) {
	const meta = [subtask.title, subtask.type].filter(Boolean).join(" · ");
	return (
		<div className="jira-sub">
			<span className="jira-sub__key">{subtask.key}</span>
			{meta ? <span className="jira-sub__meta">{meta}</span> : <span className="jira-sub__meta" />}
			{subtask.status ? (
				<span className="jira-sub__pill" style={statusPillStyle(subtask.statusCategory)}>
					{subtask.status}
				</span>
			) : null}
		</div>
	);
}

function MetaRow({ k, v }: { k: string; v: React.ReactNode }) {
	return (
		<div className="jira-meta__row">
			<dt className="jira-meta__k">{k}</dt>
			<dd className="jira-meta__v">{v}</dd>
		</div>
	);
}

// statusPillStyle tints the status pill from Jira's status CATEGORY (not the
// free-form name): new → amber (to-do / needs attention), indeterminate → blue
// (in progress), done → green. Mirrors the TimelinePill color-mix treatment.
function statusPillStyle(category?: string): React.CSSProperties {
	const tone = statusTone(category);
	return {
		color: tone,
		background: `color-mix(in srgb, ${tone} 14%, transparent)`,
		borderColor: `color-mix(in srgb, ${tone} 42%, transparent)`,
	};
}

function statusTone(category?: string): string {
	switch (category) {
		case "done":
			return "var(--success)";
		case "indeterminate":
			return "var(--accent)";
		default:
			return "var(--amber)";
	}
}

function priorityTone(priority: string): string {
	const p = priority.toLowerCase();
	if (p.includes("highest") || p.includes("high") || p.includes("critical") || p.includes("blocker"))
		return "var(--red)";
	if (p.includes("low")) return "var(--fg-muted)";
	return "var(--orange)";
}

// formatSprintDates renders "29 Jun – 10 Jul" from ISO start/end, dropping the
// pair when either date is missing/unparseable.
function formatSprintDates(start?: string, end?: string): string {
	const s = parseDate(start);
	const e = parseDate(end);
	if (!s || !e) return "";
	const fmt = (d: Date) => d.toLocaleDateString(undefined, { day: "numeric", month: "short" });
	return `${fmt(s)} – ${fmt(e)}`;
}

function parseDate(iso?: string): Date | null {
	if (!iso) return null;
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? null : d;
}
