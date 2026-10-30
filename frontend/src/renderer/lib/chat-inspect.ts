import { isObject, type TaskState, unfinished } from "./chat-event";
import type { ToolCallItem, Transcript, TurnEndItem } from "./chat-fold";
import { ruleLabel } from "./chat-tools";

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

export function latestTodos(tr: Transcript): Todo[] | undefined {
	for (let i = tr.items.length - 1; i >= 0; i--) {
		const item = tr.items[i];
		if (item.kind !== "tool_call" || item.name !== "TodoWrite" || item.parentToolUseId !== "") continue;
		if (item.result?.isError) continue;
		const todos = isObject(item.input) && Array.isArray(item.input.todos) ? item.input.todos : [];
		return todos.filter(isObject).map((t) => ({
			content: str(t.content),
			activeForm: str(t.activeForm),
			status: TODO_STATUSES.has(str(t.status)) ? (t.status as TodoStatus) : "pending",
		}));
	}
	return undefined;
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
			title = "Bash · background";
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
			tools: subagentTools.get(item.id) ?? 0,
		});
	});
	for (const task of tr.tasks) {
		if (linked.has(task.id)) continue;
		out.push({
			key: task.id,
			kind: "task",
			title: task.taskType || "Task",
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
	const last = turnEnds(tr)
		.filter((e) => e.contextTokens !== undefined)
		.at(-1);
	if (last?.contextTokens === undefined) return undefined;
	const window = last.contextWindows[tr.model];
	return window === undefined ? { tokens: last.contextTokens } : { tokens: last.contextTokens, window };
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
