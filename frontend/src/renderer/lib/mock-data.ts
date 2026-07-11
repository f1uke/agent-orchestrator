import type { PRState, PullRequestFacts, WorkspaceSummary } from "../types/workspace";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import type { components } from "../../api/schema";

const now = new Date().toISOString();
const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60 * 1000).toISOString();
const hoursAgo = (hours: number) => new Date(Date.now() - hours * 60 * 60 * 1000).toISOString();

const demoPr = (
	number: number,
	state: PRState,
	ci: PullRequestFacts["ci"] = "passing",
	review: PullRequestFacts["review"] = "none",
	mergeability: PullRequestFacts["mergeability"] = "mergeable",
): PullRequestFacts => ({
	url: `https://github.com/acme-inc/ao-demo/pull/${number}`,
	number,
	state,
	ci,
	review,
	mergeability,
	reviewComments: review === "changes_requested",
	updatedAt: now,
});

export const mockWorkspaces: WorkspaceSummary[] = [
	{
		id: "ao-demo",
		name: "ao-demo",
		path: "/demo/ao-demo",
		type: "main",
		orchestratorAgent: "codex",
		accentColor: "#6ee7b7",
		sessions: [
			{
				id: "ao-demo-orchestrator",
				terminalHandleId: "ao-demo-orchestrator/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Project orchestrator",
				provider: "codex",
				kind: "orchestrator",
				branch: "main",
				status: "working",
				createdAt: hoursAgo(6),
				updatedAt: minutesAgo(3),
				activity: { state: "active", lastActivityAt: minutesAgo(3) },
				prs: [],
			},
			{
				id: "ao-demo-72",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "settings-store-migration",
				provider: "codex",
				kind: "worker",
				branch: "chore/autonudge-settings-store",
				status: "todo",
				isTodo: true,
				baseBranch: "main-fluke",
				prTarget: "main-fluke",
				autoNameBranch: false,
				createdBy: "ao-demo-orchestrator",
				prompt:
					"Introduce a dedicated autonudge settings store holding the global default for auto-nudging review comments, and migrate the existing per-session flag onto a nullable override that falls back to it.\n\nDeliverables: a new settings store + migration; auto_nudge_comments becomes a nullable per-session override; settings API read/write path; the frontend Switch binds to the resolved value.",
				createdAt: hoursAgo(2),
				updatedAt: hoursAgo(2),
				prs: [],
			},
			{
				id: "ao-demo-73",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "gitlab-webhook-retry",
				provider: "claude-code",
				kind: "worker",
				branch: "",
				status: "todo",
				isTodo: true,
				baseBranch: "main-fluke",
				prTarget: "main-fluke",
				autoNameBranch: true,
				createdBy: "ao-demo-orchestrator",
				prompt:
					"Failed GitLab webhook deliveries are dropped, so transient 5xx responses lose MR events. Add bounded retries so deliveries survive transient failures.",
				createdAt: minutesAgo(40),
				updatedAt: minutesAgo(40),
				prs: [],
			},
			{
				id: "demo-working",
				terminalHandleId: "demo-working/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Build screenshot-ready dashboard data",
				issueId: "jira:DEMO-101",
				provider: "codex",
				branch: "demo/dashboard-screenshot",
				status: "working",
				displayStatus: "working",
				createdAt: hoursAgo(3),
				updatedAt: minutesAgo(2),
				activity: { state: "active", lastActivityAt: minutesAgo(2) },
				changedFiles: [
					{ path: "frontend/src/renderer/lib/mock-data.ts", additions: 156, deletions: 22 },
					{ path: "docs/readme.md", additions: 18, deletions: 4 },
				],
				commitMessage: "prepare readme screenshot data",
				prs: [],
			},
			{
				id: "demo-needs-input",
				terminalHandleId: "demo-needs-input/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Resolve reviewer feedback on terminal polish",
				provider: "claude-code",
				branch: "demo/terminal-polish",
				status: "changes_requested",
				displayStatus: "needs_you",
				createdAt: hoursAgo(5),
				updatedAt: minutesAgo(18),
				activity: { state: "waiting_input", lastActivityAt: minutesAgo(18) },
				changedFiles: [
					{ path: "frontend/src/renderer/components/TerminalPane.tsx", additions: 41, deletions: 9 },
					{ path: "frontend/src/renderer/styles.css", additions: 27, deletions: 3 },
				],
				commitMessage: "polish terminal screenshots",
				prs: [demoPr(318, "open", "passing", "changes_requested")],
			},
			{
				id: "demo-review-stack",
				terminalHandleId: "demo-review-stack/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Review stacked browser preview flow",
				provider: "codex",
				branch: "demo/browser-preview-stack",
				status: "review_pending",
				displayStatus: "needs_you",
				createdAt: hoursAgo(7),
				updatedAt: minutesAgo(7),
				activity: { state: "idle", lastActivityAt: minutesAgo(7) },
				previewUrl: "http://localhost:5173",
				previewRevision: 4,
				changedFiles: [
					{ path: "frontend/src/renderer/components/BrowserPanel.tsx", additions: 52, deletions: 11 },
					{ path: "frontend/src/renderer/hooks/useBrowserView.ts", additions: 33, deletions: 6 },
					{ path: "docs/assets/readme/browser-preview.png", additions: 1, deletions: 0 },
				],
				commitMessage: "wire readme browser preview",
				prs: [
					demoPr(319, "open", "passing", "none"),
					demoPr(320, "open", "pending", "none", "unknown"),
					demoPr(321, "draft", "pending", "none", "unknown"),
				],
			},
			{
				id: "demo-in-review",
				terminalHandleId: "demo-in-review/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Wait for CI on project settings copy",
				provider: "opencode",
				branch: "demo/project-settings-copy",
				status: "review_pending",
				displayStatus: "unknown",
				createdAt: hoursAgo(4),
				updatedAt: minutesAgo(31),
				activity: { state: "idle", lastActivityAt: minutesAgo(31) },
				prs: [demoPr(322, "open", "pending", "none", "unknown")],
			},
			{
				id: "demo-ready",
				terminalHandleId: "demo-ready/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Merge README screenshot asset update",
				provider: "codex",
				branch: "demo/readme-assets",
				status: "mergeable",
				displayStatus: "mergeable",
				createdAt: hoursAgo(9),
				updatedAt: minutesAgo(5),
				activity: { state: "idle", lastActivityAt: minutesAgo(5) },
				changedFiles: [
					{ path: "docs/assets/readme/dashboard.png", additions: 1, deletions: 0 },
					{ path: "docs/assets/readme/session-terminal.png", additions: 1, deletions: 0 },
				],
				prs: [demoPr(323, "open", "passing", "approved")],
			},
			{
				id: "demo-ci-failed",
				terminalHandleId: "demo-ci-failed/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Fix flaky NewTaskDialog smoke test",
				provider: "codex",
				branch: "demo/new-task-flake",
				status: "ci_failed",
				displayStatus: "needs_you",
				createdAt: hoursAgo(8),
				updatedAt: minutesAgo(46),
				activity: { state: "idle", lastActivityAt: minutesAgo(46) },
				prs: [demoPr(324, "open", "failing", "none")],
			},
			// Archived (done bar). Listed out of order on purpose so the board's
			// recent-first sort visibly reorders them: expected 25m → 2h → 5h → 3d.
			{
				id: "demo-terminated-old",
				terminalHandleId: "demo-terminated-old/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Abandon spike on legacy tmux bridge",
				provider: "codex",
				branch: "demo/tmux-spike",
				status: "terminated",
				createdAt: hoursAgo(80),
				updatedAt: hoursAgo(72),
				activity: { state: "exited", lastActivityAt: hoursAgo(72) },
				prs: [],
			},
			{
				id: "demo-merged-recent",
				terminalHandleId: "demo-merged-recent/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Ship sidebar footer alignment fix",
				provider: "codex",
				branch: "demo/sidebar-footer",
				status: "merged",
				createdAt: hoursAgo(4),
				updatedAt: minutesAgo(25),
				activity: { state: "exited", lastActivityAt: minutesAgo(25) },
				prs: [demoPr(325, "merged", "passing", "approved")],
			},
			{
				id: "demo-terminated-mid",
				terminalHandleId: "demo-terminated-mid/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Kill runaway notification retry loop",
				provider: "claude-code",
				branch: "demo/notif-retry",
				status: "terminated",
				createdAt: hoursAgo(6),
				updatedAt: hoursAgo(5),
				activity: { state: "exited", lastActivityAt: hoursAgo(5) },
				prs: [],
			},
			{
				id: "demo-merged-earlier",
				terminalHandleId: "demo-merged-earlier/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Merge status-badge pulse timing tweak",
				provider: "codex",
				branch: "demo/status-pulse",
				status: "merged",
				createdAt: hoursAgo(5),
				updatedAt: hoursAgo(2),
				activity: { state: "exited", lastActivityAt: hoursAgo(2) },
				prs: [demoPr(326, "merged", "passing", "approved")],
			},
		],
	},
	{
		id: "docs-site",
		name: "docs-site",
		path: "/demo/docs-site",
		type: "main",
		orchestratorAgent: "claude-code",
		accentColor: "#93c5fd",
		sessions: [
			{
				id: "docs-installation",
				terminalHandleId: "docs-installation/terminal_0",
				workspaceId: "docs-site",
				workspaceName: "docs-site",
				title: "Tighten installation guide",
				provider: "claude-code",
				branch: "demo/install-docs",
				status: "working",
				createdAt: hoursAgo(2),
				updatedAt: minutesAgo(13),
				activity: { state: "active", lastActivityAt: minutesAgo(13) },
				prs: [],
			},
			{
				id: "docs-ready",
				terminalHandleId: "docs-ready/terminal_0",
				workspaceId: "docs-site",
				workspaceName: "docs-site",
				title: "Publish troubleshooting section",
				provider: "codex",
				branch: "demo/troubleshooting",
				status: "approved",
				createdAt: hoursAgo(12),
				updatedAt: minutesAgo(22),
				activity: { state: "idle", lastActivityAt: minutesAgo(22) },
				prs: [demoPr(411, "open", "passing", "approved")],
			},
		],
	},
];

const prSummary = (sessionId: string, number: number, overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => {
	const session = mockWorkspaces.flatMap((workspace) => workspace.sessions).find((item) => item.id === sessionId);
	const facts = session?.prs.find((item) => item.number === number);
	const url = facts?.url ?? `https://github.com/me/${session?.workspaceName ?? "preview"}/pull/${number}`;
	return {
		url,
		htmlUrl: url,
		number,
		title: session?.title ?? `PR #${number}`,
		state: facts?.state ?? "open",
		provider: "github",
		repo: `me/${session?.workspaceName ?? "preview"}`,
		author: "preview-agent",
		sourceBranch: session?.branch ?? "",
		targetBranch: "main",
		headSha: `preview-${number}`,
		additions: 42,
		deletions: 8,
		changedFiles: 3,
		ci: {
			state: facts?.ci === "failing" ? "failing" : facts?.ci === "pending" ? "pending" : "passing",
			failingChecks: [],
		},
		review: {
			decision:
				facts?.review === "changes_requested"
					? "changes_requested"
					: facts?.review === "approved"
						? "approved"
						: "none",
			hasUnresolvedHumanComments: facts?.reviewComments ?? false,
			unresolvedBy: [],
		},
		mergeability: {
			state:
				facts?.mergeability === "conflicting"
					? "conflicting"
					: facts?.mergeability === "blocked"
						? "blocked"
						: facts?.mergeability === "unstable"
							? "unstable"
							: facts?.mergeability === "unknown"
								? "unknown"
								: "mergeable",
			reasons: [],
			prUrl: url,
			conflictFiles: [],
		},
		updatedAt: facts?.updatedAt ?? now,
		observedAt: facts?.updatedAt ?? now,
		ciObservedAt: facts?.updatedAt ?? now,
		reviewObservedAt: facts?.updatedAt ?? now,
		...overrides,
	};
};

export const mockSessionScmSummaries: Record<string, SessionPRSummary[]> = {
	"fix-auth-timeouts": [
		prSummary("fix-auth-timeouts", 184, {
			changedFiles: 5,
			additions: 91,
			deletions: 17,
			ci: {
				state: "failing",
				failingChecks: [
					{
						name: "backend / go test ./...",
						status: "failed",
						conclusion: "failure",
						url: "https://github.com/me/api-gateway/actions/runs/184001/job/1",
					},
					{
						name: "lint / golangci",
						status: "failed",
						conclusion: "failure",
						url: "https://github.com/me/api-gateway/actions/runs/184001/job/2",
					},
					{
						name: "api contract drift",
						status: "failed",
						conclusion: "failure",
						url: "https://github.com/me/api-gateway/actions/runs/184001/job/3",
					},
					{
						name: "frontend typecheck",
						status: "failed",
						conclusion: "",
						url: "https://github.com/me/api-gateway/actions/runs/184001/job/4",
					},
				],
			},
		}),
	],
	"texture-leak": [
		prSummary("texture-leak", 51, {
			changedFiles: 4,
			additions: 74,
			deletions: 22,
			ci: {
				state: "failing",
				failingChecks: [
					{
						name: "render tests",
						status: "failed",
						conclusion: "failure",
						url: "https://github.com/me/webgl-preview/actions/runs/51001/job/1",
					},
					{
						name: "visual regression",
						status: "failed",
						conclusion: "failure",
						url: "https://github.com/me/webgl-preview/actions/runs/51001/job/2",
					},
				],
			},
			mergeability: {
				state: "conflicting",
				reasons: ["conflicts"],
				prUrl: "https://github.com/me/webgl-preview/pull/51",
				conflictFiles: [
					{
						path: "src/render/texture-cache.ts",
						url: "https://github.com/me/webgl-preview/pull/51/conflicts#src-render-texture-cache-ts",
					},
					{
						path: "src/render/webgl-context.ts",
						url: "https://github.com/me/webgl-preview/pull/51/conflicts#src-render-webgl-context-ts",
					},
				],
			},
		}),
	],
	"review-camera-pan": [
		prSummary("review-camera-pan", 52, {
			changedFiles: 6,
			additions: 128,
			deletions: 31,
			review: {
				decision: "review_required",
				hasUnresolvedHumanComments: false,
				unresolvedBy: [],
			},
		}),
	],
	"input-pointer-lock": [
		prSummary("input-pointer-lock", 56, {
			changedFiles: 3,
			additions: 48,
			deletions: 14,
			review: {
				decision: "changes_requested",
				hasUnresolvedHumanComments: true,
				unresolvedBy: [
					{
						reviewerId: "maya",
						count: 3,
						reviewUrl: "https://github.com/me/webgl-preview/pull/56#pullrequestreview-1001",
						links: [
							{
								url: "https://github.com/me/webgl-preview/pull/56#discussion_r1001",
								file: "src/input/pointer-lock.ts",
								line: 88,
							},
							{
								url: "https://github.com/me/webgl-preview/pull/56#discussion_r1002",
								file: "src/input/keyboard.ts",
								line: 41,
							},
						],
					},
					{
						reviewerId: "copilot",
						count: 1,
						isBot: true,
						reviewUrl: "https://github.com/me/webgl-preview/pull/56#pullrequestreview-1002",
						links: [],
					},
				],
			},
		}),
	],
	"invoice-export": [
		prSummary("invoice-export", 117, {
			changedFiles: 8,
			additions: 212,
			deletions: 36,
			mergeability: {
				state: "blocked",
				reasons: ["behind_base", "review_required", "blocked_by_provider", "ci_failing"],
				prUrl: "https://github.com/me/billing-portal/pull/117",
				conflictFiles: [],
			},
		}),
	],
};

// Mock Jira context for the standalone (VITE_NO_ELECTRON) preview, so the
// Summary tab's JIRA ISSUE section renders without a daemon. Keyed by session id;
// demo-working carries issueId "jira:DEMO-101". The description exercises the
// main ADF node kinds (bold "heading" paragraphs, nested bullets, an attachment
// chip, a smart link, and an acceptance-criteria checklist).
export const mockSessionJiraContexts: Record<string, components["schemas"]["JiraContextResponse"]> = {
	"demo-working": {
		sessionId: "demo-working",
		linked: true,
		issue: {
			key: "DEMO-101",
			url: "https://example.atlassian.net/browse/DEMO-101",
			type: "Story",
			title: "Participating funds eligibility UI",
			status: "Ready for QA",
			statusCategory: "new",
			statusColor: "blue-gray",
			priority: "Medium",
			assignee: "Alex Rivera",
			reporter: "Sam Chen",
			sprint: {
				name: "Sprint 2026-14",
				state: "active",
				startDate: "2026-06-29T09:38:37.895Z",
				endDate: "2026-07-10T11:00:00.000Z",
			},
			subtasks: [
				{
					key: "DEMO-102",
					type: "Sub-task",
					title: "iOS",
					status: "Pull Request",
					statusCategory: "indeterminate",
					statusColor: "yellow",
				},
				{
					key: "DEMO-103",
					type: "Sub-task",
					title: "ADR",
					status: "Pull Request",
					statusCategory: "indeterminate",
					statusColor: "yellow",
				},
			],
			description: [
				{ type: "paragraph", content: [{ type: "text", text: "Background", marks: [{ type: "strong" }] }] },
				{
					type: "paragraph",
					content: [
						{
							type: "text",
							text: "Move the eligibility panel from the result screen to the review screen so customers benefit earlier.",
						},
					],
				},
				{ type: "paragraph", content: [{ type: "text", text: "Story", marks: [{ type: "strong" }] }] },
				{
					type: "bulletList",
					content: [
						{
							type: "listItem",
							content: [
								{
									type: "paragraph",
									content: [
										{ type: "text", text: "Build the participating-funds UI from the usable-coupon API response." },
									],
								},
							],
						},
						{
							type: "listItem",
							content: [
								{ type: "paragraph", content: [{ type: "text", text: "Participating funds CTA" }] },
								{
									type: "bulletList",
									content: [
										{
											type: "listItem",
											content: [
												{
													type: "paragraph",
													content: [
														{ type: "text", text: "Open a webview as a bottom sheet at " },
														{ type: "text", text: "/promotions/eligible/funds", marks: [{ type: "code" }] },
													],
												},
											],
										},
									],
								},
							],
						},
					],
				},
				{ type: "mediaSingle", content: [{ type: "media", attrs: { filename: "order-eligible-ui.png" } }] },
				{ type: "paragraph", content: [{ type: "text", text: "Design", marks: [{ type: "strong" }] }] },
				{
					type: "paragraph",
					content: [{ type: "inlineCard", attrs: { url: "https://example.com/design/participating-funds" } }],
				},
				{ type: "paragraph", content: [{ type: "text", text: "Acceptance Criteria", marks: [{ type: "strong" }] }] },
				{
					type: "taskList",
					content: [
						{
							type: "taskItem",
							attrs: { state: "TODO" },
							content: [{ type: "text", text: "UI renders participating funds from the usable API correctly." }],
						},
						{
							type: "taskItem",
							attrs: { state: "DONE" },
							content: [{ type: "text", text: "Summary totals across all fund types are correct." }],
						},
					],
				},
			],
		},
	},
};

// Available status transitions per session, read live in production; here they
// let the Move-status dialog demo in browser-preview mode. Synthetic data only.
export const mockSessionJiraTransitions: Record<string, components["schemas"]["JiraTransition"][]> = {
	"demo-working": [
		{ id: "11", name: "Start Testing", to: "In Progress", toCategory: "indeterminate" },
		{ id: "21", name: "Abandoned", to: "Abandoned", toCategory: "done" },
		{ id: "31", name: "Cancel", to: "Cancelled", toCategory: "done" },
	],
};

// A synthetic cross-project issue pool for the New-task / link-existing pickers
// under preview (VITE_NO_ELECTRON=1). Fully fictional — DEMO/ACME keys only.
const mockJiraIssuePool: components["schemas"]["JiraIssueSummary"][] = [
	{
		key: "DEMO-101",
		type: "Story",
		title: "Participating funds eligibility UI",
		status: "Ready for QA",
		statusCategory: "new",
		assignee: "Alex Rivera",
		url: "https://example.atlassian.net/browse/DEMO-101",
	},
	{
		key: "DEMO-140",
		type: "Story",
		title: "Example story summary",
		status: "In Progress",
		statusCategory: "indeterminate",
		assignee: "Sam Chen",
		url: "https://example.atlassian.net/browse/DEMO-140",
	},
	{
		key: "DEMO-88",
		type: "Bug",
		title: "Example bug summary",
		status: "To Do",
		statusCategory: "new",
		assignee: "",
		url: "https://example.atlassian.net/browse/DEMO-88",
	},
	{
		key: "ACME-12",
		type: "Task",
		title: "Example task summary",
		status: "Ready for UAT",
		statusCategory: "done",
		assignee: "Jamie Lee",
		url: "https://example.atlassian.net/browse/ACME-12",
	},
];

/** Preview-mode search: filters the synthetic pool by key/title (or project). */
export function mockJiraSearch(project: string, query: string): components["schemas"]["JiraIssueSummary"][] {
	const q = query.trim().toLowerCase();
	const proj = project.trim().toUpperCase();
	return mockJiraIssuePool.filter((it) => {
		const key = it.key ?? "";
		if (proj && !key.toUpperCase().startsWith(`${proj}-`)) return false;
		if (!q) return true;
		return key.toLowerCase().includes(q) || (it.title ?? "").toLowerCase().includes(q);
	});
}

const mockJiraProjectPool: components["schemas"]["JiraProject"][] = [
	{ key: "DEMO", name: "Demo Project" },
	{ key: "ACME", name: "Acme Platform" },
	{ key: "PLAT", name: "Platform Services" },
	{ key: "WEB", name: "Web App" },
];

/** Preview-mode project list: filters the synthetic pool by key/name. */
export function mockJiraProjects(query: string): components["schemas"]["JiraProject"][] {
	const q = query.trim().toLowerCase();
	if (!q) return mockJiraProjectPool;
	return mockJiraProjectPool.filter(
		(p) => (p.key ?? "").toLowerCase().includes(q) || (p.name ?? "").toLowerCase().includes(q),
	);
}
