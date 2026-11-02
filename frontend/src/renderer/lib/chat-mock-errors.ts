import type { ChatEntry, Direction } from "./chat-event";

export const MOCK_ERRORS_CHAT_SESSION = "demo-chat-errors";

const START = Date.parse("2026-10-31T14:00:00.000Z");

export const ERRORS_GATEWAY_TEXT =
	'API Error: 503 [claude/claude-opus-5-5] [429]: {"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed the rate limit for this demo account."},"request_id":"req_demo"} (reset after 1m 45s). This is a server-side issue, usually temporary.';
export const ERRORS_SPEND_TEXT =
	"You've hit your monthly spend limit · raise it at https://example.test/usage · your limit resets 10:30pm (UTC)";

type Step = [Direction, object];

const asked = (uuid: string, text: string): Step => [
	"in",
	{ type: "user", uuid, session_id: "", message: { role: "user", content: text } },
];
const lifecycle = (uuid: string, state: string): Step => [
	"out",
	{ type: "command_lifecycle", command_uuid: uuid, state },
];
const failed = (id: string, error: string, text: string): Step[] => [
	["out", { type: "assistant", error, message: { id, model: "<synthetic>", content: [{ type: "text", text }] } }],
	["out", { type: "result", subtype: "success", is_error: true, num_turns: 1, result: text }],
];

export function errorEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		asked("er-1", "Run the retry budget review."),
		lifecycle("er-1", "started"),
		...failed("msg_er1", "server_error", ERRORS_GATEWAY_TEXT),
		lifecycle("er-1", "completed"),
		asked("er-2", "Try again."),
		lifecycle("er-2", "started"),
		...failed("msg_er2", "rate_limit", ERRORS_SPEND_TEXT),
		lifecycle("er-2", "completed"),
		asked("er-3", "One more time, then stop."),
		lifecycle("er-3", "started"),
		["out", { type: "user", message: { content: [{ type: "text", text: "[Request interrupted by user]" }] } }],
		["out", { type: "result", subtype: "error_during_execution", is_error: true, num_turns: 1 }],
		lifecycle("er-3", "completed"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}
