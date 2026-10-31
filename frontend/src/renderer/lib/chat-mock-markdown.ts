import type { ChatEntry, Direction } from "./chat-event";

export const MOCK_MARKDOWN_CHAT_SESSION = "demo-chat-markdown";

const SESSION = "8c1d4e7a-2b6f-4a90-9e35-71f0c2d8b4a6";
const START = Date.parse("2026-10-31T11:00:00.000Z");

type Step = [Direction, object];

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

export const MARKDOWN_LONG_LINE =
	'\tlog.Printf("deliver %s: attempt %d of %d failed after %s, next wait %s, parked=%t, endpoint=%s", ev.ID, attempt, s.budget.Max, elapsed, wait, parked, s.endpoint)';

const REPLY = [
	"# Retry budget review",
	"",
	"The sender now stops after **five attempts**, waits with *full jitter*, and ~~sleeps a fixed 2s~~ no longer blocks on a fixed sleep. The helper is `Budget.Delay`.",
	"",
	"## What changed",
	"",
	"1. `Sender.Deliver` asks the budget before each attempt",
	"2. A spent budget parks the event",
	"   - the dead-letter row keeps the attempt count",
	"   - `ao` shows parked events in the summary",
	"3. The soak test runs 50 times",
	"",
	"### Checklist",
	"",
	"- [x] Budget type with full jitter",
	"- [x] Dead-letter count fixed",
	"- [ ] Dashboard panel for parked events",
	"",
	"```go",
	"func (s *Sender) Deliver(ctx context.Context, ev Event) error {",
	"\tfor attempt := 0; s.budget.Allow(attempt); attempt++ {",
	MARKDOWN_LONG_LINE,
	"\t}",
	"\treturn ErrGaveUp",
	"}",
	"```",
	"",
	"> The budget is per event, not per endpoint. A noisy endpoint cannot starve the others.",
	"",
	"| Attempt | Ceiling | Observed p50 | Observed p99 | Parked after | Endpoint | Notes on the run |",
	"| ---: | ---: | ---: | ---: | :---: | --- | --- |",
	"| 1 | 200ms | 104ms | 198ms | no | billing-webhook.internal.example | first try, warm connection pool |",
	"| 2 | 400ms | 210ms | 391ms | no | billing-webhook.internal.example | one retry after a 503 from the gateway |",
	"| 5 | 3.2s | 1.6s | 3.1s | yes | billing-webhook.internal.example | budget spent, event parked for replay |",
	"",
	"---",
	"",
	"Links: [the PR](https://github.com/example/billing/pull/42), [mail the owner](mailto:owner@example.com), and [a script link](javascript:alert(1)) that must stay text.",
	"",
	'<script>alert("raw html stays text")</script>',
].join("\n");

const STREAMING_TAIL = [
	"Next, the replay command. It reads parked events in order:",
	"",
	"```go",
	"func Replay(ctx context.Context, store *Store) error {",
	"\tfor ev := range store.Parked(ctx) {",
	"\t\tif err := ",
].join("\n");

export function markdownEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		asked("md-1", "Summarise the retry budget change."),
		lifecycle("md-1", "started"),
		say("msg_md1", REPLY),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 1, total_cost_usd: 0.02 }],
		lifecycle("md-1", "completed"),
		asked("md-2", "And the replay command?"),
		lifecycle("md-2", "started"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}

/** The reply to the second question, streamed word by word, stopping inside an open fence and never settling. */
export function markdownDeltas(): unknown[] {
	const event = (e: object) => ({ type: "stream_event", session_id: SESSION, parent_tool_use_id: null, event: e });
	const words = STREAMING_TAIL.match(/\S+\s*|\s+/g) ?? [];
	return [
		event({ type: "message_start", message: { id: "msg_md2" } }),
		event({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } }),
		...words.map((w) => event({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: w } })),
	];
}
