import type { ChatEntry, Direction } from "./chat-event";

export const MOCK_FOLD_CHAT_SESSION = "demo-chat-fold";

const SESSION = "5e2a7c41-9d3b-4f86-b0c2-3a1e8d6f9b70";
const START = Date.parse("2026-11-06T09:00:00.000Z");

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

const QUESTION = "Which cache should the report page use?";
const QUESTIONS = {
	questions: [
		{
			question: QUESTION,
			header: "Cache",
			multiSelect: false,
			options: [
				{ label: "Per-request memo (Recommended)", description: "No new state; the slow query runs once per page." },
				{ label: "Shared LRU", description: "Faster on repeat visits, but it needs eviction and invalidation." },
			],
		},
	],
};
const ANSWER = "Per-request memo (Recommended)";

export const FOLD_ANSWER_HEADING = "Where the report page spends its time";

const LONG_ANSWER = [
	`### ${FOLD_ANSWER_HEADING}`,
	"The page runs the same totals query five times, once per widget, and each run scans the whole orders table.",
	[
		"| Widget | Query | Time |",
		"| --- | --- | --- |",
		"| Revenue | totals by month | 410 ms |",
		"| Orders | totals by month | 395 ms |",
		"| Refunds | totals by month | 402 ms |",
		"| Top items | totals by item | 120 ms |",
		"| Regions | totals by region | 98 ms |",
	].join("\n"),
	"Two ways to cut it:",
	[
		"- A per-request memo runs each distinct query once per page.",
		"- A shared LRU also skips the query on repeat visits, at the cost of invalidation.",
	].join("\n"),
	"I recommend the per-request memo: it removes three of the five scans with no new state. Which one should I build?",
].join("\n\n");

export const FOLD_NARRATION = "Looking at how the report page loads its numbers.";
export const FOLD_CLOSING = "Building the per-request memo now, starting with the totals query.";

export function foldAnswerEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		[
			"in",
			{ type: "user", uuid: "f-1", session_id: "", message: { role: "user", content: "Why is the report page slow?" } },
		],
		["out", { type: "command_lifecycle", command_uuid: "f-1", state: "started" }],
		say("msg_f1", FOLD_NARRATION),
		tool("f-r1", "msg_f2", "Read", { file_path: "/work/project/src/report/page.ts" }),
		result("f-r1", "export async function loadReport() {}"),
		tool("f-g1", "msg_f3", "Grep", { pattern: "totalsQuery" }),
		result("f-g1", "src/report/widgets.ts:12\nsrc/report/widgets.ts:31"),
		tool("f-b1", "msg_f4", "Bash", { command: "npm run bench:report", description: "Time the report queries" }),
		result("f-b1", "totals by month x3, totals by item x1, totals by region x1"),
		say("msg_f5", LONG_ANSWER),
		tool("f-ask", "msg_f6", "AskUserQuestion", QUESTIONS),
		[
			"out",
			{
				type: "control_request",
				request_id: "req-f-ask",
				request: {
					subtype: "can_use_tool",
					tool_name: "AskUserQuestion",
					tool_use_id: "f-ask",
					input: QUESTIONS,
					permission_suggestions: [],
				},
			},
		],
		[
			"in",
			{
				type: "control_response",
				response: {
					subtype: "success",
					request_id: "req-f-ask",
					response: { behavior: "allow", updatedInput: { ...QUESTIONS, answers: { [QUESTION]: ANSWER } } },
				},
			},
		],
		result("f-ask", `User has answered your questions: "${QUESTION}"="${ANSWER}".`),
		say("msg_f7", FOLD_CLOSING),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 5, total_cost_usd: 0.05 }],
		["out", { type: "command_lifecycle", command_uuid: "f-1", state: "completed" }],
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 8_000).toISOString(),
		dir,
		payload,
	}));
}
