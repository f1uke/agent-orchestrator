import { Brain, ChevronRight } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { unfinished } from "../lib/chat-event";
import type { ChatRow, ProcessMember } from "../lib/chat-rows";
import { formatDuration } from "../lib/chat-time";
import {
	isAgentCall,
	isAgentTask,
	latestLine,
	type LiveStep,
	liveStep,
	processSentence,
	type ProcessSummary,
	shortenPaths,
	toolRunning,
	waitingVerb,
} from "../lib/chat-tools";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";
import { useOpenTasks } from "./ChatTasks";
import { TaskOutputView } from "./ChatTaskOutput";
import { ResolvedLine, ToolCard } from "./ChatToolCard";

export type OpenRows = Readonly<Record<string, boolean>>;
export type ToggleRow = (key: string, open: boolean) => void;

export function ThinkingRow({
	text,
	open = false,
	onToggle,
}: {
	text: string;
	open?: boolean;
	onToggle: (open: boolean) => void;
}) {
	if (text.trim() === "") return null;
	return (
		<div className="chat-think">
			<button aria-expanded={open} className="chat-think__head" onClick={() => onToggle(!open)} type="button">
				<ChevronRight className={cn("chat-chev", open && "chat-chev--open")} aria-hidden="true" />
				<Brain className="chat-icon" aria-hidden="true" />
				Thought
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

function LiveRow({
	verb,
	since,
	detail,
	open,
	onToggle,
}: {
	verb: string;
	since?: string;
	detail?: string;
	open: boolean;
	onToggle: (open: boolean) => void;
}) {
	return (
		<button
			aria-expanded={open}
			className="chat-group__head chat-live-row"
			onClick={() => onToggle(!open)}
			type="button"
		>
			<span className="chat-live">
				<LiveDots />
				<span className="chat-live__verb">{verb}</span>
				{detail ? <span className="chat-live__detail">{detail}</span> : null}
				{since ? <Elapsed since={since} /> : null}
			</span>
			<ChevronRight className={cn("chat-chev", open && "chat-chev--open")} aria-hidden="true" />
		</button>
	);
}

function LiveLine({ step }: { step: LiveStep }) {
	return (
		<div className="chat-group__running">
			<span className="chat-live__verb">{step.verb}</span>
			{step.detail ? <span className="chat-live__detail">{step.detail}</span> : null}
		</div>
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

type MembersProps = {
	openRows: OpenRows;
	toggleRow: ToggleRow;
	theme: Theme;
	root: string;
};

function Member({ member, openRows, toggleRow, theme, root }: MembersProps & { member: ProcessMember }) {
	if (member.kind === "thinking") {
		const key = member.key;
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
	const running = summary.live ? row.members.filter((m) => m.kind === "tool" && toolRunning(m.call)).at(-1) : undefined;
	const runningAgent = running?.kind === "tool" && isAgentCall(running.call) ? running : undefined;
	const shown = open ? row.members : row.members.filter((m) => m === runningAgent);
	const thinking = summary.live && !running ? summary.live : undefined;
	return (
		<div
			className="chat-group"
			data-group
			data-live={summary.live ? "" : undefined}
			data-thinking={thinking ? "" : undefined}
		>
			{summary.live ? (
				<LiveRow
					detail={summary.live.detail}
					onToggle={(next) => rest.toggleRow(row.key, next)}
					open={open}
					since={summary.live.since}
					verb={summary.live.verb}
				/>
			) : (
				<Head onToggle={(next) => rest.toggleRow(row.key, next)} open={open}>
					<span className="chat-group__text">{processSentence(summary)}</span>
				</Head>
			)}
			{shown.length > 0 || (open && thinking) ? (
				<div className="chat-group__body">
					{shown.map((member) =>
						member === running && member.kind === "tool" && member !== runningAgent ? (
							<LiveLine key={member.key} step={liveStep(member.call)} />
						) : (
							<Member key={member.key} member={member} {...rest} />
						),
					)}
					{open && thinking ? <LiveLine step={thinking} /> : null}
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
	const openTasks = useOpenTasks();
	const open = !openTasks && (openRows[row.key] ?? false);
	const since = row.tasks
		.map((t) => t.since)
		.filter((ts) => ts !== "")
		.sort()[0];
	return (
		<div className="chat-group" data-live="" data-waiting>
			<LiveRow
				onToggle={(next) => (openTasks ? openTasks(null) : toggleRow(row.key, next))}
				open={open}
				since={since}
				verb={waitingVerb(row.tasks.map((t) => t.task))}
			/>
			{open ? (
				<div className="chat-group__body">
					{row.tasks.map(({ task, command }) => (
						<div className="chat-waiting" key={task.id}>
							<div className="chat-waiting__title">{task.description || task.id}</div>
							{command ? <pre className="chat-term">$ {shortenPaths(command, root)}</pre> : null}
							{isAgentTask(task) ? null : <TaskOutputView running={unfinished(task.state)} taskId={task.id} />}
						</div>
					))}
				</div>
			) : null}
		</div>
	);
}

export function LiveThinkingRow({
	row,
	openRows,
	toggleRow,
}: {
	row: Extract<ChatRow, { kind: "thinking_live" }>;
	openRows: OpenRows;
	toggleRow: ToggleRow;
}) {
	const open = openRows[row.key] ?? false;
	const hasText = row.text.trim() !== "";
	return (
		<div className="chat-group" data-live="" data-thinking>
			<LiveRow
				detail={hasText && !open ? latestLine(row.text) : undefined}
				onToggle={(next) => toggleRow(row.key, next)}
				open={open}
				since={row.since}
				verb="Thinking"
			/>
			{open && hasText ? <div className="chat-think__body">{row.text}</div> : null}
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
