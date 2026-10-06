import {
	Archive,
	CirclePause,
	GitBranch,
	GitFork,
	GitMerge,
	Minus,
	TriangleAlert,
	type LucideIcon,
} from "lucide-react";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";
import {
	CHILD_META,
	type ChildGroup,
	type ChildState,
	type SessionChild,
	childGroups,
	childTooltip,
} from "../lib/children";

const CHILD_ICON: Record<ChildState, LucideIcon> = {
	running: GitBranch,
	merging: GitMerge,
	held: CirclePause,
	conflict: TriangleAlert,
	preserved: Archive,
	merged: GitMerge,
	removed: Minus,
};

function ChildPip({ group }: { group: ChildGroup }) {
	const meta = CHILD_META[group.state];
	const Icon = CHILD_ICON[group.state];
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					className="inline-flex shrink-0 items-center gap-1 rounded-full px-1 py-px text-[10px] font-medium"
					data-child-pip={group.state}
					data-child-count={group.children.length}
					aria-label={`${group.children.length} ${meta.word}`}
					style={{ color: meta.tone }}
				>
					<Icon className="h-[9px] w-[9px] shrink-0" aria-hidden="true" />
					<span className="tabular-nums">{group.children.length}</span>
				</span>
			</TooltipTrigger>
			<TooltipContent className="max-w-xs">
				<ul className="flex flex-col gap-1">
					{group.children.map((child) => (
						<li key={child.agentId}>{childTooltip(child)}</li>
					))}
				</ul>
			</TooltipContent>
		</Tooltip>
	);
}

/**
 * The subagent strip: the worker's child worktrees, under the crew strip.
 *
 * Each subagent the worker ran with its own worktree is counted under the state
 * of its work - waiting on a person (a conflict, a merge held behind the
 * worker's own edits, or work kept on its own branch after the worker ended),
 * still running, merged into the worker's branch, or finished without changes.
 * One pip per state, most urgent first, so the strip stays one line however
 * many there are; each pip's tooltip names the subagents and what to do.
 * Renders nothing for a task that never ran one.
 */
export function ChildStrip({ items }: { items: SessionChild[] }) {
	if (items.length === 0) return null;
	return (
		<TooltipProvider delayDuration={200}>
			<div
				className="@container/children flex min-w-0 items-center gap-1 px-[13px] py-1.5"
				data-child-strip=""
				style={{ borderTop: "1px solid var(--kanban-card-divider)" }}
				onClick={(event) => event.stopPropagation()}
			>
				{/* The label is what sheds on a narrow card (162px at a 1280px
				    window): the pips are the information, and a fork glyph keeps
				    the row recognisable without the word. */}
				<span className="hidden shrink-0 text-[10px] text-passive @[190px]/children:inline">subagents</span>
				<GitFork className="h-[10px] w-[10px] shrink-0 text-passive @[190px]/children:hidden" aria-label="subagents" />
				{childGroups(items).map((group) => (
					<ChildPip group={group} key={group.state} />
				))}
			</div>
		</TooltipProvider>
	);
}
