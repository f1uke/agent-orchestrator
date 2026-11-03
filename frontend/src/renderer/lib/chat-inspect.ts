import { isObject } from "./chat-event";
import type { ChatItem, ToolCallItem, Transcript, TurnEndItem } from "./chat-fold";
import { resultText } from "./chat-tools";

export type TodoStatus = "pending" | "in_progress" | "completed";

export type Todo = { content: string; activeForm: string; status: TodoStatus };

export type ContextUsage = { tokens: number; window?: number };

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

const turnStarted = (item: ChatItem) => item.kind === "user" && item.state !== "sent" && item.state !== "queued";

export function shownTodos(tr: Transcript): Todo[] | undefined {
	const state = todoState(tr);
	if (!state || state.todos.length === 0) return undefined;
	if (state.todos.some((t) => t.status !== "completed")) return state.todos;
	return tr.items.slice(state.at + 1).some(turnStarted) ? undefined : state.todos;
}

export function contextUsage(tr: Transcript): ContextUsage | undefined {
	const ends = turnEnds(tr);
	const tokens = tr.contextTokens ?? ends.filter((e) => e.contextTokens !== undefined).at(-1)?.contextTokens;
	if (tokens === undefined) return undefined;
	const window = ends.filter((e) => e.contextWindows[tr.model] !== undefined).at(-1)?.contextWindows[tr.model];
	return window === undefined ? { tokens } : { tokens, window };
}

export function formatTokens(n: number): string {
	if (n < 1_000) return String(n);
	if (n < 100_000) return `${(n / 1_000).toFixed(1).replace(/\.0$/, "")}k`;
	if (n < 1_000_000) return `${Math.round(n / 1_000)}k`;
	return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
}

export function formatCost(usd: number): string {
	return `$${usd.toFixed(usd < 1 ? 3 : 2)}`;
}
