import * as Dialog from "@radix-ui/react-dialog";
import { ArrowUpRight, ChevronDown, ChevronRight, X } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { type JiraIssue, type JiraIssueSummary, type JiraSubtask, useJiraIssue } from "../hooks/useSessionJiraContext";
import { formatSprintDates, priorityTone, statusPillStyle } from "../lib/jira-format";
import { JiraAdf } from "./JiraAdf";
import { JiraIssueMoveDialog, type MoveTarget } from "./JiraMoveStatusDialog";

/**
 * Browse Jira issue detail — a READ-ONLY drawer over an issue, reusing the Summary
 * tab's rendering (the same jira-card/jira-head/jira-meta classes, the ADF render
 * via JiraAdf) plus the sanctioned Move-status control (by key, pre-session). If the
 * issue is a subtask, a clickable PARENT breadcrumb navigates the drawer to the
 * parent (Image #36); a Parent field in the details does the same. Read-only apart
 * from Move-status — no field edits, no issue creation from here.
 */
export function JiraIssueDetail({
	issueKey,
	open,
	onOpenChange,
	onCreateSession,
}: {
	issueKey: string | null;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onCreateSession: (issue: JiraIssueSummary) => void;
}) {
	// The drawer can navigate to a parent; track the key currently shown, reset to
	// the opening key whenever it (re)opens.
	const [currentKey, setCurrentKey] = useState<string | null>(issueKey);
	useEffect(() => {
		if (open) setCurrentKey(issueKey);
	}, [open, issueKey]);

	const { data: issue, isLoading, isError, error } = useJiraIssue(currentKey ?? undefined, open && Boolean(currentKey));
	const [moveTarget, setMoveTarget] = useState<MoveTarget | null>(null);

	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Overlay className="jira-detail__overlay" />
				<Dialog.Content className="jira-detail__drawer" aria-label="Jira issue detail">
					<div className="jira-detail__bar">
						{issue?.parent ? (
							<button
								type="button"
								className="jira-detail__crumb"
								onClick={() => setCurrentKey(issue.parent!.key)}
								aria-label={`Open parent ${issue.parent.key}`}
								title={`Open parent ${issue.parent.key}`}
							>
								<ChevronRight className="jira-detail__crumb-caret size-3" aria-hidden="true" />
								<span className="jira-detail__crumb-key">{issue.parent.key}</span>
								{issue.parent.title ? <span className="jira-detail__crumb-title"> · {issue.parent.title}</span> : null}
							</button>
						) : (
							<span className="jira-detail__crumb jira-detail__crumb--none">Issue detail</span>
						)}
						<Dialog.Close asChild>
							<button type="button" className="jira-detail__close" aria-label="Close">
								<X className="size-4" aria-hidden="true" />
							</button>
						</Dialog.Close>
					</div>
					<Dialog.Title className="sr-only">Jira issue {currentKey ?? ""}</Dialog.Title>

					<div className="jira-detail__body">
						{isLoading ? (
							<p className="inspector-empty">Loading Jira issue…</p>
						) : isError ? (
							<p className="jira-fetch-error">
								{error instanceof Error ? error.message : "Couldn't load the Jira issue."}
							</p>
						) : !issue ? (
							<p className="jira-fetch-error">Jira issue not found.</p>
						) : (
							<>
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
												onClick={() =>
													setMoveTarget({ key: issue.key, type: issue.type, title: issue.title, status: issue.status })
												}
												title="Move status"
												aria-haspopup="dialog"
											>
												{issue.status}
												<ChevronDown className="jira-status__caret" aria-hidden="true" />
											</button>
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
										{issue.parent ? (
											<MetaRow
												k="Parent"
												v={
													<button
														type="button"
														className="jira-detail__parent-link"
														onClick={() => setCurrentKey(issue.parent!.key)}
													>
														{issue.parent.key}
														{issue.parent.title ? ` · ${issue.parent.title}` : ""}
													</button>
												}
											/>
										) : null}
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
									<div className="jira-detail__actions">
										<button
											type="button"
											className="jira-browse__create"
											onClick={() => onCreateSession(toSummary(issue))}
										>
											Create session ▷
										</button>
									</div>
								</div>

								{issue.description && issue.description.length > 0 ? (
									<div className="jira-card">
										<p className="jira-sect-label">Description</p>
										<JiraAdf nodes={issue.description} />
									</div>
								) : null}

								{issue.subtasks && issue.subtasks.length > 0 ? (
									<div className="jira-card">
										<p className="jira-sect-label">Subtasks · {issue.subtasks.length}</p>
										{issue.subtasks.map((s) => (
											<SubtaskRow
												key={s.key}
												subtask={s}
												onOpen={() => setCurrentKey(s.key)}
												onMove={() => setMoveTarget({ key: s.key, type: s.type, title: s.title, status: s.status })}
											/>
										))}
									</div>
								) : null}
							</>
						)}
					</div>

					{moveTarget ? (
						<JiraIssueMoveDialog
							target={moveTarget}
							open={Boolean(moveTarget)}
							onOpenChange={(o) => !o && setMoveTarget(null)}
						/>
					) : null}
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}

function SubtaskRow({ subtask, onOpen, onMove }: { subtask: JiraSubtask; onOpen: () => void; onMove: () => void }) {
	return (
		<div className="jira-sub">
			<button type="button" className="jira-sub__key jira-sub__key--btn" onClick={onOpen} title={`Open ${subtask.key}`}>
				{subtask.key}
			</button>
			{meta(subtask) ? <span className="jira-sub__meta">{meta(subtask)}</span> : <span className="jira-sub__meta" />}
			{subtask.status ? (
				<button
					type="button"
					className="jira-sub__pill jira-sub__pill--btn"
					style={statusPillStyle(subtask.statusCategory)}
					onClick={onMove}
					title="Move status"
					aria-haspopup="dialog"
				>
					{subtask.status}
					<ChevronDown className="jira-status__caret" aria-hidden="true" />
				</button>
			) : null}
		</div>
	);
}

function meta(subtask: JiraSubtask): string {
	return [subtask.title, subtask.type].filter(Boolean).join(" · ");
}

function MetaRow({ k, v }: { k: string; v: ReactNode }) {
	return (
		<div className="jira-meta__row">
			<dt className="jira-meta__k">{k}</dt>
			<dd className="jira-meta__v">{v}</dd>
		</div>
	);
}

// toSummary projects the full issue back to the picker-row shape so the detail
// view's "Create session" reuses the same New-task handoff as the list.
function toSummary(issue: JiraIssue): JiraIssueSummary {
	return {
		key: issue.key,
		type: issue.type,
		title: issue.title,
		status: issue.status,
		statusCategory: issue.statusCategory,
		statusColor: issue.statusColor,
		assignee: issue.assignee,
		url: issue.url,
		sprint: issue.sprint,
	};
}
