import type { ChatEntry, Direction } from "./chat-event";

export const MOCK_TASKS_CHAT_SESSION = "demo-chat-tasks";

export const TASKS_AGENT_RUNNING = "t-agent-run";
export const TASKS_AGENT_DONE = "t-agent-done";
export const TASKS_SOAK_TASK = "tasks-soak";

const SESSION = "7c1e9a52-3b6d-4e0f-8a14-2d5f9b7c3e60";
const START = Date.parse("2026-11-03T09:00:00.000Z");
const SUB_MODEL = "claude-sonnet-5-5";

type Step = [Direction, object];

export const TASKS_AUDIT_PROMPT = [
	"Audit the retry paths of the webhook sender in /work/project/internal/webhook.",
	"",
	"Find every place an event is retried, how the delay between attempts is chosen, and what happens to an event after its last attempt.",
	"List each path with its file and line, and say which of them can retry forever.",
	"Do not change any file. Report back with a short list, the riskiest path first, and one sentence on why it is risky.",
	"Keep the whole report under 200 words.",
].join("\n");

const say = (messageId: string, text: string, parent: string | null = null): Step => [
	"out",
	{
		type: "assistant",
		parent_tool_use_id: parent,
		session_id: SESSION,
		message: { id: messageId, model: parent ? SUB_MODEL : "claude-opus-5-5", content: [{ type: "text", text }] },
	},
];
const tool = (id: string, messageId: string, name: string, input: object, parent: string | null = null): Step => [
	"out",
	{
		type: "assistant",
		parent_tool_use_id: parent,
		session_id: SESSION,
		message: {
			id: messageId,
			model: parent ? SUB_MODEL : "claude-opus-5-5",
			content: [{ type: "tool_use", id, name, input }],
		},
	},
];
const result = (id: string, content: string, parent: string | null = null): Step => [
	"out",
	{
		type: "user",
		parent_tool_use_id: parent,
		session_id: SESSION,
		message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content, is_error: false }] },
	},
];
const asked = (uuid: string, text: string): Step => [
	"in",
	{ type: "user", uuid, session_id: "", message: { role: "user", content: text } },
];
const lifecycle = (uuid: string, state: string): Step => [
	"out",
	{ type: "command_lifecycle", command_uuid: uuid, state },
];
const started = (taskId: string, toolUseId: string, description: string, taskType: string): Step => [
	"out",
	{
		type: "system",
		subtype: "task_started",
		task_id: taskId,
		tool_use_id: toolUseId,
		task_type: taskType,
		is_backgrounded: true,
		description,
		session_id: SESSION,
	},
];

export function tasksEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		asked("tk-1", "Audit the webhook retry paths, run the soak test, and map the sender callers."),
		lifecycle("tk-1", "started"),
		say("msg_k1", "I'll map the callers first, then audit the retry paths and run the soak test in the background."),
		tool(TASKS_AGENT_DONE, "msg_k2", "Agent", {
			description: "Map the sender callers",
			subagent_type: "Explore",
			prompt: "Find every caller of Sender.Deliver and report file and line for each.",
		}),
		started("tasks-agent-done", TASKS_AGENT_DONE, "Map the sender callers", "local_agent"),
		tool("t-sub-d1", "msg_kd1", "Grep", { pattern: "Deliver\\(", path: "/work/project" }, TASKS_AGENT_DONE),
		result("t-sub-d1", "Found 3 matches\ninternal/webhook/sender.go:65\ninternal/admin/replay.go:19", TASKS_AGENT_DONE),
		say("msg_kd2", "Three callers: sender.go:65, replay.go:19 and cmd/requeue/main.go:44.", TASKS_AGENT_DONE),
		result(TASKS_AGENT_DONE, "Async agent launched successfully."),
		[
			"out",
			{
				type: "system",
				subtype: "task_updated",
				task_id: "tasks-agent-done",
				patch: { status: "completed" },
				session_id: SESSION,
			},
		],
		[
			"out",
			{
				type: "system",
				subtype: "task_notification",
				task_id: "tasks-agent-done",
				tool_use_id: TASKS_AGENT_DONE,
				status: "completed",
				summary: "Three callers: sender.go:65, replay.go:19 and cmd/requeue/main.go:44.",
				session_id: SESSION,
			},
		],
		tool(TASKS_AGENT_RUNNING, "msg_k3", "Agent", {
			description: "Audit the webhook retry paths",
			subagent_type: "Explore",
			prompt: TASKS_AUDIT_PROMPT,
		}),
		started("tasks-agent-run", TASKS_AGENT_RUNNING, "Audit the webhook retry paths", "local_agent"),
		say("msg_ka1", "Reading the sender and the dead-letter store first.", TASKS_AGENT_RUNNING),
		tool("t-sub-1", "msg_ka2", "Read", { file_path: "/work/project/internal/webhook/sender.go" }, TASKS_AGENT_RUNNING),
		result("t-sub-1", "     1\tpackage webhook\n     2\t...", TASKS_AGENT_RUNNING),
		tool("t-sub-2", "msg_ka3", "Grep", { pattern: "attempt", path: "/work/project/internal" }, TASKS_AGENT_RUNNING),
		result("t-sub-2", "Found 7 matches\ninternal/webhook/sender.go:61", TASKS_AGENT_RUNNING),
		tool(
			"t-sub-3",
			"msg_ka4",
			"Bash",
			{ command: "go test ./internal/webhook/... -run Retry -count=1" },
			TASKS_AGENT_RUNNING,
		),
		result("t-sub-3", "ok  \tbilling/internal/webhook\t0.412s", TASKS_AGENT_RUNNING),
		say(
			"msg_ka5",
			"The sender retries on a fixed two second sleep with no cap, so a failing endpoint is retried forever. I'm checking the replay path next.",
			TASKS_AGENT_RUNNING,
		),
		result(TASKS_AGENT_RUNNING, "Async agent launched successfully."),
		tool("t-bash-soak", "msg_k4", "Bash", {
			command: "go test ./internal/webhook/... -run Soak -count=50",
			description: "Run the soak test",
			run_in_background: true,
		}),
		result("t-bash-soak", `Command running in background with ID: ${TASKS_SOAK_TASK}.`),
		started(TASKS_SOAK_TASK, "t-bash-soak", "Run the soak test", "local_bash"),
		say(
			"msg_k5",
			"One agent finished. The retry audit and the soak test are still running in the background, and I will report when they land.",
		),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 5, total_cost_usd: 0.09 }],
		lifecycle("tk-1", "completed"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}
