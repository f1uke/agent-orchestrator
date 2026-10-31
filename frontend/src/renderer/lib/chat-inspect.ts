import { isObject, type TaskState, unfinished } from "./chat-event";
import type { ChatItem, ToolCallItem, Transcript, TurnEndItem } from "./chat-fold";
import { resultText, ruleLabel, taskLabel } from "./chat-tools";

export type TodoStatus = "pending" | "in_progress" | "completed";

export type Todo = { content: string; activeForm: string; status: TodoStatus };

export type ActivityKind = "agent" | "background" | "task";

export type Activity = {
	key: string;
	kind: ActivityKind;
	title: string;
	detail: string;
	state: TaskState;
	tools: number;
};

/** Totals for the latest claude session id, whose total_cost_usd carries across a --resume. */
export type UsageSummary = { sessionId: string; costUsd: number; outputTokens: number; cacheReadTokens: number };

export type ContextUsage = { tokens: number; window?: number };

export type AddedRule = { rule: string; behavior: string };

export type HistoryRow = {
	itemIndex: number;
	startedAt: string;
	resumed: boolean;
	sessionId: string;
	turns: number;
	costUsd: number;
};

const SUBAGENT_TOOLS = new Set(["Agent", "Task"]);
const TODO_STATUSES = new Set<string>(["pending", "in_progress", "completed"]);

function str(value: unknown): string {
	return typeof value === "string" ? value : "";
}

function turnEnds(tr: Transcript): TurnEndItem[] {
	return tr.items.filter((i): i is TurnEndItem => i.kind === "turn_end");
}

type TodoEntry = { id: string; todo: Todo };

const asStatus = (value: unknown): TodoStatus | undefined =>
	typeof value === "string" && TODO_STATUSES.has(value) ? (value as TodoStatus) : undefined;

function todoWrite(input: Record<string, unknown>): TodoEntry[] {
	const todos = Array.isArray(input.todos) ? input.todos : [];
	return todos.filter(isObject).map((t, i) => ({
		id: `w${i}`,
		todo: {
			content: str(t.content),
			activeForm: str(t.activeForm),
			status: asStatus(t.status) ?? "pending",
		},
	}));
}

// claude names a created task in the result: "Task #1 created successfully: ...".
function createdId(call: ToolCallItem, list: readonly TodoEntry[]): string {
	return /#(\d+)/.exec(resultText(call.result))?.[1] ?? String(list.length + 1);
}

function taskUpdate(list: readonly TodoEntry[], input: Record<string, unknown>): TodoEntry[] {
	const id = str(input.taskId);
	if (str(input.status) === "deleted") return list.filter((e) => e.id !== id);
	return list.map((entry) => {
		if (entry.id !== id) return entry;
		const status = asStatus(input.status) ?? entry.todo.status;
		const subject = str(input.subject);
		return { id, todo: { ...entry.todo, status, content: subject || entry.todo.content } };
	});
}

type TodoState = { todos: Todo[]; at: number };

function todoState(tr: Transcript): TodoState | undefined {
	let list: TodoEntry[] | undefined;
	let at = -1;
	tr.items.forEach((item, index) => {
		if (item.kind !== "tool_call" || item.parentToolUseId !== "" || item.result?.isError) return;
		const before = list;
		const input = isObject(item.input) ? item.input : {};
		if (item.name === "TodoWrite") {
			list = todoWrite(input);
		} else if (item.name === "TaskCreate" && item.result) {
			const subject = str(input.subject);
			const todo: Todo = { content: subject, activeForm: str(input.activeForm) || subject, status: "pending" };
			list = [...(list ?? []), { id: createdId(item, list ?? []), todo }];
		} else if (item.name === "TaskUpdate" && list) {
			list = taskUpdate(list, input);
		}
		if (list !== before) at = index;
	});
	return list && { todos: list.map((entry) => entry.todo), at };
}

/** The todo list as the latest TodoWrite, or the TaskCreate and TaskUpdate calls since, leave it. */
export function latestTodos(tr: Transcript): Todo[] | undefined {
	return todoState(tr)?.todos;
}

const turnStarted = (item: ChatItem) => item.kind === "user" && item.state !== "sent" && item.state !== "queued";

/**
 * The list the todo bar shows: the latest one while any item is open. A list
 * that is all completed stays until the next user turn starts, then goes.
 */
export function shownTodos(tr: Transcript): Todo[] | undefined {
	const state = todoState(tr);
	if (!state || state.todos.length === 0) return undefined;
	if (state.todos.some((t) => t.status !== "completed")) return state.todos;
	return tr.items.slice(state.at + 1).some(turnStarted) ? undefined : state.todos;
}

function callState(call: ToolCallItem, processEnded: boolean): TaskState {
	if (call.result) return call.result.isError ? "error" : "done";
	return processEnded ? "stopped" : "running";
}

export function sessionActivity(tr: Transcript): Activity[] {
	const taskByToolUse = new Map(tr.tasks.filter((t) => t.toolUseId !== "").map((t) => [t.toolUseId, t]));
	const subagentTools = new Map<string, number>();
	let lastBoundary = -1;
	tr.items.forEach((item, i) => {
		if (item.kind === "tool_call" && item.parentToolUseId !== "") {
			subagentTools.set(item.parentToolUseId, (subagentTools.get(item.parentToolUseId) ?? 0) + 1);
		}
		if (item.kind === "notice" && item.notice !== "compacted" && item.notice !== "interrupted") lastBoundary = i;
	});

	const out: Activity[] = [];
	const linked = new Set<string>();
	tr.items.forEach((item, i) => {
		if (item.kind !== "tool_call" || item.parentToolUseId !== "") return;
		const input = isObject(item.input) ? item.input : {};
		const task = taskByToolUse.get(item.id);
		let kind: ActivityKind;
		let title: string;
		let detail: string;
		if (SUBAGENT_TOOLS.has(item.name)) {
			kind = "agent";
			title = str(input.subagent_type) || "Agent";
			detail = str(input.description);
		} else if (item.name === "Bash" && input.run_in_background === true && (task || !item.result)) {
			kind = "background";
			title = taskLabel("local_bash");
			detail = str(input.command);
		} else {
			return;
		}
		if (task) linked.add(task.id);
		out.push({
			key: item.id,
			kind,
			title,
			detail,
			state: task?.state ?? callState(item, i < lastBoundary),
			tools: Math.max(subagentTools.get(item.id) ?? 0, task?.usage?.toolUses ?? 0),
		});
	});
	for (const task of tr.tasks) {
		if (linked.has(task.id) || task.background === false) continue;
		out.push({
			key: task.id,
			kind: "task",
			title: taskLabel(task.taskType),
			detail: task.description || task.id,
			state: task.state,
			tools: 0,
		});
	}
	const newest = out.reverse();
	return [...newest.filter((a) => unfinished(a.state)), ...newest.filter((a) => !unfinished(a.state))];
}

export function usageSummary(tr: Transcript): UsageSummary | undefined {
	const ends = turnEnds(tr);
	const last = ends.at(-1);
	if (!last) return undefined;
	const mine = ends.filter((e) => e.sessionId === last.sessionId);
	return {
		sessionId: last.sessionId,
		costUsd: last.costUsd,
		outputTokens: mine.reduce((n, e) => n + e.usage.outputTokens, 0),
		cacheReadTokens: mine.reduce((n, e) => n + e.usage.cacheReadTokens, 0),
	};
}

export function contextUsage(tr: Transcript): ContextUsage | undefined {
	const ends = turnEnds(tr);
	const tokens = tr.contextTokens ?? ends.filter((e) => e.contextTokens !== undefined).at(-1)?.contextTokens;
	if (tokens === undefined) return undefined;
	const window = ends.filter((e) => e.contextWindows[tr.model] !== undefined).at(-1)?.contextWindows[tr.model];
	return window === undefined ? { tokens } : { tokens, window };
}

export function addedRules(tr: Transcript): AddedRule[] {
	const rules = new Map<string, AddedRule>();
	for (const item of tr.items) {
		if (item.kind !== "tool_call") continue;
		for (const update of item.permission?.updatedPermissions ?? []) {
			if (!isObject(update) || update.type !== "addRules" || !Array.isArray(update.rules)) continue;
			const behavior = str(update.behavior) || "allow";
			for (const rule of update.rules.filter(isObject)) {
				const label = ruleLabel(rule);
				const key = `${behavior} ${label}`;
				if (!rules.has(key)) rules.set(key, { rule: label, behavior });
			}
		}
	}
	return [...rules.values()];
}

export function sessionHistory(tr: Transcript): HistoryRow[] {
	const rows: HistoryRow[] = [];
	const open = (itemIndex: number, startedAt: string, resumed: string): HistoryRow => {
		const row = { itemIndex, startedAt, resumed: resumed !== "", sessionId: resumed, turns: 0, costUsd: 0 };
		rows.push(row);
		return row;
	};
	let row: HistoryRow | undefined;
	tr.items.forEach((item, i) => {
		if (item.kind === "notice" && (item.notice === "spawn" || item.notice === "resumed")) {
			row = open(i, item.ts, item.notice === "resumed" ? item.text : "");
		} else if (item.kind === "turn_end") {
			row ??= open(0, "", "");
			row.turns += 1;
			row.costUsd = item.costUsd;
			if (item.sessionId !== "") row.sessionId = item.sessionId;
		}
	});
	return rows;
}

export function formatTokens(n: number): string {
	if (n < 1_000) return String(n);
	if (n < 100_000) return `${(n / 1_000).toFixed(1).replace(/\.0$/, "")}k`;
	if (n < 1_000_000) return `${Math.round(n / 1_000)}k`;
	return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
}
