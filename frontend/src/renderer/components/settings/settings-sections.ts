import type { LucideIcon } from "lucide-react";
import { Bot, FileText, FolderGit2, MessageSquare, Monitor, Workflow } from "lucide-react";

// The two settings scopes share one two-pane shell; each scope shows only its
// own section set (proposal §3). Keys are stable ids used for nav selection and
// (later) deep links; keywords back the in-settings search so a field-level term
// (e.g. "branch") surfaces the section that holds it.
export type SettingsScope = "project" | "global";

export type SectionMeta = {
	key: string;
	label: string;
	Icon: LucideIcon;
	// One-line "· …" suffix next to the section title (mirrors the mockups' hints).
	hint: string;
	// Extra terms the search field matches on beyond the label.
	keywords: string;
};

export const PROJECT_SECTIONS: SectionMeta[] = [
	{
		key: "general",
		label: "General",
		Icon: FolderGit2,
		hint: "repository, worktrees & branch naming",
		keywords: "identity id kind path repo workspace repos default branch session prefix git convention workflow prefix",
	},
	{
		key: "agents",
		label: "Agents",
		Icon: Bot,
		hint: "who runs, on which model & permission",
		keywords: "worker orchestrator agent refresh model override permission mode reviewer",
	},
	{
		key: "prompts",
		label: "Prompts",
		Icon: FileText,
		hint: "additional system prompts appended for this project",
		keywords: "orchestrator worker reviewer additional system prompt",
	},
	{
		key: "automation",
		label: "Automation",
		Icon: Workflow,
		hint: "things AO does on its own for this project",
		keywords: "tracker intake issue assignee approval rule required approvals",
	},
];

export const GLOBAL_SECTIONS: SectionMeta[] = [
	{
		key: "prompts",
		label: "Prompts",
		Icon: FileText,
		hint: "the global base each session kind starts from",
		keywords: "orchestrator worker reviewer system prompt base",
	},
	{
		key: "messages",
		label: "Messages",
		Icon: MessageSquare,
		hint: "runtime nudge messages sent into a worker",
		keywords: "review comment dispatch ci failing merge conflict tracker bot ao reviewer batch single template",
	},
	{
		key: "automation",
		label: "Automation",
		Icon: Workflow,
		hint: "orchestrator & daemon automatic behaviour",
		keywords:
			"confirm spawning workers auto-send unresolved pr comments auto-reclaim grace period evidence retention ttl purge smoke test screenshots delete age",
	},
	{
		key: "system",
		label: "System",
		Icon: Monitor,
		hint: "notifications, updates & migration",
		keywords: "notifications test updates channel version check migration import legacy",
	},
];

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
