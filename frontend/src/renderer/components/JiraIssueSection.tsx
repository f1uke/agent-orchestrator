import { useState } from "react";
import { ArrowUpRight, ChevronDown, Plus } from "lucide-react";
import { useSessionJiraContext, useUnlinkJira, type JiraIssue, type JiraSubtask } from "../hooks/useSessionJiraContext";
import { JiraAdf } from "./JiraAdf";
import { JiraLinkDialog } from "./JiraLinkDialog";
import { JiraMoveStatusDialog } from "./JiraMoveStatusDialog";

/**
 * The JIRA ISSUE section rendered at the top of the Summary tab for a session
 * bound to a Jira key. Display-only: the status pill shows the live status (its
 * Move-status action is wired in a later slice); subtasks are shown for context.
 * Renders nothing when the session is not Jira-linked.
 */
export function JiraIssueSection({ sessionId, linked }: { sessionId: string; linked: boolean }) {
	const query = useSessionJiraContext(sessionId, linked);
	// An unlinked session still gets an entry point to attach a Jira issue after
	// the fact (e.g. a session created before the Jira integration existed).
	if (!linked) return <JiraLinkPrompt sessionId={sessionId} />;

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
			<IssueLead sessionId={sessionId} issue={data.issue} />
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

// JiraLinkPrompt is shown on the Summary tab of a session that is NOT yet bound to
// a Jira issue — a quiet entry point to attach one after the fact (opens the same
// live search picker as the New-task modal).
function JiraLinkPrompt({ sessionId }: { sessionId: string }) {
	const [open, setOpen] = useState(false);
	return (
		<section className="jira-section">
			<JiraEyebrow />
			<div className="jira-card jira-link-prompt">
				<p className="jira-link-prompt__text">No Jira issue linked to this session.</p>
				<button type="button" className="jira-link-prompt__btn" onClick={() => setOpen(true)}>
					<Plus className="size-3.5" aria-hidden="true" />
					Link a Jira issue
				</button>
			</div>
			<JiraLinkDialog sessionId={sessionId} open={open} onOpenChange={setOpen} />
		</section>
	);
}

function IssueLead({ sessionId, issue }: { sessionId: string; issue: JiraIssue }) {
	// The status pill doubles as the entry point to the ONE sanctioned Jira write
	// (Move status). Placed in Slice 1, wired here.
	const [moveOpen, setMoveOpen] = useState(false);
	const unlink = useUnlinkJira(sessionId);
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
					<button
						type="button"
						className="jira-status jira-status--btn"
						style={statusPillStyle(issue.statusCategory)}
						onClick={() => setMoveOpen(true)}
						title="Move status"
						aria-haspopup="dialog"
					>
						{issue.status}
						<ChevronDown className="jira-status__caret" aria-hidden="true" />
					</button>
				) : null}
				<button
					type="button"
					className="jira-unlink"
					onClick={() => unlink.mutate()}
					disabled={unlink.isPending}
					title="Unlink this Jira issue from the session"
				>
					{unlink.isPending ? "Unlinking…" : "Unlink"}
				</button>
			</div>
			{unlink.isError ? (
				<p className="jira-fetch-error" role="alert">
					{unlink.error instanceof Error ? unlink.error.message : "Couldn't unlink the Jira issue."}
				</p>
			) : null}
			<JiraMoveStatusDialog sessionId={sessionId} issue={issue} open={moveOpen} onOpenChange={setMoveOpen} />
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
