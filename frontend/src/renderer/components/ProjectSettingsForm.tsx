import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { components } from "../../api/schema";
import { agentsQueryKey, agentsQueryOptions, refreshAgents } from "../hooks/useAgentsQuery";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { newestActiveOrchestrator } from "../types/workspace";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import { DashboardSubhead } from "./DashboardSubhead";
import {
	buildIntake,
	deriveRepoWebURL,
	deriveTrackerRepo,
	IntakeFields,
	type IntakeForm,
	intakeNeedsRule,
	providerFromOrigin,
} from "./IntakeFields";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Textarea } from "./ui/textarea";

type Project = components["schemas"]["Project"];
type ProjectConfig = components["schemas"]["ProjectConfig"];
type TrackerIntakeConfig = components["schemas"]["TrackerIntakeConfig"];
type GitConventionConfig = components["schemas"]["GitConventionConfig"];

const PERMISSION_MODE_OPTIONS = [
	{ value: "default", label: "Default" },
	{ value: "accept-edits", label: "Accept edits" },
	{ value: "auto", label: "Auto" },
	{ value: "bypass-permissions", label: "Bypass permissions" },
] as const;

const REVIEWER_OPTIONS = ["claude-code", "codex", "opencode"] as const;

// "none" is the UI spelling of the default (unset) convention; it maps to an
// undefined gitConvention so an otherwise-empty config still persists as unset.
const GIT_WORKFLOW_OPTIONS = [
	{ value: "none", label: "None" },
	{ value: "gitflow", label: "gitflow" },
	{ value: "custom", label: "custom" },
] as const;

// buildGitConvention turns the workflow + prefix fields into the typed convention,
// or undefined when the workflow is none so the config field is omitted entirely.
function buildGitConvention(workflow: string, branchPrefix: string): GitConventionConfig | undefined {
	if (workflow !== "gitflow" && workflow !== "custom") return undefined;
	const prefix = branchPrefix.trim();
	return prefix ? { workflow, branchPrefix: prefix } : { workflow };
}

const projectQueryKey = (id: string) => ["project", id] as const;

export function ProjectSettingsForm({ projectId }: { projectId: string }) {
	const queryClient = useQueryClient();

	const query = useQuery({
		queryKey: projectQueryKey(projectId),
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error("Project config is unavailable (degraded).");
			return data.project as Project;
		},
	});

	if (query.isLoading) {
		return <CenteredNote>Loading project settings…</CenteredNote>;
	}
	if (query.isError || !query.data) {
		return (
			<CenteredNote>{query.error instanceof Error ? query.error.message : "Could not load project."}</CenteredNote>
		);
	}

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<DashboardSubhead title="Settings" subtitle={query.data.path} />
			<div className="min-h-0 flex-1 overflow-y-auto p-[18px]">
				<SettingsBody
					key={projectId}
					project={query.data}
					onSaved={() => queryClient.invalidateQueries({ queryKey: workspaceQueryKey })}
					projectId={projectId}
				/>
			</div>
		</div>
	);
}

// Project carries no typed provider/host field (see components["schemas"]["Project"]):
// only id/kind/name/path/repo/config/workspaceRepos, where "kind" is
// single_repo|workspace (workspace shape), not an SCM provider. The daemon
// resolves GitLab vs GitHub server-side from the git origin host (see
// gitlab.Provider.Host in backend/internal/adapters/scm/gitlab/provider.go), so
// mirror that here client-side purely for display via providerFromOrigin, which
// inspects the origin host rather than substring-matching the whole URL.

function SettingsBody({ project, projectId, onSaved }: { project: Project; projectId: string; onSaved: () => void }) {
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	const config = project.config ?? {};
	const projectProvider = providerFromOrigin(project.repo);
	const isGitLabProject = projectProvider === "gitlab";
	const workspace = workspaceQuery.data?.find((item) => item.id === projectId);
	const activeOrchestrator = newestActiveOrchestrator(workspace?.sessions ?? []);
	const intake: TrackerIntakeConfig = config.trackerIntake ?? {};
	const gitConvention: GitConventionConfig = config.gitConvention ?? {};
	const [form, setForm] = useState({
		defaultBranch: config.defaultBranch ?? project.defaultBranch ?? "",
		sessionPrefix: config.sessionPrefix ?? "",
		// Widen the enum to string so the shared string-based select handlers apply;
		// buildGitConvention narrows it back to the typed workflow on save.
		gitWorkflow: (gitConvention.workflow ?? "") as string,
		branchPrefix: gitConvention.branchPrefix ?? "",
		workerAgent: config.worker?.agent ?? "",
		orchestratorAgent: config.orchestrator?.agent ?? "",
		model: config.agentConfig?.model ?? "",
		permissions: config.agentConfig?.permissions ?? "",
		reviewerHarness: config.reviewers?.[0]?.harness ?? "",
		intakeEnabled: intake.enabled ?? false,
		intakeRepo: intake.repo ?? "",
		intakeAssignee: intake.assignee ?? "",
		minApprovals: config.minApprovals != null ? String(config.minApprovals) : "",
		orchestratorPrompt: config.systemPromptAdditions?.orchestrator ?? "",
		workerPrompt: config.systemPromptAdditions?.worker ?? "",
		reviewerPrompt: config.systemPromptAdditions?.reviewer ?? "",
	});
	const [savedAt, setSavedAt] = useState<number | null>(null);
	const [replacementError, setReplacementError] = useState<string | null>(null);
	const [validationError, setValidationError] = useState<string | null>(null);
	const initialOrchestratorAgent = config.orchestrator?.agent ?? "";
	const missingRequiredAgent = form.workerAgent === "" || form.orchestratorAgent === "";
	const agentsQuery = useQuery(agentsQueryOptions);
	const agentCatalog = agentsQuery.data;
	const refreshAgentsMutation = useMutation({
		mutationFn: refreshAgents,
		onSuccess: (next) => queryClient.setQueryData(agentsQueryKey, next),
	});

	// The Electron app only registers git projects today, so the daemon always has a usable
	// git origin to derive the repo/provider from (trackerRepo() in observer.go) when
	// trackerIntake.repo is unset — there's no manual override input here. This mirrors that
	// same derivation client-side purely for display (a link to the repo being polled).
	// Intake provider follows the project's SCM, derived from the git origin, except when a
	// provider was explicitly stored via the CLI (--tracker-provider), which is respected.
	const intakeProvider = intake.provider ?? projectProvider;
	const intakeForm: IntakeForm = {
		enabled: form.intakeEnabled,
		provider: intakeProvider,
		repo: form.intakeRepo,
		assignee: form.intakeAssignee,
	};
	const patchIntake = (patch: Partial<IntakeForm>) =>
		setForm((f) => ({
			...f,
			intakeEnabled: patch.enabled ?? f.intakeEnabled,
			intakeRepo: patch.repo ?? f.intakeRepo,
			intakeAssignee: patch.assignee ?? f.intakeAssignee,
		}));
	const intakeRepoOverride = form.intakeRepo.trim();
	const effectiveIntakeRepo = intakeRepoOverride || deriveTrackerRepo(project.repo, intakeProvider);
	// Only link to the origin-derived repo; a manual override has no known web URL.
	const intakeRepoURL = intakeRepoOverride ? undefined : deriveRepoWebURL(project.repo);
	const intakeIncomplete = intakeNeedsRule(intakeForm);
	// A custom workflow needs a branch prefix; gitflow defaults to feature/ server-side.
	const gitConventionIncomplete = form.gitWorkflow === "custom" && form.branchPrefix.trim() === "";

	const mutation = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.settings_save_requested", { project_id: projectId });
			// PUT replaces the whole config; merge the edited fields over what loaded
			// so we don't drop env/symlinks/postCreate the form doesn't expose.
			const next: ProjectConfig = {
				...config,
				defaultBranch: form.defaultBranch || undefined,
				sessionPrefix: form.sessionPrefix || undefined,
				worker: { ...config.worker, agent: form.workerAgent },
				orchestrator: { ...config.orchestrator, agent: form.orchestratorAgent },
				agentConfig: blankToUndefined({
					...config.agentConfig,
					model: form.model || undefined,
					permissions: form.permissions || undefined,
				}),
				reviewers: form.reviewerHarness ? [{ harness: form.reviewerHarness }] : undefined,
				trackerIntake: buildIntake(intakeForm),
				gitConvention: buildGitConvention(form.gitWorkflow, form.branchPrefix),
				minApprovals: form.minApprovals.trim() === "" ? undefined : Number(form.minApprovals),
				systemPromptAdditions: blankToUndefined({
					orchestrator: form.orchestratorPrompt || undefined,
					worker: form.workerPrompt || undefined,
					reviewer: form.reviewerPrompt || undefined,
				}),
			};
			const { error } = await apiClient.PUT("/api/v1/projects/{id}/config", {
				params: { path: { id: projectId } },
				body: { config: next },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (
				form.orchestratorAgent !== initialOrchestratorAgent ||
				(activeOrchestrator && activeOrchestrator.provider !== form.orchestratorAgent)
			) {
				try {
					await spawnOrchestrator(projectId, "settings", true);
				} catch (error) {
					return {
						replacementError: error instanceof Error ? error.message : "Could not replace orchestrator",
					};
				}
			}
			return { replacementError: null };
		},
		onSuccess: (result) => {
			void captureRendererEvent("ao.renderer.settings_save_succeeded", { project_id: projectId });
			setSavedAt(Date.now());
			setReplacementError(result.replacementError);
			setValidationError(null);
			void queryClient.invalidateQueries({ queryKey: ["project", projectId] });
			onSaved();
		},
		onError: () => {
			void captureRendererEvent("ao.renderer.settings_save_failed", { project_id: projectId });
		},
	});

	return (
		<form
			className="mx-auto flex max-w-2xl flex-col gap-4"
			onSubmit={(event) => {
				event.preventDefault();
				setSavedAt(null);
				setReplacementError(null);
				if (missingRequiredAgent) {
					setValidationError("Worker and orchestrator agents are required.");
					return;
				}
				if (intakeIncomplete) {
					setValidationError("Enabling intake requires an assignee.");
					return;
				}
				if (gitConventionIncomplete) {
					setValidationError("A custom git workflow requires a branch prefix.");
					return;
				}
				setValidationError(null);
				mutation.mutate();
			}}
		>
			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Identity</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-2 font-mono text-[12px] text-muted-foreground">
					<ReadonlyRow label="id" value={project.id} />
					<ReadonlyRow label="path" value={project.path} />
					<ReadonlyRow label="repo" value={project.repo || "—"} />
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Worktrees</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-4">
					<Field label="Default branch" htmlFor="defaultBranch">
						<input
							id="defaultBranch"
							className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
							value={form.defaultBranch}
							onChange={(e) => setForm((f) => ({ ...f, defaultBranch: e.target.value }))}
							placeholder="main"
						/>
					</Field>
					<Field label="Session prefix" htmlFor="sessionPrefix">
						<input
							id="sessionPrefix"
							className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
							value={form.sessionPrefix}
							onChange={(e) => setForm((f) => ({ ...f, sessionPrefix: e.target.value }))}
							placeholder="ao"
						/>
					</Field>
					{isGitLabProject && (
						<Field label="Minimum approvals" htmlFor="minApprovals">
							<input
								id="minApprovals"
								type="number"
								min={1}
								className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
								value={form.minApprovals}
								onChange={(e) => setForm((f) => ({ ...f, minApprovals: e.target.value }))}
								placeholder="3"
							/>
							<p className="text-[11px] text-muted-foreground">
								Applies only when the GitLab repo has no approval rule of its own. Default 3.
							</p>
						</Field>
					)}
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Git convention</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-4">
					<Field label="Branch workflow" htmlFor="gitWorkflow">
						<GitWorkflowSelect
							id="gitWorkflow"
							value={form.gitWorkflow}
							onChange={(v) => setForm((f) => ({ ...f, gitWorkflow: v }))}
						/>
						<p className="text-[11px] text-muted-foreground">
							Prefixes auto-named worker branches and tells the orchestrator how to name them. None keeps the
							current behavior.
						</p>
					</Field>
					{(form.gitWorkflow === "gitflow" || form.gitWorkflow === "custom") && (
						<Field label="Branch prefix" htmlFor="branchPrefix">
							<input
								id="branchPrefix"
								className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
								value={form.branchPrefix}
								onChange={(e) => setForm((f) => ({ ...f, branchPrefix: e.target.value }))}
								placeholder="feature/"
							/>
							<p className="text-[11px] text-muted-foreground">
								{form.gitWorkflow === "custom"
									? "Required. Every branch is forced under this prefix (e.g. feat/, story/)."
									: "Optional. Default type prefix; gitflow still picks bugfix/ or hotfix/ per task."}
							</p>
						</Field>
					)}
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Agents</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-4">
					<RequiredAgentField
						id="workerAgent"
						value={form.workerAgent}
						placeholder="Select worker agent"
						label="Default worker agent"
						authorized={agentCatalog?.authorized}
						installed={agentCatalog?.installed}
						supported={agentCatalog?.supported}
						disabled={agentsQuery.isFetching && agentCatalog === undefined}
						invalid={validationError !== null && form.workerAgent === ""}
						onChange={(v) => setForm((f) => ({ ...f, workerAgent: v }))}
					/>
					<RequiredAgentField
						id="orchestratorAgent"
						value={form.orchestratorAgent}
						placeholder="Select orchestrator agent"
						label="Default orchestrator agent"
						authorized={agentCatalog?.authorized}
						installed={agentCatalog?.installed}
						supported={agentCatalog?.supported}
						disabled={agentsQuery.isFetching && agentCatalog === undefined}
						invalid={validationError !== null && form.orchestratorAgent === ""}
						onChange={(v) => setForm((f) => ({ ...f, orchestratorAgent: v }))}
					/>
					<div className="flex items-center justify-between gap-3 text-[12px] leading-5 text-muted-foreground">
						<span>Agent availability is cached.</span>
						<button
							type="button"
							className="shrink-0 rounded text-foreground underline-offset-2 hover:underline disabled:pointer-events-none disabled:opacity-50"
							disabled={refreshAgentsMutation.isPending}
							onClick={() => refreshAgentsMutation.mutate()}
						>
							{refreshAgentsMutation.isPending ? "Refreshing..." : "Refresh agents"}
						</button>
					</div>
					{refreshAgentsMutation.isError && (
						<p className="text-[12px] leading-5 text-error">
							{refreshAgentsMutation.error instanceof Error
								? refreshAgentsMutation.error.message
								: "Could not refresh agent catalog."}
						</p>
					)}
					{missingRequiredAgent && (
						<p className="text-[12px] leading-5 text-error">Worker and orchestrator agents are required.</p>
					)}
					<Field label="Model override" htmlFor="model">
						<input
							id="model"
							className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
							value={form.model}
							onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))}
							placeholder="(agent default)"
						/>
					</Field>
					<Field label="Permission mode" htmlFor="permissionMode">
						<PermissionModeSelect
							id="permissionMode"
							value={form.permissions}
							onChange={(v) => setForm((f) => ({ ...f, permissions: v }))}
						/>
					</Field>
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Reviewers</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-4">
					<Field label="Default reviewer agent" htmlFor="reviewerHarness">
						<ReviewerSelect
							id="reviewerHarness"
							value={form.reviewerHarness}
							onChange={(v) => setForm((f) => ({ ...f, reviewerHarness: v }))}
						/>
					</Field>
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Tracker intake</CardTitle>
				</CardHeader>
				<CardContent>
					<IntakeFields
						form={intakeForm}
						onChange={patchIntake}
						repoPreview={{ value: effectiveIntakeRepo, url: intakeRepoURL }}
					/>
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Additional system prompts</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-4">
					<p className="text-[12px] text-muted-foreground">
						Extra text appended on top of the global base for this project. Leave blank to append nothing.
					</p>
					<Field label="Orchestrator additional prompt" htmlFor="add-orchestrator">
						<Textarea
							id="add-orchestrator"
							value={form.orchestratorPrompt}
							onChange={(e) => setForm((f) => ({ ...f, orchestratorPrompt: e.target.value }))}
						/>
					</Field>
					<Field label="Worker additional prompt" htmlFor="add-worker">
						<Textarea
							id="add-worker"
							value={form.workerPrompt}
							onChange={(e) => setForm((f) => ({ ...f, workerPrompt: e.target.value }))}
						/>
					</Field>
					<Field label="Reviewer additional prompt" htmlFor="add-reviewer">
						<Textarea
							id="add-reviewer"
							value={form.reviewerPrompt}
							onChange={(e) => setForm((f) => ({ ...f, reviewerPrompt: e.target.value }))}
						/>
					</Field>
				</CardContent>
			</Card>

			<div className="flex items-center gap-3">
				<Button type="submit" variant="primary" disabled={mutation.isPending}>
					{mutation.isPending ? "Saving…" : "Save changes"}
				</Button>
				{validationError && <span className="text-[12px] text-error">{validationError}</span>}
				{mutation.isError && (
					<span className="text-[12px] text-error">
						{mutation.error instanceof Error ? mutation.error.message : "Save failed"}
					</span>
				)}
				{savedAt && !mutation.isPending && !mutation.isError && (
					<span className="text-[12px] text-success">Saved.</span>
				)}
				{replacementError && !mutation.isPending && !mutation.isError && (
					<span className="text-[12px] text-warning">Orchestrator restart failed: {replacementError}</span>
				)}
			</div>
		</form>
	);
}

function PermissionModeSelect({
	id,
	value,
	onChange,
}: {
	id: string;
	value: string;
	onChange: (value: string) => void;
}) {
	return (
		<Select value={value || "__default__"} onValueChange={(v) => onChange(v === "__default__" ? "" : v)}>
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__default__">Project default</SelectItem>
				{PERMISSION_MODE_OPTIONS.map((opt) => (
					<SelectItem key={opt.value} value={opt.value}>
						{opt.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function GitWorkflowSelect({
	id,
	value,
	onChange,
}: {
	id: string;
	value: string;
	onChange: (value: string) => void;
}) {
	// Empty (unset) maps to the "none" option; selecting "none" clears the value.
	return (
		<Select value={value || "none"} onValueChange={(v) => onChange(v === "none" ? "" : v)}>
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				{GIT_WORKFLOW_OPTIONS.map((opt) => (
					<SelectItem key={opt.value} value={opt.value}>
						{opt.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function ReviewerSelect({ id, value, onChange }: { id: string; value: string; onChange: (value: string) => void }) {
	return (
		<Select value={value || "__default__"} onValueChange={(v) => onChange(v === "__default__" ? "" : v)}>
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__default__">Project default</SelectItem>
				{REVIEWER_OPTIONS.map((reviewer) => (
					<SelectItem key={reviewer} value={reviewer}>
						{reviewer}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function Field({ label, htmlFor, children }: { label: string; htmlFor?: string; children: React.ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<Label htmlFor={htmlFor} className="text-[12px] text-muted-foreground">
				{label}
			</Label>
			{children}
		</div>
	);
}

function ReadonlyRow({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex items-center gap-3">
			<span className="w-12 shrink-0 text-passive">{label}</span>
			<span className="min-w-0 flex-1 truncate text-foreground">{value}</span>
		</div>
	);
}

function CenteredNote({ children }: { children: React.ReactNode }) {
	return (
		<div className="grid h-full place-items-center bg-background p-6 text-center text-[12px] text-passive">
			{children}
		</div>
	);
}

// Drop an object whose every value is undefined so we send `undefined` (omit)
// rather than an empty {} the daemon would persist.
function blankToUndefined<T extends object>(obj: T): T | undefined {
	return Object.values(obj).some((v) => v !== undefined) ? obj : undefined;
}
