import {
	type Block,
	type CanUseTool,
	type ChatEntry,
	type ChatEvent,
	type CommandState,
	type HostNote,
	type Origin,
	isObject,
	parseEvent,
	parseHostNote,
} from "./chat-event";

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
};

export type ToolCallItem = {
	kind: "tool_call";
	id: string;
	name: string;
	parentToolUseId: string;
	input: unknown;
	result?: ToolResult;
	permission?: PermissionOutcome;
};

export type ChatItem =
	| UserItem
	| { kind: "assistant_text"; messageId: string; text: string }
	| { kind: "thinking"; text: string }
	| ToolCallItem
	| { kind: "turn_end"; subtype: string; isError: boolean; numTurns: number; costUsd: number }
	| { kind: "notice"; notice: NoticeKind; text: string };

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

export type Transcript = {
	claudeSessionIds: string[];
	items: ChatItem[];
	pending: Pending[];
	queued: string[];
	busy: boolean;
	permissionMode: string;
	/** The latest result's total_cost_usd, cumulative for the claude process. */
	costUsd: number;
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
			lastSeq: this.last,
		};
	}

	private applyOut(event: ChatEvent): void {
		switch (event.type) {
			case "system_init":
				if (!this.sessions.includes(event.sessionId)) this.sessions.push(event.sessionId);
				if (event.permissionMode !== "") this.mode = event.permissionMode;
				break;
			case "system":
				if (event.permissionMode !== "") this.mode = event.permissionMode;
				if (event.subtype === "compact_boundary") this.items.push({ kind: "notice", notice: "compacted", text: "" });
				break;
			case "assistant":
				this.applyAssistant(event.messageId, event.parentToolUseId, event.blocks);
				break;
			case "user":
				if (!event.isReplay) this.applyToolResults(event.blocks);
				break;
			case "result":
				this.cost = event.totalCostUsd;
				this.items.push({
					kind: "turn_end",
					subtype: event.subtype,
					isError: event.isError,
					numTurns: event.numTurns,
					costUsd: event.totalCostUsd,
				});
				break;
			case "control_request":
				if (event.canUseTool) this.openPermission(event.requestId, event.canUseTool);
				break;
			case "control_response":
				this.answerPermission(event);
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
	}

	private applyAssistant(messageId: string, parentToolUseId: string, blocks: Block[]): void {
		for (const block of blocks) {
			if (block.type === "text") {
				this.items.push({ kind: "assistant_text", messageId, text: block.text });
			} else if (block.type === "thinking") {
				this.items.push({ kind: "thinking", text: block.text });
			} else if (block.type === "tool_use" && !this.tools.has(block.id)) {
				this.tools.set(block.id, this.items.length);
				this.items.push({ kind: "tool_call", id: block.id, name: block.name, parentToolUseId, input: block.input });
			}
		}
	}

	private applyToolResults(blocks: Block[]): void {
		for (const block of blocks) {
			if (block.type === "tool_result") {
				this.updateTool(block.toolUseId, { result: { isError: block.isError, content: block.content } });
			} else if (block.type === "text" && block.text.startsWith(INTERRUPT_MARKER)) {
				this.items.push({ kind: "notice", notice: "interrupted", text: "" });
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
		const item: UserItem = { kind: "user", uuid, text, images, state: this.commands.get(uuid) ?? "sent" };
		const origin = this.origins.get(uuid);
		if (origin) item.origin = origin;
		if (uuid !== "") this.users.set(uuid, this.items.length);
		this.items.push(item);
	}

	private setCommand(uuid: string, state: CommandState): void {
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
				this.items.push(
					note.resume !== ""
						? { kind: "notice", notice: "resumed", text: note.resume }
						: { kind: "notice", notice: "spawn", text: "" },
				);
				break;
			case "exit":
				this.endProcess();
				this.items.push({
					kind: "notice",
					notice: "exit",
					text: note.code === null ? "terminated by signal" : `exit code ${note.code}`,
				});
				break;
			case "user_origin":
				if (!note.origin || note.uuid === "") return;
				this.origins.set(note.uuid, note.origin);
				this.updateUser(note.uuid, { origin: note.origin });
				break;
		}
	}

	private endProcess(): void {
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
