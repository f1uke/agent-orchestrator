import { Archive, CirclePause, GitBranch, GitMerge, TriangleAlert, type LucideIcon } from "lucide-react";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";
import {
	CHILD_META,
	type ChildState,
	type SessionChild,
	childLabel,
	childStripModel,
	childTooltip,
} from "../lib/children";
import { cn } from "../lib/utils";

const CHILD_ICON: Record<ChildState, LucideIcon> = {
	running: GitBranch,
	merging: GitMerge,
	held: CirclePause,
	conflict: TriangleAlert,
	preserved: Archive,
	merged: GitMerge,
	removed: GitBranch,
};

function ChildChip({ child }: { child: SessionChild }) {
	const meta = CHILD_META[child.state];
	const Icon = CHILD_ICON[child.state];
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					className={cn(
						"inline-flex min-w-0 max-w-[9rem] items-center gap-1 rounded-full border border-border-strong px-1.5 py-px text-[10px] font-medium",
						!meta.attention && child.state === "merged" && "opacity-70",
					)}
					data-child-chip={child.agentId}
					data-child-state={child.state}
				>
					<Icon className="h-[9px] w-[9px] shrink-0" style={{ color: meta.tone }} aria-hidden="true" />
					<span className="truncate" style={{ color: meta.attention ? meta.tone : undefined }}>
						{childLabel(child)}
					</span>
				</span>
			</TooltipTrigger>
			<TooltipContent className="max-w-xs">{childTooltip(child)}</TooltipContent>
		</Tooltip>
	);
}

/**
 * The subagent strip: the worker's child worktrees, under the crew strip.
 *
 * Each chip is one subagent the worker ran with its own worktree. Its glyph and
 * colour say where that work is - still running, merged into the worker's
 * branch, or waiting on a person (a held merge, a conflict, or work kept on its
 * own branch after the worker ended) - and its tooltip says what to do. Renders
 * nothing for a task that never ran one.
 */
export function ChildStrip({ items }: { items: SessionChild[] }) {
	if (items.length === 0) return null;
	const { shown, overflow, empty } = childStripModel(items);
	return (
		<TooltipProvider delayDuration={200}>
			<div
				className="flex min-w-0 flex-wrap items-center gap-1 px-[13px] py-1.5"
				data-child-strip=""
				style={{ borderTop: "1px solid var(--kanban-card-divider)" }}
				onClick={(event) => event.stopPropagation()}
			>
				<span className="shrink-0 text-[10px] text-passive">subagents</span>
				{shown.map((child) => (
					<ChildChip child={child} key={child.agentId} />
				))}
				{overflow > 0 && <span className="shrink-0 text-[10px] text-passive">+{overflow}</span>}
				{shown.length === 0 && empty > 0 && (
					<span className="shrink-0 text-[10px] text-passive">{empty} without changes</span>
				)}
			</div>
		</TooltipProvider>
	);
}
