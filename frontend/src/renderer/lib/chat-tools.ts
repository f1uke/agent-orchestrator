import { isObject, type TaskState, unfinished } from "./chat-event";
import { type CodeView, editView, lineCounts, readView, writeView } from "./chat-code";
import { type ChatImage, imagesOf } from "./chat-images";
import { READ_ME, SHOW_WIDGET } from "./chat-widget";
import type { BackgroundTask, PermissionOutcome, ToolCallItem, ToolResult } from "./chat-fold";

export type ToolStatus =
	| { tone: "running"; text?: string }
	| { tone: "idle"; text: string }
	| { tone: "ok"; text: string }
	| { tone: "error"; text: string }
	| { tone: "diff"; added: number; removed: number };

export type Question = {
	question: string;
	header: string;
	multiSelect: boolean;
	options: Array<{ label: string; description: string; preview: string }>;
};

export type ToolBody =
	| {
			kind: "bash";
			command: string;
			output: string;
			failed: boolean;
			exitCode?: number;
			task?: { id: string; running: boolean };
	  }
	| { kind: "code"; view: CodeView; mode: "write" | "edit" | "read" }
	| { kind: "output"; text: string }
	| { kind: "image"; images: ChatImage[]; path: string }
	| { kind: "plan"; plan: string }
	| { kind: "generic"; input: string; output: string };

export type ToolView = {
	family: "bash" | "edit" | "write" | "read" | "search" | "agent" | "todo" | "plan" | "question" | "other";
	title: string;
	/** For a file card, the base name. The full path stays in `path` for the row's tooltip. */
	arg: string;
	path: string;
	status: ToolStatus;
	body: ToolBody;
	compact: boolean;
};

/** What a card needs beyond the call: the worktree root to shorten paths against, and the call's task. */
export type ToolContext = { root?: string; subagentTools?: number; task?: BackgroundTask };

const TASK_TYPE_LABEL: Record<string, string> = { local_bash: "Bash · background", local_agent: "Agent" };

/** The name for a task in the inspector, whatever claude's raw task_type. */
export const taskLabel = (taskType: string) => TASK_TYPE_LABEL[taskType] ?? "Task";

export const TASK_STATE_LABEL: Record<TaskState, string> = {
	pending: "pending",
	running: "running",
	paused: "paused",
	done: "done",
	error: "failed",
	stopped: "stopped",
};

function escapeRegExp(text: string): string {
	return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** `<root>/x` becomes `x` and a bare `<root>` becomes `.`. A longer path that merely starts with root is left alone. */
export function shortenPaths(text: string, root: string | undefined): string {
	const base = (root ?? "").replace(/\/+$/, "");
	if (base === "") return text;
	const pattern = new RegExp(`(?<![\\w.~/-])${escapeRegExp(base)}(/|(?![\\w./-]))`, "g");
	return text.replace(pattern, (_, tail: string) => (tail === "/" ? "" : "."));
}

export type ProcessKind =
	"commands" | "reads" | "searches" | "edits" | "writes" | "agents" | "skills" | "web" | "todos" | "guides" | "tools";

const PROCESS_KIND: Record<string, ProcessKind> = {
	Bash: "commands",
	Read: "reads",
	Grep: "searches",
	Glob: "searches",
	Edit: "edits",
	MultiEdit: "edits",
	NotebookEdit: "edits",
	Write: "writes",
	Agent: "agents",
	Task: "agents",
	Skill: "skills",
	WebFetch: "web",
	WebSearch: "web",
	TodoWrite: "todos",
	TaskCreate: "todos",
	TaskUpdate: "todos",
	[READ_ME]: "guides",
};

export const processKind = (toolName: string): ProcessKind => PROCESS_KIND[toolName] ?? "tools";

export type LiveTool = { verb: string; since: string };

export type ProcessSummary = { counts: { kind: ProcessKind; count: number }[]; failed: number; live?: LiveTool };

const PHRASE: Record<ProcessKind, (n: number) => string> = {
	commands: (n) => `ran ${n === 1 ? "a command" : `${n} commands`}`,
	reads: (n) => `read ${n === 1 ? "a file" : `${n} files`}`,
	searches: (n) => `ran ${n === 1 ? "a search" : `${n} searches`}`,
	edits: (n) => `edited ${n === 1 ? "a file" : `${n} files`}`,
	writes: (n) => `wrote ${n === 1 ? "a file" : `${n} files`}`,
	agents: (n) => `started ${n === 1 ? "an agent" : `${n} agents`}`,
	skills: (n) => (n === 1 ? "ran a skill" : `ran skill ${n} times`),
	web: (n) => `made ${n === 1 ? "a web request" : `${n} web requests`}`,
	todos: () => "updated todos",
	guides: () => "read visual guidelines",
	tools: (n) => `used ${n === 1 ? "another tool" : `${n} other tools`}`,
};

const capitalize = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

export function processSentence(summary: ProcessSummary): string {
	return capitalize(summary.counts.map(({ kind, count }) => PHRASE[kind](count)).join(", "));
}

const baseName = (path: string) => path.split("/").filter(Boolean).at(-1) ?? path;

const LIVE_VERB: Record<string, (input: Record<string, unknown>) => string> = {
	Bash: () => "Running a command",
	Read: (i) => `Reading ${baseName(str(i.file_path))}`,
	Skill: (i) => `Running skill /${str(i.skill)}`,
	Agent: (i) => (str(i.subagent_type) ? `Running agent ${str(i.subagent_type)}` : "Running an agent"),
	Grep: () => "Searching",
	Glob: () => "Searching",
	Edit: (i) => `Editing ${baseName(str(i.file_path))}`,
	MultiEdit: (i) => `Editing ${baseName(str(i.file_path))}`,
	Write: (i) => `Editing ${baseName(str(i.file_path))}`,
};
LIVE_VERB.Task = LIVE_VERB.Agent;
LIVE_VERB[SHOW_WIDGET] = () => "Making a visual";
LIVE_VERB[READ_ME] = () => "Reading visual guidelines";

export function liveVerb(call: ToolCallItem): string {
	const say = LIVE_VERB[call.name];
	return say ? say(isObject(call.input) ? call.input : {}) : `Running ${call.name}`;
}

export const isAgentTask = (task: BackgroundTask) => task.taskType.includes("agent");

export function waitingVerb(tasks: readonly BackgroundTask[]): string {
	if (tasks.length === 1) {
		const [task] = tasks;
		const what = isAgentTask(task) ? "agent" : "background command";
		return task.description ? `Waiting on ${what} "${task.description}"` : `Waiting on 1 ${what}`;
	}
	const agents = tasks.filter(isAgentTask).length;
	const noun = agents === tasks.length ? "agents" : agents === 0 ? "background commands" : "background tasks";
	return `Waiting on ${tasks.length} ${noun}`;
}

export function toolRunning(call: ToolCallItem): boolean {
	return !call.result && call.permission?.status !== "denied" && call.permission?.status !== "cancelled";
}

/** A tool that did not run to success: its result errored, the user denied it, or its background task failed. */
export function toolFailed(call: ToolCallItem, task?: BackgroundTask): boolean {
	return call.result?.isError === true || call.permission?.status === "denied" || task?.state === "error";
}

/** What a call touches, so that three edits of one file count as one file. */
function countKey(call: ToolCallItem): string {
	const input = isObject(call.input) ? call.input : {};
	const kind = processKind(call.name);
	const path = kind === "reads" || kind === "edits" || kind === "writes" ? str(input.file_path) : "";
	return path === "" ? call.id : `${kind}:${path}`;
}

const KIND_ORDER: ProcessKind[] = [
	"commands",
	"reads",
	"searches",
	"edits",
	"writes",
	"agents",
	"skills",
	"web",
	"todos",
	"guides",
	"tools",
];

export function summarizeTools(
	calls: readonly ToolCallItem[],
	taskOf: (toolUseId: string) => BackgroundTask | undefined = () => undefined,
): ProcessSummary {
	const seen = new Map<ProcessKind, Set<string>>();
	let failed = 0;
	for (const call of calls) {
		const kind = processKind(call.name);
		seen.set(kind, (seen.get(kind) ?? new Set()).add(countKey(call)));
		if (toolFailed(call, taskOf(call.id))) failed += 1;
	}
	const counts = KIND_ORDER.filter((kind) => seen.has(kind)).map((kind) => ({ kind, count: seen.get(kind)!.size }));
	return { counts, failed };
}

const NEW_FILE_PREVIEW_LINES = 40;
const READ_PREVIEW_LINES = 200;

function str(value: unknown): string {
	return typeof value === "string" ? value : "";
}

export function resultText(result: ToolResult | undefined): string {
	if (!result) return "";
	if (typeof result.content === "string") return result.content;
	if (Array.isArray(result.content)) {
		return result.content
			.map((block) => (isObject(block) && block.type === "text" ? str(block.text) : ""))
			.filter((text) => text !== "")
			.join("\n");
	}
	return "";
}

function lineCount(text: string): number {
	return text === "" ? 0 : text.replace(/\n$/, "").split("\n").length;
}

function plural(n: number, one: string, many = `${one}s`): string {
	return `${n} ${n === 1 ? one : many}`;
}

function outcome(call: ToolCallItem, ok: () => string): ToolStatus {
	if (!call.result) return { tone: "running" };
	if (call.result.isError) return { tone: "error", text: "error" };
	return { tone: "ok", text: ok() };
}

export function parseQuestions(input: unknown): Question[] {
	if (!isObject(input) || !Array.isArray(input.questions)) return [];
	return input.questions.filter(isObject).map((q) => ({
		question: str(q.question),
		header: str(q.header),
		multiSelect: q.multiSelect === true,
		options: (Array.isArray(q.options) ? q.options : [])
			.filter(isObject)
			.map((o) => ({ label: str(o.label), description: str(o.description), preview: str(o.preview) })),
	}));
}

export function parsePlan(input: unknown): { plan: string; path: string } {
	return isObject(input) ? { plan: str(input.plan), path: str(input.planFilePath) } : { plan: "", path: "" };
}

function backgroundStatus(call: ToolCallItem, task: BackgroundTask | undefined): ToolStatus {
	if (!call.result) return { tone: "running" };
	if (call.result.isError) return { tone: "error", text: "error" };
	if (!task) return { tone: "ok", text: "started in background" };
	const exit = task.exitCode === undefined ? "" : ` · exit ${task.exitCode}`;
	switch (task.state) {
		case "done":
			return { tone: "ok", text: `done${exit}` };
		case "error":
			return { tone: "error", text: `failed${exit}` };
		case "stopped":
			return { tone: "idle", text: "stopped" };
		default:
			return { tone: "running", text: "running in background" };
	}
}

function agentStatus(call: ToolCallItem, ctx: ToolContext): ToolStatus {
	const { task } = ctx;
	if (!task) return outcome(call, () => `done · ${plural(ctx.subagentTools ?? 0, "tool use")}`);
	switch (task.state) {
		case "done":
			return { tone: "ok", text: `done · ${plural(ctx.subagentTools || task.usage?.toolUses || 0, "tool use")}` };
		case "error":
			return { tone: "error", text: TASK_STATE_LABEL.error };
		case "stopped":
			return { tone: "idle", text: TASK_STATE_LABEL.stopped };
		default:
			return { tone: "running" };
	}
}

function todoArg(call: ToolCallItem, input: Record<string, unknown>): string {
	if (call.name === "TaskCreate") return str(input.subject);
	if (call.name === "TaskUpdate") {
		const id = str(input.taskId);
		return `${id ? `#${id} ` : ""}${str(input.status) || str(input.subject)}`.trim();
	}
	return Array.isArray(input.todos) ? plural(input.todos.length, "item") : "";
}

const TODO_TITLE: Record<string, string> = { TaskCreate: "Add todo", TaskUpdate: "Update todo", TodoWrite: "Todos" };

export function describeTool(call: ToolCallItem, ctx: ToolContext = {}): ToolView {
	const input = isObject(call.input) ? call.input : {};
	const output = resultText(call.result);
	const base = { title: call.name, compact: false, path: "" };
	const short = (text: string) => shortenPaths(text, ctx.root);

	switch (call.name) {
		case "Bash": {
			const command = short(str(input.command));
			const description = str(input.description);
			const exit = /^Exit code (\d+)/.exec(output);
			const failed = call.result?.isError === true;
			const status: ToolStatus =
				input.run_in_background === true
					? backgroundStatus(call, ctx.task)
					: !call.result
						? { tone: "running" }
						: failed
							? { tone: "error", text: "Failed" }
							: { tone: "ok", text: "done" };
			return {
				...base,
				title: description || command,
				family: "bash",
				arg: description ? command : "",
				status,
				body: {
					kind: "bash",
					command,
					output: exit ? output.slice(exit[0].length).replace(/^\n/, "") : output,
					failed,
					exitCode: exit ? Number(exit[1]) : undefined,
					task:
						input.run_in_background === true && ctx.task
							? { id: ctx.task.id, running: unfinished(ctx.task.state) }
							: undefined,
				},
			};
		}
		case "Edit":
		case "MultiEdit": {
			if (typeof input.old_string !== "string" && !Array.isArray(input.edits)) break;
			const path = short(str(input.file_path));
			const view = editView(path, input, call.result);
			return {
				...base,
				title: "Edited",
				family: "edit",
				arg: baseName(path),
				path,
				status: call.result?.isError ? { tone: "error", text: "error" } : { tone: "diff", ...lineCounts(view) },
				body: { kind: "code", view, mode: "edit" },
			};
		}
		case "Write": {
			if (typeof input.content !== "string") break;
			const path = short(str(input.file_path));
			const view = writeView(path, input.content, NEW_FILE_PREVIEW_LINES);
			return {
				...base,
				title: "Created",
				family: "write",
				arg: baseName(path),
				path,
				status: call.result?.isError
					? { tone: "error", text: "error" }
					: { tone: "diff", added: view.lines.length + view.hidden, removed: 0 },
				body: { kind: "code", view, mode: "write" },
			};
		}
		case "Read": {
			const path = short(str(input.file_path));
			const images = imagesOf(call.result?.content);
			const code = call.result?.isError || images.length ? undefined : readView(path, output, READ_PREVIEW_LINES);
			const file = { arg: baseName(path), path };
			if (code && code.lines.length > 0) {
				return {
					...base,
					...file,
					family: "read",
					status: outcome(call, () => plural(code.lines.length + code.hidden, "line")),
					body: { kind: "code", view: code, mode: "read" },
					compact: true,
				};
			}
			return {
				...base,
				...file,
				family: "read",
				status: outcome(call, () =>
					images.length
						? images.length === 1
							? "image"
							: plural(images.length, "image")
						: plural(lineCount(output), "line"),
				),
				body: images.length ? { kind: "image", images, path } : { kind: "output", text: output },
				compact: true,
			};
		}
		case "Grep": {
			const pattern = str(input.pattern);
			const where = short(str(input.path) || str(input.glob));
			const found = /^Found \d+ (?:files?|match(?:es)?|lines?)/.exec(output);
			return {
				...base,
				family: "search",
				arg: where ? `"${pattern}" in ${where}` : `"${pattern}"`,
				status: outcome(call, () => (found ? found[0].replace(/^Found /, "") : plural(lineCount(output), "line"))),
				body: { kind: "output", text: output },
				compact: true,
			};
		}
		case "Glob":
			return {
				...base,
				family: "search",
				arg: str(input.path) ? `${str(input.pattern)} in ${short(str(input.path))}` : str(input.pattern),
				status: outcome(call, () => plural(lineCount(output), "file")),
				body: { kind: "output", text: output },
				compact: true,
			};
		case "Agent":
		case "Task": {
			const kind = str(input.subagent_type) || "agent";
			const description = str(input.description);
			return {
				...base,
				title: "Agent",
				family: "agent",
				arg: description ? `${kind} · "${description}"` : kind,
				status: agentStatus(call, ctx),
				body: { kind: "output", text: ctx.task?.summary || output },
				compact: true,
			};
		}
		case "TaskCreate":
		case "TaskUpdate":
		case "TodoWrite":
			return {
				...base,
				title: TODO_TITLE[call.name],
				family: "todo",
				arg: todoArg(call, input),
				status: outcome(call, () => "done"),
				body: { kind: "generic", input: JSON.stringify(call.input ?? null, null, 2), output },
				compact: true,
			};
		case "ExitPlanMode":
			return {
				...base,
				title: "Plan",
				family: "plan",
				arg: parsePlan(call.input).path,
				status: outcome(call, () => "done"),
				body: { kind: "plan", plan: parsePlan(call.input).plan },
			};
		case "AskUserQuestion": {
			const questions = parseQuestions(call.input);
			return {
				...base,
				title: "Question",
				family: "question",
				arg: questions.map((q) => q.header || q.question).join(" · "),
				status: outcome(call, () => "answered"),
				body: { kind: "output", text: output },
				compact: true,
			};
		}
	}
	return {
		...base,
		family: "other",
		arg: "",
		status: outcome(call, () => "done"),
		body: { kind: "generic", input: JSON.stringify(call.input ?? null, null, 2), output },
	};
}

/** A permission rule as claude's settings write it: `Bash(go test:*)`, or the bare tool name. */
export function ruleLabel(rule: Record<string, unknown>): string {
	const content = str(rule.ruleContent);
	return content ? `${str(rule.toolName)}(${content})` : str(rule.toolName);
}

export function describeUpdates(updates: readonly unknown[]): { rules: string[]; mode?: string } {
	const rules: string[] = [];
	let mode: string | undefined;
	for (const update of updates) {
		if (!isObject(update)) continue;
		if (update.type === "setMode" && typeof update.mode === "string") mode = update.mode;
		if (update.type === "addRules" && Array.isArray(update.rules)) {
			for (const rule of update.rules.filter(isObject)) rules.push(ruleLabel(rule));
		}
	}
	return { rules, mode };
}

const SETTINGS_FILE: Record<string, string> = {
	localSettings: ".claude/settings.local.json",
	projectSettings: ".claude/settings.json",
	userSettings: "~/.claude/settings.json",
};

export type SuggestionView = { verb: string; code: string; after: string; file?: string };

export function describeSuggestions(suggestions: readonly unknown[]): SuggestionView[] {
	const views: SuggestionView[] = [];
	for (const s of suggestions) {
		if (!isObject(s)) continue;
		const destination = str(s.destination);
		const file = SETTINGS_FILE[destination];
		const scope = destination === "session" ? " for this session" : "";
		if (s.type === "addRules") {
			const { rules } = describeUpdates([s]);
			if (rules.length) views.push({ verb: "adds", code: rules.join(", "), after: scope, file });
		} else if (s.type === "setMode" && typeof s.mode === "string") {
			views.push({ verb: "switches to", code: s.mode, after: ` mode${scope}`, file });
		} else if (s.type === "addDirectories" && Array.isArray(s.directories)) {
			const dirs = s.directories.filter((d): d is string => typeof d === "string");
			if (dirs.length) views.push({ verb: "adds the directory", code: dirs.join(", "), after: scope, file });
		}
	}
	return views;
}

export function parseAnswers(call: ToolCallItem): string[] {
	const text = resultText(call.result);
	const answers = new Map<string, string>();
	for (const match of text.matchAll(/"([^"]*)"="([^"]*)"/g)) answers.set(match[1], match[2]);
	return parseQuestions(call.input)
		.filter((q) => answers.has(q.question))
		.map((q) => `${q.header || q.question} = ${answers.get(q.question)}`);
}

export type Settled = { tone: "ok" | "no" | "muted"; text: string; code?: string };

const withMessage = (text: string, message: string) => (message ? `${text}: ${message}` : text);

export function describeSettled(call: ToolCallItem, permission: PermissionOutcome): Settled {
	if (permission.status === "cancelled") return { tone: "muted", text: "Request withdrawn before it was answered" };
	const denied = permission.status === "denied";
	const { rules, mode } = describeUpdates(permission.updatedPermissions);
	switch (call.name) {
		case "AskUserQuestion": {
			if (denied) return { tone: "no", text: withMessage("Declined", permission.message) };
			const answers = parseAnswers(call);
			return { tone: "ok", text: answers.length ? `Answered: ${answers.join(" · ")}` : "Answered" };
		}
		case "ExitPlanMode":
			if (denied) return { tone: "no", text: withMessage("Plan sent back", permission.message) };
			return { tone: "ok", text: mode ? `Plan approved · now in ${mode} mode` : "Plan approved" };
	}
	if (denied) return { tone: "no", text: withMessage(`Denied ${call.name}`, permission.message) };
	if (rules.length) return { tone: "ok", text: `Always allowed ${call.name} with`, code: rules.join(", ") };
	if (mode) return { tone: "ok", text: `Allowed ${call.name} and switched to ${mode} mode` };
	return { tone: "ok", text: `Allowed ${call.name} once` };
}
