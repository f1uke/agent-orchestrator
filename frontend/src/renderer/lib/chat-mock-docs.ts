import type { ChatEntry, Direction } from "./chat-event";

export const MOCK_DOCS_CHAT_SESSION = "demo-chat-docs";

const SESSION = "5d2b8e71-0c4a-4f63-9a1e-7b3c6d92f0a8";
const START = Date.parse("2026-10-31T11:00:00.000Z");

type Step = [Direction, object];

export const DOCS_ARTIFACT_URL = "https://claude.ai/artifact/DemoArtifact000000000001";

export const DOCS_PLAN_PATH = "/work/project/docs/plan.md";
export const DOCS_SURVEY = "plans/demo--survey.md";
export const DOCS_JSON = "tmp/batch-3-response.json";
export const DOCS_LOG = "logs/export.log";
export const DOCS_PATCH = "rate-limit.patch";
export const DOCS_PDF = "docs/billing-export-spec.pdf";
export const DOCS_HTML = "docs/preview.html";
export const DOCS_MISSING = "docs/never-saved.md";

export const DOCS_PLAN_MD = [
	"# Retry budget plan",
	"",
	"## Steps",
	"",
	"1. Add a `Budget` type with full jitter.",
	"2. Wire it into the sender.",
	"3. Park events that run out of attempts.",
].join("\n");

const REPLY = [
	"The plan is written and the export failure is traced. These are the files of this turn.",
	"",
	`- Survey: \`${DOCS_SURVEY}\``,
	`- The failing response: \`${DOCS_JSON}\``,
	`- The full log: \`${DOCS_LOG}\``,
	`- The fix as a patch: \`${DOCS_PATCH}\``,
	`- The spec: \`${DOCS_PDF}\``,
	`- The preview page: \`${DOCS_HTML}\``,
	`- A note I meant to save: \`${DOCS_MISSING}\``,
	"",
	`The design reference is [Demo design system](${DOCS_ARTIFACT_URL}).`,
].join("\n");

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

export function docsEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		asked("d-1", "Plan the retry budget, trace the export failure, and tell me where everything is."),
		lifecycle("d-1", "started"),
		say("msg_d1", "I'll write the plan first."),
		[
			"out",
			{
				type: "assistant",
				parent_tool_use_id: null,
				session_id: SESSION,
				message: {
					id: "msg_d2",
					content: [
						{
							type: "tool_use",
							id: "t-docs-write",
							name: "Write",
							input: { file_path: DOCS_PLAN_PATH, content: DOCS_PLAN_MD },
						},
					],
				},
			},
		],
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
							tool_use_id: "t-docs-write",
							content: `File created successfully at: ${DOCS_PLAN_PATH}`,
							is_error: false,
						},
					],
				},
				tool_use_result: {
					type: "create",
					filePath: DOCS_PLAN_PATH,
					content: DOCS_PLAN_MD,
					structuredPatch: [],
					originalFile: null,
				},
			},
		],
		say("msg_d3", REPLY),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 2, total_cost_usd: 0.02 }],
		lifecycle("d-1", "completed"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}
