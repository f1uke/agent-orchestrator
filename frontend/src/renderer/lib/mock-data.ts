import type { PRState, PullRequestFacts, WorkspaceSummary } from "../types/workspace";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import type { components } from "../../api/schema";

type WorkspaceChangesResponse = components["schemas"]["WorkspaceChangesResponse"];

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
				tokenUsage: {
					input: 5600000,
					cacheCreation: 42000000,
					cacheRead: 900000000,
					output: 42000000,
					turns: 1840,
					rawTotal: 989600000,
					costWeighted: 386000000,
					runaway: true,
					updatedAt: minutesAgo(1),
				},
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
				tokenUsage: {
					input: 82010,
					cacheCreation: 2525549,
					cacheRead: 152740511,
					output: 998731,
					turns: 602,
					rawTotal: 156346801,
					costWeighted: 23506652,
					runaway: false,
					updatedAt: minutesAgo(18),
				},
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
				isTerminated: true,
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
				isTerminated: true,
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
				isTerminated: true,
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
				isTerminated: true,
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
	// Real in-review sessions carry approval-progress facts so the preview
	// (VITE_NO_ELECTRON=1) exercises the meter across surfaces and states.
	// demo-ready: threshold met (green). demo-in-review: short (neutral + "more
	// needed"). demo-review-stack: an SCM-native rule met.
	"demo-ready": [
		prSummary("demo-ready", 323, {
			provider: "gitlab",
			review: {
				decision: "none",
				hasUnresolvedHumanComments: false,
				unresolvedBy: [],
				approvalRuleSource: "ao",
				approvalsCount: 2,
				requiredApprovals: 2,
			},
		}),
	],
	"demo-in-review": [
		prSummary("demo-in-review", 322, {
			provider: "gitlab",
			review: {
				decision: "none",
				hasUnresolvedHumanComments: false,
				unresolvedBy: [],
				approvalRuleSource: "ao",
				approvalsCount: 1,
				requiredApprovals: 2,
			},
		}),
	],
	"demo-review-stack": [
		prSummary("demo-review-stack", 319, {
			provider: "gitlab",
			review: {
				decision: "approved",
				hasUnresolvedHumanComments: false,
				unresolvedBy: [],
				approvalRuleSource: "scm",
				approvalsCount: 2,
				requiredApprovals: 2,
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
const activeSprint = {
	name: "Sprint 2026-14",
	state: "active",
	startDate: "2026-06-29T09:38:37.895Z",
	endDate: "2026-07-10T11:00:00.000Z",
};

const mockJiraIssuePool: components["schemas"]["JiraIssueSummary"][] = [
	{
		// An Epic heading the tree — a context-only group header (Fix 5): no status
		// pill / start / send actions. Its children (Stories) nest beneath it.
		key: "DEMO-100",
		type: "Epic",
		title: "E-Coupon 3.0",
		status: "In Progress",
		statusCategory: "indeterminate",
		assignee: "",
		url: "https://example.atlassian.net/browse/DEMO-100",
		sprint: activeSprint,
	},
	{
		key: "DEMO-101",
		type: "Story",
		title: "Participating funds eligibility UI",
		status: "Ready for QA",
		statusCategory: "new",
		assignee: "Alex Rivera",
		url: "https://example.atlassian.net/browse/DEMO-101",
		parent: { key: "DEMO-100", title: "E-Coupon 3.0" },
		sprint: activeSprint,
	},
	{
		key: "DEMO-140",
		type: "Story",
		title: "Example story summary",
		status: "In Progress",
		statusCategory: "indeterminate",
		assignee: "Sam Chen",
		url: "https://example.atlassian.net/browse/DEMO-140",
		parent: { key: "DEMO-100", title: "E-Coupon 3.0" },
		sprint: activeSprint,
	},
	{
		// A sub-task of DEMO-140 assigned to someone else — exercises the list's
		// parent-under-subtask nesting (#37) and the detail parent breadcrumb (#36).
		key: "DEMO-141",
		type: "Sub-task",
		title: "Backend eligibility endpoint",
		status: "In Progress",
		statusCategory: "indeterminate",
		assignee: "Alex Rivera",
		url: "https://example.atlassian.net/browse/DEMO-141",
		parent: { key: "DEMO-140", title: "Example story summary" },
		sprint: activeSprint,
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

/** A stable synthetic accountId for a mock assignee name (real Jira ids are
 *  opaque; preview just needs a consistent key to filter/dropdown on). */
const mockAccountId = (name: string): string =>
	name.trim() ? `acc-${name.trim().toLowerCase().replace(/\s+/g, "-")}` : "";

type MockSearchFilters = {
	assignee?: string;
	types?: string[];
	hideDone?: boolean;
	activeSprint?: boolean;
	jql?: string;
};

/**
 * Preview-mode search: filters the synthetic pool by key/title (or project), then
 * mirrors the server-side JQL filters — assignee (a derived accountId, or the
 * "unassigned" token), issue types, hide-done and active-sprint — so Browse Jira
 * behaves in preview as it does live. Advanced JQL can't be parsed here, so it just
 * returns the project pool. Each row carries its derived assigneeAccountId.
 */
export function mockJiraSearch(
	project: string,
	query: string,
	filters: MockSearchFilters = {},
): components["schemas"]["JiraIssueSummary"][] {
	const q = query.trim().toLowerCase();
	const proj = project.trim().toUpperCase();
	const assignee = filters.assignee ?? "";
	const typeNames = (filters.types ?? []).map((t) => t.trim().toLowerCase()).filter(Boolean);
	return mockJiraIssuePool
		.map((it) => ({ ...it, assigneeAccountId: it.assigneeAccountId ?? mockAccountId(it.assignee ?? "") }))
		.filter((it) => {
			const key = it.key ?? "";
			if (proj && !key.toUpperCase().startsWith(`${proj}-`)) return false;
			if (q && !(key.toLowerCase().includes(q) || (it.title ?? "").toLowerCase().includes(q))) return false;
			if (assignee === "unassigned") {
				if ((it.assignee ?? "").trim()) return false;
			} else if (assignee && it.assigneeAccountId !== assignee) {
				return false;
			}
			if (typeNames.length > 0) {
				const t = (it.type ?? "").toLowerCase();
				if (!typeNames.some((name) => t === name || t.includes(name) || name.includes(t))) return false;
			}
			if (filters.hideDone && (it.statusCategory ?? "") === "done") return false;
			if (filters.activeSprint && it.sprint?.state !== "active") return false;
			return true;
		});
}

// A pool row with its derived accountId filled in (what the live search returns).
function poolRowWithAccount(it: components["schemas"]["JiraIssueSummary"]) {
	return { ...it, assigneeAccountId: it.assigneeAccountId ?? mockAccountId(it.assignee ?? "") };
}

/** Preview-mode current user: a fixed account matching a pool assignee so the "You"
 *  highlight (Fix 3) is demoable without a daemon. */
export function mockJiraMyself(): { accountId: string; displayName: string } {
	return { accountId: mockAccountId("Alex Rivera"), displayName: "Alex Rivera" };
}

/**
 * Preview-mode tree-context (Fix 2): walk the pool's parent links to return the
 * ancestors + descendants of `roots` (excluding the roots), so the 3-level tree nests
 * end-to-end without a daemon. Descendants respect hide-done/active-sprint; ancestors
 * do not — mirroring collectTreeContext.
 */
export function mockJiraTreeContext(
	roots: { key: string }[],
	opts: { hideDone?: boolean; activeSprint?: boolean } = {},
): components["schemas"]["JiraIssueSummary"][] {
	const rootKeys = new Set(roots.map((r) => r.key));
	const seen = new Set(rootKeys);
	const out: components["schemas"]["JiraIssueSummary"][] = [];
	const rows = mockJiraIssuePool.map(poolRowWithAccount);
	const passesDescent = (it: components["schemas"]["JiraIssueSummary"]) =>
		!(opts.hideDone && (it.statusCategory ?? "") === "done") && !(opts.activeSprint && it.sprint?.state !== "active");

	// DESCENT: rows whose parent chain reaches a root (BFS), respecting the toggles.
	let frontier = new Set(rootKeys);
	for (let step = 0; step < 2 && frontier.size > 0; step += 1) {
		const next = new Set<string>();
		for (const it of rows) {
			const pk = it.parent?.key;
			if (pk && frontier.has(pk) && !seen.has(it.key ?? "") && passesDescent(it)) {
				seen.add(it.key ?? "");
				out.push(it);
				next.add(it.key ?? "");
			}
		}
		frontier = next;
	}
	// ASCENT: parent chain up from the roots (+ descendants), no toggle filter.
	let pending = [...roots.map((r) => rows.find((it) => it.key === r.key)).filter(Boolean), ...out] as typeof rows;
	for (let step = 0; step < 2; step += 1) {
		const wanted = new Set<string>();
		for (const it of pending) {
			const pk = it.parent?.key;
			if (pk && !seen.has(pk)) wanted.add(pk);
		}
		if (wanted.size === 0) break;
		const found = rows.filter((it) => wanted.has(it.key ?? "") && !seen.has(it.key ?? ""));
		found.forEach((it) => seen.add(it.key ?? ""));
		out.push(...found);
		pending = found;
	}
	return out;
}

/** Preview-mode detail read: build a full issue projection from the pool summary,
 *  deriving subtasks from any pool rows that name this issue as their parent and
 *  synthesizing a short description so the Browse-Jira detail drawer (#36) renders
 *  end-to-end without a daemon. */
export function mockJiraIssue(key: string): components["schemas"]["JiraIssue"] | null {
	const row = mockJiraIssuePool.find((it) => it.key === key);
	if (!row) return null;
	const subtasks = mockJiraIssuePool
		.filter((it) => it.parent?.key === key)
		.map((it) => ({
			key: it.key,
			type: it.type,
			title: it.title,
			status: it.status,
			statusCategory: it.statusCategory,
			statusColor: it.statusColor,
		}));
	return {
		key: row.key ?? key,
		type: row.type,
		title: row.title,
		status: row.status,
		statusCategory: row.statusCategory,
		statusColor: row.statusColor,
		assignee: row.assignee,
		reporter: "Sam Chen",
		priority: row.type === "Bug" ? "High" : "Medium",
		url: row.url,
		parent: row.parent,
		sprint: row.sprint,
		description: [
			{
				type: "paragraph",
				content: [
					{
						type: "text",
						text: `Read-only preview of ${row.key ?? key}. Live Jira data replaces this when a JIRA_API_TOKEN is configured.`,
					},
				],
			},
		],
		subtasks: subtasks.length > 0 ? subtasks : undefined,
	};
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

// Mock smoke checklist for the VITE_NO_ELECTRON renderer harness (no daemon).
// Only the primary demo worker has a checklist; other sessions render the empty
// state (not every worker authors one). Shared by useSessionSmokeChecks so the
// Tests tab and the Summary readiness strip read the same mock.
export function mockSmokeChecks(sessionId: string, worker?: string): components["schemas"]["ListSmokeChecksResponse"] {
	if (sessionId !== "demo-working") {
		return { worker: worker || "worker", checks: [] };
	}
	return {
		worker: worker || "fix gl note render",
		checks: [
			{
				id: "gitlab-mr-appears",
				sessionId,
				projectId: "agent-orchestrator",
				seq: 1,
				name: "A fresh GitLab MR shows up in Reviews on its own",
				why: "The fix broadens re-polling to every open MR; this confirms one appears without a manual refresh.",
				steps: [
					"Open the gitlab-mr-review project and go to the Reviews tab.",
					"On GitLab, open a brand-new MR against the tracked branch.",
					"Wait one review interval (~60s) without touching the app.",
				],
				expected: "The new MR appears in Reviews automatically, with CI + review status filled in.",
				prNum: 36,
				fileRef: "scmobserver.go:936",
				verdict: "pass",
				note: "Appeared after ~55s, statuses correct.",
				evidence: [],
				decidedAt: now,
				createdAt: now,
				updatedAt: now,
			},
			{
				id: "canceling-pipeline",
				sessionId,
				projectId: "agent-orchestrator",
				seq: 2,
				name: 'A canceling pipeline reads as "In progress", never "Unknown"',
				why: "A canceling GitLab pipeline briefly reported Unknown before; this verifies it stays In progress.",
				steps: ["Trigger a pipeline then cancel it.", "Watch the badge during the cancel."],
				expected: 'The badge shows "In progress" then the terminal state — never "Unknown".',
				prNum: 36,
				fileRef: "normalize.go:451",
				verdict: "fail",
				note: "Flashed Unknown for ~1s before In progress.",
				evidence: [
					{
						id: "ev_demo1",
						checkId: "canceling-pipeline",
						sessionId,
						kind: "image",
						filename: "unknown-flash.png",
						mime: "image/png",
						sizeBytes: 84213,
						createdAt: now,
					},
				],
				decidedAt: now,
				createdAt: now,
				updatedAt: now,
			},
			{
				id: "reviewers-unchanged",
				sessionId,
				projectId: "agent-orchestrator",
				seq: 3,
				name: "GitHub PRs still review exactly as before",
				why: "The change only touches the GitLab path; GitHub review flow must be untouched.",
				steps: ["Open a GitHub-backed session with an open PR.", "Trigger a review and watch it complete."],
				expected: "GitHub review behaves identically to before the change.",
				prNum: 34,
				fileRef: "observer.go:201",
				verdict: "skip",
				note: "No GitHub project handy right now.",
				evidence: [],
				decidedAt: now,
				createdAt: now,
				updatedAt: now,
			},
			{
				id: "ios-sim",
				sessionId,
				projectId: "agent-orchestrator",
				seq: 4,
				name: "iOS simulator smoke of the share sheet",
				why: "Native share-sheet timing can't be unit-tested.",
				steps: ["Open the app in the iOS simulator.", "Tap Share."],
				expected: "The share sheet opens without a frame drop.",
				prNum: 31,
				fileRef: "ShareView.swift:88",
				verdict: "pending",
				note: "",
				evidence: [],
				createdAt: now,
				updatedAt: now,
			},
		],
	};
}

/**
 * Changes-mode fixtures for the Files panel, keyed by session. Covers one of
 * every row shape the panel must render — modified, added, deleted, renamed,
 * binary, and an uncommitted row — plus the two degraded states, so the rail's
 * responsive breakpoints and empty views are verifiable with `VITE_NO_ELECTRON=1`
 * and no daemon.
 */
export function mockWorkspaceChanges(sessionId: string): WorkspaceChangesResponse {
	if (sessionId === "demo-no-target") {
		return { available: false, reason: "no_target_branch", files: [], truncated: false };
	}
	if (sessionId === "demo-merged") {
		return { available: false, reason: "no_workspace", files: [], truncated: false };
	}
	if (sessionId === "demo-clean") {
		return {
			available: true,
			targetBranch: "main",
			targetSource: "pr",
			files: [],
			truncated: false,
		};
	}
	return {
		available: true,
		targetBranch: "main",
		targetSource: "pr",
		mergeBase: "abc1234",
		truncated: false,
		files: [
			{
				path: "frontend/src/renderer/components/DiffRows.tsx",
				status: "modified",
				additions: 42,
				deletions: 6,
				binary: false,
				committed: true,
			},
			{
				path: "frontend/src/renderer/components/FilesPanel.tsx",
				status: "added",
				additions: 180,
				deletions: 0,
				binary: false,
				committed: true,
			},
			{
				path: "backend/internal/service/session/workspace_changes.go",
				status: "added",
				additions: 210,
				deletions: 0,
				binary: false,
				committed: true,
			},
			{
				path: "frontend/src/renderer/lib/legacy-diff.ts",
				status: "deleted",
				additions: 0,
				deletions: 38,
				binary: false,
				committed: true,
			},
			{
				path: "frontend/src/renderer/lib/tree.ts",
				oldPath: "frontend/src/renderer/lib/session-tree.ts",
				status: "renamed",
				additions: 12,
				deletions: 3,
				binary: false,
				committed: true,
			},
			{
				path: "frontend/src/renderer/styles.css",
				status: "modified",
				additions: 18,
				deletions: 2,
				binary: false,
				committed: true,
			},
			{
				path: "frontend/src/api/schema.ts",
				status: "modified",
				additions: 9,
				deletions: 0,
				binary: false,
				committed: false,
			},
			{
				path: "frontend/ao-dashboard-preview.png",
				status: "modified",
				additions: 0,
				deletions: 0,
				binary: true,
				committed: true,
			},
		],
	};
}
