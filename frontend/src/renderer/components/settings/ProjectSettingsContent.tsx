import type { components } from "../../../api/schema";
import { RequiredAgentField } from "../CreateProjectAgentSheet";
import { IntakeFields } from "../IntakeFields";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";
import { Textarea } from "../ui/textarea";
import { caFilesCount, caFilesSummary, parseCaFileLines } from "../../lib/sim-trust";
import { ModelField, nextModelOnAgentChange } from "./ModelField";
import { SectionHeading, SettingRow, SettingRows } from "./SettingRow";
import { SettingEditorControl } from "./SettingEditorControl";
import { SettingsReadOnlyPanel, ReadonlyRow } from "./SettingsReadOnlyPanel";
import { RESPONSE_LANGUAGE_OPTIONS } from "./response-language";
import { PROJECT_SECTIONS } from "./settings-sections";
import type { SimTrustMode, useProjectSettingsForm } from "./useProjectSettingsForm";

type Project = components["schemas"]["Project"];
type AgentInfo = components["schemas"]["AgentInfo"];

// "" is stored as unset. The visible option used to read "Project default", which
// is wrong twice over: we ARE standing in the project's settings, and there is no
// project-wide value behind it - the agent's own default mode applies.
const PERMISSION_UNSET_LABEL = "Agent's default mode";
const PERMISSION_MODE_OPTIONS = [
	{ value: "default", label: "Default" },
	{ value: "accept-edits", label: "Accept edits" },
	{ value: "auto", label: "Auto" },
	{ value: "bypass-permissions", label: "Bypass permissions" },
] as const;

// Same correction for the reviewer: unset does not mean "whatever the project
// says", it means claude-code (domain.FallbackReviewerHarness).
const REVIEWER_UNSET_LABEL = "claude-code (default)";
const REVIEWER_OPTIONS = ["claude-code", "codex", "opencode"] as const;

// "none" is the UI spelling of the default (unset) convention; it maps to an
// undefined gitConvention so an otherwise-empty config still persists as unset.
const GIT_WORKFLOW_OPTIONS = [
	{ value: "none", label: "None" },
	{ value: "gitflow", label: "gitflow" },
	{ value: "custom", label: "custom" },
] as const;

// "off" is the UI spelling of an unset mobileScripts; it maps to "" in the form
// and to an omitted field on save.
const MOBILE_SCRIPTS_OPTIONS = [
	{ value: "off", label: "Off" },
	{ value: "ios", label: "Scripts only - iOS" },
	{ value: "android", label: "Scripts only - Android" },
] as const;

// Absent, empty and a list are three different answers (see SimTrustMode), so
// each gets its own option rather than an empty textarea standing for two.
const SIM_TRUST_OPTIONS: { value: SimTrustMode; label: string }[] = [
	{ value: "inherit", label: "Use the global list" },
	{ value: "none", label: "Trust nothing on this project" },
	{ value: "files", label: "Use these files" },
];

// Mirrors domain.DefaultMobileScriptsStore, shown where the field is left empty.
const MOBILE_SCRIPTS_DEFAULT_STORE = "~/Documents/Projects/mobile-ui-scripts";

const INPUT_CLASS =
	"h-8 w-full max-w-[340px] rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak";

type ProjectForm = ReturnType<typeof useProjectSettingsForm>;

function hint(key: string): string {
	return PROJECT_SECTIONS.find((s) => s.key === key)?.hint ?? "";
}

// ProjectSettingsContent renders the active Project section against the shared
// form hook. Only one section shows at a time, but the draft lives in the hook
// above it, so navigating between sections never loses an edit and one save bar
// commits the whole config.
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
		case "start":
			return <StartingATaskSection form={form} />;
		case "told":
			return <WhatAgentsAreToldSection form={form} />;
		case "flow":
			return <IncomingOutgoingSection form={form} />;
		default:
			return <RepositorySection project={project} form={form} />;
	}
}

function RepositorySection({ project, form }: { project: Project; form: ProjectForm }) {
	const { form: draft, setField, isFieldDirty } = form;
	const conventionActive = draft.gitWorkflow === "gitflow" || draft.gitWorkflow === "custom";
	return (
		<>
			<SectionHeading title="Repository & branches" hint={hint("repo")} />

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

			<SettingRows>
				<SettingRow
					name="Default branch"
					summary="The branch a new worktree is cut from when a worker starts here."
					detail="It is also the branch named in the convention text injected into orchestrator and worker prompts, so it is what they treat as the base when nothing else is said."
					ownership={{ kind: "project-only" }}
					timing="next-worker"
					value={draft.defaultBranch || "main"}
					modified={isFieldDirty("defaultBranch")}
					controlId="defaultBranch"
				>
					<input
						id="defaultBranch"
						className={INPUT_CLASS}
						value={draft.defaultBranch}
						onChange={(e) => setField("defaultBranch", e.target.value)}
						placeholder="main"
					/>
				</SettingRow>

				<SettingRow
					name="Session prefix"
					summary="The prefix in front of every session id here - and, when AO names a branch itself, the namespace that branch goes under."
					detail="Empty falls back to the first 12 characters of the project id. The label says session, but branch names follow it too."
					ownership={{ kind: "project-only" }}
					timing="next-worker"
					value={draft.sessionPrefix || `${project.id.slice(0, 12)} (default)`}
					modified={isFieldDirty("sessionPrefix")}
					controlId="sessionPrefix"
				>
					<input
						id="sessionPrefix"
						className={INPUT_CLASS}
						value={draft.sessionPrefix}
						onChange={(e) => setField("sessionPrefix", e.target.value)}
						placeholder={project.id.slice(0, 12)}
					/>
				</SettingRow>

				<SettingRow
					name="Branch workflow"
					summary="Prefixes the branches AO names itself, and tells the orchestrator and its workers how branches are named here."
					detail="None keeps the current behaviour. gitflow still picks bugfix/ or hotfix/ per task, with the prefix below as the default type. custom forces every branch under the prefix you give, and then the prefix is required."
					ownership={{ kind: "project-only" }}
					timing="next-worker"
					value={draft.gitWorkflow || "None"}
					modified={isFieldDirty("gitWorkflow")}
					controlId="gitWorkflow"
				>
					<GitWorkflowSelect id="gitWorkflow" value={draft.gitWorkflow} onChange={(v) => setField("gitWorkflow", v)} />
				</SettingRow>

				{conventionActive && (
					<SettingRow
						name="Branch prefix"
						summary="The prefix every auto-named branch here is put under."
						detail={
							draft.gitWorkflow === "custom"
								? "Required. Every branch is forced under this prefix (e.g. feat/, story/)."
								: "Optional. Default type prefix; gitflow still picks bugfix/ or hotfix/ per task."
						}
						ownership={{ kind: "project-only" }}
						timing="next-worker"
						value={draft.branchPrefix || "none"}
						modified={isFieldDirty("branchPrefix")}
						controlId="branchPrefix"
					>
						<input
							id="branchPrefix"
							className={INPUT_CLASS}
							value={draft.branchPrefix}
							onChange={(e) => setField("branchPrefix", e.target.value)}
							placeholder="feature/"
						/>
					</SettingRow>
				)}
			</SettingRows>
		</>
	);
}

function StartingATaskSection({ form }: { form: ProjectForm }) {
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
	const agentEntry = (agentId: string): AgentInfo | undefined => agentCatalog?.supported?.find((a) => a.id === agentId);
	// Changing an agent degrades the model gracefully: a fixed-tier target that
	// can't run the previously-selected value falls back to that agent's default
	// (empty). A valid free-form value on an open-ended target is preserved even
	// when it isn't a listed suggestion — see nextModelOnAgentChange.
	const changeAgent = (
		agentField: "workerAgent" | "orchestratorAgent",
		modelField: "workerModel" | "orchestratorModel",
		agentId: string,
	) => {
		setField(agentField, agentId);
		const next = nextModelOnAgentChange(draft[modelField], agentEntry(agentId));
		if (next !== draft[modelField]) {
			setField(modelField, next);
		}
	};
	return (
		<>
			<SectionHeading title="Starting a task" hint={hint("start")} />

			{refreshAgentsMutation.isError && (
				<p className="mb-3 text-[12px] leading-5 text-error">
					{refreshAgentsMutation.error instanceof Error
						? refreshAgentsMutation.error.message
						: "Could not refresh agent catalog."}
				</p>
			)}
			{missingRequiredAgent && (
				<p className="mb-3 text-[12px] leading-5 text-error">Worker and orchestrator agents are required.</p>
			)}

			<SettingRows>
				<SettingRow
					name="Worker agent"
					summary="Which coding agent a worker on this project runs."
					detail="Required. Availability is cached - use Refresh agents at the bottom of this section after installing one."
					ownership={{ kind: "project-only" }}
					timing="next-worker"
					value={draft.workerAgent || "not set"}
					modified={isFieldDirty("workerAgent")}
				>
					<RequiredAgentField
						id="workerAgent"
						value={draft.workerAgent}
						placeholder="Select worker agent"
						label="Worker agent"
						hideLabel
						authorized={agentCatalog?.authorized}
						installed={agentCatalog?.installed}
						supported={agentCatalog?.supported}
						disabled={agentsQuery.isFetching && agentCatalog === undefined}
						invalid={validationError !== null && draft.workerAgent === ""}
						onChange={(v) => changeAgent("workerAgent", "workerModel", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Worker model"
					summary="The model a worker here runs on."
					detail="Default falls back to a project-wide model if one was set with the CLI, and only then to the agent's own default - the project-wide one is not shown on this page."
					ownership={{ kind: "project-only" }}
					timing="next-worker"
					value={draft.workerModel || "Default"}
					modified={isFieldDirty("workerModel")}
					controlId="workerModel"
				>
					<ModelField
						id="workerModel"
						value={draft.workerModel}
						agent={agentEntry(draft.workerAgent)}
						onChange={(v) => setField("workerModel", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Orchestrator agent"
					summary="Which agent runs this project's orchestrator."
					detail="Changing this restarts the orchestrator as soon as you save - the running one is replaced."
					ownership={{ kind: "project-only" }}
					timing="on-save"
					value={draft.orchestratorAgent || "not set"}
					modified={isFieldDirty("orchestratorAgent")}
				>
					<RequiredAgentField
						id="orchestratorAgent"
						value={draft.orchestratorAgent}
						placeholder="Select orchestrator agent"
						label="Orchestrator agent"
						hideLabel
						authorized={agentCatalog?.authorized}
						installed={agentCatalog?.installed}
						supported={agentCatalog?.supported}
						disabled={agentsQuery.isFetching && agentCatalog === undefined}
						invalid={validationError !== null && draft.orchestratorAgent === ""}
						onChange={(v) => changeAgent("orchestratorAgent", "orchestratorModel", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Orchestrator model"
					summary="The model the orchestrator here runs on."
					// The form respawns the orchestrator only when the AGENT changed, so a
					// model-only change waits for the next start. The old note said nothing
					// about that and read as though both restarted.
					detail="Unlike the agent above, changing only the model does not restart the orchestrator - it is picked up the next time that session starts."
					ownership={{ kind: "project-only" }}
					timing="next-orchestrator"
					value={draft.orchestratorModel || "Default"}
					modified={isFieldDirty("orchestratorModel")}
					controlId="orchestratorModel"
				>
					<ModelField
						id="orchestratorModel"
						value={draft.orchestratorModel}
						agent={agentEntry(draft.orchestratorAgent)}
						onChange={(v) => setField("orchestratorModel", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Permission mode"
					summary="How much a worker or orchestrator here may do without asking you first."
					detail="Applies to both roles. Unset means the agent's own default mode, not a project-wide value - and a per-role mode set with the CLI wins over this and is not shown here."
					ownership={{ kind: "project-only" }}
					timing="next-worker"
					value={PERMISSION_MODE_OPTIONS.find((o) => o.value === draft.permissions)?.label ?? PERMISSION_UNSET_LABEL}
					modified={isFieldDirty("permissions")}
					controlId="permissionMode"
				>
					<PermissionModeSelect
						id="permissionMode"
						value={draft.permissions}
						onChange={(v) => setField("permissions", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Never form a crew automatically"
					summary="No qa is ever created on its own here, whatever the task size - and an agent asking for one is refused."
					detail="You can still add a qa to a single task by hand, from + qa in its topbar or ao crew add in your own shell; that hatch is a person's. This changes the number of agents only: a standard or deep task still gets the full ceremony. A qa already mid-task keeps working."
					ownership={{ kind: "project-only" }}
					// Read at the eligibility seam, so it applies to future spawns and
					// touches on sessions that are already running.
					timing="live"
					value={draft.disableAutoCrew ? "On" : "Off"}
					modified={isFieldDirty("disableAutoCrew")}
				>
					<div className="flex items-center gap-3">
						<Switch
							id="disableAutoCrew"
							checked={draft.disableAutoCrew}
							onCheckedChange={(v) => setField("disableAutoCrew", v)}
						/>
						<label htmlFor="disableAutoCrew" className="text-[12px] text-muted-foreground">
							Never form a crew automatically
						</label>
					</div>
				</SettingRow>

				<SettingRow
					name="Check in with me before implementing"
					summary="A worker here stops once it understands the task and hands back to you before it changes anything."
					detail="The task moves to Needs you on the board while it waits, and it implements only after you reply. Mechanical tasks never stop - they are already authorised to go straight to the edit. Your orchestrator is told the gate is on here, so it briefs workers to fit it. A worker already running keeps the prompt it was born with; a restore recomputes it."
					ownership={{ kind: "project-only" }}
					timing="next-worker"
					value={draft.pauseBeforeImplementing ? "On" : "Off"}
					modified={isFieldDirty("pauseBeforeImplementing")}
				>
					<div className="flex items-center gap-3">
						<Switch
							id="pauseBeforeImplementing"
							checked={draft.pauseBeforeImplementing}
							onCheckedChange={(v) => setField("pauseBeforeImplementing", v)}
						/>
						<label htmlFor="pauseBeforeImplementing" className="text-[12px] text-muted-foreground">
							Check in with me before implementing
						</label>
					</div>
				</SettingRow>

				<SettingRow
					name="Refresh agents"
					summary="Re-reads which agents are installed and authorised on this Mac."
					detail="The agent lists above are cached, so one you installed since AO started will not appear until this runs."
					ownership={{ kind: "global-only" }}
					timing="instant"
				>
					<button
						type="button"
						className="!text-[12px] rounded text-foreground underline-offset-2 hover:underline disabled:pointer-events-none disabled:opacity-50"
						disabled={refreshAgentsMutation.isPending}
						onClick={() => refreshAgentsMutation.mutate()}
					>
						{refreshAgentsMutation.isPending ? "Refreshing..." : "Refresh agents"}
					</button>
				</SettingRow>
			</SettingRows>
		</>
	);
}

function WhatAgentsAreToldSection({ form }: { form: ProjectForm }) {
	const { form: draft, setField, isFieldDirty, globalResponseLanguage, globalSimTrustCaFiles } = form;
	const globalSimTrust = globalSimTrustCaFiles ? caFilesSummary(globalSimTrustCaFiles) : "the global list";
	return (
		<>
			<SectionHeading title="What agents are told" hint={hint("told")} />
			<SettingRows>
				<SettingRow
					name="Response language"
					summary="The language this project's agents write their human-facing prose in."
					detail="The one setting on this page where a project genuinely overrides a global value. Code, commit messages, PR/MR titles and bodies, branch names and identifiers always stay English."
					ownership={{
						kind: "project-override",
						globalValue: globalResponseLanguage,
						overriding: draft.responseLanguage !== "",
					}}
					timing="next-worker"
					value={draft.responseLanguage || `${globalResponseLanguage} (inherited)`}
					modified={isFieldDirty("responseLanguage")}
					controlId="responseLanguage"
					onUseGlobal={() => setField("responseLanguage", "")}
				>
					<ProjectLanguageSelect
						id="responseLanguage"
						value={draft.responseLanguage}
						onChange={(v) => setField("responseLanguage", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Orchestrator additional prompt"
					summary={
						'Extra text added on top of the global orchestrator base for this project - including its "Must ask" and "Just do" lists.'
					}
					ownership={{ kind: "project-appends", base: "Orchestrator base prompt" }}
					timing="next-orchestrator"
					value={draft.orchestratorPrompt ? "Customised" : "None"}
					modified={isFieldDirty("orchestratorPrompt")}
				>
					<SettingEditorControl
						name="Orchestrator additional prompt"
						textareaLabel="Orchestrator additional prompt"
						value={draft.orchestratorPrompt}
						defaultValue=""
						onChange={(v) => setField("orchestratorPrompt", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Worker additional prompt"
					summary="Extra text added on top of the global worker base for this project's workers."
					ownership={{ kind: "project-appends", base: "Worker base prompt" }}
					timing="next-worker"
					value={draft.workerPrompt ? "Customised" : "None"}
					modified={isFieldDirty("workerPrompt")}
				>
					<SettingEditorControl
						name="Worker additional prompt"
						textareaLabel="Worker additional prompt"
						value={draft.workerPrompt}
						defaultValue=""
						onChange={(v) => setField("workerPrompt", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Reviewer additional prompt"
					summary="Extra text added on top of the global reviewer base for this project."
					ownership={{ kind: "project-appends", base: "Reviewer base prompt" }}
					timing="next-worker"
					value={draft.reviewerPrompt ? "Customised" : "None"}
					modified={isFieldDirty("reviewerPrompt")}
				>
					<SettingEditorControl
						name="Reviewer additional prompt"
						textareaLabel="Reviewer additional prompt"
						value={draft.reviewerPrompt}
						defaultValue=""
						onChange={(v) => setField("reviewerPrompt", v)}
					/>
				</SettingRow>

				<SettingRow
					name="This project has a web UI"
					summary="One switch, three effects: the inspector gains a Browser tab, agents here are told to use ao preview, and ao preview is accepted at all."
					detail="The tab appears and ao preview starts being accepted straight away; the guidance in an agent's prompt only reaches the next worker. Off by default - a project with nothing to preview never shows a permanently empty panel."
					ownership={{ kind: "project-only" }}
					timing="live"
					value={draft.hasWebUI ? "On" : "Off"}
					modified={isFieldDirty("hasWebUI")}
				>
					<div className="flex items-center gap-3">
						<Switch id="hasWebUI" checked={draft.hasWebUI} onCheckedChange={(v) => setField("hasWebUI", v)} />
						<label htmlFor="hasWebUI" className="text-[12px] text-muted-foreground">
							This project has a web UI
						</label>
					</div>
				</SettingRow>

				<SettingRow
					name="This project targets iOS"
					summary="The inspector gains a Simulator tab, and workers here are told how to drive a booted simulator."
					detail="The tab shows a booted iOS Simulator's screen live and can drive it under the same lease and gesture hold ao sim tap uses. Nothing captures unless that tab is open and this window is focused. The tab appears at once; the prompt guidance reaches the next worker."
					ownership={{ kind: "project-only" }}
					timing="live"
					value={draft.hasIOSSimulator ? "On" : "Off"}
					modified={isFieldDirty("hasIOSSimulator")}
				>
					<div className="flex items-center gap-3">
						<Switch
							id="hasIOSSimulator"
							checked={draft.hasIOSSimulator}
							onCheckedChange={(v) => setField("hasIOSSimulator", v)}
						/>
						<label htmlFor="hasIOSSimulator" className="text-[12px] text-muted-foreground">
							This project targets iOS
						</label>
					</div>
				</SettingRow>

				<SettingRow
					name="Simulator root CAs"
					summary="Which root certificates a simulator AO boots or claims for this project is made to trust, so HTTPS through a debugging proxy works inside the app."
					detail={
						<>
							Use the global list unless this project's traffic goes through a different proxy. Trust nothing turns it
							off here. Use these files replaces the global list: one PEM or DER file per line, as an absolute path or
							one starting with <code>~/</code>. A file that is not on this Mac is skipped silently. A simulator picks
							up a change the next time AO boots or claims it.
						</>
					}
					ownership={{
						kind: "project-override",
						globalValue: globalSimTrust,
						overriding: draft.simTrustMode !== "inherit",
					}}
					timing="live"
					value={simTrustValue(draft.simTrustMode, draft.simTrustCaFiles)}
					modified={isFieldDirty("simTrustMode") || isFieldDirty("simTrustCaFiles")}
					controlId="simTrustMode"
					onUseGlobal={() => setField("simTrustMode", "inherit")}
				>
					<div className="flex flex-col gap-2.5">
						<SimTrustSelect
							id="simTrustMode"
							value={draft.simTrustMode}
							onChange={(v) => setField("simTrustMode", v)}
						/>
						{draft.simTrustMode === "files" && (
							<Textarea
								aria-label="Root-CA files for this project"
								className="min-h-20 max-w-[620px] resize-y font-mono text-[12.5px] leading-relaxed"
								wrap="off"
								placeholder="~/path/to/root-ca.pem"
								spellCheck={false}
								value={draft.simTrustCaFiles}
								onChange={(e) => setField("simTrustCaFiles", e.target.value)}
							/>
						)}
					</div>
				</SettingRow>

				<SettingRow
					name="Drive devices only through scripts"
					summary="Workers here reach a screen by running a reusable Maestro script from the scripts store, never by tapping through the app step by step."
					detail="Their prompt teaches the script workflow in place of the ao sim tap catalog - find the script, run it, judge the screen it left, write a missing one once, never finish a failed run by hand - and qa drives its checks with scripts. iOS scripts run through ao sim flow run; Android has no ao sim, so they run through maestro --device. Off leaves the guidance exactly as it is."
					ownership={{ kind: "project-only" }}
					timing="next-worker"
					value={
						MOBILE_SCRIPTS_OPTIONS.find((o) => o.value === (draft.mobileScriptsPlatform || "off"))?.label ??
						draft.mobileScriptsPlatform
					}
					modified={isFieldDirty("mobileScriptsPlatform")}
					controlId="mobileScriptsPlatform"
				>
					<MobileScriptsSelect
						id="mobileScriptsPlatform"
						value={draft.mobileScriptsPlatform}
						onChange={(v) => setField("mobileScriptsPlatform", v)}
					/>
				</SettingRow>

				{draft.mobileScriptsPlatform !== "" && (
					<SettingRow
						name="Scripts product"
						summary="The product's folder in the scripts store, shared by its iOS and Android apps."
						detail="Required. Workers look scripts up in projects/<product>/INDEX.md and run them with bin/flow run <product>."
						ownership={{ kind: "project-only" }}
						timing="next-worker"
						value={draft.mobileScriptsProduct || "not set"}
						modified={isFieldDirty("mobileScriptsProduct")}
						controlId="mobileScriptsProduct"
					>
						<input
							id="mobileScriptsProduct"
							className={INPUT_CLASS}
							value={draft.mobileScriptsProduct}
							onChange={(e) => setField("mobileScriptsProduct", e.target.value)}
							placeholder="nter"
						/>
					</SettingRow>
				)}

				{draft.mobileScriptsPlatform !== "" && (
					<SettingRow
						name="Scripts store"
						summary="The checkout holding bin/flow, projects/ and accounts/."
						detail="Optional. An absolute path, or one starting with ~/. Empty uses the default store."
						ownership={{ kind: "project-only" }}
						timing="next-worker"
						value={draft.mobileScriptsStore || `${MOBILE_SCRIPTS_DEFAULT_STORE} (default)`}
						modified={isFieldDirty("mobileScriptsStore")}
						controlId="mobileScriptsStore"
					>
						<input
							id="mobileScriptsStore"
							className={INPUT_CLASS}
							value={draft.mobileScriptsStore}
							onChange={(e) => setField("mobileScriptsStore", e.target.value)}
							placeholder={MOBILE_SCRIPTS_DEFAULT_STORE}
						/>
					</SettingRow>
				)}
			</SettingRows>
		</>
	);
}

function IncomingOutgoingSection({ form }: { form: ProjectForm }) {
	const {
		form: draft,
		setField,
		isFieldDirty,
		isGitLabProject,
		hasKnownRemote,
		intakeForm,
		patchIntake,
		effectiveIntakeRepo,
		intakeRepoURL,
	} = form;
	const approvalGate = !hasKnownRemote
		? "AO couldn't detect a git remote for this project, so it can't tell which forge it lives on and forge-specific settings stay unavailable. Add a remote to the repository, then reopen this page."
		: !isGitLabProject
			? "Approvals are counted for GitLab only in this version, and this project is not on GitLab."
			: undefined;
	return (
		<>
			<SectionHeading title="Incoming & outgoing" hint={hint("flow")} />
			<SettingRows>
				<SettingRow
					name="Tracker intake"
					summary="AO watches the tracker and spawns a worker itself when an issue matching your rule appears."
					detail="Read-only toward the tracker: matching issues spawn sessions, but AO does not comment on them or move them. Enabling it requires an assignee - a username, or * for any."
					ownership={{ kind: "project-only" }}
					timing="on-save"
					value={draft.intakeEnabled ? "On" : "Off"}
					modified={isFieldDirty("intakeEnabled") || isFieldDirty("intakeRepo") || isFieldDirty("intakeAssignee")}
				>
					<IntakeFields
						form={intakeForm}
						onChange={patchIntake}
						repoPreview={{ value: effectiveIntakeRepo, url: intakeRepoURL }}
					/>
				</SettingRow>

				<SettingRow
					name="Default reviewer agent"
					summary="The agent that runs AO's code review on this project's pull requests."
					detail="Unset means claude-code, not whatever the project says - there is no project-wide reviewer behind this one."
					ownership={{ kind: "project-only" }}
					timing="on-save"
					value={draft.reviewerHarness || REVIEWER_UNSET_LABEL}
					modified={isFieldDirty("reviewerHarness")}
					controlId="reviewerHarness"
				>
					<ReviewerSelect
						id="reviewerHarness"
						value={draft.reviewerHarness}
						onChange={(v) => setField("reviewerHarness", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Require approvals before Ready to merge"
					summary="A merge request is only reported as Ready to merge once it has the required number of approvals."
					detail="Off by default; applies only when the GitLab repo has no approval rule of its own."
					ownership={{ kind: "project-only" }}
					timing="on-save"
					value={draft.approvalRuleEnabled ? "On" : "Off"}
					modified={isFieldDirty("approvalRuleEnabled") || isFieldDirty("approvalThreshold")}
					gate={approvalGate}
				>
					<div className="flex flex-col gap-3">
						<div className="flex items-center gap-3">
							<Switch
								id="approvalRuleEnabled"
								checked={draft.approvalRuleEnabled}
								onCheckedChange={(v) => setField("approvalRuleEnabled", v)}
							/>
							<label htmlFor="approvalRuleEnabled" className="text-[12px] text-muted-foreground">
								Require approvals before Ready to merge
							</label>
						</div>
						{draft.approvalRuleEnabled && (
							<div className="flex flex-col gap-1.5">
								<label htmlFor="approvalThreshold" className="text-[12px] text-muted-foreground">
									Required approvals
								</label>
								<input
									id="approvalThreshold"
									type="number"
									min={1}
									className={INPUT_CLASS}
									value={draft.approvalThreshold}
									onChange={(e) => setField("approvalThreshold", e.target.value)}
									placeholder="2"
								/>
								<span className="text-[11px] text-passive">Default 2.</span>
							</div>
						)}
					</div>
				</SettingRow>

				<SettingRow
					name="Learn from sessions"
					summary="AO keeps redacted excerpts of what you type to this project's sessions, so it can later propose memories from what you taught."
					detail="Off means nothing here is read: no transcript is opened and nothing is stored. On, AO reads this project's Claude Code transcripts and keeps only your own turns, with a short window of what the agent did around each - secrets, test-account values and pasted payloads are taken out first. Nothing is sent anywhere. Turning it off stops capture; ao learn forget then deletes what was kept."
					ownership={{ kind: "project-only" }}
					timing="on-save"
					value={draft.learnFromSessions ? "On" : "Off"}
					modified={isFieldDirty("learnFromSessions")}
				>
					<div className="flex items-center gap-3">
						<Switch
							id="learnFromSessions"
							checked={draft.learnFromSessions}
							onCheckedChange={(v) => setField("learnFromSessions", v)}
						/>
						<label htmlFor="learnFromSessions" className="text-[12px] text-muted-foreground">
							Learn from sessions
						</label>
					</div>
				</SettingRow>
			</SettingRows>
		</>
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
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__default__">{PERMISSION_UNSET_LABEL}</SelectItem>
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
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
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

// The row's at-rest value: what this project's simulators actually trust.
function simTrustValue(mode: SimTrustMode, lines: string): string {
	if (mode === "inherit") return "Global list (inherited)";
	if (mode === "none") return "None";
	return caFilesCount(parseCaFileLines(lines));
}

function SimTrustSelect({
	id,
	value,
	onChange,
}: {
	id: string;
	value: SimTrustMode;
	onChange: (value: SimTrustMode) => void;
}) {
	return (
		<Select value={value} onValueChange={(v) => onChange(v as SimTrustMode)}>
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				{SIM_TRUST_OPTIONS.map((opt) => (
					<SelectItem key={opt.value} value={opt.value}>
						{opt.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function MobileScriptsSelect({
	id,
	value,
	onChange,
}: {
	id: string;
	value: string;
	onChange: (value: string) => void;
}) {
	// Empty (unset) maps to the "off" option; selecting "off" clears the value.
	return (
		<Select value={value || "off"} onValueChange={(v) => onChange(v === "off" ? "" : v)}>
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				{MOBILE_SCRIPTS_OPTIONS.map((opt) => (
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
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
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
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__default__">{REVIEWER_UNSET_LABEL}</SelectItem>
				{REVIEWER_OPTIONS.map((reviewer) => (
					<SelectItem key={reviewer} value={reviewer}>
						{reviewer}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}
