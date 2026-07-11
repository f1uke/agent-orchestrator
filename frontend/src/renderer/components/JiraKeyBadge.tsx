import { useSessionJiraContext } from "../hooks/useSessionJiraContext";
import { cn } from "../lib/utils";

/**
 * The display-only Jira chip on a board card / sidebar row: `KEY · type · status`,
 * colored by status category, DECOUPLED from the AO board lane (no drift/alignment
 * cue — user decision #4). The KEY is known from the `jira:<KEY>` binding alone, so
 * it renders immediately; the type square and status text fill in when Slice 1's
 * cached Jira context loads, and a Jira-side fetch failure just leaves the KEY.
 *
 * Not a link — the board card and sidebar row are their own click targets, and the
 * `<span>`-only markup is safe nested inside the sidebar row's `<button>`. (The
 * terminal linkifier and the Summary tab's "Open in Jira" own navigation.)
 */
export function JiraKeyBadge({
	sessionId,
	issueKey,
	variant = "card",
}: {
	sessionId: string;
	issueKey: string;
	variant?: "card" | "row";
}) {
	const { data } = useSessionJiraContext(sessionId, true);
	const issue = data?.linked ? data.issue : undefined;
	const status = issue?.status;

	return (
		<span
			className={cn("jira-badge", variant === "row" && "jira-badge--row")}
			title={statusTitle(issueKey, issue?.type, status)}
		>
			{variant === "row" ? (
				<span className="jira-badge__diamond" aria-hidden="true">
					◈
				</span>
			) : (
				<span className="jira-badge__sq" style={{ background: issueTypeColor(issue?.type) }} aria-hidden="true" />
			)}
			<span className="jira-badge__key">{issueKey}</span>
			{status ? (
				<>
					<span className="jira-badge__sep" aria-hidden="true">
						·
					</span>
					<span className="jira-badge__status" style={{ color: statusTone(issue?.statusCategory) }}>
						{status}
					</span>
				</>
			) : null}
		</span>
	);
}

function statusTitle(key: string, type?: string, status?: string): string {
	return [key, type, status].filter(Boolean).join(" · ");
}

// The type square's hue keys off the Jira issue type (mockup 04): bug → red,
// sub-task → purple, story → green, epic → purple, everything else (task, …) →
// the accent. Display cue only.
function issueTypeColor(type?: string): string {
	const t = (type ?? "").toLowerCase();
	if (t.includes("bug")) return "var(--red)";
	if (t.includes("sub")) return "var(--purple)";
	if (t.includes("epic")) return "var(--purple)";
	if (t.includes("story")) return "var(--green)";
	return "var(--accent)";
}

// Status text is tinted from Jira's status CATEGORY (not the free-form name):
// new → amber, indeterminate → accent (in progress), done → success/green.
// Mirrors JiraIssueSection's statusTone so the card and Summary agree.
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
