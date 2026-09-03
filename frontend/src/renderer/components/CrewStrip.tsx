import { Check, Contrast, Eye, Moon, Plus, Slash, type LucideIcon } from "lucide-react";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";
import {
	type CrewChipState,
	type ReviewGateState,
	type Task,
	canAttachRole,
	crewChipState,
	crewJoinLine,
	neverStarted,
} from "../lib/crew";
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
 * instead: the missing chip is a structural fact about a task that has not
 * needed a second agent, and nagging about a chair nobody meant to fill is the
 * wrong signal on the quietest card on the board.
 *
 * A crew task additionally carries ONE LINE saying how it became a crew, because
 * it did not start as one: a card can gain its qa while you are looking at it,
 * and gaining one gives the merge gate a real input - which can move the card
 * back a lane. The line is what makes that read as the gate working.
 */

// What a chip's state MEANS, in the words a person would use. There is no
// "waiting its turn" any more: both members work at the same time, so a member
// that is not running is either paused or has simply never been started - and
// only the second of those has a button.
const CHIP_STATE_TITLE: Record<CrewChipState, string> = {
	working: "working",
	asleep: "paused",
	done: "finished",
};

/**
 * What the review gate LOOKS like in each verdict. Exported because the session
 * topbar's member switcher is the same vocabulary one level down and must draw
 * the same pip - one table, so the board and the topbar cannot drift.
 */
export const REVIEW_PIP: Record<ReviewGateState, { Icon: LucideIcon; label: string; tone: string }> = {
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
	const detail =
		state === "working"
			? statusLabel(member).toLowerCase()
			: neverStarted(member)
				? "not started — open it to start"
				: CHIP_STATE_TITLE[state];

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
					{/* The ROLE always shows. It is three characters and it is the whole
					    point of the chip - a strip of three unlabelled glyphs at the
					    board's real column width (~122px of content) says nothing at all.
					    What sheds instead is the gate's word, below. */}
					<span className="truncate" style={{ color: tone }}>
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
					{/* The gate's word is what sheds when the column is narrow: its three
					    states already have three unlike silhouettes (check / half-filled /
					    eye), and the tooltip names the verdict either way - where a chip
					    stripped to a bare dot would say nothing a person could read. */}
					<span className="truncate @[190px]/crew:inline hidden" style={{ color: tone }}>
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
					{/* `mechanical` says both things at once - one agent, and WHY - in one
					    word that fits a 150px column. Anything else solo just says solo:
					    "standard" on a one-agent card would be a puzzle, not a fact. */}
					<span className="truncate">{size === "mechanical" ? "mechanical" : "solo"}</span>
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

/**
 * `+ qa` - the escape hatch, on the card.
 *
 * The crew is decided at spawn, and this is the only way to change that mind
 * afterwards. It sits beside the solo marker exactly as the design asks - "an
 * unobtrusive `+ add a role`, never empty seats" - so it reads as an offer
 * rather than as a chair nobody filled.
 *
 * What it does is worth being plain about: it STARTS a second agent, right now,
 * in the same worktree. There is nothing left for a new member to wait for, and
 * a member created asleep with no control to start it is exactly the dead end
 * this replaced. The agent already working is not interrupted.
 */
function AddRoleButton({ onAdd, pending }: { onAdd: () => void; pending?: boolean }) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					className="inline-flex shrink-0 items-center gap-0.5 rounded-full px-1 py-px text-[10px] text-passive transition-colors hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
					data-crew-add="qa"
					disabled={pending}
					onClick={(event) => {
						event.stopPropagation();
						onAdd();
					}}
					type="button"
				>
					<Plus className="h-[9px] w-[9px] shrink-0" aria-hidden="true" />
					<span className="truncate">qa</span>
				</button>
			</TooltipTrigger>
			<TooltipContent>
				Add a qa to this task. It starts working in the same worktree straight away, beside the agent that is already
				there - nothing that is running now is interrupted. dev asks for one itself when it believes the change is done;
				this is how you add one before that, or on a task where dev may not ask.
			</TooltipContent>
		</Tooltip>
	);
}

export function CrewStrip({
	task,
	review,
	onOpenMember,
	onOpenReviews,
	onAddRole,
	addRolePending,
}: {
	task: Task;
	review: ReviewGateState;
	onOpenMember: (member: WorkspaceSession) => void;
	onOpenReviews?: () => void;
	/** Attach a qa to this task. Omitted where attaching is not offered at all. */
	onAddRole?: () => void;
	addRolePending?: boolean;
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
					<>
						<SoloMarker size={task.dev.taskSize} />
						{onAddRole && canAttachRole(task) && <AddRoleButton onAdd={onAddRole} pending={addRolePending} />}
					</>
				)}
				<span aria-hidden="true" className="mx-0.5 h-2.5 w-px shrink-0 bg-[var(--kanban-card-divider)]" />
				<ReviewPip state={review} onOpen={onOpenReviews} />
			</div>
			<CrewJoinLine task={task} />
		</TooltipProvider>
	);
}

/**
 * The line under the strip, which says one of two things.
 *
 * On a crew: `qa joined · dev asked for a review` - what changed the shape of a
 * card somebody may have been looking at. On a solo task that DROVE THE APP:
 * `dev drove the simulator · no qa was asked for`, which is the human's half of
 * the warning that replaced AO's old habit of adding a qa by itself the moment it
 * saw the app being driven. It sits directly beside the `+ qa` control that
 * answers it.
 *
 * Quiet and passive either way - it is background, not a status - and it renders
 * nothing at all for a task with nothing to explain. It sits BELOW the chips
 * rather than inside them because the strip is a row of live things and this is a
 * fact about the past; and because the card's column is narrow enough that
 * anything added to that row would push a chip off it.
 */
function CrewJoinLine({ task }: { task: Task }) {
	const line = crewJoinLine(task);
	if (!line) return null;
	const joined = task.qa?.crew?.joinReason;
	return (
		<div
			className="truncate px-[13px] pb-1.5 text-[10px] text-passive"
			data-crew-join={joined}
			data-crew-unreviewed={joined ? undefined : task.dev.runtimeTouch}
			onClick={(event) => event.stopPropagation()}
		>
			{line}
		</div>
	);
}
