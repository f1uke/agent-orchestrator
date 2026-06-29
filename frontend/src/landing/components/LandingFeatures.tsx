"use client";

import { type ReactNode, useMemo, useState } from "react";

type AgentHarness = {
	id: string;
	name: string;
	org: string;
	logo?: string;
	command: string;
	delivery: string;
	restore: string;
	hooks: string;
};

const primaryAgents: AgentHarness[] = [
	{
		id: "claude-code",
		name: "Claude Code",
		org: "Anthropic",
		logo: "/docs/logos/claude-code.svg",
		command: "claude --append-system-prompt-file .ao/AGENTS.md",
		delivery: "native CLI launch",
		restore: "resume supported",
		hooks: "workspace hooks",
	},
	{
		id: "codex",
		name: "Codex",
		org: "OpenAI",
		logo: "/docs/logos/codex.svg",
		command: "codex --config ao.session=session/ao-204",
		delivery: "session flags",
		restore: "codex resume",
		hooks: "session flags",
	},
	{
		id: "opencode",
		name: "OpenCode",
		org: "OpenCode",
		logo: "/docs/logos/opencode.svg",
		command: "opencode run --session session/ao-204",
		delivery: "terminal agent",
		restore: "session API",
		hooks: "activity bridge",
	},
	{
		id: "aider",
		name: "Aider",
		org: "Aider",
		logo: "/docs/logos/aider.png",
		command: "aider --message-file .ao/prompt.md",
		delivery: "prompt file",
		restore: "supported",
		hooks: "PATH wrappers",
	},
	{
		id: "cursor",
		name: "Cursor",
		org: "Cursor",
		logo: "/docs/logos/cursor.svg",
		command: "cursor-agent --print --force",
		delivery: "one-shot CLI",
		restore: "fresh launch",
		hooks: "terminal activity",
	},
	{
		id: "goose",
		name: "Goose",
		org: "Block",
		logo: "https://www.google.com/s2/favicons?domain=goose-docs.ai&sz=64",
		command: "goose run --resume --session-id ao-204",
		delivery: "native CLI launch",
		restore: "session id",
		hooks: "workspace hooks",
	},
];

const adapterNames = [
	"Claude Code",
	"Codex",
	"Cursor",
	"OpenCode",
	"Aider",
	"Amp",
	"Goose",
	"Copilot",
	"Grok",
	"Qwen",
	"Kimi",
	"Crush",
	"Cline",
	"Droid",
	"Devin",
	"Auggie",
	"Continue",
	"Kiro",
	"Kilo Code",
	"Agy",
	"Roo Code",
	"Windsurf",
	"Vibe",
];

const workspaceSessions = [
	{
		id: "ao-204",
		title: "Split terminal mux responsibilities",
		agent: "Claude Code",
		branch: "session/ao-204",
		path: ".ao/worktrees/ao-204",
		status: "working",
		color: "#f59f4c",
		files: ["backend/internal/terminal/manager.go", "frontend/src/renderer/components/TerminalPane.tsx"],
	},
	{
		id: "int-8",
		title: "fix auth timeout retry loop",
		agent: "Codex",
		branch: "fix/auth-timeouts",
		path: ".ao/worktrees/int-8",
		status: "ci failed",
		color: "#ff6b73",
		files: ["backend/internal/httpd/auth.go", "backend/internal/session_manager/restore.go"],
	},
	{
		id: "ao-211",
		title: "publish linux desktop install path",
		agent: "Aider",
		branch: "docs/linux-install",
		path: ".ao/worktrees/ao-211",
		status: "approved",
		color: "#6ee79a",
		files: ["frontend/src/landing/content/docs/installation.mdx", "README.md"],
	},
];

const feedbackSessions = [
	{
		id: "pr-184",
		number: "#184",
		title: "fix auth timeout retry loop",
		agent: "Codex",
		branch: "fix/auth-timeouts",
		session: "int-8",
		state: "needs you",
		color: "#ff6b73",
		checks: [
			{ name: "lint", state: "passed", color: "#6ee79a" },
			{ name: "unit", state: "passed", color: "#6ee79a" },
			{ name: "e2e", state: "failed", color: "#ff6b73" },
		],
		comments: ["Auth retry leaks stale token after timeout", "Add regression coverage for 401 retry path"],
		nudge: "CI failed on PR #184. Fix auth retry timeout and push an update.",
	},
	{
		id: "pr-185",
		number: "#185",
		title: "add rate limit headers",
		agent: "OpenCode",
		branch: "feat/rate-limit-headers",
		session: "ao-185",
		state: "in review",
		color: "#93b4f8",
		checks: [
			{ name: "lint", state: "passed", color: "#6ee79a" },
			{ name: "unit", state: "passed", color: "#6ee79a" },
			{ name: "review", state: "pending", color: "#93b4f8" },
		],
		comments: ["Reviewer asked for header docs", "Open question on retry-after semantics"],
		nudge: "Review comments landed on PR #185. Address docs and retry-after behavior.",
	},
	{
		id: "pr-204",
		number: "#204",
		title: "Build onboarding test for published npm package",
		agent: "Cursor",
		branch: "test/onboarding-harness",
		session: "ao-204",
		state: "ready to merge",
		color: "#6ee79a",
		checks: [
			{ name: "lint", state: "passed", color: "#6ee79a" },
			{ name: "unit", state: "passed", color: "#6ee79a" },
			{ name: "review", state: "approved", color: "#6ee79a" },
		],
		comments: ["Approved with two reviews", "Mergeability clean"],
		nudge: "PR #204 is approved and mergeable. Ready for final merge.",
	},
];

const daemonChecks = [
	{ label: "daemon", value: "ready on 127.0.0.1:3001", state: "ok" },
	{ label: "database", value: "~/.ao/data/ao.sqlite", state: "ok" },
	{ label: "git", value: "available", state: "ok" },
	{ label: "runtime", value: "tmux detected", state: "ok" },
];

export function LandingFeatures() {
	const [workerId, setWorkerId] = useState("codex");
	const [orchestratorId, setOrchestratorId] = useState("claude-code");
	const [workspaceId, setWorkspaceId] = useState("int-8");
	const [feedbackId, setFeedbackId] = useState("pr-184");

	const worker = useMemo(() => primaryAgents.find((agent) => agent.id === workerId) ?? primaryAgents[0], [workerId]);
	const orchestrator = useMemo(
		() => primaryAgents.find((agent) => agent.id === orchestratorId) ?? primaryAgents[0],
		[orchestratorId],
	);
	const workspace = useMemo(
		() => workspaceSessions.find((session) => session.id === workspaceId) ?? workspaceSessions[0],
		[workspaceId],
	);
	const feedback = useMemo(
		() => feedbackSessions.find((session) => session.id === feedbackId) ?? feedbackSessions[0],
		[feedbackId],
	);

	return (
		<section id="features" data-testid="features-grid" className="relative py-24 sm:py-32">
			<div className="container-page">
				<div className="mb-12 grid items-end gap-8 lg:grid-cols-12">
					<div className="lg:col-span-7">
						<div className="serial-num mb-3 font-mono text-xs">What&apos;s inside</div>
						<h2
							className="max-w-5xl font-sans font-semibold leading-[1.02] tracking-[-0.03em] text-[color:var(--fg)]"
							style={{ fontSize: "clamp(34px, 3.45vw, 52px)" }}
						>
							Run the agent you already use.
							<span className="block text-[color:var(--fg-muted)]" style={{ fontSize: "clamp(28px, 2.6vw, 42px)" }}>
								AO wraps the workflow around it.
							</span>
						</h2>
					</div>
					<div className="lg:col-span-5">
						<p className="max-w-xl text-[15px] leading-relaxed text-[color:var(--fg-muted)]">
							Claude Code, Codex, Cursor, OpenCode, Aider, Goose, Droid, Kilo and the rest stay native terminal tools.
							AO standardizes launch, restore, hooks, activity and PR ownership through one adapter contract.
						</p>
					</div>
				</div>

				<div className="relative space-y-16 pb-4">
					<div className="landing-feature-stack-card grid gap-5 lg:grid-cols-[0.78fr_1.22fr]">
						<FeatureNarrative worker={worker} orchestrator={orchestrator} />
						<AgentHarnessDemo
							worker={worker}
							orchestrator={orchestrator}
							workerId={workerId}
							orchestratorId={orchestratorId}
							onWorkerChange={setWorkerId}
							onOrchestratorChange={setOrchestratorId}
						/>
					</div>

					<div className="landing-feature-stack-card grid gap-5 lg:grid-cols-[1.18fr_0.82fr]">
						<WorkspaceIsolationDemo activeId={workspaceId} onSelect={setWorkspaceId} workspace={workspace} />
						<WorkspaceNarrative workspace={workspace} />
					</div>

					<div className="landing-feature-stack-card grid gap-5 lg:grid-cols-[0.82fr_1.18fr]">
						<FeedbackNarrative feedback={feedback} />
						<FeedbackRoutingDemo activeId={feedbackId} onSelect={setFeedbackId} feedback={feedback} />
					</div>

					<div className="landing-feature-stack-card grid gap-5 lg:grid-cols-[1.18fr_0.82fr]">
						<DaemonControlDemo />
						<DaemonNarrative />
					</div>
				</div>
			</div>
		</section>
	);
}

function FeatureNarrative({ worker, orchestrator }: { worker: AgentHarness; orchestrator: AgentHarness }) {
	return (
		<FeatureCopy
			eyebrow="Feature 01"
			title="Bring your own agent."
			accent="AO gives it a workflow."
			meta="23 harnesses"
		>
			<p>
				AO does not replace <FeatureStrong>{worker.name}</FeatureStrong>, <FeatureStrong>{orchestrator.name}</FeatureStrong>,
				Cursor, Aider, or OpenCode. It launches the same terminal-native tools you already trust, then standardizes the
				parts around them: <FeatureStrong>session restore, prompt delivery, hooks, runtime panes, and ownership.</FeatureStrong>
			</p>
			<p>
				Pick one agent to write and another to supervise. AO keeps the contract stable while every CLI keeps its native
				behavior.
			</p>
		</FeatureCopy>
	);
}

function AgentHarnessDemo({
	worker,
	orchestrator,
	workerId,
	orchestratorId,
	onWorkerChange,
	onOrchestratorChange,
}: {
	worker: AgentHarness;
	orchestrator: AgentHarness;
	workerId: string;
	orchestratorId: string;
	onWorkerChange: (id: string) => void;
	onOrchestratorChange: (id: string) => void;
}) {
	const [targetSlot, setTargetSlot] = useState<"worker" | "orchestrator">("worker");

	return (
		<article className="surface relative overflow-hidden bg-[#010102] p-0">
			<div className="flex items-center justify-between border-b border-[color:var(--border)] px-5 py-4">
				<div className="flex items-center gap-3">
					<img src="/ao-logo-transparent.png" alt="" className="h-7 w-7 object-contain" />
					<div>
						<div className="text-sm font-semibold text-[color:var(--fg)]">Project agents</div>
						<div className="font-mono text-[11px] text-[color:var(--fg-dim)]">/repo/agent-orchestrator</div>
					</div>
				</div>
				<div className="hidden rounded-full border border-[color:var(--border)] bg-white/[0.03] px-3 py-1.5 font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-dim)] sm:block">
					adapter contract
				</div>
			</div>

			<div className="grid gap-0 lg:grid-cols-[0.86fr_1fr]">
				<div className="border-b border-[color:var(--border)] p-5 lg:border-b-0 lg:border-r">
					<div className="mb-4 grid gap-3 sm:grid-cols-2">
						<AgentSelectLabel
							label="Worker agent"
							agent={worker}
							active={targetSlot === "worker"}
							onClick={() => setTargetSlot("worker")}
						/>
						<AgentSelectLabel
							label="Orchestrator agent"
							agent={orchestrator}
							active={targetSlot === "orchestrator"}
							onClick={() => setTargetSlot("orchestrator")}
						/>
					</div>

					<div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
						{primaryAgents.map((agent) => (
							<button
								key={agent.id}
								type="button"
								onClick={() => {
									setTargetSlot("worker");
									onWorkerChange(agent.id);
								}}
								onDoubleClick={() => {
									setTargetSlot("orchestrator");
									onOrchestratorChange(agent.id);
								}}
								className={`group relative flex min-h-[82px] cursor-pointer flex-col items-start justify-between overflow-hidden rounded-lg border p-3 text-left transition duration-200 ease-out hover:-translate-y-0.5 hover:border-white/15 hover:bg-white/[0.045] ${
									workerId === agent.id
										? "border-white/18 bg-white/[0.055] shadow-[inset_0_0_0_1px_rgba(147,180,248,0.16)]"
										: "border-[color:var(--border)] bg-white/[0.025]"
								}`}
								aria-pressed={workerId === agent.id}
							>
								{workerId === agent.id ? (
									<span className="absolute inset-y-3 left-0 w-px rounded-full bg-[color:var(--accent)] opacity-80" />
								) : null}
								<div className="flex w-full items-center justify-between gap-2">
									<AgentLogo agent={agent} className="h-7 w-7" />
									<span className="font-mono text-[9px] uppercase tracking-[0.16em] text-[color:var(--fg-dim)]">
										{agent.restore.includes("fresh") ? "new" : "resume"}
									</span>
								</div>
								<div>
									<div className="text-[13px] font-semibold leading-tight text-[color:var(--fg)]">{agent.name}</div>
									<div className="mt-0.5 font-mono text-[10px] text-[color:var(--fg-dim)]">{agent.org}</div>
								</div>
							</button>
						))}
					</div>

					<div className="mt-4 text-[12px] leading-relaxed text-[color:var(--fg-dim)]">
						Click an agent to set the worker. Double-click an agent to promote it into the orchestrator slot.
					</div>
				</div>

				<div className="p-5">
					<div className="mb-4 flex items-center justify-between gap-3">
						<div>
							<div className="text-lg font-semibold tracking-[-0.02em] text-[color:var(--fg)]">Launch preview</div>
							<div className="font-mono text-[11px] text-[color:var(--fg-dim)]">
								same daemon route, different native CLI
							</div>
						</div>
						<div className="rounded-md bg-[color:var(--accent-soft)] px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.16em] text-[color:var(--accent)]">
							ready
						</div>
					</div>

					<div className="overflow-hidden rounded-xl border border-[color:var(--border)] bg-[#050507]">
						<div className="flex items-center gap-1.5 border-b border-[color:var(--border)] px-3 py-2">
							<span className="h-2.5 w-2.5 rounded-full bg-[#ff5f57]" />
							<span className="h-2.5 w-2.5 rounded-full bg-[#ffbd2e]" />
							<span className="h-2.5 w-2.5 rounded-full bg-[#28c840]" />
							<span className="ml-3 font-mono text-[10px] text-[color:var(--fg-dim)]">ao spawn</span>
						</div>
						<div className="space-y-2 px-4 py-4 font-mono text-[12px] leading-relaxed">
							<TerminalLine muted text="$ ao spawn --project agent-orchestrator" />
							<TerminalLine text={`worker        ${worker.name}`} />
							<TerminalLine text={`orchestrator  ${orchestrator.name}`} />
							<TerminalLine accent text={`exec          ${worker.command}`} />
							<TerminalLine success text="workspace     .ao/worktrees/session-ao-204" />
							<TerminalLine success text="activity      hooks installed, session visible" />
						</div>
					</div>

					<div className="mt-4 grid gap-2 sm:grid-cols-3">
						<PipelineStep title="detect" detail="binary on PATH" active />
						<PipelineStep title="launch" detail={worker.delivery} active />
						<PipelineStep title="observe" detail={worker.hooks} active />
					</div>

					<div className="mt-5 overflow-hidden rounded-xl border border-[color:var(--border)] bg-white/[0.02]">
						<div className="grid grid-cols-[1fr_auto_1fr] items-center gap-3 px-4 py-4">
							<AdapterNode agent={orchestrator} label="orchestrator" />
							<div className="relative flex h-px min-w-12 items-center justify-center bg-[color:var(--border)]">
								<span className="landing-adapter-pulse absolute h-1.5 w-1.5 rounded-full bg-[color:var(--accent)]" />
							</div>
							<AdapterNode agent={worker} label="worker" />
						</div>
						<div className="border-t border-[color:var(--border)] px-4 py-3">
							<div className="flex flex-wrap gap-2">
								{adapterNames.map((name) => (
									<span
										key={name}
										className="rounded-full border border-[color:var(--border)] bg-black/30 px-2.5 py-1 font-mono text-[10px] text-[color:var(--fg-dim)]"
									>
										{name}
									</span>
								))}
							</div>
						</div>
					</div>
				</div>
			</div>
		</article>
	);
}

function AgentSelectLabel({
	label,
	agent,
	active,
	onClick,
}: {
	label: string;
	agent: AgentHarness;
	active: boolean;
	onClick: () => void;
}) {
	return (
		<button type="button" onClick={onClick} className="block w-full cursor-pointer text-left">
			<div className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-dim)]">{label}</div>
			<div
				className={`flex items-center gap-2 rounded-lg border px-3 py-2 transition duration-200 ${
					active ? "border-[color:var(--accent)] bg-[color:var(--accent-soft)]" : "border-[color:var(--border)] bg-white/[0.035]"
				}`}
			>
				<AgentLogo agent={agent} className="h-6 w-6" />
				<div className="min-w-0">
					<div className="truncate text-[13px] font-semibold text-[color:var(--fg)]">{agent.name}</div>
					<div className="truncate font-mono text-[10px] text-[color:var(--fg-dim)]">{agent.id}</div>
				</div>
			</div>
		</button>
	);
}

function AgentLogo({ agent, className }: { agent: AgentHarness; className: string }) {
	if (!agent.logo) {
		return (
			<div
				className={`${className} flex items-center justify-center rounded-md bg-[color:var(--accent-soft)] text-xs font-bold`}
			>
				{agent.name.slice(0, 1)}
			</div>
		);
	}

	return (
		<img src={agent.logo} alt="" referrerPolicy="no-referrer" className={`${className} rounded-md object-contain`} />
	);
}

function TerminalLine({
	text,
	muted,
	accent,
	success,
}: {
	text: string;
	muted?: boolean;
	accent?: boolean;
	success?: boolean;
}) {
	return (
		<div
			className={`landing-stream-line ${
				accent
					? "text-[color:var(--accent)]"
					: success
						? "text-[color:var(--status-ok)]"
						: muted
							? "text-[color:var(--fg-dim)]"
							: "text-[color:var(--fg-muted)]"
			}`}
		>
			{text}
		</div>
	);
}

function PipelineStep({ title, detail, active }: { title: string; detail: string; active?: boolean }) {
	return (
		<div className="rounded-lg border border-[color:var(--border)] bg-white/[0.025] p-3">
			<div className="flex items-center gap-2">
				<span
					className={`h-1.5 w-1.5 rounded-full ${active ? "landing-sse-pulse bg-[color:var(--accent)]" : "bg-[color:var(--fg-dim)]"}`}
				/>
				<span className="font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-muted)]">{title}</span>
			</div>
			<div className="mt-2 truncate text-[12px] text-[color:var(--fg-dim)]">{detail}</div>
		</div>
	);
}

function AdapterNode({ agent, label }: { agent: AgentHarness; label: string }) {
	return (
		<div className="flex min-w-0 items-center gap-3">
			<AgentLogo agent={agent} className="h-9 w-9" />
			<div className="min-w-0">
				<div className="font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-dim)]">{label}</div>
				<div className="truncate text-sm font-semibold text-[color:var(--fg)]">{agent.name}</div>
			</div>
		</div>
	);
}

function WorkspaceIsolationDemo({
	activeId,
	onSelect,
	workspace,
}: {
	activeId: string;
	onSelect: (id: string) => void;
	workspace: (typeof workspaceSessions)[number];
}) {
	const [actionState, setActionState] = useState("session attached");

	return (
		<article className="surface relative min-h-[640px] overflow-hidden bg-[#010102] p-0">
			<div className="grid h-full min-h-[640px] grid-cols-[220px_1fr]">
				<aside className="flex min-h-0 flex-col border-r border-[color:var(--border)] bg-[#050506]">
					<div className="flex items-center justify-between border-b border-[color:var(--border)] px-4 py-4">
						<div className="flex min-w-0 items-center gap-2.5">
							<img src="/ao-logo-transparent.png" alt="" className="h-6 w-6 object-contain" />
							<div className="truncate text-[13px] font-semibold text-[color:var(--fg)]">Agent Orchestrator</div>
						</div>
						<div className="h-3 w-3 rounded-sm border border-[color:var(--border-strong)]" />
					</div>

					<div className="flex-1 overflow-hidden px-3 py-4">
						<div className="mb-3 flex items-center justify-between">
							<span className="font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)]">
								Projects
							</span>
							<span className="font-mono text-[13px] text-[color:var(--fg-dim)]">+</span>
						</div>

						<div className="rounded-lg bg-white/[0.045] px-3 py-2">
							<div className="flex items-center justify-between gap-2">
								<span className="truncate text-[13px] font-semibold text-[color:var(--fg)]">agent-orchestrator</span>
								<span className="rounded-md bg-black/35 px-1.5 py-0.5 font-mono text-[10px] text-[color:var(--fg-dim)]">
									3
								</span>
							</div>
						</div>

						<div className="mt-2 space-y-1.5">
							{workspaceSessions.map((session) => (
								<button
									key={session.id}
									type="button"
									onClick={() => onSelect(session.id)}
									className={`group relative flex w-full cursor-pointer items-start gap-2 rounded-md px-3 py-2.5 text-left transition duration-200 hover:bg-white/[0.05] ${
										activeId === session.id ? "bg-white/[0.065]" : ""
									}`}
								>
									{activeId === session.id ? (
										<span className="absolute inset-y-2 left-0 w-px rounded-full bg-[color:var(--accent)]" />
									) : null}
									<span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full" style={{ background: session.color }} />
									<div className="min-w-0">
										<div className="truncate text-[12px] leading-snug text-[color:var(--fg-muted)] group-hover:text-[color:var(--fg)]">
											{session.title}
										</div>
										<div className="mt-1 font-mono text-[9px] text-[color:var(--fg-dim)]">{session.id}</div>
									</div>
								</button>
							))}
						</div>
					</div>

					<div className="border-t border-[color:var(--border)] px-4 py-3">
						<div className="font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-dim)]">settings</div>
					</div>
				</aside>

				<div className="min-w-0">
					<div className="flex items-center justify-between border-b border-[color:var(--border)] px-5 py-4">
						<div className="min-w-0">
							<div className="flex items-center gap-3">
								<h4 className="text-xl font-semibold tracking-[-0.03em] text-[color:var(--fg)]">Session</h4>
								<span
									className="rounded-full px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.14em]"
									style={{ color: workspace.color, background: `${workspace.color}1a` }}
								>
									{workspace.status}
								</span>
							</div>
							<div className="mt-1 truncate font-mono text-[11px] text-[color:var(--fg-dim)]">
								{workspace.agent} {"->"} {workspace.branch}
							</div>
						</div>
						<div className="flex gap-2">
							<button
								type="button"
								onClick={() => setActionState(`${workspace.id} restored`)}
								className="cursor-pointer rounded-md border border-[color:var(--border)] bg-white/[0.03] px-3 py-2 text-[12px] font-medium text-[color:var(--fg-muted)] transition hover:border-white/20 hover:bg-white/[0.06]"
							>
								Restore
							</button>
							<button
								type="button"
								onClick={() => setActionState(`PR opened for ${workspace.branch}`)}
								className="cursor-pointer rounded-md bg-[color:var(--accent)] px-3 py-2 text-[12px] font-semibold text-[#061126] transition hover:brightness-110"
							>
								Open PR
							</button>
						</div>
					</div>

					<div className="grid min-h-[575px] grid-cols-[1fr_285px]">
						<div className="flex min-w-0 flex-col border-r border-[color:var(--border)]">
							<div className="flex items-center justify-between border-b border-[color:var(--border)] bg-white/[0.015] px-4 py-3">
								<div>
									<div className="text-[14px] font-semibold text-[color:var(--fg)]">{workspace.title}</div>
									<div className="mt-1 font-mono text-[10px] text-[color:var(--fg-dim)]">{workspace.path}</div>
								</div>
								<div className="rounded-md border border-[color:var(--border)] px-2 py-1 font-mono text-[10px] text-[color:var(--fg-dim)]">
									{workspace.id}
								</div>
							</div>

							<div className="flex-1 bg-[#020203] p-4">
								<div className="h-full overflow-hidden rounded-lg border border-[color:var(--border)] bg-black">
									<div className="flex items-center gap-1.5 border-b border-[color:var(--border)] px-3 py-2">
										<span className="h-2.5 w-2.5 rounded-full bg-[#ff5f57]" />
										<span className="h-2.5 w-2.5 rounded-full bg-[#ffbd2e]" />
										<span className="h-2.5 w-2.5 rounded-full bg-[#28c840]" />
										<span className="ml-3 font-mono text-[10px] text-[color:var(--fg-dim)]">terminal</span>
									</div>
									<div className="space-y-2 px-4 py-4 font-mono text-[12px] leading-relaxed">
										<TerminalLine muted text={`$ pwd`} />
										<TerminalLine text={`/repo/agent-orchestrator/${workspace.path}`} />
										<TerminalLine muted text="$ git status --short --branch" />
										<TerminalLine accent text={`## ${workspace.branch}`} />
										{workspace.files.map((file) => (
											<TerminalLine key={file} text={` M ${file}`} />
										))}
										<TerminalLine success text="main checkout untouched; session owns this diff" />
										<TerminalLine success text={`action        ${actionState}`} />
									</div>
								</div>
							</div>
						</div>

						<aside className="bg-[#050506] p-4">
							<div className="mb-4">
								<div className="font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)]">
									Inspector
								</div>
								<div className="mt-2 text-[16px] font-semibold tracking-[-0.02em] text-[color:var(--fg)]">
									Workspace facts
								</div>
							</div>

							<div className="space-y-2">
								<InspectorFact label="runtime" value="tmux pane" />
								<InspectorFact label="worktree" value={workspace.path} />
								<InspectorFact label="branch" value={workspace.branch} />
								<InspectorFact label="owner" value={workspace.agent} />
							</div>

							<div className="mt-5 border-t border-[color:var(--border)] pt-4">
								<div className="mb-2 font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-dim)]">
									changed files
								</div>
								<div className="space-y-2">
									{workspace.files.map((file) => (
										<div
											key={file}
											className="rounded-md border border-[color:var(--border)] bg-white/[0.025] px-2.5 py-2 font-mono text-[10px] leading-snug text-[color:var(--fg-muted)]"
										>
											{file}
										</div>
									))}
								</div>
							</div>

							<div className="mt-5 rounded-lg border border-[color:var(--border)] bg-white/[0.025] p-3">
								<div className="mb-2 flex items-center gap-2">
									<span className="h-1.5 w-1.5 rounded-full bg-[color:var(--status-ok)]" />
									<span className="font-mono text-[10px] uppercase tracking-[0.16em] text-[color:var(--fg-muted)]">
										isolated
									</span>
								</div>
								<p className="text-[12px] leading-relaxed text-[color:var(--fg-dim)]">
									This pane, branch, and diff belong to one AO session.
								</p>
							</div>
						</aside>
					</div>
				</div>
			</div>
		</article>
	);
}

function WorkspaceNarrative({ workspace }: { workspace: (typeof workspaceSessions)[number] }) {
	return (
		<FeatureCopy
			eyebrow="Feature 02"
			title="Every task gets its own checkout."
			accent="Your main repo stays clean."
			meta={workspace.id}
		>
			<p>
				Each AO session runs in a separate <FeatureStrong>git worktree</FeatureStrong> with its own branch, terminal pane,
				changed files, and owner. The selected session here belongs to <FeatureStrong>{workspace.agent}</FeatureStrong> on{" "}
				<FeatureStrong>{workspace.branch}</FeatureStrong>.
			</p>
			<p>
				That means one agent can fail CI, another can keep shipping, and cleanup is just removing the session worktree.
				No stash juggling. No branch collisions.
			</p>
		</FeatureCopy>
	);
}

function InspectorFact({ label, value }: { label: string; value: string }) {
	return (
		<div className="rounded-lg border border-[color:var(--border)] bg-black/25 px-3 py-2.5">
			<div className="font-mono text-[9px] uppercase tracking-[0.18em] text-[color:var(--fg-dim)]">{label}</div>
			<div className="mt-1 truncate font-mono text-[11px] text-[color:var(--fg-muted)]">{value}</div>
		</div>
	);
}

function FeedbackNarrative({ feedback }: { feedback: (typeof feedbackSessions)[number] }) {
	return (
		<FeatureCopy
			eyebrow="Feature 03"
			title="Reviews route back to the owner."
			accent="Not to a random terminal."
			meta={feedback.number}
		>
			<p>
				AO watches <FeatureStrong>checks, reviews, comments, mergeability, and PR state</FeatureStrong>, then resolves the
				session that owns the branch. For this PR, feedback goes back to <FeatureStrong>{feedback.agent}</FeatureStrong> in{" "}
				<FeatureStrong>{feedback.session}</FeatureStrong>.
			</p>
			<p>
				The agent gets the actionable context, not a vague “CI failed” notification you have to manually trace.
			</p>
		</FeatureCopy>
	);
}

function FeedbackRoutingDemo({
	activeId,
	onSelect,
	feedback,
}: {
	activeId: string;
	onSelect: (id: string) => void;
	feedback: (typeof feedbackSessions)[number];
}) {
	const [sentSession, setSentSession] = useState<string | null>(null);

	return (
		<article className="surface relative min-h-[640px] overflow-hidden bg-[#010102] p-0">
			<div className="flex items-center justify-between border-b border-[color:var(--border)] px-5 py-4">
				<div>
					<div className="text-sm font-semibold text-[color:var(--fg)]">Pull requests</div>
					<div className="font-mono text-[11px] text-[color:var(--fg-dim)]">
						CI, reviews and comments mapped to sessions
					</div>
				</div>
				<div className="rounded-md border border-[color:var(--border)] bg-white/[0.03] px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.16em] text-[color:var(--fg-muted)]">
					lifecycle
				</div>
			</div>

			<div className="grid min-h-[584px] grid-cols-[280px_1fr]">
				<aside className="border-r border-[color:var(--border)] bg-[#050506] p-4">
					<div className="mb-3 font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)]">
						Open PRs
					</div>
					<div className="space-y-2">
						{feedbackSessions.map((item) => (
							<button
								key={item.id}
								type="button"
								onClick={() => onSelect(item.id)}
								className={`relative w-full cursor-pointer rounded-lg border px-3 py-3 text-left transition duration-200 hover:-translate-y-0.5 hover:border-white/15 hover:bg-white/[0.045] ${
									activeId === item.id
										? "border-white/18 bg-white/[0.055] shadow-[inset_0_0_0_1px_rgba(147,180,248,0.14)]"
										: "border-[color:var(--border)] bg-white/[0.02]"
								}`}
							>
								{activeId === item.id ? (
									<span className="absolute inset-y-3 left-0 w-px rounded-full bg-[color:var(--accent)] opacity-80" />
								) : null}
								<div className="flex items-center justify-between gap-3">
									<span className="font-mono text-[11px] text-[color:var(--fg-muted)]">{item.number}</span>
									<span
										className="rounded-full px-2 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em]"
										style={{ color: item.color, background: `${item.color}18` }}
									>
										{item.state}
									</span>
								</div>
								<div className="mt-2 line-clamp-2 text-[13px] font-semibold leading-snug text-[color:var(--fg)]">
									{item.title}
								</div>
								<div className="mt-2 font-mono text-[10px] text-[color:var(--fg-dim)]">
									{item.agent} / {item.session}
								</div>
							</button>
						))}
					</div>
				</aside>

				<div className="min-w-0 p-5">
					<div className="mb-5 flex items-start justify-between gap-4">
						<div className="min-w-0">
							<div className="flex items-center gap-3">
								<span className="font-mono text-[12px] text-[color:var(--fg-dim)]">{feedback.number}</span>
								<h4 className="truncate text-xl font-semibold tracking-[-0.03em] text-[color:var(--fg)]">
									{feedback.title}
								</h4>
							</div>
							<div className="mt-1 font-mono text-[11px] text-[color:var(--fg-dim)]">
								{feedback.branch} {"->"} {feedback.agent} session {feedback.session}
							</div>
						</div>
						<button
							type="button"
							onClick={() => setSentSession(feedback.session)}
							className="cursor-pointer rounded-md bg-[color:var(--accent)] px-3 py-2 text-[12px] font-semibold text-[#061126] transition hover:brightness-110"
						>
							{sentSession === feedback.session ? "Sent" : "Send to agent"}
						</button>
					</div>

					<div className="grid gap-4 xl:grid-cols-[0.95fr_1.05fr]">
						<div className="space-y-4">
							<div className="rounded-xl border border-[color:var(--border)] bg-[#050507] p-4">
								<div className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--fg-dim)]">
									checks
								</div>
								<div className="space-y-2">
									{feedback.checks.map((check) => (
										<div
											key={check.name}
											className="flex items-center justify-between rounded-md border border-[color:var(--border)] bg-white/[0.025] px-3 py-2"
										>
											<span className="flex items-center gap-2 text-[13px] text-[color:var(--fg-muted)]">
												<span className="h-1.5 w-1.5 rounded-full" style={{ background: check.color }} />
												{check.name}
											</span>
											<span
												className="font-mono text-[10px] uppercase tracking-[0.14em]"
												style={{ color: check.color }}
											>
												{check.state}
											</span>
										</div>
									))}
								</div>
							</div>

							<div className="rounded-xl border border-[color:var(--border)] bg-[#050507] p-4">
								<div className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--fg-dim)]">
									review comments
								</div>
								<div className="space-y-2">
									{feedback.comments.map((comment) => (
										<div
											key={comment}
											className="rounded-md border border-[color:var(--border)] bg-white/[0.025] px-3 py-2 text-[12px] leading-relaxed text-[color:var(--fg-muted)]"
										>
											{comment}
										</div>
									))}
								</div>
							</div>
						</div>

						<div className="overflow-hidden rounded-xl border border-[color:var(--border)] bg-black">
							<div className="flex items-center justify-between border-b border-[color:var(--border)] px-3 py-2">
								<div className="flex items-center gap-1.5">
									<span className="h-2.5 w-2.5 rounded-full bg-[#ff5f57]" />
									<span className="h-2.5 w-2.5 rounded-full bg-[#ffbd2e]" />
									<span className="h-2.5 w-2.5 rounded-full bg-[#28c840]" />
								</div>
								<span className="font-mono text-[10px] text-[color:var(--fg-dim)]">ao send</span>
							</div>
							<div className="space-y-2 px-4 py-4 font-mono text-[12px] leading-relaxed">
								<TerminalLine muted text={`$ ao session claim-pr ${feedback.session} ${feedback.number}`} />
								<TerminalLine text={`owner         ${feedback.agent}`} />
								<TerminalLine text={`session       ${feedback.session}`} />
								<TerminalLine accent text={`message       ${feedback.nudge}`} />
								<TerminalLine success text={sentSession === feedback.session ? "feedback routed to the running worker pane" : "ready to route feedback"} />
							</div>
						</div>
					</div>

					<div className="mt-4 grid gap-2 sm:grid-cols-3">
						<PipelineStep title="observe" detail="GitHub facts" active />
						<PipelineStep title="match" detail={feedback.session} active />
						<PipelineStep title="nudge" detail={feedback.agent} active />
					</div>
				</div>
			</div>
		</article>
	);
}

function DaemonControlDemo() {
	return (
		<article className="surface relative min-h-[640px] overflow-hidden bg-[#010102] p-0">
			<div className="flex items-center justify-between border-b border-[color:var(--border)] px-5 py-4">
				<div>
					<div className="text-sm font-semibold text-[color:var(--fg)]">Local control plane</div>
					<div className="font-mono text-[11px] text-[color:var(--fg-dim)]">
						desktop and CLI talk to the same daemon
					</div>
				</div>
				<div className="rounded-md border border-[color:var(--border)] bg-white/[0.03] px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.16em] text-[color:var(--fg-muted)]">
					127.0.0.1
				</div>
			</div>

			<div className="grid min-h-[584px] grid-cols-[1fr_300px]">
				<div className="border-r border-[color:var(--border)] p-5">
					<div className="overflow-hidden rounded-xl border border-[color:var(--border)] bg-black">
						<div className="flex items-center justify-between border-b border-[color:var(--border)] px-3 py-2">
							<div className="flex items-center gap-1.5">
								<span className="h-2.5 w-2.5 rounded-full bg-[#ff5f57]" />
								<span className="h-2.5 w-2.5 rounded-full bg-[#ffbd2e]" />
								<span className="h-2.5 w-2.5 rounded-full bg-[#28c840]" />
							</div>
							<span className="font-mono text-[10px] text-[color:var(--fg-dim)]">ao doctor</span>
						</div>
						<div className="space-y-2 px-4 py-4 font-mono text-[12px] leading-relaxed">
							<TerminalLine muted text="$ ao start" />
							<TerminalLine success text="daemon started in background" />
							<TerminalLine muted text="$ ao status --json" />
							<TerminalLine text='{ "ready": true, "port": 3001, "bind": "127.0.0.1" }' />
							<TerminalLine muted text="$ ao doctor" />
							{daemonChecks.map((check) => (
								<TerminalLine key={check.label} success text={`✓ ${check.label.padEnd(9)} ${check.value}`} />
							))}
						</div>
					</div>

					<div className="mt-4 grid gap-3 lg:grid-cols-3">
						<DaemonNode title="CLI" body="ao spawn, send, status" active />
						<DaemonNode title="Daemon" body="HTTP over loopback" active />
						<DaemonNode title="Desktop" body="board, terminal, settings" active />
					</div>

					<div className="mt-4 rounded-xl border border-[color:var(--border)] bg-[#050507] p-4">
						<div className="mb-3 font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--fg-dim)]">
							event stream
						</div>
						<div className="space-y-2">
							<EventRow seq="2401" label="session_created" detail="ao-204" />
							<EventRow seq="2402" label="pr_check_recorded" detail="e2e failed" />
							<EventRow seq="2403" label="session_updated" detail="needs_you" />
							<EventRow seq="2404" label="terminal_snapshot" detail="attached" />
						</div>
					</div>
				</div>

				<aside className="bg-[#050506] p-4">
					<div className="mb-4 flex items-center gap-3">
						<img src="/ao-logo-transparent.png" alt="" className="h-8 w-8 object-contain" />
						<div>
							<div className="text-[15px] font-semibold text-[color:var(--fg)]">AO daemon</div>
							<div className="font-mono text-[10px] text-[color:var(--fg-dim)]">agent-orchestrator-daemon</div>
						</div>
					</div>

					<div className="space-y-2">
						<InspectorFact label="bind" value="127.0.0.1" />
						<InspectorFact label="port" value="3001" />
						<InspectorFact label="data dir" value="~/.ao/data" />
						<InspectorFact label="store" value="SQLite + change_log" />
					</div>

					<div className="mt-5 rounded-lg border border-[color:var(--border)] bg-white/[0.025] p-3">
						<div className="mb-2 flex items-center gap-2">
							<span className="landing-sse-pulse h-1.5 w-1.5 rounded-full bg-[color:var(--status-ok)]" />
							<span className="font-mono text-[10px] uppercase tracking-[0.16em] text-[color:var(--fg-muted)]">
								live
							</span>
						</div>
						<p className="text-[12px] leading-relaxed text-[color:var(--fg-dim)]">
							The Electron app and `ao` CLI are just clients. The daemon owns sessions, worktrees, lifecycle and events.
						</p>
					</div>
				</aside>
			</div>
		</article>
	);
}

function DaemonNarrative() {
	return (
		<FeatureCopy
			eyebrow="Feature 04"
			title="Desktop and CLI share one brain."
			accent="A local daemon owns the loop."
			meta="127.0.0.1"
		>
			<p>
				The Electron app and <FeatureStrong>ao</FeatureStrong> CLI are clients of the same loopback daemon. It owns{" "}
				<FeatureStrong>sessions, worktrees, terminals, durable facts, and live events</FeatureStrong>.
			</p>
			<p>
				Start work from the CLI, inspect it in the desktop app, and route feedback back through the same local control
				plane.
			</p>
		</FeatureCopy>
	);
}

function FeatureCopy({
	eyebrow,
	title,
	accent,
	children,
	meta,
}: {
	eyebrow: string;
	title: string;
	accent: string;
	children: ReactNode;
	meta?: string;
}) {
	return (
		<article className="relative flex min-h-[640px] flex-col justify-center overflow-hidden border border-[color:var(--border)] bg-[#0b0b0b] p-7 sm:p-10">
			<div className="max-w-[34rem]">
				<div className="mb-5 flex items-center gap-3">
					<div className="font-mono text-[10px] uppercase tracking-[0.24em] text-[color:var(--accent)]">{eyebrow}</div>
					{meta ? (
						<div className="rounded-full border border-[color:var(--border)] bg-black/35 px-2.5 py-1 font-mono text-[10px] text-[color:var(--fg-dim)]">
							{meta}
						</div>
					) : null}
				</div>
				<h3 className="text-4xl font-semibold leading-[1.02] tracking-[-0.045em] text-[color:var(--fg)] sm:text-5xl">
					{title}
					<span className="block text-[color:var(--fg-muted)]">{accent}</span>
				</h3>
				<div className="mt-7 space-y-4 text-[17px] leading-[1.55] text-[color:var(--fg-muted)]">{children}</div>
			</div>
		</article>
	);
}

function FeatureStrong({ children }: { children: ReactNode }) {
	return <span className="font-medium text-[color:var(--fg)]">{children}</span>;
}

function DaemonNode({ title, body, active }: { title: string; body: string; active?: boolean }) {
	return (
		<div className="rounded-xl border border-[color:var(--border)] bg-white/[0.025] p-3">
			<div className="flex items-center gap-2">
				<span
					className={`h-1.5 w-1.5 rounded-full ${active ? "bg-[color:var(--accent)]" : "bg-[color:var(--fg-dim)]"}`}
				/>
				<span className="font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-muted)]">{title}</span>
			</div>
			<div className="mt-2 text-[12px] leading-snug text-[color:var(--fg-dim)]">{body}</div>
		</div>
	);
}

function EventRow({ seq, label, detail }: { seq: string; label: string; detail: string }) {
	return (
		<div className="grid grid-cols-[48px_1fr_auto] items-center gap-3 rounded-md border border-[color:var(--border)] bg-white/[0.025] px-3 py-2 font-mono text-[10px]">
			<span className="text-[color:var(--fg-dim)]">{seq}</span>
			<span className="text-[color:var(--fg-muted)]">{label}</span>
			<span className="text-[color:var(--accent)]">{detail}</span>
		</div>
	);
}
