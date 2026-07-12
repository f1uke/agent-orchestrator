import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { components } from "../../../api/schema";
import { agentsQueryKey, agentsQueryOptions, refreshAgents } from "../../hooks/useAgentsQuery";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { captureRendererEvent } from "../../lib/telemetry";
import { spawnOrchestrator } from "../../lib/spawn-orchestrator";
import { newestActiveOrchestrator } from "../../types/workspace";
import {
	buildIntake,
	deriveRepoWebURL,
	deriveTrackerRepo,
	type IntakeForm,
	intakeNeedsRule,
	providerFromOrigin,
} from "../IntakeFields";

type Project = components["schemas"]["Project"];
type ProjectConfig = components["schemas"]["ProjectConfig"];
type TrackerIntakeConfig = components["schemas"]["TrackerIntakeConfig"];
type GitConventionConfig = components["schemas"]["GitConventionConfig"];
type ApprovalRule = components["schemas"]["ApprovalRule"];

// The flat, string/boolean-backed shape the settings sections edit. Kept flat so
// dirty tracking is a shallow compare and each field maps 1:1 to a control.
export type ProjectSettingsFormState = {
	defaultBranch: string;
	sessionPrefix: string;
	gitWorkflow: string;
	branchPrefix: string;
	workerAgent: string;
	orchestratorAgent: string;
	model: string;
	permissions: string;
	reviewerHarness: string;
	intakeEnabled: boolean;
	intakeRepo: string;
	intakeAssignee: string;
	approvalRuleEnabled: boolean;
	approvalThreshold: string;
	orchestratorPrompt: string;
	workerPrompt: string;
	reviewerPrompt: string;
};

export type ProjectSettingsFieldKey = keyof ProjectSettingsFormState;

// extractForm reads the current config into the flat draft shape (identical field
// derivation to the pre-redesign form so save payloads are unchanged).
function extractForm(project: Project, config: ProjectConfig): ProjectSettingsFormState {
	const intake: TrackerIntakeConfig = config.trackerIntake ?? {};
	const gitConvention: GitConventionConfig = config.gitConvention ?? {};
	const approvalRule: ApprovalRule = config.approvalRule ?? {};
	return {
		defaultBranch: config.defaultBranch ?? project.defaultBranch ?? "",
		sessionPrefix: config.sessionPrefix ?? "",
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
		approvalRuleEnabled: approvalRule.enabled ?? false,
		approvalThreshold: approvalRule.threshold != null ? String(approvalRule.threshold) : "",
		orchestratorPrompt: config.systemPromptAdditions?.orchestrator ?? "",
		workerPrompt: config.systemPromptAdditions?.worker ?? "",
		reviewerPrompt: config.systemPromptAdditions?.reviewer ?? "",
	};
}

function shallowEqual(a: ProjectSettingsFormState, b: ProjectSettingsFormState): boolean {
	return (Object.keys(a) as ProjectSettingsFieldKey[]).every((key) => a[key] === b[key]);
}

// buildGitConvention turns the workflow + prefix fields into the typed
// convention, or undefined when the workflow is none so the field is omitted.
function buildGitConvention(workflow: string, branchPrefix: string): GitConventionConfig | undefined {
	if (workflow !== "gitflow" && workflow !== "custom") return undefined;
	const prefix = branchPrefix.trim();
	return prefix ? { workflow, branchPrefix: prefix } : { workflow };
}

// buildApprovalRule turns the enable toggle + threshold into the typed rule, or
// undefined when off. A blank threshold falls back to the backend default (2).
function buildApprovalRule(enabled: boolean, threshold: string): ApprovalRule | undefined {
	if (!enabled) return undefined;
	const trimmed = threshold.trim();
	return trimmed === "" ? { enabled: true } : { enabled: true, threshold: Number(trimmed) };
}

// Drop an object whose every value is undefined so we send `undefined` (omit)
// rather than an empty {} the daemon would persist.
function blankToUndefined<T extends object>(obj: T): T | undefined {
	return Object.values(obj).some((v) => v !== undefined) ? obj : undefined;
}

// useProjectSettingsForm owns the Project scope's editable state, dirty tracking,
// save (the single PUT that replaces the whole config, preserving hidden fields +
// the orchestrator respawn when the orchestrator agent changed), and discard. It
// is the pre-redesign SettingsBody logic, lifted so every section is a thin
// controlled view and one save bar commits the lot.
export function useProjectSettingsForm({
	project,
	projectId,
	onSaved,
}: {
	project: Project;
	projectId: string;
	onSaved: () => void;
}) {
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	const config = project.config ?? {};
	const projectProvider = providerFromOrigin(project.repo);
	const isGitLabProject = projectProvider === "gitlab";
	const workspace = workspaceQuery.data?.find((item) => item.id === projectId);
	const activeOrchestrator = newestActiveOrchestrator(workspace?.sessions ?? []);

	const [form, setForm] = useState<ProjectSettingsFormState>(() => extractForm(project, config));
	// The last-saved snapshot. Dirty = form ≠ baseline; on save success baseline
	// advances to form so the bar returns to idle without a refetch race.
	const [baseline, setBaseline] = useState<ProjectSettingsFormState>(form);
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

	const setField = <K extends ProjectSettingsFieldKey>(key: K, value: ProjectSettingsFormState[K]) => {
		setSavedAt(null);
		setForm((f) => ({ ...f, [key]: value }));
	};

	const dirty = !shallowEqual(form, baseline);
	const isFieldDirty = (key: ProjectSettingsFieldKey) => form[key] !== baseline[key];

	// Intake derivations (display-only repo preview + provider), mirroring the
	// daemon's origin-derived routing. Provider follows an explicit CLI override.
	const intakeProvider = config.trackerIntake?.provider ?? projectProvider;
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
	const intakeRepoURL = intakeRepoOverride ? undefined : deriveRepoWebURL(project.repo);
	const intakeIncomplete = intakeNeedsRule(intakeForm);
	const gitConventionIncomplete = form.gitWorkflow === "custom" && form.branchPrefix.trim() === "";

	const mutation = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.settings_save_requested", { project_id: projectId });
			// PUT replaces the whole config; merge edited fields over what loaded so
			// we don't drop env/symlinks/postCreate the form doesn't expose.
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
				approvalRule: buildApprovalRule(form.approvalRuleEnabled, form.approvalThreshold),
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
			setBaseline(form);
			void queryClient.invalidateQueries({ queryKey: ["project", projectId] });
			onSaved();
		},
		onError: () => {
			void captureRendererEvent("ao.renderer.settings_save_failed", { project_id: projectId });
		},
	});

	// submit runs the same client-side validation the pre-redesign form did, then
	// fires the PUT. Blocked validation leaves the bar dirty with the message.
	const submit = () => {
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
	};

	const discard = () => {
		setForm(baseline);
		setSavedAt(null);
		setReplacementError(null);
		setValidationError(null);
	};

	return {
		form,
		setField,
		dirty,
		isFieldDirty,
		isGitLabProject,
		agentCatalog,
		agentsQuery,
		refreshAgentsMutation,
		missingRequiredAgent,
		intakeForm,
		patchIntake,
		effectiveIntakeRepo,
		intakeRepoURL,
		mutation,
		savedAt,
		replacementError,
		validationError,
		submit,
		discard,
	};
}
