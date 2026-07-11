import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleDashed, Loader2, Play, X } from "lucide-react";
import { type FormEvent, useEffect, useId, useState } from "react";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { BranchCombobox } from "./BranchCombobox";
import { JiraIssuePicker } from "./JiraIssuePicker";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import type { components } from "../../api/schema";
import type { JiraIssueSummary } from "../hooks/useSessionJiraContext";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { returnFocusToTerminal } from "../lib/terminal-focus";
import { captureRendererEvent } from "../lib/telemetry";
import type { AgentProvider } from "../types/workspace";
import { agentsQueryKey, agentsQueryOptions, refreshAgents } from "../hooks/useAgentsQuery";
import { useProjectBranches } from "../hooks/useProjectBranches";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { cn } from "../lib/utils";

type Project = components["schemas"]["Project"];

/** Create either spawns now or queues a deferred TODO on the board. */
type StartMode = "now" | "todo";

type NewTaskDialogProps = {
	open: boolean;
	projectId?: string;
	/**
	 * Pre-selected Jira issue to bind — the Browse-Jira "Create session" handoff
	 * (mockup 03). When set, the dialog opens with the issue already linked and its
	 * title pre-filled. Absent for the plain New-task flow.
	 */
	initialIssue?: JiraIssueSummary | null;
	/** Called after a Start-now create with the new live session id (board navigates to it). */
	onCreated: (sessionId: string) => void;
	/** Called after an Add-to-TODO create; the board just refreshes to show the card. */
	onQueued?: (sessionId: string) => void;
	onOpenChange: (open: boolean) => void;
};

// A Jira issue key: PROJECT-123. The field is a live search picker (JiraIssuePicker);
// this regex is the fallback that still lets a user type a full key straight into
// the box and bind it without picking from the dropdown.
const JIRA_KEY_RE = /^[A-Z][A-Z0-9]+-\d+$/;

export function NewTaskDialog({
	open,
	projectId,
	initialIssue,
	onCreated,
	onQueued,
	onOpenChange,
}: NewTaskDialogProps) {
	const queryClient = useQueryClient();
	const titleId = useId();
	const promptId = useId();
	const jiraId = useId();
	const branchId = useId();
	const baseId = useId();
	const prTargetId = useId();
	const agentId = useId();
	const [title, setTitle] = useState("");
	const [jiraQuery, setJiraQuery] = useState("");
	const [linkedIssue, setLinkedIssue] = useState<JiraIssueSummary | null>(null);
	const [prompt, setPrompt] = useState("");
	const [branch, setBranch] = useState("");
	const [base, setBase] = useState("");
	const [baseTouched, setBaseTouched] = useState(false);
	const [prTarget, setPrTarget] = useState("");
	const [prTargetTouched, setPrTargetTouched] = useState(false);
	const [agent, setAgent] = useState("");
	const [agentTouched, setAgentTouched] = useState(false);
	const [startMode, setStartMode] = useState<StartMode>("now");
	const [isSubmitting, setIsSubmitting] = useState(false);
	const [error, setError] = useState<string | undefined>();

	const projectQuery = useQuery({
		queryKey: ["project", projectId],
		enabled: open && Boolean(projectId),
		queryFn: async () => {
			const { data, error: apiError } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId as string } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
			if (data?.status !== "ok") throw new Error("Project config is unavailable.");
			return data.project as Project;
		},
	});
	const agentsQuery = useQuery({
		...agentsQueryOptions,
		enabled: open,
	});
	const refreshAgentsMutation = useMutation({
		mutationFn: refreshAgents,
		onSuccess: (next) => queryClient.setQueryData(agentsQueryKey, next),
	});
	const defaultWorkerAgent = projectQuery.data?.config?.worker?.agent ?? "";
	const defaultBaseBranch = projectQuery.data?.defaultBranch ?? "";
	const agentCatalog = agentsQuery.data;
	const { branches: fetchedBranches } = useProjectBranches(open ? projectId : undefined);
	const branches =
		defaultBaseBranch && !fetchedBranches.includes(defaultBaseBranch)
			? [defaultBaseBranch, ...fetchedBranches]
			: fetchedBranches;

	useEffect(() => {
		if (!open) {
			setTitle("");
			setJiraQuery("");
			setLinkedIssue(null);
			setPrompt("");
			setBranch("");
			setBase("");
			setBaseTouched(false);
			setPrTarget("");
			setPrTargetTouched(false);
			setAgent("");
			setAgentTouched(false);
			setStartMode("now");
			setError(undefined);
			setIsSubmitting(false);
		}
	}, [open]);

	// Browse-Jira handoff: when the dialog opens carrying a pre-selected issue,
	// bind it and pre-fill the title (only if the user hasn't typed one), matching
	// the plain picker's onPick behavior. Runs after the reset effect above so it
	// seeds a freshly-cleared form.
	useEffect(() => {
		if (open && initialIssue?.key) {
			setLinkedIssue(initialIssue);
			setJiraQuery("");
			setTitle((current) => (current.trim() ? current : (initialIssue.title ?? "")));
		}
	}, [open, initialIssue]);

	useEffect(() => {
		if (open && !agentTouched) {
			setAgent(defaultWorkerAgent);
		}
	}, [open, agentTouched, defaultWorkerAgent]);

	useEffect(() => {
		if (open && !baseTouched && defaultBaseBranch) {
			setBase(defaultBaseBranch);
		}
	}, [open, baseTouched, defaultBaseBranch]);

	useEffect(() => {
		if (open && !prTargetTouched && defaultBaseBranch) {
			setPrTarget(defaultBaseBranch);
		}
	}, [open, prTargetTouched, defaultBaseBranch]);

	const canSubmit = title.trim().length > 0 && prompt.trim().length > 0;
	const isNow = startMode === "now";

	const submit = async (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		if (!projectId || isSubmitting) return;

		const cleanTitle = title.trim();
		const cleanPrompt = prompt.trim();
		const cleanBranch = branch.trim();
		const cleanBase = base.trim();
		const cleanPrTarget = prTarget.trim();
		if (!cleanTitle || !cleanPrompt) {
			setError("Title and brief are required.");
			return;
		}
		// Optional Jira link: blank = a plain manual task. A picked issue (or a
		// full key typed and left in the search box) binds the session to
		// "jira:<KEY>" so its context shows in Summary.
		let boundKey = "";
		if (linkedIssue?.key) {
			boundKey = linkedIssue.key;
		} else {
			const typedKey = jiraQuery.trim().toUpperCase();
			if (typedKey !== "") {
				if (!JIRA_KEY_RE.test(typedKey)) {
					setError("Pick a Jira issue from the list, or clear the search field.");
					return;
				}
				boundKey = typedKey;
			}
		}
		const jiraLinked = boundKey !== "";

		setIsSubmitting(true);
		setError(undefined);
		void captureRendererEvent("ao.renderer.task_create_requested", { project_id: projectId, mode: startMode });
		try {
			const { data, error: apiError } = await apiClient.POST("/api/v1/sessions", {
				body: {
					projectId,
					kind: "worker",
					harness: agentTouched && agent ? (agent as AgentProvider) : undefined,
					// A Jira-linked task binds issueId to the key and keeps the human
					// title as the sidebar label (displayName, capped at 20 by the API);
					// an unlinked task keeps the existing behavior of storing the title
					// in issueId.
					issueId: jiraLinked ? `jira:${boundKey}` : cleanTitle,
					displayName: jiraLinked ? cleanTitle.slice(0, 20) : undefined,
					prompt: cleanPrompt,
					branch: cleanBranch || undefined,
					baseBranch: cleanBase || undefined,
					prTarget: cleanPrTarget || undefined,
					autoNameBranch: cleanBranch === "" ? true : undefined,
					// Absent => start now (unchanged). false => stage as a TODO.
					startImmediately: isNow ? undefined : false,
				},
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Unable to create task"));
			if (!data?.session?.id) throw new Error("Task creation returned no session");
			void captureRendererEvent("ao.renderer.task_create_succeeded", { project_id: projectId, mode: startMode });
			if (isNow) {
				onCreated(data.session.id);
			} else {
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
				onQueued?.(data.session.id);
			}
			onOpenChange(false);
		} catch (err) {
			void captureRendererEvent("ao.renderer.task_create_failed", { project_id: projectId, mode: startMode });
			void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
			setError(err instanceof Error ? err.message : "Unable to create task");
		} finally {
			setIsSubmitting(false);
		}
	};

	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-50 bg-black/55 data-[state=open]:animate-overlay-in" />
				<Dialog.Content
					// This dialog opens over the orchestrator terminal. On close (Cancel,
					// ✕, Esc, or an outside click) return the caret to the terminal so the
					// user can keep typing — instead of parking focus on the "New task"
					// trigger. Falls back to Radix's default focus return when no terminal
					// is mounted (e.g. opened from the board).
					onCloseAutoFocus={returnFocusToTerminal}
					// Blue top accent marks the create modal (vs the TODO detail modal's
					// grey), echoing the design handoff.
					style={{ borderTopColor: "var(--accent)", borderTopWidth: 2 }}
					className="fixed left-1/2 top-1/2 z-50 flex max-h-[calc(100vh-32px)] w-[min(560px,calc(100vw-32px))] -translate-x-1/2 -translate-y-1/2 flex-col rounded-lg border border-border bg-popover p-0 text-popover-foreground shadow-xl data-[state=open]:animate-modal-in"
				>
					<div className="flex items-start justify-between gap-4 border-b border-border px-5 py-4">
						<div className="min-w-0">
							<Dialog.Title className="text-[15px] font-semibold text-foreground">New task</Dialog.Title>
							<Dialog.Description className="mt-1 text-[12px] text-muted-foreground">
								Prepare a worker — start it now, or queue it in TODO.
							</Dialog.Description>
						</div>
						<Dialog.Close asChild>
							<button
								type="button"
								className="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground transition hover:bg-surface hover:text-foreground"
								aria-label="Close new task dialog"
							>
								<X className="size-4" aria-hidden="true" />
							</button>
						</Dialog.Close>
					</div>

					<form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
						<div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
							<div className="space-y-1.5">
								<label className="text-[12px] font-medium text-muted-foreground" htmlFor={jiraId}>
									Jira issue{" "}
									<span className="font-normal text-passive">
										— optional, link one to bind & show its context in Summary
									</span>
								</label>
								{linkedIssue ? (
									<div className="jira-linked-chip">
										<span className="jira-linked-chip__key">{linkedIssue.key}</span>
										<span className="jira-linked-chip__title">{linkedIssue.title}</span>
										{linkedIssue.status ? <span className="jira-linked-chip__status">{linkedIssue.status}</span> : null}
										<button
											type="button"
											className="jira-linked-chip__clear"
											aria-label="Unlink Jira issue"
											onClick={() => setLinkedIssue(null)}
										>
											<X className="size-3.5" aria-hidden="true" />
										</button>
									</div>
								) : (
									<JiraIssuePicker
										inputId={jiraId}
										query={jiraQuery}
										onQueryChange={setJiraQuery}
										enabled={open}
										placeholder="Search Jira to link an issue (e.g. DEMO-2272) — or leave blank"
										onPick={(issue) => {
											setLinkedIssue(issue);
											setJiraQuery("");
											if (!title.trim() && issue.title) setTitle(issue.title);
										}}
									/>
								)}
								<p className="text-[11px] leading-relaxed text-passive">
									Leave blank for a plain manual task. Linking an issue binds the session to its key.
								</p>
							</div>

							<div className="space-y-1.5">
								<label className="text-[12px] font-medium text-muted-foreground" htmlFor={titleId}>
									Title
								</label>
								<Input
									id={titleId}
									autoFocus
									placeholder="Fix WebGL fallback renderer"
									value={title}
									onChange={(event) => setTitle(event.target.value)}
								/>
							</div>

							<div className="space-y-1.5">
								<label className="text-[12px] font-medium text-muted-foreground" htmlFor={promptId}>
									Brief
								</label>
								<textarea
									id={promptId}
									className="min-h-[112px] w-full resize-y rounded-md border border-border bg-transparent px-3 py-2 text-[13px] leading-relaxed text-foreground outline-none transition placeholder:text-passive focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent-weak"
									placeholder="Describe the change, constraints, and expected verification."
									value={prompt}
									onChange={(event) => setPrompt(event.target.value)}
								/>
							</div>

							<div className="space-y-1.5">
								<label className="text-[12px] font-medium text-muted-foreground" htmlFor={baseId}>
									Start from
								</label>
								<BranchCombobox
									id={baseId}
									branches={branches}
									value={base}
									onChange={(value) => {
										setBase(value);
										setBaseTouched(true);
									}}
								/>
							</div>

							<div className="space-y-1.5">
								<label className="text-[12px] font-medium text-muted-foreground" htmlFor={branchId}>
									New branch name
								</label>
								<Input
									id={branchId}
									placeholder="optional — AI names it if blank"
									value={branch}
									onChange={(event) => setBranch(event.target.value)}
								/>
							</div>

							<div className="space-y-1.5">
								<label className="text-[12px] font-medium text-muted-foreground" htmlFor={prTargetId}>
									PR target
								</label>
								<BranchCombobox
									id={prTargetId}
									branches={branches}
									value={prTarget}
									onChange={(value) => {
										setPrTarget(value);
										setPrTargetTouched(true);
									}}
								/>
							</div>

							<div className="space-y-1.5">
								<RequiredAgentField
									id={agentId}
									label="Agent"
									placeholder="Project default"
									value={agent}
									authorized={agentCatalog?.authorized}
									installed={agentCatalog?.installed}
									supported={agentCatalog?.supported}
									disabled={agentsQuery.isFetching && agentCatalog === undefined}
									onChange={(value) => {
										setAgent(value);
										setAgentTouched(true);
									}}
								/>
								<button
									type="button"
									className="text-[12px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:pointer-events-none disabled:opacity-50"
									disabled={refreshAgentsMutation.isPending}
									onClick={() => refreshAgentsMutation.mutate()}
								>
									{refreshAgentsMutation.isPending ? "Refreshing agents..." : "Refresh agents"}
								</button>
							</div>

							{error && (
								<div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-[12px] text-destructive">
									{error}
								</div>
							)}

							{refreshAgentsMutation.isError && (
								<div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-[12px] text-destructive">
									{refreshAgentsMutation.error instanceof Error
										? refreshAgentsMutation.error.message
										: "Could not refresh agent catalog."}
								</div>
							)}
						</div>

						<div className="flex flex-wrap items-center gap-3 border-t border-border px-5 py-4">
							{/* Segmented start-mode toggle: Start now (blue) vs Add to TODO (grey). */}
							<div
								className="inline-flex shrink-0 items-center gap-0.5 rounded-md border border-border p-0.5"
								role="group"
								aria-label="Start mode"
							>
								<button
									type="button"
									aria-label="Mode: start immediately"
									aria-pressed={isNow}
									onClick={() => setStartMode("now")}
									className={cn(
										"inline-flex items-center gap-1.5 rounded-[5px] px-2.5 py-1 text-[12px] font-medium transition-colors",
										isNow ? "bg-accent text-white" : "text-muted-foreground hover:text-foreground",
									)}
								>
									<Play className="size-3" aria-hidden="true" />
									Start now
								</button>
								<button
									type="button"
									aria-label="Mode: queue in TODO"
									aria-pressed={!isNow}
									onClick={() => setStartMode("todo")}
									className={cn(
										"inline-flex items-center gap-1.5 rounded-[5px] px-2.5 py-1 text-[12px] font-medium transition-colors",
										!isNow ? "text-[#12121a]" : "text-muted-foreground hover:text-foreground",
									)}
									style={!isNow ? { background: "var(--lane-todo-bright)" } : undefined}
								>
									<CircleDashed className="size-3" aria-hidden="true" />
									Add to TODO
								</button>
							</div>
							<span className="min-w-0 flex-1 text-[11.5px] text-passive">
								{isNow
									? "Creates branch, worktree & session and starts the agent now."
									: "Saved to the TODO lane — nothing is created until you press Start."}
							</span>
							<Dialog.Close asChild>
								<Button type="button" variant="ghost" disabled={isSubmitting}>
									Cancel
								</Button>
							</Dialog.Close>
							<Button
								type="submit"
								disabled={isSubmitting || !projectId || !canSubmit}
								style={isNow ? undefined : { background: "var(--lane-todo-bright)", color: "#12121a" }}
							>
								{isSubmitting ? <Loader2 className="size-3.5 animate-spin" aria-hidden="true" /> : null}
								{isSubmitting
									? isNow
										? branch.trim() === ""
											? "Naming branch…"
											: "Starting…"
										: "Queuing…"
									: isNow
										? "Start now"
										: "Add to TODO"}
							</Button>
						</div>
					</form>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
