// Reads what a worker actually did from its event log: the stream-json a
// `claude -p` run prints, or the session .jsonl Claude Code writes. Both carry
// assistant messages whose content holds the tool_use blocks.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

export function readEvents(path) {
	return readFileSync(path, "utf8")
		.split("\n")
		.filter((line) => line.startsWith("{"))
		.map((line) => JSON.parse(line));
}

export function toolCalls(events) {
	const calls = [];
	for (const event of events) {
		if (event.type !== "assistant" || event.parent_tool_use_id) continue;
		for (const block of event.message?.content ?? []) {
			if (block.type === "tool_use") calls.push({ name: block.name, input: block.input ?? {} });
		}
	}
	return calls;
}

export function firstText(events) {
	for (const event of events) {
		if (event.type !== "assistant" || event.parent_tool_use_id) continue;
		for (const block of event.message?.content ?? []) {
			if (block.type === "text" && block.text.trim()) return block.text.trim();
		}
	}
	return "";
}

export function lastText(events) {
	let text = "";
	for (const event of events) {
		if (event.type !== "assistant" || event.parent_tool_use_id) continue;
		for (const block of event.message?.content ?? []) {
			if (block.type === "text" && block.text.trim()) text = block.text.trim();
		}
	}
	return text;
}

export function resultOf(events) {
	return events.findLast((e) => e.type === "result") ?? null;
}

export function pstackPlaybooksDir() {
	const config = process.env.CLAUDE_CONFIG_DIR ?? join(homedir(), ".claude");
	const installed = JSON.parse(readFileSync(join(config, "plugins", "installed_plugins.json"), "utf8"));
	const entry = Object.entries(installed.plugins).find(([name]) => name.startsWith("pstack@"));
	if (!entry) throw new Error("pstack is not installed");
	return join(entry[1][0].installPath, "skills", "poteto-mode", "playbooks");
}

const normalize = (text) =>
	text
		.toLowerCase()
		.replace(/[*`_>#"'()[\]]/g, " ")
		.replace(/[^a-z0-9 ]+/g, " ")
		.replace(/\s+/g, " ")
		.trim();

// A step counts as copied when its first six words appear in the todolist.
export function playbookSteps(name) {
	const text = readFileSync(join(pstackPlaybooksDir(), `${name}.md`), "utf8");
	return [...text.matchAll(/^\d+\.\s+(.+)$/gm)].map((m) => normalize(m[1]).split(" ").slice(0, 6).join(" "));
}

const commandOf = (call) => (call.name === "Bash" ? String(call.input.command ?? "") : "");
const pathOf = (call) => String(call.input.file_path ?? call.input.path ?? call.input.notebook_path ?? "");

export function playbookRead(call) {
	const target = call.name === "Read" ? pathOf(call) : commandOf(call);
	const m = target.match(/poteto-mode\/playbooks\/([a-z-]+)\.md/);
	return m && (call.name === "Read" || /\b(cat|sed|head|less|bat)\b/.test(target)) ? m[1] : null;
}

// The text a call adds to a todolist, or null when it is not a todolist write.
export function todoText(call) {
	if (call.name === "TaskCreate") return `${call.input.subject ?? ""}\n${call.input.description ?? ""}`;
	if (call.name === "TodoWrite") return (call.input.todos ?? []).map((t) => t.content ?? "").join("\n");
	if ((call.name === "Write" || call.name === "Edit") && /todo\.md$/i.test(pathOf(call)))
		return `${call.input.content ?? ""}${call.input.new_string ?? ""}`;
	if (call.name === "Bash" && /todo\.md/i.test(commandOf(call)) && /<<|>|tee/.test(commandOf(call)))
		return commandOf(call);
	return null;
}

// Places a worker reads or writes that are not the task's code: installed
// skills and plugins, the AO knowledge store, its own todo.md.
const OUTSIDE_REPO = /\.claude\/(plugins|skills)|\.ao\/knowledge|todo\.md/;

const SETUP_TOOLS = new Set(["Skill", "ToolSearch", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet", "TodoWrite"]);

// Code exploration: anything that looks at or changes the task's repository.
// Reading the skill, its playbooks, or writing the todolist is set-up, not
// exploration.
export function isExploration(call) {
	if (SETUP_TOOLS.has(call.name) || todoText(call) !== null || playbookRead(call)) return false;
	const target = call.name === "Bash" ? commandOf(call) : pathOf(call);
	return !OUTSIDE_REPO.test(target) && !/^\s*ao\s/.test(target);
}

// Walks one turn's calls and reports the set-up it did and when.
// `playbookAlreadyRead` lets a later turn count a playbook read in an earlier one.
export function analyzeTurn(
	calls,
	correctPlaybook,
	{ steps = playbookSteps(correctPlaybook), playbookAlreadyRead = false } = {},
) {
	steps = steps.map((s) => normalize(s).split(" ").slice(0, 6).join(" "));
	let todo = "";
	const out = {
		toolCalls: calls.length,
		playbooksRead: [],
		playbookReadAt: null,
		correctReadAt: null,
		firstExplorationAt: null,
		todoAt: null,
		stepsCoveredAt: null,
		stepsCovered: 0,
		stepsTotal: steps.length,
		todoTool: null,
	};
	calls.forEach((call, i) => {
		const pb = playbookRead(call);
		if (pb) {
			out.playbooksRead.push(pb);
			out.playbookReadAt ??= i;
			if (pb === correctPlaybook) out.correctReadAt ??= i;
		}
		const text = todoText(call);
		if (text !== null) {
			todo += `\n${normalize(text)}`;
			out.todoAt ??= i;
			out.todoTool ??= call.name;
			const covered = steps.filter((s) => todo.includes(s)).length;
			out.stepsCovered = Math.max(out.stepsCovered, covered);
			if (covered === steps.length) out.stepsCoveredAt ??= i;
		}
		if (out.firstExplorationAt === null && isExploration(call)) out.firstExplorationAt = i;
	});
	if (playbookAlreadyRead) out.correctReadAt ??= -1;
	const before = (at) => at !== null && (out.firstExplorationAt === null || at < out.firstExplorationAt);
	out.setupDone = out.correctReadAt !== null && out.stepsCoveredAt !== null;
	out.setupBeforeExploration = before(out.correctReadAt) && before(out.stepsCoveredAt);
	return out;
}

// What the Stop hook and the nudge decide on: AO cannot know which playbook is
// right, only whether some playbook was read and a todolist exists.
export function anySetup(calls) {
	return calls.some((c) => playbookRead(c)) && calls.some((c) => todoText(c) !== null);
}
