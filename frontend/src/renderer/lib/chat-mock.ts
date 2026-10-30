import type { ChatEntry, Direction } from "./chat-event";
import type { ChatMux, ChatMuxListeners } from "./chat-mux";
import type { MuxConnectionState } from "./terminal-mux";

const SESSION = "12e38fc9-a4a5-4819-b046-4107629b2c8b";
const START = Date.parse("2026-10-29T17:12:00.000Z");

const SENDER_BEFORE = `	attempt := 0
	for attempt < s.maxAttempts {
		if err := s.post(ctx, ev); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
		attempt++
	}
	return ErrGaveUp`;

const SENDER_AFTER = `	attempt := 0
	for s.budget.Allow(attempt) {
		if err := s.post(ctx, ev); err == nil {
			return nil
		}
		if err := sleepCtx(ctx, s.budget.Delay(attempt)); err != nil {
			return err
		}
		attempt++
	}
	if err := s.deadLetters.Park(ctx, ev, attempt); err != nil {
		return fmt.Errorf("park %s: %w", ev.ID, err)
	}
	return ErrGaveUp`;

const BUDGET_GO = [
	"package webhook",
	"",
	'import (\n\t"math/rand/v2"\n\t"time"\n)',
	"",
	"// Budget caps delivery attempts and spaces them with full-jitter backoff.",
	"type Budget struct {",
	"\tMax  int",
	"\tBase time.Duration",
	"\tCap  time.Duration",
	"}",
	"",
	"// Allow reports whether attempt n may run.",
	"func (b Budget) Allow(n int) bool { return n < b.Max }",
	"",
	"// Delay is a random wait in [0, min(Cap, Base·2^n)].",
	"func (b Budget) Delay(n int) time.Duration {",
	"\tceil := b.Base << n",
	"\tif ceil <= 0 || ceil > b.Cap {",
	"\t\tceil = b.Cap",
	"\t}",
	"\treturn time.Duration(rand.Int64N(int64(ceil) + 1))",
	"}",
].join("\n");

const numbered = (count: number, line: (n: number) => string) =>
	Array.from({ length: count }, (_, i) => `${String(i + 1).padStart(6)}\t${line(i + 1)}`).join("\n");

const STREAMED =
	"While you decide, I've started a background run of the flaky-looking test 50 times to make sure the new timing isn't racy. So far 31 of 50 passed with no failures.";

type Step = [Direction, object];

const tool = (id: string, messageId: string, name: string, input: object, parent: string | null = null): Step => [
	"out",
	{
		type: "assistant",
		parent_tool_use_id: parent,
		session_id: SESSION,
		message: { id: messageId, content: [{ type: "tool_use", id, name, input }] },
	},
];
const result = (id: string, content: string, isError = false, parent: string | null = null): Step => [
	"out",
	{
		type: "user",
		parent_tool_use_id: parent,
		session_id: SESSION,
		message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content, is_error: isError }] },
	},
];
const say = (messageId: string, text: string): Step => [
	"out",
	{ type: "assistant", session_id: SESSION, message: { id: messageId, content: [{ type: "text", text }] } },
];
const thinking = (messageId: string, text: string): Step => [
	"out",
	{
		type: "assistant",
		session_id: SESSION,
		message: { id: messageId, content: [{ type: "thinking", thinking: text }] },
	},
];
const sent = (uuid: string, text: string): Step => [
	"in",
	{ type: "user", uuid, session_id: "", message: { role: "user", content: text } },
];
const lifecycle = (uuid: string, state: string): Step => [
	"out",
	{ type: "command_lifecycle", command_uuid: uuid, state },
];
const origin = (uuid: string, from: string): Step => [
	"host",
	{ kind: "user_origin", uuid, origin: { via: "ao_send", from } },
];
const ask = (requestId: string, toolUseId: string, name: string, input: object, suggestions: object[] = []): Step => [
	"out",
	{
		type: "control_request",
		request_id: requestId,
		request: {
			subtype: "can_use_tool",
			tool_name: name,
			tool_use_id: toolUseId,
			input,
			permission_suggestions: suggestions,
		},
	},
];
const allow = (requestId: string, updatedPermissions: object[]): Step => [
	"in",
	{
		type: "control_response",
		response: { subtype: "success", request_id: requestId, response: { behavior: "allow", updatedPermissions } },
	},
];

const QUESTIONS = {
	questions: [
		{
			question: "Which backoff should the retry budget use?",
			header: "Backoff",
			multiSelect: false,
			options: [
				{ label: "Full jitter (Recommended)", description: "Random delay in [0, base·2^n], capped at 30 s." },
				{ label: "Equal jitter", description: "Half fixed, half random. Never retries instantly." },
				{ label: "No jitter", description: "Pure exponential. Simple, but retries line up." },
			],
		},
		{
			question: "How should a parked event be reported?",
			header: "Alerts",
			multiSelect: true,
			options: [
				{ label: "Metrics counter", description: "webhook_parked_total by endpoint." },
				{ label: "Log at warn", description: "One line per parked event with the last error." },
				{ label: "Merchant email", description: "Daily digest to the endpoint owner." },
			],
		},
	],
};

const GO_TEST_RULE = { type: "addRules", rules: [{ toolName: "Bash", ruleContent: "go test:*" }], behavior: "allow" };

const SCRIPT: Step[] = [
	origin("brief-1", "ao-demo-orchestrator"),
	sent(
		"brief-1",
		"[from @ao-demo-orchestrator] Add a retry budget to the webhook sender: at most 5 attempts per event with exponential backoff, and park the event in the dead-letter table after the last one. Keep the public API unchanged. Open the PR against `main` when tests pass.",
	),
	lifecycle("brief-1", "started"),
	["out", { type: "system", subtype: "init", session_id: SESSION, permissionMode: "default" }],
	thinking(
		"msg_m10",
		"The sender lives in internal/webhook. I should find where attempts are counted today, then check whether the dead-letter table already exists before adding a migration.",
	),
	tool("t-grep", "msg_m10", "Grep", { pattern: "attempt", path: "internal/webhook", output_mode: "content" }),
	result(
		"t-grep",
		"Found 7 matches\ninternal/webhook/sender.go:41:\tattempt := 0\ninternal/webhook/sender.go:58:\tfor attempt < s.maxAttempts {\ninternal/webhook/sender_test.go:22:\tattempts: 3,",
	),
	tool("t-read-1", "msg_m11", "Read", { file_path: "internal/webhook/sender.go" }),
	result(
		"t-read-1",
		numbered(142, (n) => (n === 1 ? "package webhook" : "")),
	),
	tool("t-read-2", "msg_m11", "Read", { file_path: "internal/store/deadletter.go" }),
	result(
		"t-read-2",
		numbered(64, (n) => (n === 1 ? "package store" : "")),
	),
	say(
		"msg_m12",
		"The sender already loops on `maxAttempts`, but the delay is a fixed 2 s and nothing parks the event when it gives up. The dead-letter store exists, so no migration is needed. I'll add a `Budget` type and wire it in.",
	),
	tool("t-edit", "msg_m13", "Edit", {
		file_path: "internal/webhook/sender.go",
		old_string: SENDER_BEFORE,
		new_string: SENDER_AFTER,
	}),
	result("t-edit", "The file internal/webhook/sender.go has been updated successfully."),
	tool("t-write", "msg_m14", "Write", { file_path: "internal/webhook/budget.go", content: BUDGET_GO }),
	result("t-write", "File created successfully at: internal/webhook/budget.go"),
	tool("t-bash", "msg_m15", "Bash", {
		command: "go test ./internal/webhook/...",
		description: "Run the webhook tests",
	}),
	result(
		"t-bash",
		"Exit code 1\n--- FAIL: TestDeliverParksAfterBudget (0.01s)\n    sender_test.go:88: parked attempts = 4, want 5\nFAIL\tbilling/internal/webhook\t0.214s\nok  \tbilling/internal/webhook/signing\t0.031s",
		true,
	),
	say(
		"msg_m16",
		"One failure. `attempt` is incremented after the last try, so the loop exits with the right count but the test reads it before the increment. The cleaner fix is to count tries, not retries.",
	),
	origin("peer-1", "ao-demo-73"),
	sent(
		"peer-1",
		"[from @ao-demo-73] Heads up: I'm renaming `store.DeadLetter` to `store.ParkedEvent` in my branch. If you call it, use the new name or we'll conflict.",
	),
	lifecycle("peer-1", "queued"),
	tool("t-agent", "msg_m17", "Agent", {
		subagent_type: "Explore",
		description: "find every caller of store.DeadLetter",
		prompt: "Find every caller of store.DeadLetter in this repository.",
	}),
	tool("t-sub-1", "msg_s1", "Grep", { pattern: "DeadLetter" }, "t-agent"),
	result("t-sub-1", "Found 3 files", false, "t-agent"),
	tool("t-sub-2", "msg_s2", "Read", { file_path: "internal/admin/replay.go" }, "t-agent"),
	result(
		"t-sub-2",
		numbered(40, () => ""),
		false,
		"t-agent",
	),
	tool("t-sub-3", "msg_s3", "Read", { file_path: "cmd/requeue/main.go" }, "t-agent"),
	result(
		"t-sub-3",
		numbered(52, () => ""),
		false,
		"t-agent",
	),
	result("t-agent", "3 callers: internal/webhook/sender.go:65, internal/admin/replay.go:19, cmd/requeue/main.go:44"),
	say(
		"msg_m18",
		"Three callers. I'll switch them to the new name in one commit so the two branches don't conflict, then rerun the full test suite.",
	),
	tool("t-bash-2", "msg_m19", "Bash", {
		command: "go test -race -count=1 ./...",
		description: "Run the full test suite with the race detector",
	}),
	ask("req-bash-2", "t-bash-2", "Bash", { command: "go test -race -count=1 ./..." }, [GO_TEST_RULE]),
	allow("req-bash-2", [GO_TEST_RULE]),
	result("t-bash-2", "ok  \tbilling/internal/webhook\t1.204s\nok  \tbilling/internal/webhook/signing\t0.044s"),
	tool("t-ask", "msg_m20", "AskUserQuestion", QUESTIONS),
	ask("req-ask", "t-ask", "AskUserQuestion", QUESTIONS),
];

export function mockChatEntries(fixture: readonly ChatEntry[]): ChatEntry[] {
	const replay = fixture.filter((e) => !(e.dir === "host" && (e.payload as { kind?: string }).kind === "exit"));
	let seq = replay.at(-1)?.seq ?? 0;
	const scripted = SCRIPT.map(([dir, payload], i) => {
		seq += 1;
		return { seq, ts: new Date(START + i * 7_000).toISOString(), dir, payload };
	});
	return [...replay, ...scripted];
}

export function mockChatDeltas(): unknown[] {
	const event = (e: object) => ({ type: "stream_event", session_id: SESSION, parent_tool_use_id: null, event: e });
	const words = STREAMED.match(/\S+\s*/g) ?? [];
	return [
		event({ type: "message_start", message: { id: "msg_m21" } }),
		event({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } }),
		...words.map((w) => event({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: w } })),
	];
}

export const MOCK_STREAMED_TEXT = STREAMED;

const DELTA_INTERVAL_MS = 30;

export function createStubChatMux(): ChatMux {
	const sessions = new Map<string, Set<ChatMuxListeners>>();
	const connection = new Set<(state: MuxConnectionState) => void>();
	const timers: ReturnType<typeof setTimeout>[] = [];
	let disposed = false;

	const each = (id: string, fn: (l: ChatMuxListeners) => void) => sessions.get(id)?.forEach(fn);

	return {
		open: (id, fromSeq) => {
			void import("../../../../backend/internal/chatproto/testdata/first_session.jsonl?raw").then(
				({ default: raw }) => {
					if (disposed) return;
					const fixture = raw
						.split("\n")
						.filter((line) => line.trim() !== "")
						.map((line) => JSON.parse(line) as ChatEntry);
					for (const entry of mockChatEntries(fixture)) if (entry.seq > fromSeq) each(id, (l) => l.entry(entry));
					each(id, (l) => l.live());
					connection.forEach((l) => l("open"));
					mockChatDeltas().forEach((delta, i) => {
						timers.push(setTimeout(() => each(id, (l) => l.delta(delta)), (i + 1) * DELTA_INTERVAL_MS));
					});
				},
			);
		},
		close: () => undefined,
		listen: (id, listeners) => {
			const set = sessions.get(id) ?? new Set<ChatMuxListeners>();
			set.add(listeners);
			sessions.set(id, set);
			return () => set.delete(listeners);
		},
		onConnectionChange: (listener) => {
			connection.add(listener);
			return () => connection.delete(listener);
		},
		dispose: () => {
			disposed = true;
			timers.forEach(clearTimeout);
			sessions.clear();
			connection.clear();
		},
	};
}
