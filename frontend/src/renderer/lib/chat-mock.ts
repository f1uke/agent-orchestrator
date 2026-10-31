import type { AnswerBody } from "./chat-answers";
import type { ChatApi, ChatResult } from "./chat-api";
import { type ChatEntry, type Direction, isObject } from "./chat-event";
import { ChatFolder, type Pending } from "./chat-fold";
import { pngBase64, type Rgb } from "./chat-mock-images";
import { chapterEntries, MOCK_CHAPTERS_CHAT_SESSION } from "./chat-mock-chapters";
import { MOCK_WIDGETS_CHAT_SESSION, widgetEntries } from "./chat-mock-widgets";
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

const TODOS = [
	["Read sender and dead-letter store", "Reading sender and dead-letter store"],
	["Add Budget type with full jitter", "Adding Budget type"],
	["Wire Budget into Sender.Deliver", "Wiring Budget into Sender.Deliver"],
	["Fix parked-attempt count and rename callers", "Fixing parked-attempt count"],
	["Run go test -race ./...", "Running go test -race"],
	["Open PR against main", "Opening the PR"],
];

const FLAKE_RUN = "go test -run TestDeliver -count=50 ./internal/webhook";

const BRIEF_RESULT = {
	type: "result",
	subtype: "success",
	is_error: false,
	num_turns: 9,
	session_id: SESSION,
	total_cost_usd: 1.84,
	usage: {
		input_tokens: 40,
		output_tokens: 30_709,
		cache_read_input_tokens: 1_771_285,
		cache_creation_input_tokens: 21_400,
		iterations: [
			{ input_tokens: 6, output_tokens: 412, cache_read_input_tokens: 118_200, cache_creation_input_tokens: 5_794 },
		],
	},
	modelUsage: { "claude-opus-5-5": { outputTokens: 30_709, contextWindow: 200_000, costUSD: 1.8 } },
};

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
const resultWith = (id: string, content: string, toolUseResult: object): Step => [
	"out",
	{
		type: "user",
		parent_tool_use_id: null,
		session_id: SESSION,
		message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content, is_error: false }] },
		tool_use_result: toolUseResult,
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
const todoWrite = (id: string, messageId: string, done: number): Step => {
	const todos = TODOS.map(([content, activeForm], i) => ({
		content,
		activeForm,
		status: i < done ? "completed" : i === done ? "in_progress" : "pending",
	}));
	return tool(id, messageId, "TodoWrite", { todos });
};
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
const QUESTIONS = {
	questions: [
		{
			question: "Which backoff should the retry budget use?",
			header: "Backoff",
			multiSelect: false,
			options: [
				{
					label: "Full jitter (Recommended)",
					description: "Random delay in [0, base·2^n], capped at 30 s.",
					preview: [
						"delay(n) = rand(0, min(30s, 500ms · 2^n))",
						"",
						"n=0  ▕██░░░░░░░░░░▏  0 - 0.5s",
						"n=1  ▕████░░░░░░░░▏  0 - 1s",
						"n=2  ▕████████░░░░▏  0 - 2s",
						"n=3  ▕████████████▏  0 - 4s",
					].join("\n"),
				},
				{
					label: "Equal jitter",
					description: "Half fixed, half random. Never retries instantly.",
					preview: [
						"d = min(30s, 500ms · 2^n)",
						"delay(n) = d/2 + rand(0, d/2)",
						"",
						"n=0  ▕░░██░░░░░░░░▏  0.25 - 0.5s",
						"n=1  ▕░░░░████░░░░▏  0.5 - 1s",
						"n=2  ▕░░░░░░░░████▏  1 - 2s",
					].join("\n"),
				},
				{
					label: "No jitter",
					description: "Pure exponential. Simple, but retries line up.",
					preview: [
						"delay(n) = min(30s, 500ms · 2^n)",
						"",
						"n=0  ▕   █        ▏  0.5s",
						"n=1  ▕     █      ▏  1s",
						"n=2  ▕         █  ▏  2s",
					].join("\n"),
				},
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

const GO_TEST_RULE = {
	type: "addRules",
	rules: [{ toolName: "Bash", ruleContent: "go test:*" }],
	behavior: "allow",
	destination: "localSettings",
};

const BASH_2 = {
	command: "go test -race -count=1 ./...",
	description: "Run the full test suite with the race detector",
};

const PLAN = {
	plan: [
		"# Retry budget for the webhook sender",
		"",
		"1. Add `Budget{Max, Base, Cap}` with `Allow(n)` and full-jitter `Delay(n)`.",
		"2. Replace the fixed 2 s sleep in `Sender.Deliver` with a context-aware sleep on `Delay(n)`.",
		"3. Park the event via `store.ParkedEvent` after the last attempt, and bump `webhook_parked_total`.",
		"4. Table test: 5 failures park once with attempts=5; a success on try 3 parks nothing.",
	].join("\n"),
	planFilePath: "~/.claude/plans/webhook-retry-budget.md",
};

const MOCK_COMMANDS = [
	{
		name: "compact",
		description: "Summarise the conversation to free context",
		argumentHint: "<instructions>",
		builtin: true,
	},
	{ name: "clear", description: "Start a fresh Claude session in this worktree", argumentHint: "", builtin: true },
	{ name: "review", description: "Review the branch diff", argumentHint: "[pr]", builtin: true },
	{ name: "tdd", description: "Write the failing test first", argumentHint: "", builtin: false },
	{ name: "deslop", description: "Remove AI slop from the diff", argumentHint: "", builtin: false },
	{ name: "open-pr", description: "Open the PR against the session target", argumentHint: "", builtin: false },
];

function initialize(fixture: readonly ChatEntry[]): Step[] {
	const answer = fixture.find((e) => e.dir === "out" && isObject(e.payload) && e.payload.type === "control_response")
		?.payload as { response?: { response?: Record<string, unknown> } } | undefined;
	const { account: _, ...body } = answer?.response?.response ?? {};
	return [
		["in", { type: "control_request", request_id: "demo-init", request: { subtype: "initialize" } }],
		[
			"out",
			{
				type: "control_response",
				response: { subtype: "success", request_id: "demo-init", response: { ...body, commands: MOCK_COMMANDS } },
			},
		],
	];
}

const SCRIPT: Step[] = [
	origin("brief-1", "ao-demo-orchestrator"),
	sent(
		"brief-1",
		"[from @ao-demo-orchestrator] Add a retry budget to the webhook sender: at most 5 attempts per event with exponential backoff, and park the event in the dead-letter table after the last one. Keep the public API unchanged. Open the PR against `main` when tests pass.",
	),
	lifecycle("brief-1", "started"),
	[
		"out",
		{ type: "system", subtype: "init", session_id: SESSION, permissionMode: "default", model: "claude-opus-5-5" },
	],
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
	todoWrite("t-todo-1", "msg_m12", 1),
	result("t-todo-1", "Todos have been modified successfully."),
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
	["out", BRIEF_RESULT],
	lifecycle("brief-1", "completed"),
	lifecycle("peer-1", "started"),
	say(
		"msg_m18",
		"Three callers. I'll switch them to the new name in one commit so the two branches don't conflict, then rerun the full test suite.",
	),
	todoWrite("t-todo-2", "msg_m18", 3),
	result("t-todo-2", "Todos have been modified successfully."),
	tool("t-bash-bg", "msg_m18", "Bash", {
		command: FLAKE_RUN,
		description: "Run the delivery test 50 times in the background",
		run_in_background: true,
	}),
	result("t-bash-bg", "Command running in background with ID: bash_1"),
	[
		"out",
		{
			type: "system",
			subtype: "task_started",
			task_id: "bash_1",
			tool_use_id: "t-bash-bg",
			description: FLAKE_RUN,
			task_type: "local_bash",
			session_id: SESSION,
		},
	],
	tool("t-bash-2", "msg_m19", "Bash", BASH_2),
	ask("req-bash-2", "t-bash-2", "Bash", BASH_2, [GO_TEST_RULE]),
	tool("t-ask", "msg_m20", "AskUserQuestion", QUESTIONS),
	ask("req-ask", "t-ask", "AskUserQuestion", QUESTIONS),
	tool("t-plan", "msg_m21", "ExitPlanMode", PLAN),
	ask("req-plan", "t-plan", "ExitPlanMode", PLAN),
];

export function mockChatEntries(fixture: readonly ChatEntry[]): ChatEntry[] {
	const replay = fixture.filter((e) => !(e.dir === "host" && (e.payload as { kind?: string }).kind === "exit"));
	let seq = replay.at(-1)?.seq ?? 0;
	const scripted = [...initialize(fixture), ...SCRIPT].map(([dir, payload], i) => {
		seq += 1;
		return { seq, ts: new Date(START + i * 7_000).toISOString(), dir, payload };
	});
	return [...replay, ...scripted];
}

function textDeltas(messageId: string, text: string): unknown[] {
	const event = (e: object) => ({ type: "stream_event", session_id: SESSION, parent_tool_use_id: null, event: e });
	const words = text.match(/\S+\s*/g) ?? [];
	return [
		event({ type: "message_start", message: { id: messageId } }),
		event({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } }),
		...words.map((w) => event({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: w } })),
	];
}

export function mockChatDeltas(): unknown[] {
	return textDeltas("msg_m22", STREAMED);
}

export const MOCK_STREAMED_TEXT = STREAMED;

const DELTA_INTERVAL_MS = 30;

/** An allow without updatedInput reuses the request's own input, as the session host does. */
export function encodeAnswer(body: AnswerBody, request: Pending): object {
	const response =
		body.behavior === "allow"
			? {
					behavior: "allow",
					updatedInput: body.updatedInput ?? request.input,
					...(body.updatedPermissions?.length ? { updatedPermissions: body.updatedPermissions } : {}),
				}
			: { behavior: "deny", message: body.message ?? "", ...(body.interrupt ? { interrupt: true } : {}) };
	return { type: "control_response", response: { subtype: "success", request_id: body.requestId, response } };
}

function toolOutcome(request: Pending, body: AnswerBody): Step {
	if (body.behavior === "deny") return result(request.toolUseId, body.message ?? "", true);
	switch (request.toolName) {
		case "AskUserQuestion": {
			const input = isObject(body.updatedInput) ? body.updatedInput : {};
			const answers = isObject(input.answers) ? input.answers : {};
			const pairs = Object.entries(answers).map(([q, a]) => `"${q}"="${String(a)}"`);
			return result(request.toolUseId, `User has answered your questions: ${pairs.join(", ")}.`);
		}
		case "ExitPlanMode":
			return result(request.toolUseId, "User has approved your plan. You can now start coding.");
		case "Bash":
			return result(
				request.toolUseId,
				"ok  \tbilling/internal/webhook\t1.204s\nok  \tbilling/internal/webhook/signing\t0.044s",
			);
	}
	return result(request.toolUseId, "Done.");
}

type StubSession = { log: ChatEntry[]; folder: ChatFolder; sent: number };

/** The preview session that replays long_session.jsonl, a real recording with many tool calls. */
export const MOCK_LONG_CHAT_SESSION = "demo-chat-long";

/**
 * The preview session with many finished turns, each ending in a reply far
 * taller than a row's estimate, that keeps streaming new turns once it is open.
 * Its log arrives over many frames, as a real session's replay does.
 */
export const MOCK_TALL_CHAT_SESSION = "demo-chat-tall";

const TALL_TURNS = 40;
const TALL_LIVE_TURNS = 3;
const TALL_LIVE_PAUSE_MS = 600;

const tallReply = (turn: number, paragraphs: number) =>
	Array.from(
		{ length: paragraphs },
		(_, p) =>
			`Paragraph ${p + 1} of reply ${turn}. The retry budget caps the backoff at thirty seconds, and the sender logs each parked event once with its delivery id so the operator can replay it from the dead-letter table without guessing.`,
	).join("\n\n");

function tallTurn(turn: number): { open: Step[]; reply: Step; close: Step[] } {
	const uuid = `tall-${turn}`;
	const calls = [0, 1, 2].flatMap((k): Step[] => {
		const id = `t-tall-${turn}-${k}`;
		return [
			tool(id, `msg_t${turn}_${k}`, "Bash", { command: `go test ./internal/step${turn}/... -run Case${k}` }),
			result(id, `ok  \tbilling/internal/step${turn}\t0.0${k}s`),
		];
	});
	return {
		open: [
			sent(uuid, `Step ${turn}: run the checks for this package and summarise.`),
			lifecycle(uuid, "started"),
			...calls,
		],
		reply: say(`msg_t${turn}_r`, tallReply(turn, 8)),
		close: [
			["out", { type: "result", subtype: "success", is_error: false, num_turns: 4, total_cost_usd: 0.01 * turn }],
			lifecycle(uuid, "completed"),
		],
	};
}

function tallEntries(): ChatEntry[] {
	const steps = Array.from({ length: TALL_TURNS }, (_, i) => tallTurn(i + 1)).flatMap((t) => [
		...t.open,
		t.reply,
		...t.close,
	]);
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 1_000).toISOString(),
		dir,
		payload,
	}));
}

export const MOCK_IMAGES_CHAT_SESSION = "demo-chat-images";

const lerp = (a: number, b: number, t: number) => Math.round(a + (b - a) * t);

const SCREEN_PNG = pngBase64(320, 200, (x, y): Rgb => {
	const bar = Math.floor((x - 24) / 40);
	const inBars = x >= 24 && x < 296 && bar >= 0 && bar < 7 && (x - 24) % 40 < 28;
	const top = 190 - [60, 95, 80, 130, 110, 150, 120][bar < 0 ? 0 : Math.min(bar, 6)];
	if (inBars && y >= top && y < 180) return [242, 249, 60];
	return [lerp(18, 44, y / 200), lerp(30, 63, y / 200), lerp(58, 47, x / 320)];
});

const SKETCH_PNG = pngBase64(240, 160, (x, y): Rgb => ((x + y) % 48 < 24 ? [36, 52, 94] : [226, 232, 244]));

function imagesEntries(): ChatEntry[] {
	const steps: Step[] = [
		[
			"in",
			{
				type: "user",
				uuid: "img-1",
				session_id: "",
				message: {
					role: "user",
					content: [
						{ type: "text", text: "The export page looks off. Here is a sketch and the current screen." },
						{ type: "image", source: { type: "base64", media_type: "image/png", data: SKETCH_PNG } },
						{ type: "image", source: { type: "base64", media_type: "image/png", data: SCREEN_PNG } },
					],
				},
			},
		],
		lifecycle("img-1", "started"),
		say("msg_i1", "I'll look at the saved screenshot first."),
		tool("t-img-1", "msg_i2", "Read", { file_path: "/work/project/docs/export-page.png" }),
		[
			"out",
			{
				type: "user",
				parent_tool_use_id: null,
				session_id: SESSION,
				message: {
					role: "user",
					content: [
						{
							type: "tool_result",
							tool_use_id: "t-img-1",
							is_error: false,
							content: [{ type: "image", source: { type: "base64", media_type: "image/png", data: SCREEN_PNG } }],
						},
					],
				},
			},
		],
		say("msg_i3", "The bars are clipped at the right edge. I'll check the chart width next."),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 2, total_cost_usd: 0.02 }],
		lifecycle("img-1", "completed"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}

export const MOCK_CODE_CHAT_SESSION = "demo-chat-code";

const FLOW_SVG = [
	'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 560 120" font-family="IBM Plex Sans, sans-serif">',
	'  <rect width="560" height="120" fill="#ffffff"/>',
	'  <g font-size="14" fill="#01172b">',
	'    <rect x="16" y="36" width="140" height="48" rx="12" fill="#f2f2f2"/>',
	'    <text x="86" y="65" text-anchor="middle">Request</text>',
	'    <rect x="210" y="36" width="140" height="48" rx="12" fill="#f2f93c"/>',
	'    <text x="280" y="65" text-anchor="middle">Retry budget</text>',
	'    <rect x="404" y="36" width="140" height="48" rx="12" fill="#f2f2f2"/>',
	'    <text x="474" y="65" text-anchor="middle">Dead letter</text>',
	"  </g>",
	'  <g stroke="#01172b" stroke-width="2" fill="none">',
	'    <path d="M160 60 H204"/><path d="M198 54 L204 60 L198 66"/>',
	'    <path d="M354 60 H398"/><path d="M392 54 L398 60 L392 66"/>',
	"  </g>",
	"</svg>",
].join("\n");

const SUMMARY_PATCH = {
	oldStart: 456,
	oldLines: 7,
	newStart: 456,
	newLines: 12,
	lines: [
		" \tkeys := make([]string, 0, len(balances))",
		" \tfor k := range balances {",
		" \t\tkeys = append(keys, k)",
		" \t}",
		'-\tfmt.Fprintf(&b, "%s %d.%02d\\n", k, balances[k]/100, balances[k]%100)',
		"+\tc := balances[k]",
		'+\tsign := ""',
		"+\tif c < 0 {",
		'+\t\tsign, c = "-", -c',
		"+\t}",
		'+\tfmt.Fprintf(&b, "%s %s%d.%02d\\n", k, sign, c/100, c%100)',
		" \treturn b.String()",
		" }",
	],
};

const BUDGET_TS = [
	"export type Budget = { max: number; baseMs: number; capMs: number };",
	"",
	"/** Whether attempt n may run. */",
	"export function allow(budget: Budget, n: number): boolean {",
	"\treturn n < budget.max;",
	"}",
	"",
	"/** A random wait in [0, min(cap, base * 2^n)], so retries spread out. */",
	"export function delay(budget: Budget, n: number): number {",
	"\tconst ceiling = Math.min(budget.capMs, budget.baseMs * 2 ** n);",
	"\treturn Math.floor(Math.random() * (ceiling + 1));",
	"}",
].join("\n");

function codeEntries(): ChatEntry[] {
	const steps: Step[] = [
		sent("code-1", "Draw the retry flow as an SVG, fix the negative balance summary, then show me the budget helper."),
		lifecycle("code-1", "started"),
		say("msg_c1", "I'll draw the diagram first."),
		tool("t-code-write", "msg_c2", "Write", { file_path: "/work/project/docs/retry-flow.svg", content: FLOW_SVG }),
		resultWith("t-code-write", "File created successfully at: /work/project/docs/retry-flow.svg", {
			type: "create",
			filePath: "/work/project/docs/retry-flow.svg",
			content: FLOW_SVG,
			structuredPatch: [],
			originalFile: null,
		}),
		say("msg_c3", "Now the summary. Negative balances lost their sign."),
		tool("t-code-edit", "msg_c4", "Edit", {
			file_path: "/work/project/report/report.go",
			old_string: '\tfmt.Fprintf(&b, "%s %d.%02d\\n", k, balances[k]/100, balances[k]%100)',
			new_string:
				'\tc := balances[k]\n\tsign := ""\n\tif c < 0 {\n\t\tsign, c = "-", -c\n\t}\n\tfmt.Fprintf(&b, "%s %s%d.%02d\\n", k, sign, c/100, c%100)',
		}),
		resultWith("t-code-edit", "The file /work/project/report/report.go has been updated successfully.", {
			filePath: "/work/project/report/report.go",
			oldString: '\tfmt.Fprintf(&b, "%s %d.%02d\\n", k, balances[k]/100, balances[k]%100)',
			newString: "",
			originalFile: "package report\n// the whole file text stays out of the fold\n",
			structuredPatch: [SUMMARY_PATCH],
			userModified: false,
			replaceAll: false,
		}),
		say("msg_c5", "Last, the budget helper."),
		tool("t-code-read", "msg_c6", "Read", { file_path: "/work/project/src/retry/budget.ts" }),
		result(
			"t-code-read",
			numbered(BUDGET_TS.split("\n").length, (n) => BUDGET_TS.split("\n")[n - 1]),
		),
		say("msg_c7", "The sign now survives, and `delay` caps at `capMs`."),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 3, total_cost_usd: 0.03 }],
		lifecycle("code-1", "completed"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}

export const MOCK_LIVE_CHAT_SESSION = "demo-chat-live";

const LIVE_BRIEF = [
	"/pstack:poteto-mode # Goal",
	"Make the webhook sender's soak test reliable and keep it running in the background.",
	"",
	"## Done when",
	"- `go test ./internal/webhook/...` passes",
	"- the soak test runs in the background and reports back",
].join("\n");
const LIVE_COMMAND_MS = 6_000;

function liveEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		sent("live-brief", LIVE_BRIEF),
		lifecycle("live-brief", "started"),
		say("msg_l1", "Starting with the package tests, then the soak test in the background."),
		tool("t-live-1", "msg_l2", "Read", { file_path: "/work/project/internal/webhook/sender.go" }),
		result("t-live-1", "package webhook\n..."),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(Date.now() - (steps.length - i) * 1_000).toISOString(),
		dir,
		payload,
	}));
}

const liveTails = new Set<string>();

function playLiveTail(id: string, session: StubSession): void {
	if (liveTails.has(id)) return;
	liveTails.add(id);
	append(id, session, [
		tool("t-live-2", "msg_l3", "Bash", {
			command: "go test ./internal/webhook/... -count=1",
			description: "Run the webhook package tests",
		}),
	]);
	setTimeout(() => {
		append(id, session, [
			result("t-live-2", "ok  \tbilling/internal/webhook\t1.204s"),
			tool("t-live-3", "msg_l4", "Bash", {
				command: "go test ./internal/webhook/... -run Soak -count=50",
				description: "Run the soak test",
				run_in_background: true,
			}),
			[
				"out",
				{
					type: "system",
					subtype: "task_started",
					task_id: "live-soak",
					tool_use_id: "t-live-3",
					task_type: "local_bash",
					is_backgrounded: true,
					description: "Run the soak test",
					session_id: SESSION,
				},
			],
			result("t-live-3", "Command running in background with ID: live-soak."),
			say("msg_l5", "The package tests pass. The soak test runs in the background; I will report when it lands."),
			["out", { type: "result", subtype: "success", is_error: false, num_turns: 3, total_cost_usd: 0.04 }],
			lifecycle("live-brief", "completed"),
		]);
	}, LIVE_COMMAND_MS);
}

const tallTails = new Set<string>();

/** Plays the tall session's live turns once per page: each streams its reply word by word, then settles. */
function playTallTail(id: string, session: StubSession): void {
	if (tallTails.has(id)) return;
	tallTails.add(id);
	const play = (n: number) => {
		if (n > TALL_LIVE_TURNS) return;
		const turn = tallTurn(TALL_TURNS + n);
		append(id, session, turn.open);
		const [, reply] = turn.reply as [Direction, { message: { id: string; content: Array<{ text: string }> } }];
		const deltas = textDeltas(reply.message.id, reply.message.content[0].text);
		deltas.forEach((delta, i) =>
			setTimeout(() => stubListeners.get(id)?.forEach((l) => l.delta(delta)), (i + 1) * DELTA_INTERVAL_MS),
		);
		setTimeout(
			() => {
				append(id, session, [turn.reply, ...turn.close]);
				setTimeout(() => play(n + 1), TALL_LIVE_PAUSE_MS);
			},
			(deltas.length + 1) * DELTA_INTERVAL_MS,
		);
	};
	setTimeout(() => play(1), TALL_LIVE_PAUSE_MS);
}

const stubSessions = new Map<string, Promise<StubSession>>();
const stubListeners = new Map<string, Set<ChatMuxListeners>>();

function stubSession(id: string): Promise<StubSession> {
	let session = stubSessions.get(id);
	if (!session) {
		const long = id === MOCK_LONG_CHAT_SESSION;
		const raw = long
			? import("../../../../backend/internal/chatproto/testdata/long_session.jsonl?raw")
			: import("../../../../backend/internal/chatproto/testdata/first_session.jsonl?raw");
		session = raw.then(({ default: text }) => {
			const fixture = text
				.split("\n")
				.filter((line) => line.trim() !== "")
				.map((line) => JSON.parse(line) as ChatEntry);
			const log =
				id === MOCK_TALL_CHAT_SESSION
					? tallEntries()
					: id === MOCK_LIVE_CHAT_SESSION
						? liveEntries()
						: id === MOCK_IMAGES_CHAT_SESSION
							? imagesEntries()
							: id === MOCK_CODE_CHAT_SESSION
								? codeEntries()
								: id === MOCK_WIDGETS_CHAT_SESSION
									? widgetEntries()
									: id === MOCK_CHAPTERS_CHAT_SESSION
										? chapterEntries()
										: long
											? fixture
											: mockChatEntries(fixture);
			const folder = new ChatFolder();
			log.forEach((entry) => folder.apply(entry));
			return { log, folder, sent: 0 };
		});
		stubSessions.set(id, session);
	}
	return session;
}

function append(id: string, session: StubSession, steps: Step[]): void {
	for (const [dir, payload] of steps) {
		const entry: ChatEntry = { seq: session.folder.lastSeq + 1, ts: new Date().toISOString(), dir, payload };
		session.log.push(entry);
		session.folder.apply(entry);
		stubListeners.get(id)?.forEach((l) => l.entry(entry));
	}
}

const OK: ChatResult = { ok: true };

export const stubChatApi: ChatApi = {
	send: async (id, text, images) => {
		const session = await stubSession(id);
		const uuid = `stub-input-${++session.sent}`;
		const content = images.length
			? [
					{ type: "text", text },
					...images.map((img) => ({
						type: "image",
						source: { type: "base64", media_type: img.mediaType, data: img.base64 },
					})),
				]
			: text;
		const busy = session.folder.transcript().busy;
		append(id, session, [
			["in", { type: "user", uuid, session_id: "", message: { role: "user", content } }],
			lifecycle(uuid, "queued"),
			...(busy ? [] : [lifecycle(uuid, "started")]),
		]);
		return OK;
	},
	answer: async (id, body) => {
		const session = await stubSession(id);
		const request = session.folder.transcript().pending.find((p) => p.requestId === body.requestId);
		if (!request) {
			return { ok: false, error: { kind: "rejected", message: `request ${body.requestId} is not pending` } };
		}
		append(id, session, [["in", encodeAnswer(body, request)], toolOutcome(request, body)]);
		return OK;
	},
	interrupt: async (id) => {
		const session = await stubSession(id);
		const tr = session.folder.transcript();
		const running = tr.items.find((item) => item.kind === "user" && item.state === "started");
		if (running?.kind !== "user") return OK;
		const next = tr.queued[0];
		append(id, session, [
			[
				"in",
				{ type: "control_request", request_id: `stub-interrupt-${tr.lastSeq}`, request: { subtype: "interrupt" } },
			],
			[
				"out",
				{
					type: "user",
					session_id: SESSION,
					message: { role: "user", content: [{ type: "text", text: "[Request interrupted by user]" }] },
				},
			],
			[
				"out",
				{ type: "result", subtype: "error_during_execution", is_error: true, num_turns: 1, total_cost_usd: tr.costUsd },
			],
			lifecycle(running.uuid, "cancelled"),
			...(next ? [lifecycle(next, "started")] : []),
		]);
		return OK;
	},
	setMode: async (id, mode) => {
		const session = await stubSession(id);
		const requestId = `stub-mode-${session.folder.lastSeq}`;
		append(id, session, [
			["in", { type: "control_request", request_id: requestId, request: { subtype: "set_permission_mode", mode } }],
			["out", { type: "control_response", response: { subtype: "success", request_id: requestId, response: {} } }],
			["out", { type: "system", subtype: "status", status: null, permissionMode: mode, session_id: SESSION }],
		]);
		return OK;
	},
	setModel: async (id, model) => {
		const session = await stubSession(id);
		const known = session.folder.transcript().models.find((m) => m.value === model);
		if (!known) return { ok: false, error: { kind: "rejected", message: `Model '${model}' not found` } };
		const requestId = `stub-model-${session.folder.lastSeq}`;
		append(id, session, [
			["in", { type: "control_request", request_id: requestId, request: { subtype: "set_model", model } }],
			[
				"out",
				{
					type: "control_response",
					response: { subtype: "success", request_id: requestId, response: { model: known.resolvedModel } },
				},
			],
		]);
		return OK;
	},
};

// The daemon replays a session's log over its websocket, so a long log lands
// across many frames, with live entries arriving once the replay catches up.
const REPLAY_CHUNK = 20;
const REPLAY_TICK_MS = 16;

export function createStubChatMux(): ChatMux {
	const connection = new Set<(state: MuxConnectionState) => void>();
	const mine = new Map<string, Set<ChatMuxListeners>>();
	const timers: ReturnType<typeof setTimeout>[] = [];
	let disposed = false;

	const each = (id: string, fn: (l: ChatMuxListeners) => void) => mine.get(id)?.forEach(fn);
	const later = (fn: () => void, ms: number) => timers.push(setTimeout(fn, ms));

	const goLive = (id: string, session: StubSession) => {
		const live = stubListeners.get(id) ?? new Set<ChatMuxListeners>();
		mine.get(id)?.forEach((l) => live.add(l));
		stubListeners.set(id, live);
		each(id, (l) => l.live());
		connection.forEach((l) => l("open"));
		if (id === MOCK_TALL_CHAT_SESSION) playTallTail(id, session);
		else if (id === MOCK_LIVE_CHAT_SESSION) playLiveTail(id, session);
		else if (session.folder.transcript().busy) {
			mockChatDeltas().forEach((delta, i) => later(() => each(id, (l) => l.delta(delta)), (i + 1) * DELTA_INTERVAL_MS));
		}
	};

	return {
		open: (id, fromSeq) => {
			void stubSession(id).then((session) => {
				let next = session.log.findIndex((entry) => entry.seq > fromSeq);
				if (next < 0) next = session.log.length;
				const replay = () => {
					if (disposed) return;
					const end = Math.min(next + REPLAY_CHUNK, session.log.length);
					for (; next < end; next++) {
						const entry = session.log[next];
						each(id, (l) => l.entry(entry));
					}
					if (next < session.log.length) later(replay, REPLAY_TICK_MS);
					else goLive(id, session);
				};
				replay();
			});
		},
		close: () => undefined,
		listen: (id, listeners) => {
			const set = mine.get(id) ?? new Set<ChatMuxListeners>();
			set.add(listeners);
			mine.set(id, set);
			return () => {
				mine.get(id)?.delete(listeners);
				stubListeners.get(id)?.delete(listeners);
			};
		},
		onConnectionChange: (listener) => {
			connection.add(listener);
			return () => connection.delete(listener);
		},
		dispose: () => {
			disposed = true;
			timers.forEach(clearTimeout);
			for (const [id, set] of mine) set.forEach((l) => stubListeners.get(id)?.delete(l));
			mine.clear();
			connection.clear();
		},
	};
}
