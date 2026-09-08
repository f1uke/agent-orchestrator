// The settings catalog behind the redesign preview. It is DESIGN DATA, not the
// real config: every value is mock and nothing here saves. What it does carry
// faithfully is the inventory - for each setting, the honest one-line summary of
// what it changes, whether it is Global or per-Project (and which of the two can
// override the other), and WHEN it takes effect. Those three facts are the whole
// point of the redesign, so they live in the model rather than in the markup.

export type Scope = "global" | "project";

// When a setting bites. The distinction is invisible on today's page and is half
// of what makes it feel untrustworthy.
export type Timing =
	| "next-worker" // baked into the spawn/system prompt; running sessions keep what they were born with
	| "next-orchestrator" // same, but for the orchestrator session - it needs a restart
	| "live" // read at the moment the behaviour fires, so running sessions change too
	| "on-save" // takes effect as soon as the save lands; no session involved
	| "instant" // the control acts on click and never goes through the save bar
	| "readonly";

export const TIMING_LABEL: Record<Timing, string> = {
	"next-worker": "Next worker",
	"next-orchestrator": "Needs orchestrator restart",
	live: "Now, running sessions too",
	"on-save": "On save",
	instant: "Instant, not saved",
	readonly: "Read-only",
};

// Amber timings are the ones a person guesses WRONG. Everything else is quiet.
export const TIMING_TONE: Record<Timing, "quiet" | "warn"> = {
	"next-worker": "quiet",
	"next-orchestrator": "warn",
	live: "warn",
	"on-save": "quiet",
	instant: "warn",
	readonly: "quiet",
};

// How a setting relates to the other scope. Stating this AT the control is the
// second thing Fluke asked for - and the honest finding is that almost nothing
// here is actually overridable, so most rows say so plainly.
export type Ownership =
	| { kind: "global-only" }
	| { kind: "global-appendable" }
	| { kind: "project-only" }
	| { kind: "global-overridable"; overriddenBy: { project: string; value: string }[] }
	| { kind: "project-override"; globalValue: string; overriding: boolean }
	| { kind: "project-appends"; base: string }
	| { kind: "global-session-override"; followers: number }
	| { kind: "readonly" };

export type Control =
	| { type: "toggle"; value: boolean }
	| { type: "select"; value: string; options: string[] }
	| { type: "text"; value: string; placeholder?: string; mono?: boolean }
	| { type: "number"; value: number; unit: string }
	| { type: "editor"; value: string; customized: boolean; placeholders?: string[] }
	| { type: "action"; label: string; note?: string }
	| { type: "readonly"; value: string };

export type Setting = {
	id: string;
	name: string;
	// The one honest sentence. Written, never lorem - a placeholder here would
	// hide the exact problem this redesign exists to solve.
	summary: string;
	// Everything longer: caveats, exemptions, where the audit log lands. Folded.
	detail?: string;
	timing: Timing;
	ownership: Ownership;
	control: Control;
	// Section key in each variant.
	a: string;
	b: string;
	// Touched once and then never again -> below the "Rarely changed" fold in A.
	rare?: boolean;
	// Differs from its built-in default, for the "Changed from default" filter.
	changed?: boolean;
	// Shown instead of the control when the setting is conditional.
	gate?: string;
};

export type Section = { key: string; label: string; hint: string };

export type PreviewProject = {
	id: string;
	name: string;
	path: string;
	repo: string;
	kind: "single repo" | "workspace";
	forge: "github" | "gitlab" | "none";
};

export const PROJECTS: PreviewProject[] = [
	{
		id: "agent-orchestrator",
		name: "agent-orchestrator",
		path: "~/Documents/Projects/agent-orchestrator",
		repo: "git@github.com:aoagents/agent-orchestrator.git",
		kind: "single repo",
		forge: "github",
	},
	{
		id: "advisor-ios-app",
		name: "advisor-ios-app",
		path: "~/Documents/Projects/advisor-ios-app",
		repo: "git@gitlab.finnomena.com:mobile/advisor-ios-app.git",
		kind: "single repo",
		forge: "gitlab",
	},
	{
		id: "finnomena-web",
		name: "finnomena-web",
		path: "~/Documents/Projects/finnomena-web",
		repo: "",
		kind: "workspace",
		forge: "none",
	},
];

// The two projects that override the global response language. Named on the
// GLOBAL row so the override is visible from both ends, not just the project's.
const LANGUAGE_OVERRIDES = [
	{ project: "agent-orchestrator", value: "Thai" },
	{ project: "advisor-ios-app", value: "Thai" },
];

export const PROJECT_SECTIONS_A: Section[] = [
	{ key: "general", label: "General", hint: "repository, worktrees & branch naming" },
	{ key: "agents", label: "Agents", hint: "who runs, on which model & permission" },
	{ key: "prompts", label: "Prompts", hint: "additional system prompts for this project" },
	{ key: "automation", label: "Automation", hint: "things AO does on its own here" },
];

export const PROJECT_SECTIONS_B: Section[] = [
	{ key: "repo", label: "Repository & branches", hint: "where the work lands" },
	{ key: "start", label: "Starting a task", hint: "who AO puts on it, and how far it may go" },
	{ key: "told", label: "What agents are told", hint: "language, extra prompts, what this project has" },
	{ key: "flow", label: "Incoming & outgoing", hint: "issues in, reviews and merges out" },
];

export const GLOBAL_SECTIONS_A: Section[] = [
	{ key: "prompts", label: "Prompts", hint: "the global base each session kind starts from" },
	{ key: "messages", label: "Messages", hint: "runtime nudge messages sent into a worker" },
	{ key: "automation", label: "Automation", hint: "orchestrator & daemon automatic behaviour" },
	{ key: "system", label: "System", hint: "wiki, notifications, updates, companion & migration" },
];

export const GLOBAL_SECTIONS_B: Section[] = [
	{ key: "every-agent", label: "Every agent", hint: "what every session starts from" },
	{ key: "running", label: "While work runs", hint: "what AO says and does to a worker mid-task" },
	{ key: "cleanup", label: "Cleaning up", hint: "what AO deletes once work is finished" },
	{ key: "mac", label: "This Mac", hint: "the app itself, not the agents" },
];

// ---------------------------------------------------------------------------
// Global settings
// ---------------------------------------------------------------------------

const PROMPT_BASE_SAMPLE = `You are a worker session in Agent Orchestrator.

You have been given one task on one branch. Work it to completion: read the code
before you change it, run the project's own checks, and open the pull request
against the target you were spawned with.`;

const TEMPLATE_SAMPLE = `Your pull request {{.PRRef}} has review feedback you have not
addressed yet:

{{.Comments}}

Make the change, then reply on the thread once the human has confirmed.`;

export const GLOBAL_SETTINGS: Setting[] = [
	{
		id: "g.language",
		name: "Default response language",
		summary:
			"The language every agent writes its human-facing prose in - status updates, reports, questions, PR review comments.",
		detail:
			"Code, commit messages, PR/MR titles and bodies, branch names and identifiers always stay English; only prose addressed to a person changes. English is the shipped default and injects no directive at all, so the default path is byte-for-byte the prompt it always was.",
		timing: "next-worker",
		ownership: { kind: "global-overridable", overriddenBy: LANGUAGE_OVERRIDES },
		control: { type: "select", value: "English", options: ["English", "Thai", "Japanese", "German"] },
		a: "prompts",
		b: "every-agent",
	},
	{
		id: "g.prompt.orchestrator",
		name: "Orchestrator base prompt",
		summary: "The editable text every orchestrator session starts from.",
		detail:
			"AO always appends a protected coordination floor, the confidentiality guard, and dynamic context (git convention, spawn-confirm, session and project ids) on top of whatever you write here - those are not shown and cannot be removed. Use {{.ProjectID}} to insert the project id.",
		timing: "next-orchestrator",
		ownership: { kind: "global-appendable" },
		control: { type: "editor", value: PROMPT_BASE_SAMPLE, customized: false },
		a: "prompts",
		b: "every-agent",
	},
	{
		id: "g.prompt.worker",
		name: "Worker base prompt",
		summary: "The editable text every worker session starts from.",
		detail: "Same protected floor as the orchestrator base. A project can APPEND to this, but never replace it.",
		timing: "next-worker",
		ownership: { kind: "global-appendable" },
		control: { type: "editor", value: PROMPT_BASE_SAMPLE, customized: true },
		changed: true,
		a: "prompts",
		b: "every-agent",
	},
	{
		id: "g.prompt.qa",
		name: "QA base prompt",
		summary: "The base the qa member of a crew starts from - a worker session doing a different job.",
		detail:
			"Only crews have a qa. A project that never forms one still carries this base; it is simply never used there.",
		timing: "next-worker",
		ownership: { kind: "global-appendable" },
		control: { type: "editor", value: PROMPT_BASE_SAMPLE, customized: false },
		rare: true,
		a: "prompts",
		b: "every-agent",
	},
	{
		id: "g.prompt.reviewer",
		name: "Reviewer base prompt",
		summary: "The base the agent that reviews a worker's pull request starts from.",
		timing: "next-worker",
		ownership: { kind: "global-appendable" },
		control: { type: "editor", value: PROMPT_BASE_SAMPLE, customized: false },
		rare: true,
		a: "prompts",
		b: "every-agent",
	},
	{
		id: "g.tmpl.review-comment-dispatch",
		name: "Review comment dispatch",
		summary: "What AO types into a worker's terminal when its pull request picks up review feedback.",
		detail: "Go text/template. A bad edit is caught at send time and falls back to the built-in default.",
		timing: "live",
		ownership: { kind: "global-only" },
		control: {
			type: "editor",
			value: TEMPLATE_SAMPLE,
			customized: true,
			placeholders: ["{{.PRRef}}", "{{.Comments}}"],
		},
		changed: true,
		a: "messages",
		b: "running",
	},
	{
		id: "g.tmpl.ci-failing",
		name: "CI failing",
		summary: "Sent when the worker's pull request goes red.",
		timing: "live",
		ownership: { kind: "global-only" },
		control: { type: "editor", value: TEMPLATE_SAMPLE, customized: false, placeholders: ["{{.PRRef}}", "{{.Checks}}"] },
		a: "messages",
		b: "running",
	},
	{
		id: "g.tmpl.merge-conflict",
		name: "Merge conflict",
		summary: "Sent when the worker's branch stops merging cleanly into its target.",
		timing: "live",
		ownership: { kind: "global-only" },
		control: { type: "editor", value: TEMPLATE_SAMPLE, customized: false, placeholders: ["{{.PRRef}}", "{{.Base}}"] },
		a: "messages",
		b: "running",
	},
	{
		id: "g.tmpl.pr-base-mismatch",
		name: "PR base mismatch",
		summary: "Sent when a pull request would merge somewhere other than where the session was spawned to land.",
		detail:
			"AO never retargets the PR itself - a base chosen on purpose must stay possible - so this message is the whole of the response.",
		timing: "live",
		ownership: { kind: "global-only" },
		control: { type: "editor", value: TEMPLATE_SAMPLE, customized: false, placeholders: ["{{.Base}}", "{{.Target}}"] },
		rare: true,
		a: "messages",
		b: "running",
	},
	{
		id: "g.tmpl.tracker-bot-comment",
		name: "Tracker bot comment",
		summary: "Sent when the tracker issue behind a session gets a new comment.",
		timing: "live",
		ownership: { kind: "global-only" },
		control: { type: "editor", value: TEMPLATE_SAMPLE, customized: false, placeholders: ["{{.Issue}}", "{{.Body}}"] },
		rare: true,
		a: "messages",
		b: "running",
	},
	{
		id: "g.tmpl.ao-reviewer-batch",
		name: "AO reviewer - batch",
		summary: "Sent when AO's own reviewer returns more than one finding at once.",
		timing: "live",
		ownership: { kind: "global-only" },
		control: { type: "editor", value: TEMPLATE_SAMPLE, customized: false, placeholders: ["{{.Findings}}"] },
		rare: true,
		a: "messages",
		b: "running",
	},
	{
		id: "g.tmpl.ao-reviewer-single",
		name: "AO reviewer - single",
		summary: "Sent when AO's own reviewer returns exactly one finding.",
		timing: "live",
		ownership: { kind: "global-only" },
		control: { type: "editor", value: TEMPLATE_SAMPLE, customized: false, placeholders: ["{{.Finding}}"] },
		rare: true,
		a: "messages",
		b: "running",
	},
	{
		id: "g.spawn-confirm",
		name: "Confirm before spawning workers",
		summary:
			"The orchestrator shows you the task, the source branch, the new branch and the PR target, and waits for your yes before it spawns.",
		detail:
			"This is rendered into the orchestrator's own system prompt when that session starts, so the orchestrator you have open right now will not change its behaviour until it is restarted. Turning it off lets the orchestrator spawn workers directly.",
		timing: "next-orchestrator",
		ownership: { kind: "global-only" },
		control: { type: "toggle", value: true },
		a: "automation",
		b: "running",
	},
	{
		id: "g.auto-nudge",
		name: "Auto-send unresolved PR comments",
		summary:
			"A session whose pull request gets review feedback nudges its worker on its own, instead of waiting for you to send it.",
		detail:
			"This is read at the moment the feedback arrives, not at spawn - so changing it here changes what every session already running does, unless that session has set its own answer in its Reviews tab. AO fetches and stores the comments either way; this only decides whether the worker is told.",
		timing: "live",
		ownership: { kind: "global-session-override", followers: 4 },
		control: { type: "toggle", value: false },
		a: "automation",
		b: "running",
	},
	{
		id: "g.reclaim",
		name: "Auto-reclaim finished sessions",
		summary: "Once a session is merged or terminated, AO tears down its tmux window and its worktree.",
		detail:
			"The git branch is always kept, so a reclaimed session can still be restored. A worktree with uncommitted or untracked changes is never reclaimed. Every reclaim and every refusal is recorded in ~/.ao/data/reclaim.jsonl with what was removed, why it qualified, how much it freed, and the branch it left behind.",
		timing: "on-save",
		ownership: { kind: "global-only" },
		control: { type: "toggle", value: true },
		a: "automation",
		b: "cleanup",
	},
	{
		id: "g.reclaim-grace",
		name: "Grace period",
		summary: "How long AO waits after a session finishes before it reclaims anything.",
		timing: "on-save",
		ownership: { kind: "global-only" },
		control: { type: "number", value: 1440, unit: "minutes" },
		a: "automation",
		b: "cleanup",
	},
	{
		id: "g.reclaim-artifacts",
		name: "Clear build output",
		summary: "Treat derivedDataPath, Pods and node_modules as disposable rather than as work worth keeping.",
		detail:
			"Untracked build output would otherwise keep a finished worktree on disk for ever. AO moves it aside only when nothing ELSE in the worktree has changed. Turn this off to treat build output as work too.",
		timing: "on-save",
		ownership: { kind: "global-only" },
		control: { type: "toggle", value: true },
		rare: true,
		a: "automation",
		b: "cleanup",
	},
	{
		id: "g.evidence",
		name: "Smoke-test evidence retention",
		summary: "Screenshots and clips you attach in the Tests tab are deleted once they pass the age below.",
		detail: "Measured from when the evidence was captured. Turn this off to keep evidence for ever.",
		timing: "on-save",
		ownership: { kind: "global-only" },
		control: { type: "toggle", value: true },
		a: "automation",
		b: "cleanup",
	},
	{
		id: "g.evidence-days",
		name: "Delete evidence older than",
		summary: "The age at which an attached screenshot or clip is swept.",
		timing: "on-save",
		ownership: { kind: "global-only" },
		control: { type: "number", value: 30, unit: "days" },
		a: "automation",
		b: "cleanup",
	},
	{
		id: "g.evidence-purge",
		name: "Purge evidence now",
		summary: "Runs the age sweep immediately and reports what it removed.",
		detail: "It uses the SAVED retention age, not the one in the box above - save a change first for it to count.",
		timing: "instant",
		ownership: { kind: "global-only" },
		control: { type: "action", label: "Purge now" },
		rare: true,
		a: "automation",
		b: "cleanup",
	},
	{
		id: "g.updates",
		name: "Automatic updates",
		summary: "AO downloads and installs new builds of the desktop app on its own.",
		timing: "on-save",
		ownership: { kind: "global-only" },
		control: { type: "toggle", value: false },
		a: "system",
		b: "mac",
	},
	{
		id: "g.update-channel",
		name: "Update channel",
		summary: "Which build stream automatic updates follow.",
		detail:
			"Nightly builds are cut every day and can be unstable or lose data. Only use Nightly if you are comfortable with that.",
		timing: "on-save",
		ownership: { kind: "global-only" },
		control: {
			type: "select",
			value: "Stable (latest release)",
			options: ["Stable (latest release)", "Nightly (pre-release)"],
		},
		a: "system",
		b: "mac",
	},
	{
		id: "g.update-check",
		name: "Check for updates",
		summary: "Looks for a newer build right now, even when automatic updates are off.",
		timing: "instant",
		ownership: { kind: "global-only" },
		control: { type: "action", label: "Check for updates", note: "Current version v0.10.0" },
		a: "system",
		b: "mac",
	},
	{
		id: "g.wiki",
		name: "Wiki vault folder",
		summary:
			"A folder of markdown notes you can ask an agent about. Setting a path adds a Wiki entry above Projects in the sidebar.",
		detail:
			"An absolute path, or one starting with ~/. The agent runs with your notes as its working directory and can edit and create them. Leaving it empty removes the sidebar entry entirely.",
		timing: "on-save",
		ownership: { kind: "global-only" },
		control: { type: "text", value: "~/Notes", placeholder: "~/Notes", mono: true },
		changed: true,
		a: "system",
		b: "mac",
	},
	{
		id: "g.notify-test",
		name: "Test notification",
		summary:
			"Sends a native banner down the exact path a real notification takes, so you can confirm macOS is letting them through.",
		timing: "instant",
		ownership: { kind: "global-only" },
		control: { type: "action", label: "Send test notification" },
		rare: true,
		a: "system",
		b: "mac",
	},
	{
		id: "g.companion",
		name: "Desktop companion",
		summary:
			"Shows one small character per session along the bottom of your screen - working, waiting on you, or done.",
		detail:
			"It sits above the Dock and clicks pass straight through it, except on a character itself. This switch opens or closes a window immediately - it deliberately does not wait for Save.",
		timing: "instant",
		ownership: { kind: "global-only" },
		control: { type: "toggle", value: true },
		changed: true,
		a: "system",
		b: "mac",
	},
	{
		id: "g.pets",
		name: "Pet library",
		summary: "Which creature each project's sessions appear as in the companion.",
		detail: "Also reachable by right-clicking a character on the desktop, which is where most people find it.",
		timing: "instant",
		ownership: { kind: "global-only" },
		control: { type: "action", label: "Open pet library" },
		rare: true,
		a: "system",
		b: "mac",
	},
	{
		id: "g.migration",
		name: "Migration from a legacy install",
		summary: "Imports projects and orchestrator sessions from an older Agent Orchestrator install.",
		detail: "Your old files are never modified and this is safe to run more than once. Nothing found on this Mac.",
		timing: "instant",
		ownership: { kind: "global-only" },
		control: { type: "action", label: "Run migration", note: "Status: nothing to import" },
		rare: true,
		a: "system",
		b: "mac",
	},
];

// ---------------------------------------------------------------------------
// Project settings - values vary per project so the override case is visible
// ---------------------------------------------------------------------------

type ProjectValues = {
	language: string; // "" = inherit
	branch: string;
	prefix: string;
	workflow: string;
	branchPrefix: string;
	workerAgent: string;
	workerModel: string;
	orchestratorAgent: string;
	orchestratorModel: string;
	permissions: string;
	reviewer: string;
	webUI: boolean;
	ios: boolean;
	noCrew: boolean;
	checkIn: boolean;
	intake: boolean;
	workerPrompt: string;
};

const VALUES: Record<string, ProjectValues> = {
	"agent-orchestrator": {
		language: "Thai",
		branch: "main-fluke",
		prefix: "ao",
		workflow: "custom",
		branchPrefix: "feature/",
		workerAgent: "claude-code",
		workerModel: "claude-opus-5",
		orchestratorAgent: "claude-code",
		orchestratorModel: "",
		permissions: "bypass-permissions",
		reviewer: "",
		webUI: true,
		ios: false,
		noCrew: false,
		checkIn: true,
		intake: false,
		workerPrompt: "Never use the em dash. Prefer quality and long-term maintainability over development cost.",
	},
	"advisor-ios-app": {
		language: "Thai",
		branch: "develop",
		prefix: "",
		workflow: "gitflow",
		branchPrefix: "feature/",
		workerAgent: "claude-code",
		workerModel: "",
		orchestratorAgent: "claude-code",
		orchestratorModel: "",
		permissions: "",
		reviewer: "codex",
		webUI: false,
		ios: true,
		noCrew: false,
		checkIn: false,
		intake: true,
		workerPrompt: "",
	},
	"finnomena-web": {
		language: "",
		branch: "main",
		prefix: "",
		workflow: "",
		branchPrefix: "",
		workerAgent: "claude-code",
		workerModel: "",
		orchestratorAgent: "claude-code",
		orchestratorModel: "",
		permissions: "",
		reviewer: "",
		webUI: true,
		ios: false,
		noCrew: true,
		checkIn: false,
		intake: false,
		workerPrompt: "",
	},
};

const GLOBAL_LANGUAGE = "English";

export function projectSettings(project: PreviewProject): Setting[] {
	const v = VALUES[project.id];
	const list: Setting[] = [
		{
			id: "p.identity",
			name: "Identity",
			summary: "What AO knows about this project. Set when it was added; not editable here.",
			timing: "readonly",
			ownership: { kind: "readonly" },
			control: {
				type: "readonly",
				value: `${project.id} · ${project.kind}\n${project.path}\n${project.repo || "no git remote detected"}`,
			},
			a: "general",
			b: "repo",
		},
		{
			id: "p.branch",
			name: "Default branch",
			summary: "The branch a new worktree is cut from when a worker starts here.",
			detail:
				"It is also the branch named in the convention text injected into orchestrator and worker prompts, so it is what they treat as 'the base' when nothing else is said.",
			timing: "next-worker",
			ownership: { kind: "project-only" },
			control: { type: "text", value: v.branch, placeholder: "main", mono: true },
			changed: v.branch !== "main",
			a: "general",
			b: "repo",
		},
		{
			id: "p.prefix",
			name: "Session prefix",
			summary:
				"The prefix in front of every session id here - and, when AO names a branch itself, the namespace that branch goes under.",
			detail:
				"Empty falls back to the first 12 characters of the project id. The label says 'session', but branch names follow it too.",
			timing: "next-worker",
			ownership: { kind: "project-only" },
			control: { type: "text", value: v.prefix, placeholder: project.id.slice(0, 12), mono: true },
			changed: v.prefix !== "",
			a: "general",
			b: "repo",
		},
		{
			id: "p.workflow",
			name: "Branch workflow",
			summary:
				"Prefixes the branches AO names itself, and tells the orchestrator and its workers how branches are named here.",
			detail:
				"None keeps the current behaviour. gitflow still picks bugfix/ or hotfix/ per task, with the prefix below as the default type. custom forces every branch under the prefix you give, and the prefix is then required.",
			timing: "next-worker",
			ownership: { kind: "project-only" },
			control: { type: "select", value: v.workflow || "None", options: ["None", "gitflow", "custom"] },
			changed: v.workflow !== "",
			a: "general",
			b: "repo",
		},
		{
			id: "p.branch-prefix",
			name: "Branch prefix",
			summary: "The prefix every auto-named branch here is put under.",
			timing: "next-worker",
			ownership: { kind: "project-only" },
			control: { type: "text", value: v.branchPrefix, placeholder: "feature/", mono: true },
			changed: v.branchPrefix !== "",
			gate: v.workflow === "" ? "Shown when the branch workflow is gitflow or custom" : undefined,
			a: "general",
			b: "repo",
		},
		{
			id: "p.worker-agent",
			name: "Worker agent",
			summary: "Which coding agent a worker on this project runs.",
			detail: "Required. Availability is cached; Refresh agents re-reads it.",
			timing: "next-worker",
			ownership: { kind: "project-only" },
			control: { type: "select", value: v.workerAgent, options: ["claude-code", "codex", "opencode"] },
			a: "agents",
			b: "start",
		},
		{
			id: "p.worker-model",
			name: "Worker model",
			summary: "The model a worker here runs on.",
			detail:
				"Default falls back to the project-wide model if one was set with the CLI, and only then to the agent's own default - the project-wide one is not shown on this page.",
			timing: "next-worker",
			ownership: { kind: "project-only" },
			control: {
				type: "select",
				value: v.workerModel || "Default",
				options: ["Default", "claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"],
			},
			changed: v.workerModel !== "",
			a: "agents",
			b: "start",
		},
		{
			id: "p.orchestrator-agent",
			name: "Orchestrator agent",
			summary: "Which agent runs this project's orchestrator.",
			detail: "Changing this restarts the orchestrator as soon as you save - the running one is replaced.",
			timing: "on-save",
			ownership: { kind: "project-only" },
			control: { type: "select", value: v.orchestratorAgent, options: ["claude-code", "codex", "opencode"] },
			a: "agents",
			b: "start",
		},
		{
			id: "p.orchestrator-model",
			name: "Orchestrator model",
			summary: "The model the orchestrator here runs on.",
			detail:
				"Unlike the agent above, changing only the model does NOT restart the orchestrator - it is picked up the next time that session starts.",
			timing: "next-orchestrator",
			ownership: { kind: "project-only" },
			control: {
				type: "select",
				value: v.orchestratorModel || "Default",
				options: ["Default", "claude-opus-5", "claude-sonnet-5"],
			},
			changed: v.orchestratorModel !== "",
			a: "agents",
			b: "start",
		},
		{
			id: "p.permissions",
			name: "Permission mode",
			summary: "How much a worker or orchestrator here may do without asking you first.",
			detail:
				"Applies to both roles. Unset means the agent's own default mode, not a project-wide value - and a per-role mode set with the CLI wins over this and is not shown here.",
			timing: "next-worker",
			ownership: { kind: "project-only" },
			control: {
				type: "select",
				value: v.permissions || "Agent's default",
				options: ["Agent's default", "default", "accept-edits", "auto", "bypass-permissions"],
			},
			changed: v.permissions !== "",
			a: "agents",
			b: "start",
		},
		{
			id: "p.crew",
			name: "Never form a crew automatically",
			summary:
				"No qa is ever created on its own here, whatever the task size - and an agent asking for one is refused.",
			detail:
				"You can still add a qa to a single task by hand, from + qa in its topbar or ao crew add in your own shell; that hatch is a person's. This changes the number of agents only: a standard or deep task still gets the full ceremony. A qa already mid-task keeps working.",
			timing: "live",
			ownership: { kind: "project-only" },
			control: { type: "toggle", value: v.noCrew },
			changed: v.noCrew,
			a: "general",
			b: "start",
		},
		{
			id: "p.check-in",
			name: "Check in with me before implementing",
			summary: "A worker here stops once it understands the task and hands back to you before it changes anything.",
			detail:
				"The task moves to Needs you on the board while it waits, and it implements only after you reply. Mechanical tasks never stop - they are already authorised to go straight to the edit. A worker already running keeps the prompt it was born with; restoring a session recomputes it.",
			timing: "next-worker",
			ownership: { kind: "project-only" },
			control: { type: "toggle", value: v.checkIn },
			changed: v.checkIn,
			a: "general",
			b: "start",
		},
		{
			id: "p.language",
			name: "Response language",
			summary: "The language this project's agents write their human-facing prose in.",
			detail:
				"The only setting on the whole page where a project genuinely overrides a global value. Code, commits, PR titles and bodies, branch names and identifiers always stay English.",
			timing: "next-worker",
			ownership: { kind: "project-override", globalValue: GLOBAL_LANGUAGE, overriding: v.language !== "" },
			control: {
				type: "select",
				value: v.language || `Inherit global default (${GLOBAL_LANGUAGE})`,
				options: [`Inherit global default (${GLOBAL_LANGUAGE})`, "English", "Thai", "Japanese", "German"],
			},
			changed: v.language !== "",
			a: "prompts",
			b: "told",
		},
		{
			id: "p.worker-prompt",
			name: "Worker additional prompt",
			summary: "Extra text added on top of the global worker base for this project's workers.",
			detail: "It is APPENDED, not substituted - it cannot remove or contradict anything the global base says.",
			timing: "next-worker",
			ownership: { kind: "project-appends", base: "Worker base prompt" },
			control: { type: "editor", value: v.workerPrompt, customized: v.workerPrompt !== "" },
			changed: v.workerPrompt !== "",
			a: "prompts",
			b: "told",
		},
		{
			id: "p.orchestrator-prompt",
			name: "Orchestrator additional prompt",
			summary: "Extra text added on top of the global orchestrator base for this project.",
			timing: "next-orchestrator",
			ownership: { kind: "project-appends", base: "Orchestrator base prompt" },
			control: { type: "editor", value: "", customized: false },
			a: "prompts",
			b: "told",
		},
		{
			id: "p.reviewer-prompt",
			name: "Reviewer additional prompt",
			summary: "Extra text added on top of the global reviewer base for this project.",
			timing: "next-worker",
			ownership: { kind: "project-appends", base: "Reviewer base prompt" },
			control: { type: "editor", value: "", customized: false },
			rare: true,
			a: "prompts",
			b: "told",
		},
		{
			id: "p.web-ui",
			name: "This project has a web UI",
			summary:
				"One switch, three effects: the inspector gains a Browser tab, agents here are told to use ao preview, and ao preview is accepted at all.",
			detail:
				"The tab appears and ao preview starts being accepted straight away; the guidance in an agent's prompt only reaches the next worker. Off by default - a project with nothing to preview never shows a permanently empty panel.",
			timing: "live",
			ownership: { kind: "project-only" },
			control: { type: "toggle", value: v.webUI },
			changed: v.webUI,
			a: "general",
			b: "told",
		},
		{
			id: "p.ios",
			name: "This project targets iOS",
			summary: "The inspector gains a Simulator tab, and workers here are told how to drive a booted simulator.",
			detail:
				"Nothing captures unless that tab is open and this window is focused. The tab appears at once; the prompt guidance reaches the next worker. Auto-detecting an .xcodeproj was considered and rejected - it gets monorepos wrong.",
			timing: "live",
			ownership: { kind: "project-only" },
			control: { type: "toggle", value: v.ios },
			changed: v.ios,
			a: "general",
			b: "told",
		},
		{
			id: "p.intake",
			name: "Tracker intake",
			summary: "AO watches the tracker and spawns a worker itself when an issue matching your rule appears.",
			detail:
				"Read-only toward the tracker: matching issues spawn sessions, but AO does not comment on them or move them. Enabling it requires an assignee - type a username, or * for any.",
			timing: "on-save",
			ownership: { kind: "project-only" },
			control: { type: "toggle", value: v.intake },
			changed: v.intake,
			a: "automation",
			b: "flow",
		},
		{
			id: "p.reviewer",
			name: "Default reviewer agent",
			summary: "The agent that runs AO's code review on this project's pull requests.",
			detail:
				"Unset means claude-code, not 'whatever the project says' - there is no project-wide reviewer behind this one.",
			timing: "on-save",
			ownership: { kind: "project-only" },
			control: {
				type: "select",
				value: v.reviewer || "claude-code (fallback)",
				options: ["claude-code (fallback)", "claude-code", "codex", "opencode"],
			},
			changed: v.reviewer !== "",
			a: "agents",
			b: "flow",
		},
		{
			id: "p.approval",
			name: "Require approvals before Ready to merge",
			summary: "A merge request is only reported as Ready to merge once it has the required number of approvals.",
			detail:
				"Applies only when the GitLab repo has no approval rule of its own. Approvals are counted for GitLab only in this version.",
			timing: "on-save",
			ownership: { kind: "project-only" },
			control: { type: "toggle", value: false },
			gate:
				project.forge === "gitlab"
					? undefined
					: project.forge === "none"
						? "No git remote detected, so AO cannot tell which forge this project is on"
						: "GitLab projects only",
			a: "automation",
			b: "flow",
		},
		{
			id: "p.refresh-agents",
			name: "Refresh agent availability",
			summary: "Re-reads which agents are installed and authorised on this Mac.",
			detail: "The list above is cached, so an agent you installed since AO started will not appear until this is run.",
			timing: "instant",
			ownership: { kind: "global-only" },
			control: { type: "action", label: "Refresh agents" },
			rare: true,
			a: "agents",
			b: "start",
		},
	];
	return list.filter((s) => !(s.id === "p.branch-prefix" && v.workflow === ""));
}
