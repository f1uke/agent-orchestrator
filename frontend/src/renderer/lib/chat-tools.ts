import { isObject } from "./chat-event";
import { type DiffLine, diffCounts, lineDiff, newFileDiff } from "./chat-diff";
import type { PermissionOutcome, ToolCallItem, ToolResult } from "./chat-fold";

export type ToolStatus =
	| { tone: "running" }
	| { tone: "ok"; text: string }
	| { tone: "error"; text: string }
	| { tone: "diff"; added: number; removed: number; newFile: boolean };

export type Question = {
	question: string;
	header: string;
	multiSelect: boolean;
	options: Array<{ label: string; description: string; preview: string }>;
};

export type ToolBody =
	| { kind: "bash"; command: string; output: string; failed: boolean }
	| { kind: "diff"; lines: DiffLine[]; hidden: number }
	| { kind: "output"; text: string }
	| { kind: "plan"; plan: string }
	| { kind: "generic"; input: string; output: string };

export type ToolView = {
	family: "bash" | "edit" | "write" | "read" | "search" | "agent" | "plan" | "question" | "other";
	title: string;
	arg: string;
	status: ToolStatus;
	body: ToolBody;
	compact: boolean;
	defaultOpen: boolean;
};

const NEW_FILE_PREVIEW_LINES = 40;

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

function editDiff(input: Record<string, unknown>): DiffLine[] | undefined {
	if (typeof input.old_string === "string" && typeof input.new_string === "string") {
		return lineDiff(input.old_string, input.new_string);
	}
	if (Array.isArray(input.edits)) {
		return input.edits.filter(isObject).flatMap((e) => lineDiff(str(e.old_string), str(e.new_string)));
	}
	return undefined;
}

export function describeTool(call: ToolCallItem, subagentTools = 0): ToolView {
	const input = isObject(call.input) ? call.input : {};
	const output = resultText(call.result);
	const base = { title: call.name, compact: false, defaultOpen: false };

	switch (call.name) {
		case "Bash": {
			const command = str(input.command);
			const exit = /^Exit code (\d+)/.exec(output);
			const failed = call.result?.isError === true;
			const status: ToolStatus = !call.result
				? { tone: "running" }
				: failed
					? { tone: "error", text: exit ? `exit ${exit[1]}` : "error" }
					: { tone: "ok", text: "done" };
			return {
				...base,
				family: "bash",
				arg: command,
				status,
				body: {
					kind: "bash",
					command,
					output: exit ? output.slice(exit[0].length).replace(/^\n/, "") : output,
					failed,
				},
				defaultOpen: failed,
			};
		}
		case "Edit":
		case "MultiEdit": {
			const lines = editDiff(input);
			if (!lines) break;
			const { added, removed } = diffCounts(lines);
			const status: ToolStatus = call.result?.isError
				? { tone: "error", text: "error" }
				: { tone: "diff", added, removed, newFile: false };
			return {
				...base,
				family: "edit",
				arg: str(input.file_path),
				status,
				body: { kind: "diff", lines, hidden: 0 },
				defaultOpen: true,
			};
		}
		case "Write": {
			if (typeof input.content !== "string") break;
			const { lines, hidden } = newFileDiff(input.content, NEW_FILE_PREVIEW_LINES);
			const status: ToolStatus = call.result?.isError
				? { tone: "error", text: "error" }
				: { tone: "diff", added: lineCount(input.content), removed: 0, newFile: true };
			return { ...base, family: "write", arg: str(input.file_path), status, body: { kind: "diff", lines, hidden } };
		}
		case "Read":
			return {
				...base,
				family: "read",
				arg: str(input.file_path),
				status: outcome(call, () => plural(lineCount(output), "line")),
				body: { kind: "output", text: output },
				compact: true,
			};
		case "Grep": {
			const pattern = str(input.pattern);
			const where = str(input.path) || str(input.glob);
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
				arg: str(input.path) ? `${str(input.pattern)} in ${str(input.path)}` : str(input.pattern),
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
				status: outcome(call, () => `done · ${plural(subagentTools, "tool use")}`),
				body: { kind: "output", text: output },
				compact: true,
			};
		}
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

export function describeUpdates(updates: readonly unknown[]): { rules: string[]; mode?: string } {
	const rules: string[] = [];
	let mode: string | undefined;
	for (const update of updates) {
		if (!isObject(update)) continue;
		if (update.type === "setMode" && typeof update.mode === "string") mode = update.mode;
		if (update.type === "addRules" && Array.isArray(update.rules)) {
			for (const rule of update.rules.filter(isObject)) {
				const content = str(rule.ruleContent);
				rules.push(content ? `${str(rule.toolName)}(${content})` : str(rule.toolName));
			}
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
