import { Brain, ChevronRight, Loader2 } from "lucide-react";
import type { ReactNode } from "react";
import { type ChatRow, itemRowKey, type ProcessMember } from "../lib/chat-rows";
import { processPhrases, type ProcessSummary, toolFailed } from "../lib/chat-tools";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";
import { ResolvedLine, ToolCard } from "./ChatToolCard";

export type OpenRows = Readonly<Record<string, boolean>>;
export type ToggleRow = (key: string, open: boolean) => void;

const capitalize = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

const latestLine = (text: string) =>
	text
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line !== "")
		.at(-1) ?? "";

export function ThinkingRow({
	text,
	streaming = false,
	open = false,
	onToggle,
}: {
	text: string;
	streaming?: boolean;
	open?: boolean;
	onToggle: (open: boolean) => void;
}) {
	if (text.trim() === "") return null;
	return (
		<div className="chat-think">
			<button aria-expanded={open} className="chat-think__head" onClick={() => onToggle(!open)} type="button">
				<ChevronRight className={cn("chat-chev", open && "chat-chev--open")} aria-hidden="true" />
				<Brain className="chat-icon" aria-hidden="true" />
				{streaming ? "Thinking…" : "Thought"}
				{streaming && !open ? <span className="chat-think__tail">{latestLine(text)}</span> : null}
			</button>
			{open ? <div className="chat-think__body">{text}</div> : null}
		</div>
	);
}

function Failed({ count }: { count: number }) {
	return count > 0 ? <span className="chat-group__failed">{count} failed</span> : null;
}

function Head({ open, onToggle, children }: { open: boolean; onToggle: (open: boolean) => void; children: ReactNode }) {
	return (
		<button aria-expanded={open} className="chat-group__head" onClick={() => onToggle(!open)} type="button">
			<ChevronRight className={cn("chat-chev", open && "chat-chev--open")} aria-hidden="true" />
			{children}
		</button>
	);
}

type MembersProps = {
	openRows: OpenRows;
	toggleRow: ToggleRow;
	theme: Theme;
	root: string;
};

function Member({ member, openRows, toggleRow, theme, root }: MembersProps & { member: ProcessMember }) {
	if (member.kind === "thinking") {
		const key = itemRowKey(member.itemIndex);
		return <ThinkingRow onToggle={(open) => toggleRow(key, open)} open={openRows[key]} text={member.text} />;
	}
	return (
		<>
			<ToolCard
				call={member.call}
				onToggle={(open) => toggleRow(member.call.id, open)}
				open={openRows[member.call.id]}
				root={root}
				subagentTools={member.subagentTools}
				task={member.task}
				theme={theme}
			/>
			<ResolvedLine call={member.call} />
		</>
	);
}

/**
 * The tool calls between two replies, as one quiet line. A closed group still
 * shows the calls that failed or were denied.
 */
export function GroupRow({ row, ...rest }: MembersProps & { row: Extract<ChatRow, { kind: "group" }> }) {
	const open = rest.openRows[row.key] ?? false;
	const { summary } = row;
	const shown = open ? row.members : row.members.filter((m) => m.kind === "tool" && toolFailed(m.call));
	return (
		<div className="chat-group" data-group>
			<Head onToggle={(next) => rest.toggleRow(row.key, next)} open={open}>
				<span className="chat-group__text">{capitalize(processPhrases(summary).join(" · "))}</span>
				<Failed count={summary.failed} />
				<Live summary={summary} />
			</Head>
			{shown.length > 0 ? (
				<div className="chat-group__body">
					{shown.map((member) => (
						<Member key={itemRowKey(member.itemIndex)} member={member} {...rest} />
					))}
				</div>
			) : null}
		</div>
	);
}

function Live({ summary }: { summary: ProcessSummary }) {
	if (summary.live === undefined) return null;
	return (
		<span className="chat-group__live">
			<Loader2 className="chat-icon animate-spin" aria-hidden="true" />
			<span className="chat-group__live-text" title={summary.live}>
				{summary.live}
			</span>
		</span>
	);
}

const messages = (n: number) => (n === 1 ? "1 message" : `${n} messages`);

/** A finished turn's work, folded between the user's message and the last reply. */
export function TurnRow({
	row,
	openRows,
	toggleRow,
	renderRow,
}: {
	row: Extract<ChatRow, { kind: "turn" }>;
	openRows: OpenRows;
	toggleRow: ToggleRow;
	renderRow: (row: ChatRow) => ReactNode;
}) {
	const open = openRows[row.key] ?? false;
	const parts = ["Worked", ...processPhrases(row.summary), ...(row.messages > 0 ? [messages(row.messages)] : [])];
	return (
		<div className="chat-group chat-turn" data-turn>
			<Head onToggle={(next) => toggleRow(row.key, next)} open={open}>
				<span className="chat-group__text">{parts.join(" · ")}</span>
				<Failed count={row.summary.failed} />
			</Head>
			{open ? (
				<div className="chat-turn__body">
					{row.rows.map((inner) => (
						<div
							className="chat-turn__row"
							data-row={inner.kind === "item" ? inner.item.kind : inner.kind}
							key={inner.key}
						>
							{renderRow(inner)}
						</div>
					))}
				</div>
			) : null}
		</div>
	);
}
