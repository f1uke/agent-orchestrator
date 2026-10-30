import {
	type Block,
	type CanUseTool,
	type ChatEntry,
	type ChatEvent,
	type CommandState,
	type HostNote,
	type Origin,
	type TaskEvent,
	type TaskState,
	type TaskUsage,
	type Usage,
	isObject,
	unfinished,
	parseEvent,
	parseHostNote,
} from "./chat-event";
import { elapsedMs } from "./chat-time";

export type PermissionStatus = "pending" | "allowed" | "denied" | "cancelled";

export type PermissionOutcome = {
	requestId: string;
	status: PermissionStatus;
	message: string;
	updatedPermissions: unknown[];
};

export type ToolResult = { isError: boolean; content: unknown };

export type NoticeKind = "interrupted" | "spawn" | "resumed" | "exit" | "compacted";

export type UserItem = {
	kind: "user";
	uuid: string;
	text: string;
	images: number;
	origin?: Origin;
	state: CommandState;
	/** When the input entered the log. */
	ts: string;
};

export type ToolCallItem = {
	kind: "tool_call";
	id: string;
	name: string;
	parentToolUseId: string;
	input: unknown;
	result?: ToolResult;
	permission?: PermissionOutcome;
	/** The ts of the tool_use entry. */
	startedTs: string;
	/** The ts of the tool_result entry. */
	endedTs?: string;
};

export type TurnEndItem = {
	kind: "turn_end";
	subtype: string;
	isError: boolean;
	numTurns: number;
	costUsd: number;
	sessionId: string;
	usage: Usage;
	contextTokens?: number;
	contextWindows: Record<string, number>;
	ts: string;
	/** From the turn's start (its input started, or arrived) to this result; absent when the log missed the start. */
	durationMs?: number;
};

export type NoticeItem = { kind: "notice"; notice: NoticeKind; text: string; ts: string };

export type ChatItem =
	| UserItem
	| { kind: "assistant_text"; messageId: string; text: string; parentToolUseId: string }
	| { kind: "thinking"; text: string; parentToolUseId: string }
	| ToolCallItem
	| TurnEndItem
	| NoticeItem;

export type BackgroundTask = {
	id: string;
	toolUseId: string;
	description: string;
	taskType: string;
	state: TaskState;
	/** False for a foreground Bash; absent when the log does not say. */
	background?: boolean;
	exitCode?: number;
	/** The task_notification summary: for an agent, its final text. */
	summary?: string;
	usage?: TaskUsage;
};

export type PendingKind = "permission" | "question" | "plan";

export type Pending = {
	requestId: string;
	toolName: string;
	toolUseId: string;
	kind: PendingKind;
	input: unknown;
	suggestions: unknown[];
	defaultToNo: boolean;
};

/** A slash command or skill claude offers. `builtin` is absent when claude does not say. */
export type ChatCommand = { name: string; description: string; argumentHint: string; builtin?: boolean };

/** A model the session can switch to. `value` is what set_model takes: an alias such as "sonnet", or a full id. */
export type ChatModel = {
	value: string;
	resolvedModel: string;
	displayName: string;
	description: string;
	supportsEffort: boolean;
	supportedEffortLevels: string[];
};

export type Transcript = {
	claudeSessionIds: string[];
	items: ChatItem[];
	pending: Pending[];
	queued: string[];
	busy: boolean;
	permissionMode: string;
	/** The latest result's total_cost_usd, cumulative for the claude process. */
	costUsd: number;
	/** From the latest initialize response. */
	commands: ChatCommand[];
	/** From the latest initialize response. */
	models: ChatModel[];
	/** In use: the spawn's --model, then system/init or an accepted set_model. */
	model: string;
	tasks: BackgroundTask[];
	/** The size of the context the latest top-level response saw, known before the turn's result. */
	contextTokens?: number;
	lastSeq: number;
};

// The user message claude emits after an interrupt ("[Request interrupted by
// user]", or "... for tool use]").
const INTERRUPT_MARKER = "[Request interrupted by user";

type Permission = { toolUseId?: string; out: PermissionOutcome };

function pendingKind(tool: string): PendingKind {
	if (tool === "AskUserQuestion") return "question";
	if (tool === "ExitPlanMode") return "plan";
	return "permission";
}

function strField(o: Record<string, unknown>, key: string): string {
	return typeof o[key] === "string" ? o[key] : "";
}

function parseCommands(raw: unknown): ChatCommand[] {
	if (!Array.isArray(raw)) return [];
	return raw.filter(isObject).map((c) => {
		const command: ChatCommand = {
			name: strField(c, "name"),
			description: strField(c, "description"),
			argumentHint: strField(c, "argumentHint"),
		};
		if (typeof c.builtin === "boolean") command.builtin = c.builtin;
		return command;
	});
}

function parseModels(raw: unknown): ChatModel[] {
	if (!Array.isArray(raw)) return [];
	return raw.filter(isObject).map((m) => ({
		value: strField(m, "value"),
		resolvedModel: strField(m, "resolvedModel"),
		displayName: strField(m, "displayName"),
		description: strField(m, "description"),
		supportsEffort: m.supportsEffort === true,
		supportedEffortLevels: Array.isArray(m.supportedEffortLevels)
			? m.supportedEffortLevels.filter((l): l is string => typeof l === "string")
			: [],
	}));
}

function modelFlag(argv: readonly string[]): string {
	for (let i = 0; i < argv.length; i++) {
		const arg = argv[i];
		if (arg === "--") break;
		if (arg.startsWith("--model=")) return arg.slice("--model=".length);
		if (arg === "--model" && i + 1 < argv.length) return argv[i + 1];
	}
	return "";
}

function live(state: CommandState): boolean {
	return state === "sent" || state === "queued" || state === "started";
}

export class ChatFolder {
	private last = 0;
	private sessions: string[] = [];
	private items: ChatItem[] = [];
	private tools = new Map<string, number>();
	private users = new Map<string, number>();
	private origins = new Map<string, Origin>();
	private commands = new Map<string, CommandState>();
	private perms = new Map<string, Permission>();
	private pending: Pending[] = [];
	private mode = "";
	private cost = 0;
	// The id of a host's initialize or set_model request, to its subtype,
	// because claude's response does not repeat it.
	private controls = new Map<string, string>();
	private slash: ChatCommand[] = [];
	private models: ChatModel[] = [];
	private model = "";
	private tasks = new Map<string, BackgroundTask>();
	private ts = "";
	private context: number | undefined;
	// When the turn now running started, until its result.
	private turnStart: string | undefined;

	get lastSeq(): number {
		return this.last;
	}

	/**
	 * Fold one entry in. An entry whose seq is not above the last applied one is
	 * ignored, so replaying a prefix of the log changes nothing. An entry that
	 * fails to parse throws and leaves the folder untouched.
	 */
	apply(entry: ChatEntry): void {
		if (entry.seq <= this.last) return;
		this.ts = entry.ts;
		switch (entry.dir) {
			case "out":
			case "in": {
				const event = parseEvent(entry.payload);
				if (entry.dir === "out") this.applyOut(event);
				else this.applyIn(event);
				break;
			}
			case "host":
				this.applyHost(parseHostNote(entry.payload));
				break;
			default:
				throw new Error(`chat fold: entry ${entry.seq}: unknown dir ${JSON.stringify(entry.dir)}`);
		}
		this.last = entry.seq;
	}

	transcript(): Transcript {
		const queued: string[] = [];
		for (const item of this.items) {
			if (item.kind === "user" && (item.state === "sent" || item.state === "queued")) queued.push(item.uuid);
		}
		return {
			claudeSessionIds: [...this.sessions],
			items: [...this.items],
			pending: [...this.pending],
			queued,
			busy: [...this.commands.values()].includes("started"),
			permissionMode: this.mode,
			costUsd: this.cost,
			commands: [...this.slash],
			models: [...this.models],
			model: this.model,
			tasks: [...this.tasks.values()],
			contextTokens: this.context,
			lastSeq: this.last,
		};
	}

	private applyOut(event: ChatEvent): void {
		switch (event.type) {
			case "system_init":
				if (!this.sessions.includes(event.sessionId)) this.sessions.push(event.sessionId);
				if (event.permissionMode !== "") this.mode = event.permissionMode;
				if (event.model !== "") this.model = event.model;
				break;
			case "system":
				if (event.permissionMode !== "") this.mode = event.permissionMode;
				if (event.subtype === "compact_boundary") this.notice("compacted", "");
				if (event.task) this.applyTask(event.task);
				break;
			case "assistant":
				this.applyAssistant(event.messageId, event.parentToolUseId, event.blocks);
				if (event.parentToolUseId === "" && event.contextTokens !== undefined) this.context = event.contextTokens;
				break;
			case "user":
				if (!event.isReplay) this.applyToolResults(event.blocks);
				break;
			case "result":
				this.cost = event.totalCostUsd;
				if (event.contextTokens !== undefined) this.context = event.contextTokens;
				this.items.push({
					ts: this.ts,
					durationMs: elapsedMs(this.turnStart, this.ts),
					kind: "turn_end",
					subtype: event.subtype,
					isError: event.isError,
					numTurns: event.numTurns,
					costUsd: event.totalCostUsd,
					sessionId: event.sessionId,
					usage: event.usage,
					contextTokens: event.contextTokens,
					contextWindows: event.contextWindows,
				});
				this.turnStart = undefined;
				break;
			case "control_request":
				if (event.canUseTool) this.openPermission(event.requestId, event.canUseTool);
				break;
			case "control_response":
				this.answerPermission(event);
				this.answerControl(event);
				break;
			case "control_cancel":
				this.settle(event.requestId, "cancelled", "", []);
				break;
			case "command_lifecycle":
				this.setCommand(event.commandUuid, event.state);
				break;
		}
	}

	private applyIn(event: ChatEvent): void {
		if (event.type === "user") this.addUserInput(event.uuid, event.blocks);
		else if (event.type === "control_response") this.answerPermission(event);
		else if (event.type === "control_request" && (event.subtype === "initialize" || event.subtype === "set_model")) {
			this.controls.set(event.requestId, event.subtype);
		}
	}

	private answerControl(response: Extract<ChatEvent, { type: "control_response" }>): void {
		const subtype = this.controls.get(response.requestId);
		if (subtype === undefined) return;
		this.controls.delete(response.requestId);
		if (!response.ok || !isObject(response.response)) return;
		const body = response.response;
		if (subtype === "initialize") {
			this.slash = parseCommands(body.commands);
			this.models = parseModels(body.models);
		} else if (typeof body.model === "string" && body.model !== "") {
			this.model = body.model;
		}
	}

	private applyAssistant(messageId: string, parentToolUseId: string, blocks: Block[]): void {
		for (const block of blocks) {
			if (block.type === "text") {
				this.items.push({ kind: "assistant_text", messageId, text: block.text, parentToolUseId });
			} else if (block.type === "thinking") {
				this.items.push({ kind: "thinking", text: block.text, parentToolUseId });
			} else if (block.type === "tool_use" && !this.tools.has(block.id)) {
				this.tools.set(block.id, this.items.length);
				this.items.push({
					kind: "tool_call",
					id: block.id,
					name: block.name,
					parentToolUseId,
					input: block.input,
					startedTs: this.ts,
				});
			}
		}
	}

	private applyToolResults(blocks: Block[]): void {
		for (const block of blocks) {
			if (block.type === "tool_result") {
				this.updateTool(block.toolUseId, {
					result: { isError: block.isError, content: block.content },
					endedTs: this.ts,
				});
			} else if (block.type === "text" && block.text.startsWith(INTERRUPT_MARKER)) {
				this.notice("interrupted", "");
			}
		}
	}

	private updateTool(id: string, patch: Partial<ToolCallItem>): void {
		const at = this.tools.get(id);
		if (at === undefined) return;
		this.items[at] = { ...(this.items[at] as ToolCallItem), ...patch };
	}

	private addUserInput(uuid: string, blocks: Block[]): void {
		if (uuid !== "" && this.users.has(uuid)) return;
		let text = "";
		let images = 0;
		for (const block of blocks) {
			if (block.type === "text") text += block.text;
			else if (block.type === "image") images += 1;
		}
		const item: UserItem = { kind: "user", uuid, text, images, state: this.commands.get(uuid) ?? "sent", ts: this.ts };
		this.turnStart ??= this.ts;
		const origin = this.origins.get(uuid);
		if (origin) item.origin = origin;
		if (uuid !== "") this.users.set(uuid, this.items.length);
		this.items.push(item);
	}

	private setCommand(uuid: string, state: CommandState): void {
		if (state === "started" && this.commands.get(uuid) !== "started") this.turnStart = this.ts;
		this.commands.set(uuid, state);
		this.updateUser(uuid, { state });
	}

	private updateUser(uuid: string, patch: Partial<UserItem>): void {
		const at = this.users.get(uuid);
		if (at === undefined) return;
		this.items[at] = { ...(this.items[at] as UserItem), ...patch };
	}

	private openPermission(requestId: string, request: CanUseTool): void {
		if (this.perms.has(requestId)) return;
		const out: PermissionOutcome = { requestId, status: "pending", message: "", updatedPermissions: [] };
		const attached = this.tools.has(request.toolUseId);
		this.perms.set(requestId, { toolUseId: attached ? request.toolUseId : undefined, out });
		this.pending.push({
			requestId,
			toolName: request.toolName,
			toolUseId: request.toolUseId,
			kind: pendingKind(request.toolName),
			input: request.input,
			suggestions: request.suggestions,
			defaultToNo: request.defaultToNo,
		});
		if (attached) this.updateTool(request.toolUseId, { permission: out });
	}

	private answerPermission(response: Extract<ChatEvent, { type: "control_response" }>): void {
		if (!response.ok) {
			this.settle(response.requestId, "denied", response.error, []);
			return;
		}
		const body = isObject(response.response) ? response.response : undefined;
		if (body?.behavior === "allow") {
			const updated = Array.isArray(body.updatedPermissions) ? body.updatedPermissions : [];
			this.settle(response.requestId, "allowed", "", updated);
		} else if (body?.behavior === "deny") {
			this.settle(response.requestId, "denied", typeof body.message === "string" ? body.message : "", []);
		}
	}

	private settle(requestId: string, status: PermissionStatus, message: string, updated: unknown[]): void {
		const perm = this.perms.get(requestId);
		if (!perm || perm.out.status !== "pending") return;
		perm.out = { ...perm.out, status, message, updatedPermissions: updated };
		this.pending = this.pending.filter((p) => p.requestId !== requestId);
		if (perm.toolUseId !== undefined) this.updateTool(perm.toolUseId, { permission: perm.out });
	}

	private applyHost(note: HostNote): void {
		switch (note.kind) {
			case "spawn":
				this.endProcess();
				this.model = modelFlag(note.argv);
				if (note.resume !== "") this.notice("resumed", note.resume);
				else this.notice("spawn", "");
				break;
			case "exit":
				this.endProcess();
				this.notice("exit", note.code === null ? "terminated by signal" : `exit code ${note.code}`);
				break;
			case "user_origin":
				if (!note.origin || note.uuid === "") return;
				this.origins.set(note.uuid, note.origin);
				this.updateUser(note.uuid, { origin: note.origin });
				break;
		}
	}

	private notice(notice: NoticeKind, text: string): void {
		this.items.push({ kind: "notice", notice, text, ts: this.ts });
	}

	private applyTask(event: TaskEvent): void {
		const prev = this.tasks.get(event.taskId);
		this.tasks.set(event.taskId, {
			id: event.taskId,
			toolUseId: event.toolUseId || prev?.toolUseId || "",
			description: prev?.description || event.description,
			taskType: event.taskType || prev?.taskType || "",
			state: event.state ?? prev?.state ?? "running",
			background: event.background ?? prev?.background,
			exitCode: event.exitCode ?? prev?.exitCode,
			summary: event.summary ?? prev?.summary,
			usage: event.usage ?? prev?.usage,
		});
	}

	private endProcess(): void {
		for (const [id, task] of this.tasks) {
			if (unfinished(task.state)) this.tasks.set(id, { ...task, state: "stopped" });
		}
		for (const requestId of this.perms.keys()) this.settle(requestId, "cancelled", "", []);
		for (const [uuid, state] of this.commands) {
			if (live(state)) this.setCommand(uuid, "cancelled");
		}
		for (const [uuid, at] of this.users) {
			if (live((this.items[at] as UserItem).state)) this.setCommand(uuid, "cancelled");
		}
	}
}

export function foldEntries(entries: readonly ChatEntry[]): Transcript {
	const folder = new ChatFolder();
	for (const entry of entries) folder.apply(entry);
	return folder.transcript();
}
