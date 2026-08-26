import type { PRState, PullRequestFacts, WorkspaceSummary } from "../types/workspace";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import type { components } from "../../api/schema";

type WorkspaceChangesResponse = components["schemas"]["WorkspaceChangesResponse"];
type WorkspaceFilesResponse = components["schemas"]["WorkspaceFilesResponse"];
type DiffContextResponse = components["schemas"]["DiffContextResponse"];

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
		// The two projects deliberately differ here so the mock renderer exercises
		// both inspector rails: ao-demo opts into the web UI (five tabs, Browser
		// last), docs-site leaves it unset like every project that never opts in
		// (four tabs, no Browser).
		hasWebUI: true,
		// The demo project targets iOS too, so the harness can show the sixth rail
		// tab and the member switcher's device pip. Without it the Device tab does
		// not render here at all and neither can be looked at.
		hasIOSSimulator: true,
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
				targetBranch: "main-fluke",
				targetSource: "project",
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
				taskSize: "standard",
				crew: { id: "demo-working", role: "dev", hasRun: true },
			},
			{
				// A crew's qa, born asleep: a row and an id, no terminal. It is drawn on
				// dev's card as a chip and under dev in the sidebar - never as a card of
				// its own, which is what `tasksFrom` is for.
				id: "demo-working-qa",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Build screenshot-ready dashboard data",
				provider: "codex",
				branch: "demo/dashboard-screenshot",
				status: "idle",
				isSuspended: true,
				createdAt: hoursAgo(3),
				updatedAt: hoursAgo(3),
				activity: { state: "idle", lastActivityAt: hoursAgo(3) },
				prs: [],
				taskSize: "standard",
				crew: { id: "demo-working", role: "qa", hasRun: false },
			},
			{
				id: "demo-needs-input",
				taskSize: "mechanical",
				terminalHandleId: "demo-needs-input/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Resolve reviewer feedback on terminal polish",
				provider: "claude-code",
				branch: "demo/terminal-polish",
				targetBranch: "main-fluke",
				targetSource: "pr",
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
			// The NEEDS YOU lane holds four genuinely different statuses, and the
			// preview must show all four: the lane is the whole point of the board,
			// and a status with no demo card is a status nobody ever looks at. The
			// glyph for `no_signal` shipped as an unreadable speck precisely because
			// no preview surface rendered it.
			{
				id: "demo-no-signal",
				terminalHandleId: "demo-no-signal/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Port billing export to the new ledger schema",
				provider: "claude-code",
				branch: "demo/ledger-export",
				targetBranch: "main-fluke",
				targetSource: "project",
				status: "no_signal",
				displayStatus: "no_signal",
				createdAt: hoursAgo(2),
				updatedAt: hoursAgo(2),
				activity: { state: "unknown", lastActivityAt: hoursAgo(2) },
				changedFiles: [],
				prs: [],
			},
			{
				id: "demo-question",
				terminalHandleId: "demo-question/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Decide the retry policy for webhook delivery",
				provider: "codex",
				branch: "demo/webhook-retry",
				targetBranch: "main-fluke",
				targetSource: "project",
				status: "needs_input",
				displayStatus: "needs_you",
				createdAt: hoursAgo(4),
				updatedAt: minutesAgo(11),
				activity: { state: "waiting_input", lastActivityAt: minutesAgo(11) },
				changedFiles: [{ path: "backend/internal/webhook/deliver.go", additions: 63, deletions: 12 }],
				commitMessage: "sketch webhook retry ladder",
				prs: [],
			},
			{
				id: "demo-review-stack",
				terminalHandleId: "demo-review-stack/terminal_0",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Review stacked browser preview flow",
				provider: "codex",
				branch: "demo/browser-preview-stack",
				targetBranch: "release/2.1",
				targetSource: "session_pr_target",
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
				taskSize: "standard",
				crew: { id: "demo-ready", role: "dev", hasRun: true },
			},
			{
				// qa has had its turn here (hasRun), so this task's card is held out of
				// Ready to Merge by the CHECKLIST rather than by an unwoken agent.
				id: "demo-ready-qa",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Merge README screenshot asset update",
				provider: "codex",
				branch: "demo/readme-assets",
				status: "idle",
				isSuspended: true,
				createdAt: hoursAgo(9),
				updatedAt: minutesAgo(12),
				activity: { state: "idle", lastActivityAt: minutesAgo(12) },
				prs: [],
				taskSize: "standard",
				crew: { id: "demo-ready", role: "qa", hasRun: true },
			},
			{
				// THE STALL, on the demo board so the lane can be looked at: a crew
				// that finished its work and stopped. qa parked after its pass, dev is
				// asleep waiting for a turn nobody gave it, and neither has a pane.
				// Before the rollup learned to say so, this card read Ready - the one
				// state on the board that looks like nothing is wrong.
				id: "demo-stalled",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Rename the export job's retry flag",
				provider: "claude-code",
				branch: "demo/retry-flag",
				status: "mergeable",
				displayStatus: "mergeable",
				isSuspended: true,
				sleepReason: "idle",
				createdAt: hoursAgo(6),
				updatedAt: hoursAgo(1),
				activity: { state: "parked", lastActivityAt: hoursAgo(1) },
				prs: [demoPr(324, "open", "passing", "approved")],
				taskSize: "standard",
				crew: { id: "demo-stalled", role: "dev", hasRun: true },
			},
			{
				// qa ran, found nothing a person has to play, and parked. Its turn is
				// over and it never told dev - which is the other half of the same
				// failure, and why qa's floor now obliges it to hand back.
				id: "demo-stalled-qa",
				workspaceId: "ao-demo",
				workspaceName: "ao-demo",
				title: "Rename the export job's retry flag",
				provider: "claude-code",
				branch: "demo/retry-flag",
				status: "needs_input",
				statusReason: "idle_aged",
				createdAt: hoursAgo(6),
				updatedAt: minutesAgo(50),
				activity: { state: "parked", lastActivityAt: minutesAgo(50) },
				prs: [],
				taskSize: "standard",
				crew: { id: "demo-stalled", role: "qa", hasRun: true },
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
				id: "docs-awaiting-review",
				terminalHandleId: "docs-awaiting-review/terminal_0",
				workspaceId: "docs-site",
				workspaceName: "docs-site",
				title: "Rewrite the upgrade notes",
				provider: "claude-code",
				branch: "demo/upgrade-notes",
				status: "review_pending",
				createdAt: hoursAgo(6),
				updatedAt: minutesAgo(34),
				activity: { state: "idle", lastActivityAt: minutesAgo(34) },
				prs: [demoPr(412, "open", "passing", "none")],
			},
			{
				id: "docs-ao-approved",
				terminalHandleId: "docs-ao-approved/terminal_0",
				workspaceId: "docs-site",
				workspaceName: "docs-site",
				title: "Split the install guide",
				provider: "claude-code",
				branch: "demo/install-guide",
				status: "review_pending",
				createdAt: hoursAgo(8),
				updatedAt: minutesAgo(19),
				activity: { state: "idle", lastActivityAt: minutesAgo(19) },
				prs: [demoPr(413, "open", "passing", "none")],
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

/**
 * The TASK a mock session belongs to - the harness's stand-in for the daemon's
 * `TaskScoped` middleware (#242).
 *
 * A crew's two members share one worktree, one branch, one pull request and one
 * smoke checklist, and the daemon resolves any member to its dev before it
 * answers those four surfaces. The mock fixtures are keyed by session, so
 * without this the harness would show a qa an empty Summary and an empty
 * checklist - the very bug the daemon no longer has, reintroduced by the fake
 * data and easy to mistake for a real one.
 *
 * A solo session is its own task, so this is the identity for every session
 * without a crew.
 */
export function mockTaskId(sessionId: string): string {
	const session = mockWorkspaces.flatMap((workspace) => workspace.sessions).find((s) => s.id === sessionId);
	return session?.crew?.id ?? sessionId;
}

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
			// A real head commit, so the Tests tab can compare a machine result's
			// `agentSha` against it and mark the older run stale.
			headSha: "4b21e07c9a5d1f6083e2b7c4419af6d2e0d5c118",
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
	// The two ends of a quiet review, side by side in one project: an open PR that
	// nobody has looked at yet (no approval rule, no decision — the Review gate
	// reads "awaiting"), and one that carries an approval. The first is the state
	// the strip used to call Ready to Merge.
	"docs-awaiting-review": [prSummary("docs-awaiting-review", 412, { changedFiles: 2, additions: 63, deletions: 11 })],
	// The third end of the same axis: no human has looked, but AO's own reviewer
	// approved this exact head. That is not "nobody has looked", so the gate says
	// who did.
	"docs-ao-approved": [
		prSummary("docs-ao-approved", 413, {
			changedFiles: 4,
			additions: 96,
			deletions: 30,

			aoReview: {
				verdict: "approved",
				runId: "preview-run-413",
				targetSha: "preview-413",
				reviewedAt: now,
			},
		}),
	],
	"docs-ready": [prSummary("docs-ready", 411, { changedFiles: 3, additions: 88, deletions: 24 })],
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

/**
 * The machine's simulators, for the VITE_NO_ELECTRON harness.
 *
 * TWO booted devices with DIFFERENT holders, deliberately: that is the case the
 * two-agent task creates and the one nothing else can show - dev driving one
 * simulator while qa drives another. It is what makes the switcher's device pip
 * and the Device tab's role-named holder line visible without a Mac, an Xcode
 * and two running agents.
 */
export function mockSimDevices(): components["schemas"]["ListSimDevicesResponse"] {
	return {
		defaultUdid: null,
		defaultReason: "two simulators are booted, so an unqualified command has no default",
		devices: [
			{
				udid: "MOCK-UDID-A",
				name: "iPhone 16 Pro",
				runtime: "iOS 26.3",
				runtimeIdentifier: "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
				state: "Booted",
				available: true,
				default: false,
				lease: { state: "held", holder: "demo-ready" },
			},
			{
				udid: "MOCK-UDID-B",
				name: "iPhone 15",
				runtime: "iOS 26.3",
				runtimeIdentifier: "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
				state: "Booted",
				available: true,
				default: false,
				lease: { state: "held", holder: "demo-ready-qa" },
			},
		],
	};
}

// Mock smoke checklist for the VITE_NO_ELECTRON renderer harness (no daemon).
// Only the primary demo worker has a checklist; other sessions render the empty
// state (not every worker authors one). Shared by useSessionSmokeChecks so the
// Tests tab and the Summary readiness strip read the same mock.
export function mockSmokeChecks(sessionId: string, worker?: string): components["schemas"]["ListSmokeChecksResponse"] {
	if (sessionId === "demo-in-review") return mockAgentSmokeChecks(sessionId, worker);
	// demo-ready is a CREW task whose dev can land and whose qa has already run:
	// what holds it out of Ready to Merge is a case only a person can judge, which
	// is the AND this feature exists to make visible.
	if (sessionId === "demo-ready") {
		return {
			worker: worker || "readme assets",
			checks: [
				{
					id: "asset-renders",
					sessionId,
					projectId: "agent-orchestrator",
					seq: 1,
					name: "The new screenshot renders crisply at 2x",
					why: "Only a person can say whether an image looks right; a machine can only say the file loaded.",
					steps: ["Open docs/readme.md in the preview.", "Look at the dashboard screenshot at 200%."],
					expected: "No blur, no banding, text in the screenshot is legible.",
					prNum: 323,
					fileRef: "docs/assets/readme/dashboard.png:1",
					verdict: "pending",
					note: "",
					evidence: [],
					agentVerdict: "pass",
					agentNote: "Image loads and is 2560x1600.",
					agentRanAt: minutesAgo(12),
					agentEvidence: [],
					createdAt: minutesAgo(20),
					updatedAt: minutesAgo(12),
				},
			],
		} as components["schemas"]["ListSmokeChecksResponse"];
	}
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
				agentEvidence: [],
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
						source: "user",
					},
				],
				agentEvidence: [],
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
				agentEvidence: [],
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
				agentEvidence: [],
				createdAt: now,
				updatedAt: now,
			},
		],
	};
}

/**
 * The Tests tab with a MACHINE result beside the human's: one case in each
 * state the tab has to keep apart, since jsdom cannot show whether the screen
 * reads honestly and only looking at it can:
 *
 *  1. human-only, no machine run at all (renders exactly as it always has)
 *  2. machine ran and judged pass, human hasn't played it
 *  3. machine ran and judged fail
 *  4. machine ran and DECLINED to judge (`agentRanAt` set, verdict empty):
 *     evidence captured, judgement left to a person
 *  5. stale, ran against a commit that is no longer head
 *  6. retired, out of the checklist, kept with its reason
 */
function mockAgentSmokeChecks(sessionId: string, worker?: string): components["schemas"]["ListSmokeChecksResponse"] {
	const base = {
		sessionId,
		projectId: "agent-orchestrator",
		note: "",
		evidence: [],
		agentEvidence: [],
		createdAt: now,
		updatedAt: now,
	};
	const shot = (checkId: string, id: string, filename: string) => ({
		id,
		checkId,
		sessionId,
		kind: "image",
		filename,
		mime: "image/png",
		sizeBytes: 71204,
		createdAt: now,
		source: "agent",
	});
	return {
		worker: worker || "settings copy",
		checks: [
			{
				...base,
				id: "settings-copy-paint",
				seq: 1,
				name: "The settings pane still paints in one frame on open",
				why: "The copy change re-renders the whole pane; a person has to see whether it flashes.",
				steps: ["Open Project settings.", "Close it and open it again, watching the first frame."],
				expected: "No flash of unstyled or half-laid-out content.",
				prNum: 322,
				fileRef: "ProjectSettings.tsx:140",
				verdict: "pending",
			},
			{
				...base,
				id: "settings-copy-saves",
				seq: 2,
				name: "Editing the project name saves and survives a reopen",
				why: "The save path was touched by the copy refactor.",
				steps: ["Open Project settings.", "Rename the project.", "Close and reopen the pane."],
				expected: "The new name is there, and the daemon has it.",
				prNum: 322,
				fileRef: "ProjectSettings.tsx:212",
				verdict: "pending",
				agentVerdict: "pass",
				agentNote: "Typed a new name, reopened the pane twice; the value came back both times.",
				agentRanAt: minutesAgo(24),
				agentSha: "4b21e07c9a5d1f6083e2b7c4419af6d2e0d5c118",
			},
			{
				...base,
				id: "settings-copy-validation",
				seq: 3,
				name: "An empty project name is refused with a message",
				why: "The validation string moved; the refusal must still reach the user.",
				steps: ["Clear the project name field.", "Press Save."],
				expected: "Save is refused and the field explains why.",
				prNum: 322,
				fileRef: "ProjectSettings.tsx:233",
				verdict: "pending",
				agentVerdict: "fail",
				agentNote: "Save went through with an empty name; no message appeared.",
				agentRanAt: minutesAgo(24),
				agentSha: "4b21e07c9a5d1f6083e2b7c4419af6d2e0d5c118",
				agentEvidence: [shot("settings-copy-validation", "ev_agent_val", "empty-name-saved.png")],
			},
			{
				...base,
				id: "settings-copy-focus",
				seq: 4,
				name: "Focus lands in the name field, and the ring is visible",
				why: "Keyboard users open this pane and type immediately; paint and focus are not machine-judgeable.",
				steps: ["Open Project settings with ⌘,.", "Do not touch the mouse."],
				expected: "The name field holds focus with a visible ring.",
				prNum: 322,
				fileRef: "ProjectSettings.tsx:118",
				verdict: "pending",
				agentRanAt: minutesAgo(23),
				agentSha: "4b21e07c9a5d1f6083e2b7c4419af6d2e0d5c118",
				agentEvidence: [
					shot("settings-copy-focus", "ev_agent_focus", "settings-open-focus.png"),
					shot("settings-copy-focus", "ev_agent_focus2", "settings-open-tabbed.png"),
				],
			},
			{
				...base,
				id: "settings-copy-scroll",
				seq: 5,
				name: "The long settings list scrolls without stutter",
				why: "The pane grew; drag-scroll feel is exactly what a machine cannot report.",
				steps: ["Open Project settings.", "Drag the list quickly from top to bottom."],
				expected: "Scrolling tracks the pointer with no jump or stall.",
				prNum: 322,
				fileRef: "ProjectSettings.tsx:301",
				verdict: "pending",
				agentVerdict: "pass",
				agentNote: "Scrolled the container to the end programmatically; no error, all rows rendered.",
				agentRanAt: hoursAgo(6),
				agentSha: "9f0c2ad41b77e3b5c8d6a0f21e4c7b9038a1d6e5",
			},
			{
				...base,
				id: "settings-copy-legacy-toggle",
				seq: 6,
				name: "The legacy settings toggle still writes the old key",
				why: "Kept while the old key was read anywhere.",
				steps: ["Flip the legacy toggle.", "Read the config file."],
				expected: "The old key flips with it.",
				prNum: 322,
				fileRef: "ProjectSettings.tsx:410",
				verdict: "pass",
				note: "Old key flipped, checked the file by hand.",
				decidedAt: hoursAgo(20),
				retiredAt: hoursAgo(5),
				retiredReason: "The legacy key was deleted in this PR, and a Go test now covers the migration.",
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
	// The board's merged sessions are `demo-merged-recent` / `demo-merged-earlier`
	// — matching the bare id meant this branch was unreachable, so the "worktree
	// is gone" state has never actually been visible in `ao preview`.
	if (sessionId.startsWith("demo-merged")) {
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
			// A long SINGLE-CHILD path and an oversized diff: the cases the tree's
			// chain-collapsing and the stacked view's collapsed-by-default budget
			// exist for. Note this one is six path levels but only THREE rendered
			// levels — chain-collapsing merges it — which is exactly why it cannot
			// stand in for the branchy fixture below.
			{
				path: "backend/internal/httpd/controllers/testdata/fixtures/sessions_golden.json",
				status: "added",
				additions: 64,
				deletions: 0,
				binary: false,
				committed: false,
			},
			// A BRANCHY deep tree, modelled on a real Swift app. Every level here
			// forks, so chain-collapsing has nothing to merge and the rendered depth
			// really does reach five — the only shape that exercises the tree's
			// per-level indent at depth. The long single-child path above collapses
			// to three levels and can never reach it, which is how a clamp at level
			// four shipped unnoticed.
			...[
				"NterApp/NterApp/Commons/Loading/Views/ErrorViewV2.swift",
				"NterApp/NterApp/Commons/Networking/APIClient.swift",
				"NterApp/NterApp/Investment/Trade/OrderReview/ViewControllers/ConsentOrdersViewController.swift",
				"NterApp/NterApp/Investment/Trade/OrderReview/Models/CouponCellAccessory.swift",
				"NterApp/NterApp/Investment/Trade/OrderReview/ViewModels/ConsentOrdersViewModel.swift",
				"NterApp/NterApp/Investment/Trade/Portfolio/PortfolioSummaryView.swift",
				"NterApp/NterApp/Investment/Fund/FundListViewController.swift",
			].map((path) => ({
				path,
				status: "modified" as const,
				additions: 12,
				deletions: 4,
				binary: false,
				committed: true,
			})),
			{
				path: "frontend/src/renderer/lib/generated-icons.ts",
				status: "modified",
				additions: 1840,
				deletions: 220,
				binary: false,
				committed: true,
			},
		],
	};
}

/**
 * One mock edit, described in NEW-side coordinates: `dels` are the old lines it
 * replaced and `adds` is how many of the new file's lines sit at `at`.
 */
type MockEdit = { at: number; dels: string[]; adds: number };

/**
 * The uncommitted level (HEAD .. working tree) for the mock file. These MUST
 * agree with `mockWorkspaceFile`'s `changedLines`, or the two gutter lanes and
 * the discard popover would each be describing a different file.
 */
const MOCK_UNCOMMITTED_EDITS: MockEdit[] = [
	{ at: 10, dels: ["\tconst legacy = read(path);", "\tif (!legacy) return null;", "\treturn legacy.value;"], adds: 3 },
	{ at: 20, dels: ["\t// removed while working on this"], adds: 0 },
	{ at: 31, dels: [], adds: 4 },
];

/** The branch level: everything above, plus what this branch already committed. */
const MOCK_BRANCH_EDITS: MockEdit[] = [
	...MOCK_UNCOMMITTED_EDITS,
	{ at: 50, dels: ["\t// the shape this file had on the target branch"], adds: 2 },
	{ at: 70, dels: [], adds: 3 },
];

/**
 * A stand-in per-file diff for mock mode, so the stacked Changes view and the
 * editor's two change lanes both render without a daemon
 * (`verify-renderer-ui-with-mock-data.md`).
 *
 * It is built by replaying `mockWorkspaceFile`'s own content through a list of
 * edits, rather than invented separately, so the NEW side of this diff really is
 * the file the editor has open. Without that the branch lane would mark lines
 * the buffer does not have.
 *
 * The windowed form keeps THREE hunks with real numeric gaps between them and a
 * first hunk that starts mid-file. A single contiguous block would look like a
 * diff while quietly failing to exercise the case that matters there: what the
 * viewer does where the diff skips lines.
 */
export function mockWorkspaceFileDiff(
	path: string,
	options?: { base?: "target" | "head"; fullContext?: boolean },
): DiffContextResponse {
	const supplied = mockOverride()?.diff?.(path);
	if (supplied) return supplied;
	const newLines = mockFileText(path).split("\n");
	const edits = options?.base === "head" ? MOCK_UNCOMMITTED_EDITS : MOCK_BRANCH_EDITS;
	const lines: DiffContextResponse["lines"] = [];
	let oldN = 1;
	let newN = 1;

	const context = (upTo: number) => {
		while (newN < upTo && newN <= newLines.length) {
			lines.push({ kind: "context", text: newLines[newN - 1], oldLine: oldN, newLine: newN });
			oldN++;
			newN++;
		}
	};
	for (const edit of edits) {
		context(edit.at);
		for (const text of edit.dels) {
			lines.push({ kind: "del", text, oldLine: oldN, newLine: 0 });
			oldN++;
		}
		for (let i = 0; i < edit.adds; i++) {
			lines.push({ kind: "add", text: newLines[newN - 1] ?? "", oldLine: 0, newLine: newN });
			newN++;
		}
	}
	context(newLines.length + 1);

	if (options?.fullContext) return { available: true, truncated: false, mode: "file", path, lines };
	return { available: true, truncated: false, mode: "file", path, lines: windowMockDiff(lines) };
}

/**
 * Trim a whole-file diff to git's default three lines of context, inserting the
 * `hunk` skip marker wherever lines were dropped — the same marker the daemon
 * emits, and the reason the windowed payload can never be replayed as a file.
 */
function windowMockDiff(lines: DiffContextResponse["lines"]): DiffContextResponse["lines"] {
	const CONTEXT = 3;
	const keep = new Set<number>();
	lines.forEach((line, i) => {
		if (line.kind === "context") return;
		for (let j = Math.max(0, i - CONTEXT); j <= Math.min(lines.length - 1, i + CONTEXT); j++) keep.add(j);
	});
	const out: DiffContextResponse["lines"] = [];
	let skipped = false;
	lines.forEach((line, i) => {
		if (!keep.has(i)) {
			skipped = true;
			return;
		}
		if (skipped) {
			out.push({
				kind: "hunk",
				text: `@@ -${line.oldLine},0 +${line.newLine},0 @@`,
				oldLine: line.oldLine,
				newLine: line.newLine,
			});
			skipped = false;
		}
		out.push(line);
	});
	return out;
}

/**
 * Bracketed machine runs for the Tests tab's "Machine runs" strip.
 *
 * `demo-working` is the interesting one: three runs discarded in a row, which is
 * the state the escalation exists for. `demo-ready` shows the ordinary case - a
 * clean run whose result can be believed - and every other session returns
 * NOTHING, because a session that never brackets a run must get exactly the
 * Tests tab it had before this existed.
 */
export function mockCrewRuns(sessionId: string): components["schemas"]["ListCrewRunsResponse"] {
	const run = (over: Partial<components["schemas"]["CrewRun"]>): components["schemas"]["CrewRun"] =>
		({
			id: `${sessionId}-${over.id ?? "r"}`,
			sessionId,
			projectId: "agent-orchestrator",
			attempt: 1,
			detector: "live",
			genAtStart: 0,
			genAtEnd: 0,
			kind: "test",
			startedAt: minutesAgo(10),
			createdAt: minutesAgo(10),
			updatedAt: minutesAgo(10),
			...over,
		}) as components["schemas"]["CrewRun"];

	if (sessionId === "demo-working") {
		return {
			runs: [
				run({
					id: "d3",
					kind: "test",
					label: "go test ./internal/service/...",
					startedAt: minutesAgo(3),
					endedAt: minutesAgo(2),
					outcome: "discarded",
					result: "pass",
					attempt: 3,
					changedPaths: ["backend/internal/service/session/status.go", "backend/internal/domain/crewrun.go"],
				}),
				run({
					id: "d2",
					kind: "test",
					label: "go test ./internal/service/...",
					startedAt: minutesAgo(6),
					endedAt: minutesAgo(5),
					outcome: "discarded",
					result: "pass",
					attempt: 2,
					changedPaths: ["backend/internal/service/session/status.go"],
				}),
				run({
					id: "d1",
					kind: "build",
					label: "go build ./...",
					startedAt: minutesAgo(9),
					endedAt: minutesAgo(8),
					outcome: "discarded",
					attempt: 1,
					changedPaths: ["backend/internal/domain/crewrun.go"],
				}),
				run({
					id: "u1",
					kind: "device",
					label: "ao sim pass on the Reviews tab",
					startedAt: minutesAgo(21),
					endedAt: minutesAgo(19),
					outcome: "uncertified",
					result: "pass",
					detector: "down",
					detectorReason: "the daemon restarted while this run was open, so nothing watched the tree",
				}),
				run({
					id: "p1",
					kind: "build",
					label: "npm run build",
					startedAt: minutesAgo(31),
					endedAt: minutesAgo(30),
					outcome: "trusted",
					result: "fail",
				}),
			],
		};
	}
	if (sessionId === "demo-ready") {
		return {
			runs: [
				run({
					id: "p1",
					kind: "test",
					label: "npm run test",
					startedAt: minutesAgo(14),
					endedAt: minutesAgo(13),
					outcome: "trusted",
					result: "pass",
				}),
			],
		};
	}
	return { runs: [] };
}

/**
 * The ⌘⇧O index in `ao preview` (VITE_NO_ELECTRON), where there is no daemon and
 * no worktree. Deliberately shaped like a real tree rather than a tidy list — it
 * carries generated output, assets whose names read like source, and deep paths,
 * so the palette's ranking and its long-path layout are both visible without a
 * live session.
 */
export function mockWorkspaceFiles(sessionId: string): WorkspaceFilesResponse {
	const supplied = mockOverride()?.files?.(sessionId);
	if (supplied) return supplied;
	if (sessionId.startsWith("demo-merged")) {
		return { available: false, reason: "no_workspace", paths: [], truncated: false };
	}
	return {
		available: true,
		truncated: false,
		paths: [
			"AGENTS.md",
			"CLAUDE.md",
			"DESIGN.md",
			"README.md",
			"package.json",
			"package-lock.json",
			"backend/cmd/ao/main.go",
			"backend/internal/cli/session.go",
			"backend/internal/cli/smoke.go",
			"backend/internal/domain/session.go",
			"backend/internal/httpd/controllers/sessions.go",
			"backend/internal/httpd/controllers/reviews.go",
			"backend/internal/service/session/workspace_file.go",
			"backend/internal/service/session/workspace_changes.go",
			"backend/internal/storage/sqlite/gen/queries.sql.go",
			"backend/internal/storage/sqlite/queries/sessions.sql",
			"frontend/src/main.ts",
			"frontend/src/renderer/components/SessionView.tsx",
			"frontend/src/renderer/components/OpenQuicklyPalette.tsx",
			"frontend/src/renderer/components/WorkspaceFileView.tsx",
			"frontend/src/renderer/components/FilesPanel.tsx",
			"frontend/src/renderer/lib/open-quickly.ts",
			"frontend/src/renderer/lib/open-workspace-file.ts",
			"frontend/src/renderer/routeTree.gen.ts",
			"frontend/src/renderer/styles.css",
			"frontend/node_modules/react-dom/cjs/react-dom.production.min.js",
			"docs/architecture.md",
			"docs/very/deeply/nested/directory/that/keeps/going/for/a/while/before/it/finally/stops/DeeplyNestedConfiguration.tsx",
			"screenshots/ao-dashboard-preview.png",
			"screenshots/OG-Promotion-Hub 2.png",
			"ao-logo.svg",
		],
	};
}

// ── The editor, in `ao preview` ─────────────────────────────────────────────

type WorkspaceFileResponse = components["schemas"]["WorkspaceFileResponse"];
type WriteWorkspaceFileResponse = components["schemas"]["WriteWorkspaceFileResponse"];

/**
 * The path whose mock save ALWAYS answers `409 WORKSPACE_FILE_CONFLICT`.
 *
 * The conflict flow is the one part of slice 4 that cannot be judged from a
 * still: an AO worktree has agents writing in it, so "the file moved under you"
 * is the normal case, and the resolve view is the thing most worth reviewing.
 * Without a fixed path that always conflicts it would be unreachable in
 * `ao preview`, where there is no second writer to race.
 */
export const MOCK_CONFLICTING_PATH = "backend/internal/service/session/workspace_changes.go";

/**
 * Paths an imaginary agent has written since this pane opened them.
 *
 * A conflicting save puts its path in here, so the read that follows really does
 * come back with the OTHER version — which is what makes the resolve view a
 * genuine two-sided comparison in `ao preview` rather than a diff against
 * itself. A successful save clears it: the reader's bytes are the ones on disk
 * now.
 */
const mockAgentWrites = new Set<string>();

/** The mock bytes a conflicting save finds already on disk. */
const MOCK_CONFLICT_DISK_SUFFIX = [
	"",
	"// Written by the agent while you were editing this file.",
	"func (s *Service) refreshTargetOnce(ctx context.Context, workspace, branch string) {",
	'\ts.throttle.Do(workspace+"\\x00"+branch, func() { s.refreshTarget(ctx, workspace, branch) })',
	"}",
].join("\n");

/**
 * A stable content hash for mock bytes. FNV-1a over the string: the editor only
 * ever compares hashes for equality and re-sends what it was handed, so what
 * matters is that identical bytes hash identically and a save MOVES the hash —
 * not that it is really SHA-256.
 */
function mockContentHash(content: string): string {
	let h = 0x811c9dc5;
	for (let i = 0; i < content.length; i++) {
		h ^= content.charCodeAt(i);
		h = Math.imul(h, 0x01000193) >>> 0;
	}
	return `sha256:mock${h.toString(16).padStart(8, "0")}`;
}

const MOCK_FILE_BODIES: Record<string, (name: string, stem: string) => string[]> = {
	go: (name, stem) => [
		"package session",
		"",
		"import (",
		'\t"context"',
		'\t"fmt"',
		'\t"strings"',
		")",
		"",
		`// ${stem} is the mock body ${name} renders with in \`ao preview\`.`,
		`type ${stem} struct {`,
		"\tWorkspace string",
		"\tTarget    string",
		"}",
		"",
		`func (r *${stem}) Resolve(ctx context.Context) (string, error) {`,
		'\tif r.Workspace == "" {',
		'\t\treturn "", fmt.Errorf("no workspace")',
		"\t}",
		"\treturn strings.TrimSpace(r.Target), nil",
		"}",
	],
	ts: (name, stem) => [
		'import { useMemo } from "react";',
		"",
		`/** ${name} — the mock body this file renders with in \`ao preview\`. */`,
		`export function ${stem}(input: readonly string[]) {`,
		"\treturn useMemo(() => {",
		"\t\tconst seen = new Set<string>();",
		"\t\treturn input.filter((value) => {",
		"\t\t\tif (seen.has(value)) return false;",
		"\t\t\tseen.add(value);",
		"\t\t\treturn true;",
		"\t\t});",
		"\t}, [input]);",
		"}",
	],
	md: (name) => [
		`# ${name}`,
		"",
		"The mock body this file renders with in `ao preview`, where there is no",
		"daemon and no worktree to read.",
		"",
		"- one",
		"- two",
		"- three",
	],
};

/** An identifier-safe stem for a file name, so the mock body compiles by eye. */
function mockStem(name: string): string {
	const base = name.replace(/\.[^.]*$/, "").replace(/[^A-Za-z0-9]/g, "");
	const stem = base === "" ? "Mock" : base;
	return stem[0].toUpperCase() + stem.slice(1);
}

function mockFileText(path: string): string {
	const name = path.slice(path.lastIndexOf("/") + 1);
	const ext = name.slice(name.lastIndexOf(".") + 1);
	const stem = mockStem(name);
	const body =
		MOCK_FILE_BODIES[ext] ??
		MOCK_FILE_BODIES[ext === "tsx" || ext === "js" || ext === "jsx" ? "ts" : "md"] ??
		MOCK_FILE_BODIES.md;
	const head = body(name, stem);
	// Padded to a length worth scrolling, so the minimap, sticky scroll and the
	// two gutter lanes are all visible at once rather than in a 20-line stub.
	const tail: string[] = [];
	for (let i = head.length + 1; i <= 96; i++) tail.push(`// ${name}:${i}`);
	return [...head, ...tail].join("\n");
}

/**
 * One file's content for the renderer harness (`VITE_NO_ELECTRON=1`), where
 * there is no daemon to read a worktree.
 *
 * Deliberately covers the states the editor must render differently, because
 * each one is a different pane and none of them was previewable before:
 * an ordinary editable file, a file that is `too_large` to display, and a file
 * whose read was `truncated` (which is read-only — saving it would delete the
 * tail). An ABSOLUTE path is served too, and is what exercises the
 * outside-the-workspace read-only state.
 */
/**
 * The e2e editor gallery's seam.
 *
 * 🗝 `e2e/editor-gallery-api-stub.ts` serves its Swift fixture by intercepting
 * `window.fetch`. That worked while the viewer always went through the network —
 * and stopped the moment the viewer grew a preview branch that issues no request
 * at all, silently swapping the gallery's fixture for these mocks and taking
 * every `// MARK:` section with it. The harness registers its payload here
 * instead of the fixture having to become a second mock.
 */
type MockFileOverride = {
	file?(path: string): WorkspaceFileResponse | null;
	diff?(path: string): DiffContextResponse | null;
	/** The Files rail's Browse index — how `e2e/files-bench` supplies a 7k-file tree. */
	files?(sessionId: string): WorkspaceFilesResponse | null;
};

function mockOverride(): MockFileOverride | undefined {
	return (globalThis as { __aoMockWorkspaceFile?: MockFileOverride }).__aoMockWorkspaceFile;
}

export function mockWorkspaceFile(path: string): WorkspaceFileResponse {
	const supplied = mockOverride()?.file?.(path);
	if (supplied) return supplied;
	if (path.endsWith(".png") || path.endsWith(".svg")) {
		return {
			available: false,
			reason: "binary",
			path,
			lines: [],
			changedLines: [],
			trailingNewline: true,
			truncated: false,
		};
	}
	if (path === "frontend/src/renderer/lib/generated-icons.ts") {
		return {
			available: false,
			reason: "too_large",
			path,
			lines: [],
			changedLines: [],
			trailingNewline: true,
			truncated: false,
		};
	}
	const text = mockAgentWrites.has(path) ? mockFileText(path) + MOCK_CONFLICT_DISK_SUFFIX : mockFileText(path);
	const rows = text.split("\n");
	const truncated = path === "frontend/src/renderer/routeTree.gen.ts";
	return {
		available: true,
		path,
		truncated,
		trailingNewline: true,
		contentHash: mockContentHash(text),
		lines: rows.map((row, i) => ({ kind: "context", text: row, oldLine: i + 1, newLine: i + 1 })),
		// One run of each kind, so both gutter lanes and the discard popover have
		// something real to draw without a git repository behind them.
		changedLines: [
			{ start: 10, end: 12, kind: "modified" },
			{ start: 20, end: 20, kind: "removed" },
			{ start: 31, end: 34, kind: "added" },
		],
	};
}

/** What a mock save answers: the moved hash, or a conflict for the fixed path. */
export type MockSaveResult =
	| { ok: true; response: WriteWorkspaceFileResponse }
	| { ok: false; status: 409; body: components["schemas"]["APIError"] };

/**
 * 🗝 The conflict is preconditioned, not unconditional, and that is the point.
 * A save that carries the hash of the version now ON DISK succeeds; one that
 * carries the stale hash the reader started from is refused. That is exactly
 * the daemon's own contract — the route refuses a BLIND clobber, not an
 * informed one — so the preview really does demonstrate the way out of a 409,
 * rather than a dead end that can only be dismissed.
 */
export function mockWorkspaceFileSave(path: string, content: string, baseHash?: string): MockSaveResult {
	if (path === MOCK_CONFLICTING_PATH) {
		const disk = mockFileText(path) + MOCK_CONFLICT_DISK_SUFFIX;
		const diskHash = mockContentHash(disk);
		if (baseHash === diskHash) {
			mockAgentWrites.delete(path);
			return {
				ok: true,
				response: { path, contentHash: mockContentHash(content), size: content.length, changedLines: [] },
			};
		}
		mockAgentWrites.add(path);
		return {
			ok: false,
			status: 409,
			body: {
				error: "conflict",
				code: "WORKSPACE_FILE_CONFLICT",
				message: "This file changed on disk since it was read.",
				details: {
					currentHash: diskHash,
					currentSize: disk.length,
					currentModifiedAt: new Date(Date.now() - 12_000).toISOString(),
				},
			} as components["schemas"]["APIError"],
		};
	}
	return {
		ok: true,
		response: {
			path,
			contentHash: mockContentHash(content),
			size: content.length,
			changedLines: [
				{ start: 10, end: 12, kind: "modified" },
				{ start: 31, end: 34, kind: "added" },
			],
		},
	};
}
