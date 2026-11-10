import { isObject, type TaskState, unfinished } from "./chat-event";
import {
	type BackgroundTask,
	type ChatItem,
	REMOTE_CONTROL_OFF,
	type ToolCallItem,
	type Transcript,
} from "./chat-fold";
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

const kindOf = (call: ToolCallItem, task?: BackgroundTask): TaskKind | undefined => {
	if (SUBAGENT_TOOLS.has(call.name)) return "agent";
	const input = isObject(call.input) ? call.input : {};
	return call.name === "Bash" && input.run_in_background === true && (task || !call.result) ? "bash" : undefined;
};

function fromCall(
	call: ToolCallItem,
	kind: TaskKind,
	task: BackgroundTask | undefined,
	state: TaskState,
	tools: number,
) {
	const input = isObject(call.input) ? call.input : {};
	const description = task?.description ?? "";
	const subagentType = str(input.subagent_type);
	const entry: TaskEntry = {
		ref: call.id,
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
		toolUses: Math.max(tools, task?.usage?.toolUses ?? 0),
		startedTs: task?.startedTs || call.startedTs,
	};
	return entry;
}

function scan(tr: Transcript): { top: TaskEntry[]; nested: TaskEntry[] } {
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

	const top: TaskEntry[] = [];
	const nested: TaskEntry[] = [];
	const linked = new Set<string>();
	tr.items.forEach((item, i) => {
		if (item.kind !== "tool_call") return;
		const task = taskByToolUse.get(item.id);
		const kind = kindOf(item, task);
		if (!kind) return;
		if (task) linked.add(task.id);
		if (kind !== "agent" && task?.background === false) return;
		const entry = fromCall(
			item,
			kind,
			task,
			task?.state ?? callState(item, i < lastBoundary),
			subagentTools.get(item.id) ?? 0,
		);
		(item.parentToolUseId === "" ? top : nested).push(entry);
	});
	for (const task of tr.tasks) {
		if (linked.has(task.id) || task.background === false) continue;
		top.push({
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
	return { top: top.reverse(), nested };
}

/**
 * The background jobs of a session, newest first: what is still running, then what ended. The Tasks tab, the
 * status line's count and the Waiting on row all read this one list, so none of them counts a job twice.
 */
export function taskList(tr: Transcript): TaskList {
	const { top } = scan(tr);
	return { running: top.filter((e) => e.running), finished: top.filter((e) => !e.running) };
}

/**
 * A job by the tool_use id of its call or by its task id: a `Ran agent` row knows the first, claude's events the
 * second. A subagent's own agent call is not in the list, but a row inside the subagent's view can still drill in.
 */
export function taskTarget(tr: Transcript, key: string): TaskEntry | undefined {
	const { top, nested } = scan(tr);
	const known = (e: TaskEntry) => e.ref === key || e.task?.id === key || e.task?.toolUseId === key;
	return top.find(known) ?? nested.find(known);
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
		effort: tr.effort,
		chrome: tr.chrome,
		tasks: tr.tasks.filter((t) => calls.has(t.toolUseId)),
		agentModels: tr.agentModels,
		remoteControl: REMOTE_CONTROL_OFF,
		suggestion: "",
		bashBusy: false,
		lastSeq: tr.lastSeq,
	};
}
