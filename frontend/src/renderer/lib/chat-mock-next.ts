import type { ChatEntry, Direction } from "./chat-event";

export const MOCK_NEXT_CHAT_SESSION = "demo-chat-next";

export const NEXT_SUGGESTION = "what happens if a worker never calls Cancel?";

const SESSION = "5b21c0de-77a4-4e1c-9a3d-0f6b8d2e4a19";
const START = Date.parse("2026-11-03T09:00:00.000Z");

type Step = [Direction, object];

const asked = (uuid: string, text: string): Step => [
	"in",
	{ type: "user", uuid, session_id: "", message: { role: "user", content: text } },
];
const lifecycle = (uuid: string, state: string): Step => [
	"out",
	{ type: "command_lifecycle", command_uuid: uuid, state },
];
const said = (id: string, text: string): Step => [
	"out",
	{ type: "assistant", session_id: SESSION, message: { id, content: [{ type: "text", text }] } },
];
const finished = (turns: number): Step => [
	"out",
	{ type: "result", subtype: "success", is_error: false, num_turns: turns, total_cost_usd: 0.02 * turns },
];

/** Two finished turns, then claude's guess at the third, as it emits one after the second turn's result. */
export function nextEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		asked("nx-1", "What is a Go context used for?"),
		lifecycle("nx-1", "started"),
		said(
			"msg_nx1",
			"A context carries a deadline, a cancellation signal and request-scoped values across API boundaries and between goroutines.",
		),
		finished(1),
		lifecycle("nx-1", "completed"),
		asked("nx-2", "When should I cancel one?"),
		lifecycle("nx-2", "started"),
		said(
			"msg_nx2",
			"Cancel it as soon as the work it guards is no longer wanted: when the caller returns, when a sibling goroutine fails, or when a timeout has already fired. Always defer the cancel function so it runs on every path.",
		),
		finished(2),
		lifecycle("nx-2", "completed"),
		["out", { type: "prompt_suggestion", suggestion: NEXT_SUGGESTION, uuid: "nx-suggest", session_id: SESSION }],
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}
