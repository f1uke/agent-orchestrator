import { Bot } from "lucide-react";
import type { components } from "../../../api/schema";
import { RequiredAgentField } from "../CreateProjectAgentSheet";
import { IntakeFields } from "../IntakeFields";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { SettingsGroup } from "./SettingsGroup";
import { SettingsField } from "./SettingsField";
import { SettingsReadOnlyPanel, ReadonlyRow } from "./SettingsReadOnlyPanel";
import { SettingEditorRow } from "./SettingEditorRow";
import { RESPONSE_LANGUAGE_OPTIONS } from "./response-language";
import type { useProjectSettingsForm } from "./useProjectSettingsForm";

type Project = components["schemas"]["Project"];
type AgentInfo = components["schemas"]["AgentInfo"];

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

const INPUT_CLASS =
	"h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak";

type ProjectForm = ReturnType<typeof useProjectSettingsForm>;

// ProjectSettingsContent renders the active Project section against the shared
// form hook. Only one section shows at a time (the two-pane surface), but the
// draft lives in the hook above it, so navigating between sections never loses
// an edit and one save bar commits the whole config.
export function ProjectSettingsContent({
	project,
	form,
	activeSection,
}: {
	project: Project;
	form: ProjectForm;
	activeSection: string;
}) {
	switch (activeSection) {
		case "agents":
			return <AgentsSection form={form} />;
		case "prompts":
			return <PromptsSection form={form} />;
		case "automation":
			return <AutomationSection form={form} />;
		default:
			return <GeneralSection project={project} form={form} />;
	}
}

function SectionTitle({ title, hint }: { title: string; hint: string }) {
	return (
		<div className="mb-4 flex items-center gap-2.5 border-b border-border pb-3 pt-2.5">
			<h2 className="text-[15px] font-semibold tracking-[-0.01em] text-foreground">{title}</h2>
			<span className="text-[12px] font-normal text-passive">· {hint}</span>
		</div>
	);
}

function GeneralSection({ project, form }: { project: Project; form: ProjectForm }) {
	const { form: draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionTitle title="General" hint="repository, worktrees & branch naming" />

			<SettingsReadOnlyPanel title="Identity · read-only">
				<ReadonlyRow label="id" value={project.id} copyable />
				<ReadonlyRow label="kind" value={project.kind === "workspace" ? "workspace" : "single repo"} />
				<ReadonlyRow label="path" value={project.path} copyable />
				<ReadonlyRow label="repo" value={project.repo || "—"} copyable={Boolean(project.repo)} />
			</SettingsReadOnlyPanel>

			{project.kind === "workspace" && (
				<SettingsReadOnlyPanel title="Workspace repos · read-only">
					{project.workspaceRepos?.length ? (
						project.workspaceRepos.map((repo) => (
							<div
								key={repo.name}
								className="grid grid-cols-[minmax(0,120px)_minmax(0,1fr)] gap-3 rounded-md border border-border px-3 py-2"
							>
								<span className="truncate text-foreground">{repo.name}</span>
								<span className="min-w-0 truncate text-muted-foreground">
									{repo.relativePath}
									{repo.repo ? ` · ${repo.repo}` : ""}
								</span>
							</div>
						))
					) : (
						<p className="text-[12px] text-muted-foreground">No child repositories are registered.</p>
					)}
				</SettingsReadOnlyPanel>
			)}

			<SettingsGroup title="Worktrees">
				<SettingsField label="Default branch" htmlFor="defaultBranch" modified={isFieldDirty("defaultBranch")}>
					<input
						id="defaultBranch"
						className={INPUT_CLASS}
						value={draft.defaultBranch}
						onChange={(e) => setField("defaultBranch", e.target.value)}
						placeholder="main"
					/>
				</SettingsField>
				<SettingsField label="Session prefix" htmlFor="sessionPrefix" modified={isFieldDirty("sessionPrefix")}>
					<input
						id="sessionPrefix"
						className={INPUT_CLASS}
						value={draft.sessionPrefix}
						onChange={(e) => setField("sessionPrefix", e.target.value)}
						placeholder="ao"
					/>
				</SettingsField>
			</SettingsGroup>

			<SettingsGroup title="Git convention">
				<SettingsField
					label="Branch workflow"
					htmlFor="gitWorkflow"
					modified={isFieldDirty("gitWorkflow")}
					help="Prefixes auto-named worker branches and tells the orchestrator how to name them. None keeps the current behavior."
				>
					<GitWorkflowSelect id="gitWorkflow" value={draft.gitWorkflow} onChange={(v) => setField("gitWorkflow", v)} />
				</SettingsField>
				{(draft.gitWorkflow === "gitflow" || draft.gitWorkflow === "custom") && (
					<SettingsField
						label="Branch prefix"
						htmlFor="branchPrefix"
						modified={isFieldDirty("branchPrefix")}
						help={
							draft.gitWorkflow === "custom"
								? "Required. Every branch is forced under this prefix (e.g. feat/, story/)."
								: "Optional. Default type prefix; gitflow still picks bugfix/ or hotfix/ per task."
						}
					>
						<input
							id="branchPrefix"
							className={INPUT_CLASS}
							value={draft.branchPrefix}
							onChange={(e) => setField("branchPrefix", e.target.value)}
							placeholder="feature/"
						/>
					</SettingsField>
				)}
			</SettingsGroup>
		</>
	);
}

function AgentsSection({ form }: { form: ProjectForm }) {
	const {
		form: draft,
		setField,
		isFieldDirty,
		agentCatalog,
		agentsQuery,
		refreshAgentsMutation,
		missingRequiredAgent,
		validationError,
	} = form;
	// The supported list carries every agent's full identity, including its model
	// tiers, so the per-kind model selectors resolve their options from the agent
	// the user picked above.
	const agentEntry = (agentId: string): AgentInfo | undefined =>
		agentCatalog?.supported?.find((a) => a.id === agentId);
	// Changing an agent degrades the model gracefully: if the newly-chosen agent
	// doesn't offer the previously-selected tier, fall back to that agent's
	// default (empty) rather than carrying a tier the new agent can't run.
	const changeAgent = (agentField: "workerAgent" | "orchestratorAgent", modelField: "workerModel" | "orchestratorModel", agentId: string) => {
		setField(agentField, agentId);
		const models = agentEntry(agentId)?.models ?? [];
		if (draft[modelField] && !models.some((m) => m.id === draft[modelField])) {
			setField(modelField, "");
		}
	};
	return (
		<>
			<SectionTitle title="Agents" hint="who runs, on which model & permission" />

			<SettingsGroup title="Defaults">
				<RequiredAgentField
					id="workerAgent"
					value={draft.workerAgent}
					placeholder="Select worker agent"
					label="Default worker agent"
					authorized={agentCatalog?.authorized}
					installed={agentCatalog?.installed}
					supported={agentCatalog?.supported}
					disabled={agentsQuery.isFetching && agentCatalog === undefined}
					invalid={validationError !== null && draft.workerAgent === ""}
					onChange={(v) => changeAgent("workerAgent", "workerModel", v)}
				/>
				<RequiredAgentField
					id="orchestratorAgent"
					value={draft.orchestratorAgent}
					placeholder="Select orchestrator agent"
					label="Default orchestrator agent"
					authorized={agentCatalog?.authorized}
					installed={agentCatalog?.installed}
					supported={agentCatalog?.supported}
					disabled={agentsQuery.isFetching && agentCatalog === undefined}
					invalid={validationError !== null && draft.orchestratorAgent === ""}
					onChange={(v) => changeAgent("orchestratorAgent", "orchestratorModel", v)}
				/>
				<p className="text-[11px] text-passive">Changing the orchestrator agent restarts the orchestrator on save.</p>
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
				<SettingsField
					label="Orchestrator model"
					htmlFor="orchestratorModel"
					modified={isFieldDirty("orchestratorModel")}
					help="Model tier for this project's orchestrator session. Default keeps the agent's own default."
				>
					<ModelSelect
						id="orchestratorModel"
						value={draft.orchestratorModel}
						agent={agentEntry(draft.orchestratorAgent)}
						onChange={(v) => setField("orchestratorModel", v)}
					/>
				</SettingsField>
				<SettingsField
					label="Worker model"
					htmlFor="workerModel"
					modified={isFieldDirty("workerModel")}
					help="Model tier for this project's worker sessions. Default keeps the agent's own default."
				>
					<ModelSelect
						id="workerModel"
						value={draft.workerModel}
						agent={agentEntry(draft.workerAgent)}
						onChange={(v) => setField("workerModel", v)}
					/>
				</SettingsField>
				<SettingsField label="Permission mode" htmlFor="permissionMode" modified={isFieldDirty("permissions")}>
					<PermissionModeSelect
						id="permissionMode"
						value={draft.permissions}
						onChange={(v) => setField("permissions", v)}
					/>
				</SettingsField>
			</SettingsGroup>

			<SettingsGroup title="Reviewer">
				<SettingsField
					label="Default reviewer agent"
					htmlFor="reviewerHarness"
					modified={isFieldDirty("reviewerHarness")}
					help="Used by AO's internal code reviewer. Was its own card — folded into Agents."
				>
					<ReviewerSelect
						id="reviewerHarness"
						value={draft.reviewerHarness}
						onChange={(v) => setField("reviewerHarness", v)}
					/>
				</SettingsField>
			</SettingsGroup>
		</>
	);
}

function PromptsSection({ form }: { form: ProjectForm }) {
	const { form: draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionTitle title="Prompts" hint="additional system prompts appended for this project" />

			<SettingsGroup title="Human-facing response language">
				<p className="text-[12px] leading-5 text-muted-foreground">
					Overrides the global default for this project's agents (orchestrator, worker, reviewer). They write their
					human-facing output - status updates, reports, questions, PR/MR review comments - in this language; code,
					commits, PR/MR titles and bodies, branch names, and identifiers always stay English. Inherit global default
					keeps whatever the Global settings specify.
				</p>
				<SettingsField label="Response language" htmlFor="responseLanguage" modified={isFieldDirty("responseLanguage")}>
					<ProjectLanguageSelect
						id="responseLanguage"
						value={draft.responseLanguage}
						onChange={(v) => setField("responseLanguage", v)}
					/>
				</SettingsField>
			</SettingsGroup>

			<p className="mb-4 mt-6 text-[12px] leading-relaxed text-passive">
				Extra text appended on top of the global base for this project. Leave blank to append nothing.
			</p>
			<SettingEditorRow
				icon={Bot}
				name="Orchestrator additional prompt"
				purpose="Appended to the orchestrator's base for this project"
				textareaLabel="Orchestrator additional prompt"
				value={draft.orchestratorPrompt}
				defaultValue=""
				modified={isFieldDirty("orchestratorPrompt")}
				onChange={(v) => setField("orchestratorPrompt", v)}
			/>
			<SettingEditorRow
				icon={Bot}
				name="Worker additional prompt"
				purpose="Appended to each worker's base for this project"
				textareaLabel="Worker additional prompt"
				value={draft.workerPrompt}
				defaultValue=""
				modified={isFieldDirty("workerPrompt")}
				onChange={(v) => setField("workerPrompt", v)}
			/>
			<SettingEditorRow
				icon={Bot}
				name="Reviewer additional prompt"
				purpose="Appended to the reviewer's base for this project"
				textareaLabel="Reviewer additional prompt"
				value={draft.reviewerPrompt}
				defaultValue=""
				modified={isFieldDirty("reviewerPrompt")}
				onChange={(v) => setField("reviewerPrompt", v)}
			/>
		</>
	);
}

function AutomationSection({ form }: { form: ProjectForm }) {
	const { form: draft, setField, isGitLabProject, intakeForm, patchIntake, effectiveIntakeRepo, intakeRepoURL } = form;
	return (
		<>
			<SectionTitle title="Automation" hint="things AO does on its own for this project" />

			<SettingsGroup title="Tracker intake">
				<IntakeFields
					form={intakeForm}
					onChange={patchIntake}
					repoPreview={{ value: effectiveIntakeRepo, url: intakeRepoURL }}
				/>
			</SettingsGroup>

			{isGitLabProject && (
				<SettingsGroup title="Approval rule">
					<label className="flex items-center gap-2.5 text-[13px] text-foreground">
						<input
							type="checkbox"
							className="h-4 w-4 accent-accent"
							checked={draft.approvalRuleEnabled}
							onChange={(e) => setField("approvalRuleEnabled", e.target.checked)}
						/>
						Require approvals before Ready to merge
					</label>
					<p className="text-[11px] text-passive">
						When enabled, a merge request is only marked Ready to merge once it has at least the required number of
						approvals. Off by default; applies only when the GitLab repo has no approval rule of its own.
					</p>
					{draft.approvalRuleEnabled && (
						<SettingsField label="Required approvals" htmlFor="approvalThreshold" help="Default 2.">
							<input
								id="approvalThreshold"
								type="number"
								min={1}
								className={INPUT_CLASS}
								value={draft.approvalThreshold}
								onChange={(e) => setField("approvalThreshold", e.target.value)}
								placeholder="2"
							/>
						</SettingsField>
					)}
				</SettingsGroup>
			)}
		</>
	);
}

function ModelSelect({
	id,
	value,
	agent,
	onChange,
}: {
	id: string;
	value: string;
	agent: AgentInfo | undefined;
	onChange: (value: string) => void;
}) {
	const models = agent?.models ?? [];
	if (models.length === 0) {
		// The chosen agent exposes no tier choice (or none is selected yet):
		// surface a short hint rather than an empty selector.
		return (
			<p className="text-[12px] leading-8 text-passive">
				{agent
					? `${agent.label} uses its own default model (no selectable tiers).`
					: "Select an agent to choose a model."}
			</p>
		);
	}
	// Preserve a stored value the catalog doesn't list (e.g. a pinned model id set
	// via the CLI) as an extra option so switching to this control never drops it.
	const known = models.some((m) => m.id === value);
	const extra = value && !known ? [{ id: value, label: value }] : [];
	return (
		<Select value={value || "__default__"} onValueChange={(v) => onChange(v === "__default__" ? "" : v)}>
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__default__">Default ({agent?.label ?? "agent"} default)</SelectItem>
				{[...extra, ...models].map((m) => (
					<SelectItem key={m.id} value={m.id}>
						{m.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
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

function GitWorkflowSelect({ id, value, onChange }: { id: string; value: string; onChange: (value: string) => void }) {
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

function ProjectLanguageSelect({
	id,
	value,
	onChange,
}: {
	id: string;
	value: string;
	onChange: (value: string) => void;
}) {
	// Empty (unset) maps to the "inherit" option; selecting it clears the override.
	// An unknown stored value (set via API/CLI) is preserved as an extra option so
	// the user never silently loses it.
	const known = RESPONSE_LANGUAGE_OPTIONS.includes(value as (typeof RESPONSE_LANGUAGE_OPTIONS)[number]);
	const extra = value && !known ? [value] : [];
	return (
		<Select value={value || "__inherit__"} onValueChange={(v) => onChange(v === "__inherit__" ? "" : v)}>
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__inherit__">Inherit global default</SelectItem>
				{[...extra, ...RESPONSE_LANGUAGE_OPTIONS].map((lang) => (
					<SelectItem key={lang} value={lang}>
						{lang}
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
