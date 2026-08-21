import { ArrowDownToLine, ArrowRightToLine, SquareSplitHorizontal, X } from "lucide-react";
import { useState } from "react";
import type { WorkspaceSession } from "../types/workspace";
import { MAX_SPLIT_PANES, type SplitDirection } from "../lib/split-layout";
import { cn } from "../lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { SessionGlyph } from "./SessionGlyph";

type SplitSessionPickerProps = {
	/** Live sessions that may fill the new pane (lib/split-layout eligibleSplitSessions). */
	eligible: readonly WorkspaceSession[];
	/**
	 * Members of the task this pane belongs to. They are offered FIRST, under
	 * their own heading: "show me both members of this task" is the one split a
	 * crew actually asks for, and the switcher above deliberately cannot do it.
	 * Empty for a solo task, which then sees exactly the list it sees today.
	 */
	taskMemberIds?: ReadonlySet<string>;
	/** The layout is at MAX_SPLIT_PANES: offer no split, say why instead. */
	atCap: boolean;
	/** Fill a new pane beside/below this picker's pane with the chosen session. */
	onSplit: (direction: SplitDirection, sessionId: string) => void;
	/** Collapse the split to this pane only (multi view only). Sessions keep running. */
	onUnsplit?: () => void;
};

/**
 * The "Split" flow, one popover: direction first, then which running session
 * fills the new pane. Only this project's live sessions are offered, minus the
 * ones already on screen — a session can never appear in two panes, so it is
 * never offered again (spawning new workers happens elsewhere, never here).
 */
export function SplitSessionPicker({ eligible, atCap, onSplit, onUnsplit, taskMemberIds }: SplitSessionPickerProps) {
	const [direction, setDirection] = useState<SplitDirection>("right");
	const [open, setOpen] = useState(false);
	const crewmates = taskMemberIds ? eligible.filter((session) => taskMemberIds.has(session.id)) : [];
	const others = taskMemberIds ? eligible.filter((session) => !taskMemberIds.has(session.id)) : eligible;
	const choose = (sessionId: string) => {
		setOpen(false);
		onSplit(direction, sessionId);
	};
	return (
		<Popover onOpenChange={setOpen} open={open}>
			<PopoverTrigger asChild>
				<button
					aria-label="Split — watch another running session"
					className="terminal-toolbar__control terminal-toolbar__control--icon"
					title="Split — watch another running session"
					type="button"
				>
					<SquareSplitHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
				</button>
			</PopoverTrigger>
			<PopoverContent align="end" className="w-72 p-2">
				{atCap ? (
					<div className="px-2 py-3 text-[12px] leading-relaxed text-muted-foreground">
						Split view is full ({MAX_SPLIT_PANES} panes). Remove a pane to add another session — opening a session from
						the sidebar will replace the focused pane.
					</div>
				) : (
					<>
						<div className="flex gap-1 border-b border-border pb-2">
							{(
								[
									["right", "Split right", ArrowRightToLine],
									["down", "Split down", ArrowDownToLine],
								] as const
							).map(([key, label, Icon]) => (
								<button
									key={key}
									type="button"
									aria-pressed={direction === key}
									onClick={() => setDirection(key)}
									className={cn(
										"flex flex-1 items-center justify-center gap-1.5 rounded-md border px-2 py-1.5 text-[11px] font-medium transition",
										direction === key
											? "border-[var(--accent)] bg-[color-mix(in_srgb,var(--accent)_12%,transparent)] text-foreground"
											: "border-border text-muted-foreground hover:bg-interactive-hover",
									)}
								>
									<Icon aria-hidden="true" className="h-3.5 w-3.5" />
									{label}
								</button>
							))}
						</div>
						{crewmates.length > 0 ? (
							<>
								<GroupHeading>This task</GroupHeading>
								{crewmates.map((session) => (
									<SessionChoice key={session.id} onChoose={choose} session={session} />
								))}
							</>
						) : null}
						<GroupHeading>Running sessions in this project</GroupHeading>
						{others.length === 0 ? (
							<div className="px-2 py-3 text-[12px] text-muted-foreground">
								No other running sessions in this project.
							</div>
						) : (
							others.map((session) => <SessionChoice key={session.id} onChoose={choose} session={session} />)
						)}
					</>
				)}
				{onUnsplit ? (
					<div className="mt-1 border-t border-border pt-1">
						<button
							type="button"
							onClick={() => {
								setOpen(false);
								onUnsplit();
							}}
							className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-[12px] text-foreground transition hover:bg-interactive-hover"
						>
							<X aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />
							Unsplit — keep only this pane (sessions keep running)
						</button>
					</div>
				) : null}
			</PopoverContent>
		</Popover>
	);
}

function GroupHeading({ children }: { children: React.ReactNode }) {
	return (
		<div className="px-2 pb-1 pt-2 font-mono text-[10px] font-medium uppercase tracking-[0.08em] text-muted-foreground">
			{children}
		</div>
	);
}

function SessionChoice({ session, onChoose }: { session: WorkspaceSession; onChoose: (sessionId: string) => void }) {
	const role = session.crew?.role;
	return (
		<button
			className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-[12px] text-foreground transition hover:bg-interactive-hover"
			onClick={() => onChoose(session.id)}
			type="button"
		>
			<SessionGlyph session={session} />
			<span className="min-w-0 flex-1">
				<span className="block truncate">{session.kind === "orchestrator" ? "Orchestrator" : session.title}</span>
				{session.branch ? (
					<span className="block truncate font-mono text-[10px] text-muted-foreground">{session.branch}</span>
				) : null}
			</span>
			{/* The role, only where there is one. In the "This task" group both rows
			    carry the same title and the same branch - because they are the same
			    task - so the role is the only thing telling them apart. */}
			{role ? <span className="shrink-0 font-mono text-[10px] text-passive">{role}</span> : null}
		</button>
	);
}
