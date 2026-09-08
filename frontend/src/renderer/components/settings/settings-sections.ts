import type { LucideIcon } from "lucide-react";
import { Bot, FolderGit2, GitBranch, Inbox, Laptop, MessagesSquare, Trash2, Users } from "lucide-react";

// The two settings scopes share one two-pane shell; each scope shows only its own
// section set. Under variant B (chosen 2026-09-08) the sections are cut by WHAT A
// SETTING ACTS ON rather than by the shape of the config file, so the heading
// itself answers "where will I find this" and "when does it bite" before a chip
// has to. Keys are stable ids used for nav selection and (later) deep links;
// keywords back the in-settings search so a field-level term surfaces the section
// that holds it.
export type SettingsScope = "project" | "global";

export type SectionMeta = {
	key: string;
	label: string;
	Icon: LucideIcon;
	// One-line "· …" suffix next to the section title: the section's promise about
	// what lives in it.
	hint: string;
	// Extra terms the search field matches on beyond the label.
	keywords: string;
};

export const PROJECT_SECTIONS: SectionMeta[] = [
	{
		key: "repo",
		label: "Repository & branches",
		Icon: GitBranch,
		hint: "where the work lands",
		keywords:
			"identity id kind path repo remote origin workspace repos child default branch base main session prefix namespace git convention workflow gitflow custom branch prefix feature naming",
	},
	{
		key: "start",
		label: "Starting a task",
		Icon: Users,
		hint: "who AO puts on it, and how far it may go",
		keywords:
			"worker orchestrator agent harness claude code codex opencode model opus sonnet haiku permission mode accept edits auto bypass crew qa tester automatic check-in pause before implementing needs you refresh agents availability cache",
	},
	{
		key: "told",
		label: "What agents are told",
		Icon: Bot,
		hint: "language, extra prompts, and what this project has",
		keywords:
			"response language thai english japanese german localization override inherit additional system prompt append orchestrator worker reviewer web ui browser preview ao preview ios simulator xcode device",
	},
	{
		key: "flow",
		label: "Incoming & outgoing",
		Icon: Inbox,
		hint: "issues in, reviews and merges out",
		keywords:
			"tracker intake issue assignee github gitlab jira reviewer agent code review approval rule required approvals ready to merge threshold",
	},
];

export const GLOBAL_SECTIONS: SectionMeta[] = [
	{
		key: "every-agent",
		label: "Every agent",
		Icon: Bot,
		hint: "what every session starts from",
		keywords:
			"response language thai english default localization system prompt base orchestrator worker qa reviewer coordination floor confidentiality placeholder project id",
	},
	{
		key: "running",
		label: "While work runs",
		Icon: MessagesSquare,
		hint: "what AO says and does to a worker mid-task",
		keywords:
			"message template review comment dispatch ci failing merge conflict pr base mismatch tracker bot comment ao reviewer batch single nudge confirm before spawning workers approval auto-send unresolved pr comments",
	},
	{
		key: "cleanup",
		label: "Cleaning up",
		Icon: Trash2,
		hint: "what AO deletes once work is finished",
		keywords:
			"auto-reclaim reclaim finished sessions grace period tmux worktree build output derived data pods node_modules smoke test evidence retention screenshots clips delete age days purge sweep",
	},
	{
		key: "mac",
		label: "This Mac",
		Icon: Laptop,
		hint: "the app itself, not the agents",
		keywords:
			"updates automatic channel stable nightly version check install restart wiki vault obsidian notes knowledge base folder path notifications test banner companion desktop pet overlay procs library creature species migration import legacy",
	},
];

// Where the project scope's read-only identity panel lives. Kept as a constant so
// the panel and its section cannot drift apart.
export const PROJECT_IDENTITY_SECTION = "repo";

// The scope switcher's own icon, reused by the nav header.
export const SCOPE_ICON: Record<SettingsScope, LucideIcon> = {
	project: FolderGit2,
	global: Bot,
};

export function sectionsForScope(scope: SettingsScope): SectionMeta[] {
	return scope === "project" ? PROJECT_SECTIONS : GLOBAL_SECTIONS;
}

// A section stays in the filtered nav when the query is empty, or matches its
// label or any of its keywords (case-insensitive substring).
export function matchesSearch(section: SectionMeta, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (!q) return true;
	return section.label.toLowerCase().includes(q) || section.keywords.toLowerCase().includes(q);
}
