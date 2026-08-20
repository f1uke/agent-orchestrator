import { Check, Contrast, Eye, Moon, Slash, type LucideIcon } from "lucide-react";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";
import { type CrewChipState, type ReviewGateState, type Task, crewChipState } from "../lib/crew";
import { statusGlyph, statusLabel } from "../lib/status-glyph";
import { cn } from "../lib/utils";
import type { TaskSize, WorkspaceSession } from "../types/workspace";

/**
 * The crew strip: who is on this task, and what the review gate holds.
 *
 * It carries TWO KINDS OF THING, and shows that they are different rather than
 * hiding it:
 *
 *  - **chips** for the agents (`dev › qa`), bordered, each drawing that member's
 *    own status glyph. They say WHO IS ON THIS TASK, and clicking one opens that
 *    agent's terminal.
 *  - **a pip** for review, after a divider: borderless, smaller, quieter. Review
 *    is not a teammate - it has no session, no terminal and nobody to talk to;
 *    each pass is an ephemeral run that reports a verdict and closes. So it says
 *    what the VERDICT holds, and it never gets a sleep state, because a gate does
 *    not sleep.
 *
 * A SOLO task draws no empty seats. It gets a quiet `⊘ solo · mechanical` marker
 * instead: the missing chip is a structural fact about a task that deliberately
 * chose one agent, and nagging about a chair nobody meant to fill is the wrong
 * signal on the quietest card on the board.
 */

const CHIP_STATE_TITLE: Record<CrewChipState, string> = {
	working: "has the turn",
	asleep: "asleep, waiting its turn",
	done: "finished",
};

const REVIEW_PIP: Record<ReviewGateState, { Icon: LucideIcon; label: string; tone: string }> = {
	approved: { Icon: Check, label: "approved", tone: "var(--lane-merge-bright)" },
	changes: { Icon: Contrast, label: "changes", tone: "var(--lane-needs-bright)" },
	"not run": { Icon: Eye, label: "not run", tone: "var(--fg-passive)" },
};

function CrewChip({ member, onOpen }: { member: WorkspaceSession; onOpen: (member: WorkspaceSession) => void }) {
	const role = member.crew?.role ?? "dev";
	const state = crewChipState(member);
	const { Icon: StatusIcon, filled, lane } = statusGlyph(member);
	// An asleep member draws a Moon in a passive hue: it is not a status, it is
	// the absence of one, and it must not compete with the member that is running.
	const Icon = state === "asleep" ? Moon : state === "done" ? Check : StatusIcon;
	const tone = state === "working" ? lane.dotVar : "var(--fg-passive)";
	const detail = state === "working" ? statusLabel(member).toLowerCase() : CHIP_STATE_TITLE[state];

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					className={cn(
						"inline-flex min-w-0 shrink-0 items-center gap-1 rounded-full border px-1.5 py-px text-[10px] font-medium transition-colors",
						"border-border-strong hover:bg-interactive-hover",
						state !== "working" && "opacity-70",
					)}
					data-crew-chip={role}
					data-crew-chip-state={state}
					onClick={(event) => {
						event.stopPropagation();
						onOpen(member);
					}}
					type="button"
				>
					<Icon
						className="h-[9px] w-[9px] shrink-0"
						style={{ color: tone, ...(state === "working" && filled ? { fill: "currentColor" } : {}) }}
						aria-hidden="true"
					/>
					{/* The label sheds below ~150px of card width rather than truncating
					    to an unreadable stub; the glyph is the status and the tooltip
					    keeps the name. */}
					<span className="truncate @[150px]/crew:inline hidden" style={{ color: tone }}>
						{role}
					</span>
				</button>
			</TooltipTrigger>
			<TooltipContent>
				{role} — {detail}
			</TooltipContent>
		</Tooltip>
	);
}

function ReviewPip({ state, onOpen }: { state: ReviewGateState; onOpen?: () => void }) {
	const { Icon, label, tone } = REVIEW_PIP[state];
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					className="inline-flex min-w-0 shrink-0 items-center gap-1 rounded-full px-1 py-px text-[10px] transition-colors hover:bg-interactive-hover"
					data-crew-gate="review"
					data-crew-gate-state={state}
					onClick={(event) => {
						event.stopPropagation();
						onOpen?.();
					}}
					type="button"
				>
					<Icon className="h-[9px] w-[9px] shrink-0" style={{ color: tone }} aria-hidden="true" />
					<span className="truncate @[150px]/crew:inline hidden" style={{ color: tone }}>
						{label}
					</span>
				</button>
			</TooltipTrigger>
			<TooltipContent>
				Review gate — {label}. Review has no session: each pass is a run that reports a verdict and closes.
			</TooltipContent>
		</Tooltip>
	);
}

/**
 * The solo marker. It says the size out loud, because the size is now the reason
 * there is one agent rather than two - and because the orchestrator that has to
 * choose that tag can then see, on the board, what its choices produced.
 */
function SoloMarker({ size }: { size?: TaskSize }) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex min-w-0 items-center gap-1 text-[10px] text-passive" data-crew-solo={size ?? ""}>
					<Slash className="h-[9px] w-[9px] shrink-0" aria-hidden="true" />
					<span className="truncate">solo{size ? ` · ${size}` : ""}</span>
				</span>
			</TooltipTrigger>
			<TooltipContent>
				{size === "mechanical"
					? "A mechanical task is worked by one agent. That is the tag doing its job, not a missing teammate."
					: "This task has one agent."}
			</TooltipContent>
		</Tooltip>
	);
}

export function CrewStrip({
	task,
	review,
	onOpenMember,
	onOpenReviews,
}: {
	task: Task;
	review: ReviewGateState;
	onOpenMember: (member: WorkspaceSession) => void;
	onOpenReviews?: () => void;
}) {
	return (
		// Its own provider: the strip is rendered inside a card that a test (and a
		// future embedder) may mount on its own, and a tooltip with no provider
		// above it throws rather than degrading. Nesting one is free.
		<TooltipProvider delayDuration={200}>
			<div
				className="@container/crew flex min-w-0 items-center gap-1 px-[13px] py-1.5"
				data-crew-strip={task.isCrew ? "crew" : "solo"}
				style={{ borderTop: "1px solid var(--kanban-card-divider)" }}
				onClick={(event) => event.stopPropagation()}
			>
				{task.isCrew ? (
					task.members.map((member, index) => (
						<span className="flex min-w-0 items-center gap-1" key={member.id}>
							{index > 0 && (
								<span aria-hidden="true" className="shrink-0 text-[10px] text-passive">
									›
								</span>
							)}
							<CrewChip member={member} onOpen={onOpenMember} />
						</span>
					))
				) : (
					<SoloMarker size={task.dev.taskSize} />
				)}
				<span aria-hidden="true" className="mx-0.5 h-2.5 w-px shrink-0 bg-[var(--kanban-card-divider)]" />
				<ReviewPip state={review} onOpen={onOpenReviews} />
			</div>
		</TooltipProvider>
	);
}
