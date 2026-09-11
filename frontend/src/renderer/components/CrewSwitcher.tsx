import { Check, Moon, Plus, RotateCcw, Smartphone } from "lucide-react";
import { REVIEW_PIP } from "./CrewStrip";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";
import {
	type ReviewGateState,
	type Task,
	canAttachRole,
	crewChipState,
	neverStarted,
	qaToCheckAgain,
} from "../lib/crew";
import { statusGlyph, statusLabel } from "../lib/status-glyph";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";

/**
 * The MEMBER SWITCHER: who is on this task, which of them you are looking at,
 * and the one control that adds the other one.
 *
 * ## Why it lives in the topbar
 *
 * The obvious slot is the terminal toolbar, beside Split. It is wrong, and the
 * code says why: `CenterPane` draws its toolbar ONCE PER PANE — in the split
 * view that toolbar *becomes* the pane's header. A task-level control put there
 * is drawn once per pane, so the moment you use the very split view this design
 * keeps, you get two member switchers on screen, each claiming to switch the
 * thing that contains it. `ShellTopbar` is rendered exactly once, by the shell,
 * for the whole session route. That is the right cardinality, and the crew IS
 * the identity of the task, which is what that slot already holds.
 *
 * ## What it draws, and what it deliberately does not
 *
 * It is the board's {@link CrewStrip} vocabulary one level down:
 *
 * - **chips** for the agents (`dev` `qa`), bordered, each with that member's own
 *   status glyph. The one you are looking at is the accented one. Clicking the
 *   other swaps the TERMINAL — the inspector rail does not move, because after
 *   the daemon's task-scoping four of its six tabs answer for the task and are
 *   identical either way.
 * - **a pip** for review, after a divider: borderless, quieter, never a chip.
 *   Review has no session and nobody to talk to; putting a gate in a strip of
 *   teammates is the one mistake `CrewStrip` was written to avoid.
 *
 * ## Solo pays for one thing only
 *
 * A task with one agent gets NO chips, NO divider and NO pip — just the `+ qa`
 * affordance, and only where the daemon would actually accept one
 * ({@link canAttachRole}). Solo and `mechanical` tasks are the overwhelming
 * majority of real traffic, so the topbar they see is today's topbar plus one
 * quiet ~46px button. A task that cannot gain a member draws nothing at all.
 *
 * ## One slot, two questions
 *
 * That slot holds `↻ check again` once the task HAS a qa and that qa has
 * finished ({@link qaToCheckAgain}). It is the same button asking the task's
 * next question: a task with no qa is offered one, a task whose qa closed a
 * round is offered another. They are mutually exclusive by construction, so the
 * bar never grows a second control, and neither creates a member — one task, one
 * qa, and `check again` is that same session coming back into the same worktree.
 *
 * It exists because the finished state had no task-level control AT ALL: `+ qa`
 * is hidden precisely when a qa is on the task, and the only way back was to
 * open the finished member's own terminal and find a Restore button written in
 * the language of session recovery. Every reason a second round is asked for —
 * cases written after the round closed, a simulator erased, a case that expected
 * the wrong thing — is a reason to look at code that has NOT changed, so nothing
 * here is keyed to a commit.
 */
export function CrewSwitcher({
	task,
	activeSessionId,
	review,
	deviceHolders,
	showDevicePip,
	onOpenMember,
	onOpenReviews,
	onAddRole,
	addRolePending,
	onCheckAgain,
	checkAgainPending,
	autoCrewDisabled,
	style,
}: {
	task: Task;
	/** The member the route is on — the accented chip. */
	activeSessionId: string;
	review: ReviewGateState;
	/** Session ids currently holding a simulator lease. */
	deviceHolders?: ReadonlySet<string>;
	/**
	 * Whether the device pip's slot exists at all. It is reserved on every chip
	 * of a project that targets iOS, held or not, so a lease changing hands can
	 * never make the strip shift; a project with no simulator reserves nothing,
	 * because there is no lease that could ever land there.
	 */
	showDevicePip?: boolean;
	onOpenMember: (member: WorkspaceSession) => void;
	onOpenReviews?: () => void;
	onAddRole?: () => void;
	addRolePending?: boolean;
	/** Ask the qa this task already has for another round. See {@link qaToCheckAgain}. */
	onCheckAgain?: () => void;
	checkAgainPending?: boolean;
	/**
	 * Whether this project has AUTOMATIC crew formation turned off
	 * (ProjectConfig.disableAutoCrew). It changes nothing about what the button
	 * DOES — adding a qa by hand is exactly what such a project keeps — only what
	 * the button PROMISES: the default copy says dev asks for its own qa when it
	 * thinks the change is done, and on a crew-off project dev is refused, so
	 * adding one is always the human's.
	 */
	autoCrewDisabled?: boolean;
	/**
	 * Applied to the root. The topbar is a macOS window DRAG region, so
	 * everything clickable inside it has to opt out of dragging or the click
	 * never reaches the button.
	 */
	style?: React.CSSProperties;
}) {
	const canAdd = Boolean(onAddRole) && canAttachRole(task);
	const canCheckAgain = Boolean(onCheckAgain) && Boolean(qaToCheckAgain(task));
	if (!task.isCrew && !canAdd) return null;
	return (
		<TooltipProvider delayDuration={200}>
			<div
				className="flex min-w-0 shrink-0 items-center gap-1"
				data-crew-switcher={task.isCrew ? "crew" : "solo"}
				style={style}
			>
				{task.isCrew ? (
					<>
						{task.members.map((member) => (
							<MemberChip
								active={member.id === activeSessionId}
								holdsDevice={deviceHolders?.has(member.id) ?? false}
								key={member.id}
								member={member}
								onOpen={onOpenMember}
								showDevicePip={showDevicePip}
							/>
						))}
						{/* BEFORE the divider, with the chips: it acts on a member, not on
						    the review gate, and the divider is what separates those two
						    kinds of thing. */}
						{canCheckAgain ? <CheckAgainButton onCheckAgain={onCheckAgain!} pending={checkAgainPending} /> : null}
						<span aria-hidden="true" className="mx-0.5 h-3 w-px shrink-0 bg-border-strong" />
						<ReviewPip onOpen={onOpenReviews} state={review} />
					</>
				) : (
					<AddRoleButton autoCrewDisabled={autoCrewDisabled} onAdd={onAddRole!} pending={addRolePending} />
				)}
			</div>
		</TooltipProvider>
	);
}

// What a chip's state MEANS, in the words a person would use — the same three
// the board uses, because they are the same three states.
const CHIP_STATE_TITLE = { working: "working", asleep: "paused", done: "finished" } as const;

function MemberChip({
	member,
	active,
	holdsDevice,
	showDevicePip,
	onOpen,
}: {
	member: WorkspaceSession;
	active: boolean;
	holdsDevice: boolean;
	showDevicePip?: boolean;
	onOpen: (member: WorkspaceSession) => void;
}) {
	const role = member.crew?.role ?? "dev";
	const state = crewChipState(member);
	const { Icon: StatusIcon, filled, lane } = statusGlyph(member);
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
					aria-current={active ? "page" : undefined}
					className={cn(
						"inline-flex min-w-0 shrink-0 items-center gap-1 rounded-full border px-2 py-[3px] text-[11px] font-medium leading-none transition-colors",
						active
							? "border-[var(--accent)] bg-[color-mix(in_srgb,var(--accent)_12%,transparent)] text-foreground"
							: "border-border-strong text-muted-foreground opacity-80 hover:bg-interactive-hover hover:opacity-100",
					)}
					data-crew-switcher-chip={role}
					data-crew-switcher-chip-active={active ? "true" : "false"}
					data-crew-switcher-chip-device={holdsDevice ? "held" : "free"}
					onClick={() => onOpen(member)}
					type="button"
				>
					<Icon
						aria-hidden="true"
						className="h-[10px] w-[10px] shrink-0"
						style={{ color: tone, ...(state === "working" && filled ? { fill: "currentColor" } : {}) }}
					/>
					<span className="truncate">{role}</span>
					{/* The device pip, and the reason it is drawn even when there is no
					    lease: two agents can hold two simulators and the lease moves
					    between them mid-task, so a pip that appeared and vanished would
					    re-measure the chip — and the whole strip behind it — every time a
					    device changed hands. The slot is reserved and only its INK
					    changes. `invisible` keeps the box; `hidden` would not. */}
					{showDevicePip ? (
						<Smartphone
							aria-hidden="true"
							className={cn("h-[10px] w-[10px] shrink-0", !holdsDevice && "invisible")}
							data-device-pip={holdsDevice ? "held" : "free"}
							style={{ color: "var(--accent)" }}
						/>
					) : null}
				</button>
			</TooltipTrigger>
			<TooltipContent>
				{role} — {detail}
				{holdsDevice ? " · holding a simulator" : ""}
			</TooltipContent>
		</Tooltip>
	);
}

/**
 * The review gate, drawn exactly as the board draws it — same table, same three
 * verdicts — but with its word always shown. The board's copy sheds the label in
 * a 150px card column; the topbar has room, and a bare glyph beside two labelled
 * chips would read as a third teammate whose name did not fit.
 */
function ReviewPip({ state, onOpen }: { state: ReviewGateState; onOpen?: () => void }) {
	const { Icon, label, tone } = REVIEW_PIP[state];
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					className="inline-flex min-w-0 shrink-0 items-center gap-1 rounded-full px-1.5 py-[3px] text-[11px] leading-none transition-colors hover:bg-interactive-hover"
					data-crew-switcher-gate="review"
					data-crew-switcher-gate-state={state}
					onClick={() => onOpen?.()}
					type="button"
				>
					<Icon aria-hidden="true" className="h-[10px] w-[10px] shrink-0" style={{ color: tone }} />
					<span className="truncate" style={{ color: tone }}>
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
 * `↻ check again` — the finished-qa half of the `+ qa` slot.
 *
 * It is drawn exactly like {@link AddRoleButton} and for the same reason: it is
 * the same weight of decision, asked of the same task, in the same place. A
 * heavier control would claim this is a bigger act than adding a qa was, and it
 * is a smaller one — no member is created, the task's existing qa comes back
 * into the worktree it already worked in.
 */
function CheckAgainButton({ onCheckAgain, pending }: { onCheckAgain: () => void; pending?: boolean }) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					className="inline-flex shrink-0 items-center gap-0.5 rounded-full px-1.5 py-[3px] text-[11px] leading-none text-passive transition-colors hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
					data-crew-switcher-check-again="qa"
					disabled={pending}
					onClick={onCheckAgain}
					type="button"
				>
					<RotateCcw aria-hidden="true" className="h-[10px] w-[10px] shrink-0" />
					<span className="truncate">check again</span>
				</button>
			</TooltipTrigger>
			{/* Capped for the same reason AddRoleButton's is: the shared popper sets
			    no width, so an uncapped tooltip lays itself out as one line across the
			    whole window and reads as a banner. */}
			<TooltipContent className="max-w-[320px]">
				Ask this task's qa to look again. It is the same agent coming back into the same worktree, keeping its seat -
				this task still has one qa. A second round needs no new commit: cases written since it finished, a device that
				was reset, or a case that turned out to expect the wrong thing are all reasons to re-check code that has not
				moved.
			</TooltipContent>
		</Tooltip>
	);
}

/**
 * `+ qa`, in the switcher — because the switcher IS this view's crew strip.
 *
 * The button is the SAME button on a project with automatic crew turned off: it
 * is enabled, it does the same thing, and it is deliberately not marked up in
 * the bar itself. Only the sentence changes. A badge or a dot here would cost
 * every project's topbar width in a strip that already collapses its labels to
 * icons below 553px, to answer a question that is only ever asked at this
 * button — so the answer lives where the question is asked.
 */
function AddRoleButton({
	onAdd,
	pending,
	autoCrewDisabled,
}: {
	onAdd: () => void;
	pending?: boolean;
	autoCrewDisabled?: boolean;
}) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					className="inline-flex shrink-0 items-center gap-0.5 rounded-full px-1.5 py-[3px] text-[11px] leading-none text-passive transition-colors hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
					data-crew-switcher-add="qa"
					data-crew-switcher-add-auto={autoCrewDisabled ? "off" : "on"}
					disabled={pending}
					onClick={onAdd}
					type="button"
				>
					<Plus aria-hidden="true" className="h-[10px] w-[10px] shrink-0" />
					<span className="truncate">qa</span>
				</button>
			</TooltipTrigger>
			{/* Capped, because this is the longest tooltip in the bar and the shared
			    popper sets no width: uncapped it lays itself out as one line across
			    the whole window, which reads as a banner rather than a hint. */}
			<TooltipContent className="max-w-[320px]">
				Add a qa to this task. It starts working in the same worktree straight away, beside the agent that is already
				there - nothing that is running now is interrupted.{" "}
				{autoCrewDisabled
					? "No agent may ask for one here: automatic crew is off in this project's settings, so adding one is always yours to do."
					: "dev asks for one itself when it believes the change is done; this is how you add one before that."}
			</TooltipContent>
		</Tooltip>
	);
}
