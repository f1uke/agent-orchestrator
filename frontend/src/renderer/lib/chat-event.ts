export type Direction = "out" | "in" | "host";

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
	| { type: "image"; mediaType: string }
	| { type: "other"; blockType: string };

export type CanUseTool = {
	toolName: string;
	toolUseId: string;
	description: string;
	input: unknown;
	suggestions: unknown[];
};

export type ChatEvent =
	| { type: "system_init"; sessionId: string; permissionMode: string }
	| { type: "system"; subtype: string }
	| { type: "assistant"; messageId: string; parentToolUseId: string; blocks: Block[] }
	| { type: "user"; uuid: string; isReplay: boolean; blocks: Block[] }
	| { type: "result"; subtype: string; isError: boolean; numTurns: number; totalCostUsd: number }
	| { type: "control_request"; requestId: string; canUseTool?: CanUseTool }
	| { type: "control_response"; requestId: string; ok: boolean; error: string; response: unknown }
	| { type: "control_cancel"; requestId: string }
	| { type: "command_lifecycle"; commandUuid: string; state: CommandState }
	| { type: "unknown"; eventType: string };

export type Origin = { via: string; from: string };

export type HostNote =
	| { kind: "spawn"; resume: string }
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
		case "image":
			return { type: "image", mediaType: isObject(raw.source) ? str(raw.source.media_type) : "" };
	}
	return { type: "other", blockType: str(raw.type) };
}

function decodeBlocks(content: unknown): Block[] {
	if (content === undefined || content === null) return [];
	if (typeof content === "string") return [{ type: "text", text: content }];
	if (!Array.isArray(content)) throw new ParseError("content is neither a string nor an array");
	return content.map(decodeBlock);
}

function decodeKnown(type: string, line: Obj): ChatEvent | undefined {
	const message = isObject(line.message) ? line.message : {};
	switch (type) {
		case "system":
			if (line.subtype === "init") {
				return { type: "system_init", sessionId: str(line.session_id), permissionMode: str(line.permissionMode) };
			}
			return { type: "system", subtype: str(line.subtype) };
		case "assistant":
			return {
				type: "assistant",
				messageId: str(message.id),
				parentToolUseId: str(line.parent_tool_use_id),
				blocks: decodeBlocks(message.content),
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
			};
		case "control_request": {
			const request = isObject(line.request) ? line.request : undefined;
			if (!request) return undefined;
			const requestId = str(line.request_id);
			if (request.subtype !== "can_use_tool") return { type: "control_request", requestId };
			return {
				type: "control_request",
				requestId,
				canUseTool: {
					toolName: str(request.tool_name),
					toolUseId: str(request.tool_use_id),
					description: lenient(request.description),
					input: request.input,
					suggestions: Array.isArray(request.permission_suggestions) ? request.permission_suggestions : [],
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
			return { kind, resume: str(payload.resume) };
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
