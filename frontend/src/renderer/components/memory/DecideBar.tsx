import { useState } from "react";
import { AlarmClockOff, Check, ChevronDown, Clock, Pencil, RotateCcw, Undo2, X } from "lucide-react";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Textarea } from "../ui/textarea";
import type { Decision, Proposal, Written } from "../../hooks/useMemory";
import { fileName, inDays, isSnoozed } from "./model";

type Side = "keep_rule" | "words_win" | "both";

export interface DecideBarProps {
	p: Proposal;
	/** What an approved proposal wrote, as it is now (from the proposal's detail). */
	written?: Written;
	busy: boolean;
	error?: string;
	onDecide: (d: Decision) => void;
	/** Editing what would be written (pending) or what was written (approved). */
	editing: boolean;
	onEdit?: () => void;
	onCancelEdit?: () => void;
	draft?: string;
	approveLabel?: string;
	resolution?: Side;
}

const bar = "sticky bottom-0 flex flex-col gap-2 border-t border-border bg-background px-8 py-3";
const red = { color: "var(--red)", borderColor: "color-mix(in srgb, var(--red) 40%, transparent)" };

function when(iso?: string | null): string {
	return iso ? new Date(iso).toLocaleString() : "";
}

function ErrorLine({ error }: { error?: string }) {
	if (!error) return null;
	return (
		<p className="text-[12px]" style={{ color: "var(--red)" }} role="alert">
			{error}
		</p>
	);
}

/**
 * The sticky bar under a proposal. Every state has a way forward: a waiting or
 * snoozed proposal can be approved, edited, snoozed or rejected (a snoozed one
 * also unsnoozed), a rejected one reopened, and an approved one's file edited or
 * the whole decision undone.
 */
export function DecideBar(props: DecideBarProps) {
	const { p } = props;
	if (p.status === "pending") return <PendingBar {...props} />;
	if (p.status === "rejected" && p.action !== "conflict") return <RejectedBar {...props} />;
	if (p.status === "applied" || (p.status === "rejected" && p.action === "conflict")) return <DecidedBar {...props} />;
	return (
		<div className={bar}>
			<p className="text-[12px] text-muted-foreground">
				{p.status === "stale"
					? "The file changed after this was proposed; it will be proposed again against the file as it is now."
					: "Nothing to decide here."}
			</p>
		</div>
	);
}

function PendingBar({ p, busy, error, onDecide, editing, onEdit, draft, approveLabel = "Approve", resolution }: DecideBarProps) {
	const [rejecting, setRejecting] = useState(false);
	const [reason, setReason] = useState("");
	const snoozed = isSnoozed(p);
	return (
		<div className={bar}>
			<ErrorLine error={error} />
			{snoozed && !rejecting && (
				<p className="text-[12px] text-muted-foreground">
					Snoozed until {new Date(p.snoozedUntil ?? 0).toLocaleString()}. Decide it now, or bring it back to To
					decide.
				</p>
			)}
			{rejecting && (
				<Textarea
					autoFocus
					value={reason}
					onChange={(e) => setReason(e.target.value)}
					rows={2}
					placeholder="Why not? (optional - learning reads this, so it does not propose the same thing again)"
					className="text-[12.5px]"
				/>
			)}
			<div className="flex items-center gap-2">
				{rejecting ? (
					<>
						<Button
							size="sm"
							variant="outline"
							disabled={busy}
							onClick={() => onDecide({ kind: "reject", reason })}
							style={red}
						>
							<X className="size-3.5" /> Reject
						</Button>
						<Button size="sm" variant="ghost" onClick={() => setRejecting(false)}>
							Cancel
						</Button>
					</>
				) : (
					<>
						<Button
							size="sm"
							disabled={busy}
							onClick={() => onDecide({ kind: "approve", content: editing ? draft : undefined, resolution })}
						>
							<Check className="size-3.5" /> {editing && !resolution ? "Approve my edit" : approveLabel}
						</Button>
						{onEdit && !editing && (
							<Button size="sm" variant="outline" disabled={busy} onClick={onEdit}>
								<Pencil className="size-3.5" /> Edit first
							</Button>
						)}
						{snoozed && (
							<Button size="sm" variant="ghost" disabled={busy} onClick={() => onDecide({ kind: "unsnooze" })}>
								<AlarmClockOff className="size-3.5" /> Unsnooze
							</Button>
						)}
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<Button size="sm" variant="ghost" disabled={busy}>
									<Clock className="size-3.5" /> {snoozed ? "Snooze again" : "Snooze"}{" "}
									<ChevronDown className="size-3" />
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent align="start">
								<DropdownMenuItem onSelect={() => onDecide({ kind: "snooze", until: inDays(1) })}>
									Until tomorrow
								</DropdownMenuItem>
								<DropdownMenuItem onSelect={() => onDecide({ kind: "snooze", until: inDays(7) })}>
									For a week
								</DropdownMenuItem>
								<DropdownMenuItem onSelect={() => onDecide({ kind: "snooze", until: inDays(90) })}>
									Until it is taught again (90 days at most)
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
						<div className="flex-1" />
						<Button size="sm" variant="ghost" disabled={busy} onClick={() => setRejecting(true)}>
							<X className="size-3.5" /> Reject
						</Button>
					</>
				)}
			</div>
		</div>
	);
}

function RejectedBar({ p, busy, error, onDecide }: DecideBarProps) {
	return (
		<div className={bar}>
			<ErrorLine error={error} />
			<div className="flex items-center gap-3">
				<p className="min-w-0 flex-1 text-[12px] text-muted-foreground">
					Rejected {when(p.decidedAt)}: “{p.rejectReason}”. Learning will not propose it again without new evidence.
				</p>
				<Button size="sm" variant="outline" disabled={busy} onClick={() => onDecide({ kind: "reopen" })}>
					<RotateCcw className="size-3.5" /> Reopen
				</Button>
			</div>
		</div>
	);
}

/** An approved proposal, or a conflict whose side was chosen: edit what it wrote, or undo it. */
function DecidedBar({ p, written, busy, error, onDecide, editing, onEdit, onCancelEdit, draft }: DecideBarProps) {
	const [undoing, setUndoing] = useState(false);
	const conflict = p.action === "conflict";
	const file = fileName(p.targetPath);
	if (editing) {
		return (
			<div className={bar}>
				<ErrorLine error={error} />
				<div className="flex items-center gap-2">
					<Button
						size="sm"
						disabled={busy || draft === undefined}
						onClick={() => onDecide({ kind: "edit", content: draft ?? "", confirmToken: written?.token })}
					>
						<Check className="size-3.5" /> Save my edit
					</Button>
					<Button size="sm" variant="ghost" disabled={busy} onClick={onCancelEdit}>
						Cancel
					</Button>
				</div>
			</div>
		);
	}
	const changed = written?.changed === true;
	return (
		<div className={bar}>
			<ErrorLine error={error} />
			{undoing ? (
				<>
					<p className="text-[12px] leading-[1.5] text-foreground">{undoConsequence(p, written)}</p>
					{changed && (
						<p className="text-[12px] leading-[1.5]" style={{ color: "var(--amber)" }}>
							{written?.exists === false
								? `${file} is already gone; undo takes back the rest.`
								: conflict
									? "Your pinned rule changed since (the change is shown above). Undoing replaces that change too."
									: `${file} changed since AO wrote it (the change is shown above). Undoing ${
											p.action === "create_memory" ? "removes" : "replaces"
										} that change too; a backup is kept.`}
						</p>
					)}
					<div className="flex items-center gap-2">
						<Button
							size="sm"
							variant="outline"
							disabled={busy}
							style={changed ? red : undefined}
							onClick={() => onDecide({ kind: "undo", confirmToken: changed ? written?.token : undefined })}
						>
							<Undo2 className="size-3.5" /> {changed ? "Undo anyway" : "Undo"}
						</Button>
						<Button size="sm" variant="ghost" disabled={busy} onClick={() => setUndoing(false)}>
							Cancel
						</Button>
					</div>
				</>
			) : (
				<div className="flex items-center gap-2">
					<p className="min-w-0 flex-1 text-[12px] text-muted-foreground">{decidedLine(p)}</p>
					{onEdit && !conflict && (
						<Button size="sm" variant="outline" disabled={busy || !written} onClick={onEdit}>
							<Pencil className="size-3.5" /> Edit {p.action === "update_skill" ? "skill" : p.action === "edit_rule_file" ? "file" : "memory"}
						</Button>
					)}
					<Button size="sm" variant="ghost" disabled={busy} onClick={() => setUndoing(true)}>
						<Undo2 className="size-3.5" /> Undo
					</Button>
				</div>
			)}
		</div>
	);
}

function decidedLine(p: Proposal): string {
	if (p.action !== "conflict") return `Written ${when(p.decidedAt)}.`;
	const side =
		p.resolution === "keep_rule" ? "kept the rule" : p.resolution === "words_win" ? "your newer words win" : "both, scoped";
	return `Decided ${when(p.decidedAt)}: ${side}.`;
}

/** What undo will do, said before it is done. */
function undoConsequence(p: Proposal, written?: Written): string {
	const file = fileName(p.targetPath);
	if (p.action === "conflict") {
		return p.resolution === "words_win" && p.targetPath.startsWith("rule:protected-")
			? "Puts your pinned rule's earlier text back, and this card back in To decide."
			: "Nothing was written. This card goes back to To decide, to choose again.";
	}
	if (p.action === "create_memory") {
		return `Removes ${file}${written?.indexLine ? " and the line it added to MEMORY.md" : ""}, and puts this proposal back in To decide.`;
	}
	return `Puts back ${file} as it was before you approved, and this proposal back in To decide.`;
}
