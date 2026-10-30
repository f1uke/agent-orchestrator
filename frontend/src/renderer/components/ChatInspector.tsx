import { Bot, History, ListTodo } from "lucide-react";
import { type ReactNode, useMemo } from "react";
import { type TaskState, unfinished } from "../lib/chat-event";
import type { Transcript } from "../lib/chat-fold";
import {
	type Activity,
	type AddedRule,
	type ContextUsage,
	formatTokens,
	type HistoryRow,
	latestTodos,
	addedRules,
	contextUsage,
	sessionActivity,
	sessionHistory,
	type Todo,
	type UsageSummary,
	usageSummary,
} from "../lib/chat-inspect";
import { formatCost } from "./ChatPane";

type Tone = "working" | "done" | "error" | "idle";

const STATE_TONE: Record<TaskState, Tone> = {
	pending: "working",
	running: "working",
	paused: "working",
	done: "done",
	error: "error",
	stopped: "idle",
};

const STATE_LABEL: Record<TaskState, string> = {
	pending: "pending",
	running: "running",
	paused: "paused",
	done: "done",
	error: "failed",
	stopped: "stopped",
};

export function ChatInspector({ transcript, onJump }: { transcript: Transcript; onJump: (itemIndex: number) => void }) {
	const todos = useMemo(() => latestTodos(transcript), [transcript]);
	const activity = useMemo(() => sessionActivity(transcript), [transcript]);
	const context = useMemo(() => contextUsage(transcript), [transcript]);
	const usage = useMemo(() => usageSummary(transcript), [transcript]);
	const rules = useMemo(() => addedRules(transcript), [transcript]);
	const history = useMemo(() => sessionHistory(transcript), [transcript]);

	return (
		<div className="chat-insp" role="tabpanel">
			<TodoSection todos={todos} />
			<ActivitySection activity={activity} />
			<ContextSection context={context} />
			<UsageSection usage={usage} />
			<PermissionsSection mode={transcript.permissionMode} rules={rules} />
			<HistorySection onJump={onJump} rows={history} />
		</div>
	);
}

function Section({
	title,
	icon,
	aside,
	children,
}: {
	title: string;
	icon?: ReactNode;
	aside?: ReactNode;
	children: ReactNode;
}) {
	return (
		<section aria-label={title} className="chat-insp__sec">
			<h3 className="chat-insp__head">
				{icon}
				{title}
				{aside ? <span className="chat-insp__aside">{aside}</span> : null}
			</h3>
			{children}
		</section>
	);
}

function Empty({ children }: { children: ReactNode }) {
	return <p className="chat-insp__empty">{children}</p>;
}

function TodoSection({ todos }: { todos: Todo[] | undefined }) {
	const done = todos?.filter((t) => t.status === "completed").length ?? 0;
	return (
		<Section
			aside={todos?.length ? `${done} / ${todos.length}` : undefined}
			icon={<ListTodo aria-hidden="true" className="chat-insp__icon" />}
			title="Todo"
		>
			{todos?.length ? (
				<ul className="chat-insp__todos">
					{todos.map((todo, i) => (
						<li
							className="chat-insp__todo"
							data-status={todo.status}
							key={`${i}:${todo.content}`}
							title={todo.status === "in_progress" ? todo.activeForm : undefined}
						>
							<span aria-hidden="true" className="chat-insp__box" />
							{todo.content}
						</li>
					))}
				</ul>
			) : (
				<Empty>{todos ? "Claude cleared its todo list." : "Claude has not written a todo list yet."}</Empty>
			)}
		</Section>
	);
}

function activityStatus(a: Activity): string {
	const label = STATE_LABEL[a.state];
	if (a.kind !== "agent" || a.tools === 0) return label;
	return `${label} · ${a.tools} ${a.tools === 1 ? "tool" : "tools"}`;
}

function ActivitySection({ activity }: { activity: Activity[] }) {
	const running = activity.filter((a) => unfinished(a.state)).length;
	return (
		<Section
			aside={running ? `${running} running` : undefined}
			icon={<Bot aria-hidden="true" className="chat-insp__icon" />}
			title="Subagents & background"
		>
			{activity.length ? (
				activity.map((a) => (
					<div className="chat-insp__task" data-kind={a.kind} data-state={a.state} key={a.key}>
						<div className="chat-insp__task-top">
							<span aria-hidden="true" className="chat-insp__dot" data-tone={STATE_TONE[a.state]} />
							<b>{a.title}</b>
							<span className="chat-insp__task-state">{activityStatus(a)}</span>
						</div>
						{a.detail ? (
							<div className="chat-insp__task-detail" title={a.detail}>
								{a.detail}
							</div>
						) : null}
					</div>
				))
			) : (
				<Empty>No subagents or background commands in this session.</Empty>
			)}
		</Section>
	);
}

function ContextSection({ context }: { context: ContextUsage | undefined }) {
	if (!context) {
		return (
			<Section title="Context">
				<Empty>Known after the first turn completes.</Empty>
			</Section>
		);
	}
	const { tokens, window } = context;
	if (window === undefined) {
		return (
			<Section aside={`${formatTokens(tokens)} tokens`} title="Context">
				<Empty>The log does not name this model's context window yet.</Empty>
			</Section>
		);
	}
	const percent = Math.min(100, Math.round((tokens / window) * 100));
	return (
		<Section aside={`${formatTokens(tokens)} / ${formatTokens(window)}`} title="Context">
			<div
				aria-label="Context used"
				aria-valuemax={100}
				aria-valuemin={0}
				aria-valuenow={percent}
				className="chat-insp__bar"
				role="meter"
			>
				<span style={{ width: `${percent}%` }} />
			</div>
			<p className="chat-insp__note">{percent}% of the window, as of the last turn</p>
		</Section>
	);
}

function UsageSection({ usage }: { usage: UsageSummary | undefined }) {
	return (
		<Section title="Cost & usage">
			{usage ? (
				<dl className="chat-insp__kv">
					<dt>This Claude session</dt>
					<dd>{formatCost(usage.costUsd)}</dd>
					<dt>Output tokens</dt>
					<dd>{formatTokens(usage.outputTokens)}</dd>
					<dt>Cache read</dt>
					<dd>{formatTokens(usage.cacheReadTokens)}</dd>
				</dl>
			) : (
				<Empty>Known after the first turn completes.</Empty>
			)}
		</Section>
	);
}

function PermissionsSection({ mode, rules }: { mode: string; rules: AddedRule[] }) {
	return (
		<Section aside="this session" title="Permissions">
			<dl className="chat-insp__kv">
				<dt>Mode</dt>
				<dd>{mode || "unknown"}</dd>
				{rules.map((r) => (
					<RuleRow key={`${r.behavior} ${r.rule}`} rule={r} />
				))}
			</dl>
			{rules.length === 0 ? <Empty>No rules added through Always allow.</Empty> : null}
		</Section>
	);
}

function RuleRow({ rule }: { rule: AddedRule }) {
	return (
		<>
			<dt className="chat-insp__rule" title={rule.rule}>
				{rule.rule}
			</dt>
			<dd data-behavior={rule.behavior}>{rule.behavior}</dd>
		</>
	);
}

const shortId = (id: string) => (id.length > 12 ? id.slice(0, 8) : id);

function formatWhen(ts: string, now = new Date()): string {
	const d = new Date(ts);
	if (ts === "" || Number.isNaN(d.getTime())) return "";
	const time = d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
	if (d.toDateString() === now.toDateString()) return `today ${time}`;
	return `${d.toLocaleDateString("en-US", { month: "short", day: "numeric" })} ${time}`;
}

function HistorySection({ rows, onJump }: { rows: HistoryRow[]; onJump: (itemIndex: number) => void }) {
	return (
		<Section
			aside={rows.length ? `${rows.length} ${rows.length === 1 ? "process" : "processes"}` : undefined}
			icon={<History aria-hidden="true" className="chat-insp__icon" />}
			title="History"
		>
			{rows.length ? (
				<ol className="chat-insp__history">
					{rows.map((row) => (
						<li key={row.itemIndex}>
							<button className="chat-insp__hist" onClick={() => onJump(row.itemIndex)} type="button">
								<span className="chat-insp__hist-top">
									<b>{row.resumed ? "Resumed" : "Started"}</b>
									{row.sessionId ? <code title={row.sessionId}>{shortId(row.sessionId)}</code> : null}
									<span className="chat-insp__when">{formatWhen(row.startedAt)}</span>
								</span>
								<span className="chat-insp__hist-meta">
									{row.turns} {row.turns === 1 ? "turn" : "turns"}
									{row.turns > 0 ? ` · session cost ${formatCost(row.costUsd)}` : ""}
								</span>
							</button>
						</li>
					))}
				</ol>
			) : (
				<Empty>No claude process has started yet.</Empty>
			)}
		</Section>
	);
}
