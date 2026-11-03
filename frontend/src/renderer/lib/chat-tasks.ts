import { isObject, type TaskState, unfinished } from "./chat-event";
import type { BackgroundTask, ChatItem, ToolCallItem, Transcript } from "./chat-fold";
import { taskLabel } from "./chat-tools";

export type TaskKind = "agent" | "bash" | "other";

/**
 * One background job: a subagent, a background Bash, or a task claude reports with no call of ours.
 * `ref` is the tool_use id of the call that started it, or the task id when no call is known.
 * `task` is claude's own task record, which stop and the output read need: an entry without one cannot be either.
 */
export type TaskEntry = {
	ref: string;
	kind: TaskKind;
	title: string;
	state: TaskState;
	running: boolean;
	task?: BackgroundTask;
	/** The task's own description, which is what claude calls the job. */
	description: string;
	command: string;
	prompt: string;
	subagentType: string;
	toolUses: number;
	startedTs: string;
};

export type TaskList = { running: TaskEntry[]; finished: TaskEntry[] };

const SUBAGENT_TOOLS = new Set(["Agent", "Task"]);

const str = (value: unknown): string => (typeof value === "string" ? value : "");

function callState(call: ToolCallItem, processEnded: boolean): TaskState {
	if (call.result) return call.result.isError ? "error" : "done";
	return processEnded ? "stopped" : "running";
}

function entries(tr: Transcript): TaskEntry[] {
	const taskByToolUse = new Map(tr.tasks.filter((t) => t.toolUseId !== "").map((t) => [t.toolUseId, t]));
	const subagentTools = new Map<string, number>();
	let lastBoundary = -1;
	tr.items.forEach((item, i) => {
		if (item.kind === "tool_call" && item.parentToolUseId !== "") {
			subagentTools.set(item.parentToolUseId, (subagentTools.get(item.parentToolUseId) ?? 0) + 1);
		}
		if (
			item.kind === "notice" &&
			item.notice !== "compacted" &&
			item.notice !== "interrupted" &&
			item.notice !== "api_error"
		)
			lastBoundary = i;
	});

	const out: TaskEntry[] = [];
	const linked = new Set<string>();
	tr.items.forEach((item, i) => {
		if (item.kind !== "tool_call") return;
		const input = isObject(item.input) ? item.input : {};
		const task = taskByToolUse.get(item.id);
		let kind: TaskKind;
		if (SUBAGENT_TOOLS.has(item.name)) kind = "agent";
		else if (item.name === "Bash" && input.run_in_background === true && (task || !item.result)) kind = "bash";
		else return;
		if (task) linked.add(task.id);
		if (kind !== "agent" && task?.background === false) return;
		const state = task?.state ?? callState(item, i < lastBoundary);
		const description = task?.description ?? "";
		const subagentType = str(input.subagent_type);
		out.push({
			ref: item.id,
			kind,
			title:
				kind === "agent"
					? str(input.description) || description || subagentType || "Agent"
					: description || str(input.description) || str(input.command) || taskLabel("local_bash"),
			state,
			running: unfinished(state),
			task,
			description,
			command: str(input.command),
			prompt: str(input.prompt),
			subagentType,
			toolUses: Math.max(subagentTools.get(item.id) ?? 0, task?.usage?.toolUses ?? 0),
			startedTs: task?.startedTs || item.startedTs,
		});
	});
	for (const task of tr.tasks) {
		if (linked.has(task.id) || task.background === false) continue;
		out.push({
			ref: task.id,
			kind: task.taskType.includes("agent") ? "agent" : task.taskType === "local_bash" ? "bash" : "other",
			title: task.description || task.id,
			state: task.state,
			running: unfinished(task.state),
			task,
			description: task.description,
			command: "",
			prompt: "",
			subagentType: "",
			toolUses: task.usage?.toolUses ?? 0,
			startedTs: task.startedTs ?? "",
		});
	}
	return out.reverse();
}

/**
 * The background jobs of a session, newest first: what is still running, then what ended. The Tasks tab, the
 * status line's count and the Waiting on row all read this one list, so none of them counts a job twice.
 */
export function taskList(tr: Transcript): TaskList {
	const all = entries(tr);
	return { running: all.filter((e) => e.running), finished: all.filter((e) => !e.running) };
}

/** A job by the tool_use id of its call or by its task id: a `Ran agent` row knows the first, claude's events the second. */
export function taskTarget(tr: Transcript, key: string): TaskEntry | undefined {
	return entries(tr).find((e) => e.ref === key || e.task?.id === key);
}

/**
 * The conversation one Agent call ran, as a transcript the main chat's row builder draws: the lines whose
 * parent is the call move to the top level, and a nested agent's lines stay under their own parent so its row
 * can count them and drill in. It holds no user message, so it has no turn to fold and no live row.
 */
export function subagentTranscript(tr: Transcript, agentCallId: string): Transcript {
	const inside = new Set([agentCallId]);
	const items: ChatItem[] = [];
	const itemKeys: string[] = [];
	tr.items.forEach((item, index) => {
		if (item.kind !== "tool_call" && item.kind !== "assistant_text" && item.kind !== "thinking") return;
		if (!inside.has(item.parentToolUseId)) return;
		const top = item.parentToolUseId === agentCallId;
		if (item.kind === "tool_call") inside.add(item.id);
		items.push(top ? { ...item, parentToolUseId: "" } : item);
		itemKeys.push(tr.itemKeys[index]);
	});
	const calls = new Set(items.flatMap((item) => (item.kind === "tool_call" ? [item.id] : [])));
	return {
		claudeSessionIds: [],
		items,
		itemKeys,
		pending: [],
		queued: [],
		busy: false,
		permissionMode: "",
		costUsd: 0,
		commands: [],
		models: [],
		model: tr.agentModels[agentCallId] ?? "",
		tasks: tr.tasks.filter((t) => calls.has(t.toolUseId)),
		agentModels: tr.agentModels,
		lastSeq: tr.lastSeq,
	};
}
