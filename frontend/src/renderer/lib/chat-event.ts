export type Direction = "out" | "in" | "host";

export type PatchHunk = { oldStart: number; oldLines: number; newStart: number; newLines: number; lines: string[] };

export type ChatEntry = {
	seq: number;
	ts: string;
	dir: Direction;
	payload: unknown;
};

export type CommandState = "sent" | "queued" | "started" | "completed" | "cancelled" | (string & {});

export type Block =
	| { type: "text"; text: string }
	| { type: "thinking"; text: string }
	| { type: "tool_use"; id: string; name: string; input: unknown }
	| { type: "tool_result"; toolUseId: string; isError: boolean; content: unknown }
	| { type: "image"; mediaType: string; data: string }
	| { type: "other"; blockType: string };

export type CanUseTool = {
	toolName: string;
	toolUseId: string;
	description: string;
	input: unknown;
	suggestions: unknown[];
	defaultToNo: boolean;
};

export type Usage = { inputTokens: number; outputTokens: number; cacheReadTokens: number; cacheCreationTokens: number };

export type TaskState = "pending" | "running" | "paused" | "done" | "error" | "stopped";

export function unfinished(state: TaskState): boolean {
	return state === "pending" || state === "running" || state === "paused";
}

export type TaskUsage = { totalTokens: number; toolUses: number; durationMs: number };

export type TaskEvent = {
	taskId: string;
	toolUseId: string;
	description: string;
	taskType: string;
	state?: TaskState;
	/** claude's is_backgrounded: false for a foreground Bash, which it registers as a task too. */
	background?: boolean;
	/** From a task_notification summary such as "... failed with exit code 1". */
	exitCode?: number;
	/** A task_notification's summary, which for an agent is its final text. */
	summary?: string;
	usage?: TaskUsage;
};

export type TaskNotification = {
	taskId: string;
	toolUseId: string;
	status: string;
	summary: string;
	result: string;
};

const TASK_NOTIFICATION = "<task-notification>";

export function parseTaskNotification(text: string): TaskNotification | undefined {
	const body = text.trimStart();
	if (!body.startsWith(TASK_NOTIFICATION)) return undefined;
	const tag = (name: string, greedy = false) =>
		new RegExp(`<${name}>([\\s\\S]*${greedy ? "" : "?"})</${name}>`).exec(body)?.[1].trim() ?? "";
	return {
		taskId: tag("task-id"),
		toolUseId: tag("tool-use-id"),
		status: tag("status"),
		summary: tag("summary"),
		result: tag("result", true),
	};
}

export type ChatEvent =
	| { type: "system_init"; sessionId: string; permissionMode: string; model: string }
	| { type: "system"; subtype: string; permissionMode: string; task?: TaskEvent }
	| {
			type: "assistant";
			messageId: string;
			parentToolUseId: string;
			blocks: Block[];
			/** input + cache read + cache creation of message.usage: the context this response saw. */
			contextTokens?: number;
	  }
	| { type: "user"; uuid: string; isReplay: boolean; blocks: Block[] }
	| {
			type: "result";
			subtype: string;
			isError: boolean;
			numTurns: number;
			totalCostUsd: number;
			sessionId: string;
			usage: Usage;
			contextTokens?: number;
			contextWindows: Record<string, number>;
	  }
	| { type: "control_request"; requestId: string; subtype: string; canUseTool?: CanUseTool }
	| { type: "control_response"; requestId: string; ok: boolean; error: string; response: unknown }
	| { type: "control_cancel"; requestId: string }
	| { type: "command_lifecycle"; commandUuid: string; state: CommandState }
	| { type: "unknown"; eventType: string };

export type Origin = { via: string; from: string };

export type HostNote =
	| { kind: "spawn"; resume: string; argv: string[] }
	| { kind: "exit"; code: number | null }
	| { kind: "user_origin"; uuid: string; origin?: Origin }
	| { kind: "other"; noteKind: string };

type Obj = Record<string, unknown>;

export function isObject(value: unknown): value is Obj {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function str(value: unknown): string {
	return typeof value === "string" ? value : "";
}

function lenient(value: unknown): string {
	if (typeof value === "string") return value;
	if (value === undefined || value === null) return "";
	return JSON.stringify(value);
}

function num(value: unknown): number {
	return typeof value === "number" ? value : 0;
}

class ParseError extends Error {}

function decodeBlock(raw: unknown): Block {
	if (!isObject(raw)) throw new ParseError("block is not an object");
	switch (raw.type) {
		case "text":
			return { type: "text", text: str(raw.text) };
		case "thinking":
			return { type: "thinking", text: str(raw.thinking) };
		case "tool_use":
			return { type: "tool_use", id: str(raw.id), name: str(raw.name), input: raw.input };
		case "tool_result":
			return {
				type: "tool_result",
				toolUseId: str(raw.tool_use_id),
				isError: raw.is_error === true,
				content: raw.content,
			};
		case "image": {
			const source = isObject(raw.source) ? raw.source : {};
			const base64 = source.type === "base64";
			return { type: "image", mediaType: base64 ? str(source.media_type) : "", data: base64 ? str(source.data) : "" };
		}
	}
	return { type: "other", blockType: str(raw.type) };
}

function decodeBlocks(content: unknown): Block[] {
	if (content === undefined || content === null) return [];
	if (typeof content === "string") return [{ type: "text", text: content }];
	if (!Array.isArray(content)) throw new ParseError("content is neither a string nor an array");
	return content.map(decodeBlock);
}

function decodeUsage(raw: unknown): Usage {
	const u = isObject(raw) ? raw : {};
	return {
		inputTokens: num(u.input_tokens),
		outputTokens: num(u.output_tokens),
		cacheReadTokens: num(u.cache_read_input_tokens),
		cacheCreationTokens: num(u.cache_creation_input_tokens),
	};
}

// usage sums every API call of the turn, so a two-call turn reads twice the
// context it held. The last entry of usage.iterations is one call: the true size.
function decodeContext(raw: unknown): number | undefined {
	const iterations = isObject(raw) && Array.isArray(raw.iterations) ? raw.iterations : [];
	const last = iterations.at(-1);
	if (!isObject(last)) return undefined;
	const { inputTokens, cacheReadTokens, cacheCreationTokens } = decodeUsage(last);
	return inputTokens + cacheReadTokens + cacheCreationTokens;
}

function decodeMessageContext(raw: unknown): number | undefined {
	if (!isObject(raw)) return undefined;
	const { inputTokens, cacheReadTokens, cacheCreationTokens } = decodeUsage(raw);
	const total = inputTokens + cacheReadTokens + cacheCreationTokens;
	return total > 0 ? total : undefined;
}

function decodeWindows(raw: unknown): Record<string, number> {
	const windows: Record<string, number> = {};
	if (!isObject(raw)) return windows;
	for (const [model, usage] of Object.entries(raw)) {
		if (isObject(usage) && typeof usage.contextWindow === "number" && usage.contextWindow > 0) {
			windows[model] = usage.contextWindow;
		}
	}
	return windows;
}

const TASK_STATES: Record<string, TaskState> = {
	pending: "pending",
	running: "running",
	paused: "paused",
	completed: "done",
	failed: "error",
	stopped: "stopped",
	killed: "stopped",
};

function decodeTask(line: Obj): TaskEvent | undefined {
	const subtype = str(line.subtype);
	const taskId = str(line.task_id);
	if (!subtype.startsWith("task_") || taskId === "") return undefined;
	const patch = isObject(line.patch) ? line.patch : {};
	const status = subtype === "task_updated" ? str(patch.status) : str(line.status);
	const started = subtype === "task_started" || subtype === "task_progress";
	const summary = subtype === "task_notification" ? str(line.summary) : "";
	const exit = /exit code (\d+)/.exec(summary);
	const event: TaskEvent = {
		taskId,
		toolUseId: str(line.tool_use_id),
		description: str(line.description) || str(patch.description) || str(line.summary),
		taskType: str(line.task_type),
		state: started ? "running" : TASK_STATES[status],
	};
	if (typeof line.is_backgrounded === "boolean") event.background = line.is_backgrounded;
	if (exit) event.exitCode = Number(exit[1]);
	if (summary !== "") event.summary = summary;
	if (isObject(line.usage)) {
		event.usage = {
			totalTokens: num(line.usage.total_tokens),
			toolUses: num(line.usage.tool_uses),
			durationMs: num(line.usage.duration_ms),
		};
	}
	return event;
}

function decodeKnown(type: string, line: Obj): ChatEvent | undefined {
	const message = isObject(line.message) ? line.message : {};
	switch (type) {
		case "system":
			if (line.subtype === "init") {
				return {
					type: "system_init",
					sessionId: str(line.session_id),
					permissionMode: str(line.permissionMode),
					model: str(line.model),
				};
			}
			return {
				type: "system",
				subtype: str(line.subtype),
				permissionMode: str(line.permissionMode),
				task: decodeTask(line),
			};
		case "assistant":
			return {
				type: "assistant",
				messageId: str(message.id),
				parentToolUseId: str(line.parent_tool_use_id),
				blocks: decodeBlocks(message.content),
				contextTokens: decodeMessageContext(message.usage),
			};
		case "user":
			return {
				type: "user",
				uuid: str(line.uuid),
				isReplay: line.isReplay === true,
				blocks: decodeBlocks(message.content),
			};
		case "result":
			return {
				type: "result",
				subtype: str(line.subtype),
				isError: line.is_error === true,
				numTurns: num(line.num_turns),
				totalCostUsd: num(line.total_cost_usd),
				sessionId: str(line.session_id),
				usage: decodeUsage(line.usage),
				contextTokens: decodeContext(line.usage),
				contextWindows: decodeWindows(line.modelUsage),
			};
		case "control_request": {
			const request = isObject(line.request) ? line.request : undefined;
			if (!request) return undefined;
			const requestId = str(line.request_id);
			const subtype = str(request.subtype);
			if (subtype !== "can_use_tool") return { type: "control_request", requestId, subtype };
			return {
				type: "control_request",
				requestId,
				subtype,
				canUseTool: {
					toolName: str(request.tool_name),
					toolUseId: str(request.tool_use_id),
					description: lenient(request.description),
					input: request.input,
					suggestions: Array.isArray(request.permission_suggestions) ? request.permission_suggestions : [],
					defaultToNo: request.default_to_no === true,
				},
			};
		}
		case "control_response": {
			const response = isObject(line.response) ? line.response : {};
			return {
				type: "control_response",
				requestId: str(response.request_id),
				ok: response.subtype === "success",
				error: lenient(response.error),
				response: response.response,
			};
		}
		case "control_cancel_request":
			return { type: "control_cancel", requestId: str(line.request_id) };
		case "command_lifecycle":
			return { type: "command_lifecycle", commandUuid: str(line.command_uuid), state: str(line.state) };
	}
	return undefined;
}

export function parseEvent(line: unknown): ChatEvent {
	if (!isObject(line)) throw new Error("chat event: line is not an object");
	const type = str(line.type);
	if (type === "") throw new Error("chat event: missing type");
	try {
		return decodeKnown(type, line) ?? { type: "unknown", eventType: type };
	} catch (error) {
		if (error instanceof ParseError) return { type: "unknown", eventType: type };
		throw error;
	}
}

export function parseHostNote(payload: unknown): HostNote {
	if (!isObject(payload)) throw new Error("host note: payload is not an object");
	const kind = str(payload.kind);
	if (kind === "") throw new Error("host note: missing kind");
	switch (kind) {
		case "spawn":
			return {
				kind,
				resume: str(payload.resume),
				argv: Array.isArray(payload.argv) ? payload.argv.filter((a): a is string => typeof a === "string") : [],
			};
		case "exit":
			return { kind, code: typeof payload.code === "number" ? payload.code : null };
		case "user_origin": {
			const origin = isObject(payload.origin)
				? { via: str(payload.origin.via), from: str(payload.origin.from) }
				: undefined;
			return { kind, uuid: str(payload.uuid), origin };
		}
	}
	return { kind: "other", noteKind: kind };
}

export type StreamDelta = {
	kind: "text" | "thinking" | "input_json" | "other";
	index: number;
	text: string;
	parentToolUseId: string;
	/** Set on message_start, which opens the message the following deltas belong to. */
	messageStart?: string;
	/** The message id claude stamps on each stream event, when it does. */
	messageId?: string;
};

export function parseStreamDelta(line: unknown): StreamDelta | undefined {
	if (!isObject(line) || line.type !== "stream_event" || !isObject(line.event)) return undefined;
	const event = line.event;
	const delta = isObject(event.delta) ? event.delta : {};
	const out: StreamDelta = {
		kind: "other",
		index: num(event.index),
		text: "",
		parentToolUseId: str(line.parent_tool_use_id),
		messageId: str(line.api_message_id) || undefined,
	};
	if (event.type === "message_start" && isObject(event.message)) out.messageStart = str(event.message.id);
	if (event.type === "content_block_delta") {
		if (delta.type === "text_delta") Object.assign(out, { kind: "text", text: str(delta.text) });
		if (delta.type === "thinking_delta") Object.assign(out, { kind: "thinking", text: str(delta.thinking) });
		if (delta.type === "input_json_delta") Object.assign(out, { kind: "input_json", text: str(delta.partial_json) });
	}
	return out;
}
