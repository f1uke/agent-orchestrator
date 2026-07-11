import { useEffect, useState } from "react";
import { Loader2, Search } from "lucide-react";
import { Input } from "./ui/input";
import { useJiraSearch, type JiraIssueSummary } from "../hooks/useSessionJiraContext";

/**
 * A reusable Jira issue search box + results dropdown. Used to attach an issue to
 * a session — both on the New-task modal (bind at creation) and from the Summary
 * tab (link an existing session). Search is LIVE cross-project REST (jira-cli list
 * is unusable here) and needs a Jira API token in the daemon env; a fetch failure
 * (e.g. no token) surfaces inline in the dropdown rather than silently returning
 * nothing.
 */
export function JiraIssuePicker({
	query,
	onQueryChange,
	onPick,
	project = "",
	enabled = true,
	placeholder,
	autoFocus,
	inputId,
}: {
	query: string;
	onQueryChange: (value: string) => void;
	onPick: (issue: JiraIssueSummary) => void;
	project?: string;
	enabled?: boolean;
	placeholder?: string;
	autoFocus?: boolean;
	inputId?: string;
}) {
	// Debounce the typed query so we don't fan out a request per keystroke.
	const [debounced, setDebounced] = useState(query);
	useEffect(() => {
		const t = setTimeout(() => setDebounced(query), 250);
		return () => clearTimeout(t);
	}, [query]);

	const { data, isFetching, isError, error } = useJiraSearch(debounced, project, enabled);
	const results = data ?? [];
	const trimmed = query.trim();
	// The dropdown only shows once there is something to search for.
	const active = enabled && trimmed.length >= 2;

	return (
		<div className="jira-picker">
			<div className="relative">
				<Search
					className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-passive"
					aria-hidden="true"
				/>
				<Input
					id={inputId}
					className="pl-8"
					value={query}
					autoFocus={autoFocus}
					autoComplete="off"
					autoCapitalize="none"
					spellCheck={false}
					placeholder={placeholder ?? "Search Jira (e.g. DEMO-2272 or a keyword)"}
					onChange={(event) => onQueryChange(event.target.value)}
				/>
				{isFetching && active ? (
					<Loader2
						className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 animate-spin text-passive"
						aria-hidden="true"
					/>
				) : null}
			</div>
			{active ? (
				<div className="jira-picker__drop" role="listbox" aria-label="Jira issue search results">
					{isError ? (
						<p className="jira-picker__note jira-picker__note--err">
							{error instanceof Error ? error.message : "Couldn't search Jira."}
						</p>
					) : isFetching && results.length === 0 ? (
						<p className="jira-picker__note">Searching…</p>
					) : results.length === 0 ? (
						<p className="jira-picker__note">No matching issues.</p>
					) : (
						results.map((issue) => (
							<button
								key={issue.key}
								type="button"
								className="jira-picker__opt"
								role="option"
								aria-selected={false}
								onClick={() => onPick(issue)}
							>
								<span className="jira-picker__k">{issue.key}</span>
								<span className="jira-picker__t">{issue.title}</span>
								{issue.status ? (
									<span className="jira-picker__st" style={pickerStatusStyle(issue.statusCategory)}>
										{issue.status}
									</span>
								) : null}
							</button>
						))
					)}
				</div>
			) : null}
		</div>
	);
}

// pickerStatusStyle tints a result's status pill by Jira's status CATEGORY, the
// same treatment as JiraIssueSection (new → amber, indeterminate → accent, done →
// success).
function pickerStatusStyle(category?: string): React.CSSProperties {
	const tone = category === "done" ? "var(--success)" : category === "indeterminate" ? "var(--accent)" : "var(--amber)";
	return {
		color: tone,
		background: `color-mix(in srgb, ${tone} 14%, transparent)`,
		borderColor: `color-mix(in srgb, ${tone} 42%, transparent)`,
	};
}
