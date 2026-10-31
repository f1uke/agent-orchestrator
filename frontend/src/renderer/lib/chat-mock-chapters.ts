import type { ChatEntry, Direction } from "./chat-event";
import { MARK_CHAPTER } from "./chat-chapters";
import { WRAP_UP } from "./chat-wrapup";

export const MOCK_CHAPTERS_CHAT_SESSION = "demo-chat-chapters";

const SESSION = "8c1d9f2e-3b6a-4d70-a5e1-7f0b2c9d4e33";
const START = Date.parse("2026-10-31T09:00:00.000Z");

type Step = [Direction, object];

const tool = (id: string, messageId: string, name: string, input: object): Step => [
	"out",
	{
		type: "assistant",
		session_id: SESSION,
		message: { id: messageId, content: [{ type: "tool_use", id, name, input }] },
	},
];
const result = (id: string, content: string): Step => [
	"out",
	{
		type: "user",
		session_id: SESSION,
		message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content, is_error: false }] },
	},
];
const say = (messageId: string, text: string): Step => [
	"out",
	{ type: "assistant", session_id: SESSION, message: { id: messageId, content: [{ type: "text", text }] } },
];
const asked = (uuid: string, text: string): Step => [
	"in",
	{ type: "user", uuid, session_id: "", message: { role: "user", content: text } },
];
const lifecycle = (uuid: string, state: string): Step => [
	"out",
	{ type: "command_lifecycle", command_uuid: uuid, state },
];
const finish = (turns: number): Step => [
	"out",
	{ type: "result", subtype: "success", is_error: false, num_turns: turns, total_cost_usd: 0.04 },
];

const FINDINGS = [
	"The fold already keeps tool inputs, so the renderer reads them directly.",
	"Four places decide how a tool call reaches the screen:",
	[
		"`chat-fold.ts` pairs each call with its result and keeps the raw input.",
		"`chat-rows.ts` turns the transcript into rows and folds a finished turn into one line.",
		"`chat-tools.ts` words the summary line for a group of calls.",
		"`ChatPane.tsx` draws each row, and a few tools get a row of their own.",
		"`chat-rail.ts` lists the turns for the rail in the right gutter.",
		"`chat-inspect.ts` derives the task list from TaskCreate and TaskUpdate.",
	]
		.map((line) => `- ${line}`)
		.join("\n"),
	"A tool that has something to show on its own, such as a visual, must stay out of the fold. Otherwise it disappears the moment the turn ends and the reader has to open the fold to find it. The same holds for a chapter divider and for the wrap-up box, which is why one predicate decides what stays in view.",
	"A chapter divider also has to split the fold. If the whole turn folded into one line, the divider would sit above a single summary and say nothing about where each part of the work begins. One fold per chapter keeps the dividers in order with the work they name.",
	"Things to check once the renderer exists:",
	[
		"The divider keeps its place when a turn folds and when it unfolds.",
		"The contents list stays out of the turn rail and out of the text column.",
		"The task list moves to the newest turn after the next message.",
		"Only the latest wrap-up box has live reply buttons.",
		"A reply fills the composer and never sends.",
	]
		.map((line) => `- ${line}`)
		.join("\n"),
	"None of these need a new tool. The three tools from the previous change already carry everything the screen needs, so the work is in how rows are chosen and drawn.",
].join("\n\n");

const TASKS = [
	"Read the chat fold",
	"Add the sandboxed frame",
	"Wire sendPrompt to the composer",
	"Screenshot at 1920 and 2560",
];

export function chapterEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		asked("c-1", "Build the widget renderer"),
		lifecycle("c-1", "started"),
		tool("c-ch1", "msg_c1", MARK_CHAPTER, {
			title: "Codebase exploration",
			summary: "Find where tool calls become rows",
		}),
		result("c-ch1", 'Chapter marked: "Codebase exploration". Continue your work.'),
		tool("c-r1", "msg_c2", "Read", { file_path: "/work/project/src/chat-fold.ts" }),
		result("c-r1", "export class ChatFolder {}"),
		tool("c-r2", "msg_c3", "Read", { file_path: "/work/project/src/chat-rows.ts" }),
		result("c-r2", "export function chatRows() {}"),
		tool("c-g1", "msg_c4", "Grep", { pattern: "OWN_ROW_TOOLS" }),
		result("c-g1", "src/chat-rows.ts:42"),
		say("msg_c5", FINDINGS),
		finish(3),
		lifecycle("c-1", "completed"),
		asked("c-2", "Go ahead and build it"),
		lifecycle("c-2", "started"),
		tool("c-ch2", "msg_c6", MARK_CHAPTER, { title: "Widget renderer", summary: "Frame, bridge and composer prefill" }),
		result("c-ch2", 'Chapter marked: "Widget renderer". Continue your work.'),
		...TASKS.flatMap((subject, i): Step[] => [
			tool(`c-tc${i}`, `msg_c7${i}`, "TaskCreate", { subject, description: subject, activeForm: subject }),
			result(`c-tc${i}`, `Task #${i + 1} created successfully: ${subject}`),
		]),
		tool("c-tu1", "msg_c8", "TaskUpdate", { taskId: "1", status: "completed" }),
		result("c-tu1", "Updated task #1 status"),
		tool("c-tu2", "msg_c9", "TaskUpdate", { taskId: "2", status: "completed" }),
		result("c-tu2", "Updated task #2 status"),
		tool("c-tu3", "msg_c10", "TaskUpdate", { taskId: "3", status: "in_progress" }),
		result("c-tu3", "Updated task #3 status"),
		tool("c-e1", "msg_c11", "Edit", {
			file_path: "/work/project/src/ChatWidget.tsx",
			old_string: "return null;",
			new_string: "return <iframe />;",
		}),
		result("c-e1", "The file has been updated."),
		tool("c-b1", "msg_c12", "Bash", { command: "npm test chat-widget", description: "Run the widget tests" }),
		result("c-b1", "14 passed"),
		tool("c-wrap", "msg_c13", WRAP_UP, {
			recap: "The widget frame renders in a sandbox with no same-origin access. Tests pass; nothing is committed yet.",
			ask: "Commit these changes?",
			replies: ["Yes, commit", "Not yet"],
		}),
		result("c-wrap", "Box shown. Write your closing message now."),
		say("msg_c14", "The renderer is in and its 14 tests pass."),
		finish(8),
		lifecycle("c-2", "completed"),
		asked("c-3", "Now check it in a browser"),
		lifecycle("c-3", "started"),
		tool("c-ch3", "msg_c15", MARK_CHAPTER, { title: "Verification" }),
		result("c-ch3", 'Chapter marked: "Verification". Continue your work.'),
		tool("c-v1", "msg_c16", "TaskUpdate", { taskId: "3", status: "in_progress" }),
		result("c-v1", "Updated task #3 status"),
		tool("c-v3", "msg_c18", "Bash", {
			command: "npx playwright test chat-widgets",
			description: "Run the browser spec",
		}),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 1_500).toISOString(),
		dir,
		payload,
	}));
}
