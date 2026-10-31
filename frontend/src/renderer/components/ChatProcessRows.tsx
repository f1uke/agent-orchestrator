import { Brain, ChevronRight } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { type ChatRow, itemRowKey, type ProcessMember } from "../lib/chat-rows";
import { formatDuration } from "../lib/chat-time";
import {
	type LiveTool,
	liveVerb,
	processSentence,
	type ProcessSummary,
	shortenPaths,
	toolFailed,
	toolRunning,
	waitingVerb,
} from "../lib/chat-tools";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";
import { ResolvedLine, ToolCard } from "./ChatToolCard";

export type OpenRows = Readonly<Record<string, boolean>>;
export type ToggleRow = (key: string, open: boolean) => void;

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

function useNow(everyMs: number): number {
	const [now, setNow] = useState(Date.now);
	useEffect(() => {
		const timer = setInterval(() => setNow(Date.now()), everyMs);
		return () => clearInterval(timer);
	}, [everyMs]);
	return now;
}

export function Elapsed({ since }: { since: string }) {
	const now = useNow(1000);
	const start = Date.parse(since);
	if (Number.isNaN(start)) return null;
	return <span className="chat-live__time"> {formatDuration(now - start)}</span>;
}

export function LiveDots() {
	return (
		<span aria-hidden="true" className="chat-dots">
			<i />
			<i />
			<i />
		</span>
	);
}

function Head({
	open,
	onToggle,
	title,
	children,
}: {
	open: boolean;
	onToggle: (open: boolean) => void;
	title?: string;
	children: ReactNode;
}) {
	return (
		<button
			aria-expanded={open}
			className="chat-group__head"
			onClick={() => onToggle(!open)}
			title={title}
			type="button"
		>
			{children}
			<ChevronRight className={cn("chat-chev", open && "chat-chev--open")} aria-hidden="true" />
		</button>
	);
}

function LiveHead({ live }: { live: LiveTool }) {
	return (
		<span className="chat-live">
			<LiveDots />
			<span className="chat-live__verb">{live.verb}</span>
			<Elapsed since={live.since} />
		</span>
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

export function GroupRow({ row, ...rest }: MembersProps & { row: Extract<ChatRow, { kind: "group" }> }) {
	const open = rest.openRows[row.key] ?? false;
	const { summary } = row;
	const shown = open ? row.members : row.members.filter((m) => m.kind === "tool" && toolFailed(m.call, m.task));
	const running = summary.live ? row.members.filter((m) => m.kind === "tool" && toolRunning(m.call)).at(-1) : undefined;
	return (
		<div className="chat-group" data-group data-live={summary.live ? "" : undefined}>
			<Head onToggle={(next) => rest.toggleRow(row.key, next)} open={open}>
				{summary.live ? (
					<LiveHead live={summary.live} />
				) : (
					<span className="chat-group__text">
						{processSentence(summary)}
						{summary.failed > 0 ? <span className="chat-group__failed"> ({summary.failed} failed)</span> : null}
					</span>
				)}
			</Head>
			{shown.length > 0 ? (
				<div className="chat-group__body">
					{shown.map((member) =>
						member === running && member.kind === "tool" ? (
							<div className="chat-group__running" key={itemRowKey(member.itemIndex)}>
								{liveVerb(member.call)}
							</div>
						) : (
							<Member key={itemRowKey(member.itemIndex)} member={member} {...rest} />
						),
					)}
				</div>
			) : null}
			{running?.kind === "tool" ? <ResolvedLine call={running.call} /> : null}
		</div>
	);
}

export function WaitingRow({
	row,
	openRows,
	toggleRow,
	root,
}: Omit<MembersProps, "theme"> & { row: Extract<ChatRow, { kind: "waiting" }> }) {
	const open = openRows[row.key] ?? false;
	const since = row.tasks
		.map((t) => t.since)
		.filter((ts) => ts !== "")
		.sort()[0];
	return (
		<div className="chat-group" data-live="" data-waiting>
			<Head onToggle={(next) => toggleRow(row.key, next)} open={open}>
				<span className="chat-live">
					<LiveDots />
					<span className="chat-live__verb">{waitingVerb(row.tasks.map((t) => t.task))}</span>
					{since ? <Elapsed since={since} /> : null}
				</span>
			</Head>
			{open ? (
				<div className="chat-group__body">
					{row.tasks.map(({ task, command }) => (
						<div className="chat-waiting" key={task.id}>
							<div className="chat-waiting__title">{task.description || task.id}</div>
							{command ? <pre className="chat-term">$ {shortenPaths(command, root)}</pre> : null}
						</div>
					))}
				</div>
			) : null}
		</div>
	);
}

const messages = (n: number) => (n === 1 ? "1 message" : `${n} messages`);

function turnDetail(summary: ProcessSummary, replies: number): string {
	const sentence = processSentence(summary);
	return replies > 0 ? `${sentence}, ${messages(replies)}` : sentence;
}

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
	return (
		<div className="chat-group chat-turn" data-turn>
			<Head onToggle={(next) => toggleRow(row.key, next)} open={open} title={turnDetail(row.summary, row.messages)}>
				<span className="chat-group__text">
					{row.durationMs === undefined ? "Worked" : `Worked for ${formatDuration(row.durationMs)}`}
					{row.summary.failed > 0 ? <span className="chat-group__failed"> · {row.summary.failed} failed</span> : null}
				</span>
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
