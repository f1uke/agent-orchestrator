import type { ChatEntry, Direction } from "./chat-event";
import { SUGGEST_SESSION, SUGGEST_SKILL } from "./chat-suggest";

export const MOCK_SUGGEST_CHAT_SESSION = "demo-chat-suggest";

const SESSION = "3e8a6b14-52c9-4f0d-8d7a-91b04c6e2a57";
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

const REPLY = [
	"The widget tests run from the frontend folder. This one command covers them:",
	"```bash\npnpm test chat-widget\n```",
	"The full check has several steps, so it stays a script:",
	"```bash\ncd frontend\nnpm run typecheck\nnpm test\n```",
	"The command that reads the tool name is this one:",
	"```ts\nconst isOwnRow = (name: string) => OWN_ROW_TOOLS.has(name);\n```",
].join("\n\n");

export function suggestEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		[
			"in",
			{
				type: "user",
				uuid: "s-1",
				session_id: "",
				message: {
					role: "user",
					content: "How do I run the widget tests? Also fix the flaky Esc test while you are here.",
				},
			},
		],
		["out", { type: "command_lifecycle", command_uuid: "s-1", state: "started" }],
		say("msg_s1", REPLY),
		tool("s-session", "msg_s2", SUGGEST_SESSION, {
			title: "Fix the flaky Esc test",
			prompt:
				"The Esc-interrupts test in ChatComposer fails about one run in five. Find the race, fix it, and show a hundred passing runs.",
		}),
		result("s-session", "Card shown. Carry on with the current task unless the user says otherwise."),
		tool("s-skill", "msg_s3", SUGGEST_SKILL, {
			name: "verify",
			reason: "Drives the app and saves screenshots for this repo",
		}),
		result("s-skill", "Card shown. Carry on."),
		say("msg_s4", "That test is unrelated to the widget work, so it is cleaner in its own session."),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 3, total_cost_usd: 0.03 }],
		["out", { type: "command_lifecycle", command_uuid: "s-1", state: "completed" }],
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 1_500).toISOString(),
		dir,
		payload,
	}));
}
