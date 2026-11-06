import type { ChatEntry, Direction } from "./chat-event";

export const MOCK_EXITS_CHAT_SESSION = "demo-chat-exits";

const SESSION = "9c4e1f2a-3b5d-4e6f-8a7b-1c2d3e4f5a6b";
const START = Date.parse("2026-11-06T10:00:00.000Z");

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
const bash = (id: string, command: string, description = ""): Step => [
	"out",
	{
		type: "assistant",
		session_id: SESSION,
		message: { id: `msg_${id}`, content: [{ type: "tool_use", id, name: "Bash", input: { command, description } }] },
	},
];
const result = (id: string, content: string, isError: boolean): Step => [
	"out",
	{
		type: "user",
		session_id: SESSION,
		message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content, is_error: isError }] },
	},
];

export function exitsEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		asked("ex-1", "Is the old retry helper still used anywhere, and do the tests pass?"),
		lifecycle("ex-1", "started"),
		bash("ex_a", "git grep -c legacyRetry -- src", "Count uses of the old helper"),
		result("ex_a", "Exit code 1", true),
		bash("ex_b", "diff -u config/retry.yaml config/retry.example.yaml"),
		result(
			"ex_b",
			"Exit code 1\n--- config/retry.yaml\n+++ config/retry.example.yaml\n@@ -1,2 +1,2 @@\n-budget: 3\n+budget: 5",
			true,
		),
		bash("ex_c", "test -f build/retry.lock"),
		result("ex_c", "Exit code 1", true),
		bash("ex_d", "cd services/billing && rg -n 'legacyRetry'"),
		result("ex_d", "Exit code 1", true),
		bash("ex_e", "go vet ./...", "Vet the packages"),
		result("ex_e", "# billing/retry\nvet: note: build cache warm", false),
		bash("ex_f", "go test ./internal/retry/...", "Run the retry tests"),
		result(
			"ex_f",
			"Exit code 1\n--- FAIL: TestBudgetResets (0.01s)\n    budget_test.go:42: budget = 3, want 5\nFAIL\tbilling/internal/retry\t0.118s",
			true,
		),
		bash("ex_g", "retryctl --dry-run"),
		result("ex_g", "Exit code 127\nzsh: command not found: retryctl", true),
		said(
			"msg_ex1",
			"The old helper has no callers left. The retry config differs from the example in its budget, and one retry test fails on that value.",
		),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 1, total_cost_usd: 0.03 }],
		lifecycle("ex-1", "completed"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 2_000).toISOString(),
		dir,
		payload,
	}));
}
