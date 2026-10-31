import type { ChatEntry, Direction } from "./chat-event";
import { pngBase64, type Rgb } from "./chat-mock-images";

export const MOCK_REFS_CHAT_SESSION = "demo-chat-image-refs";

const SESSION = "3f6a0c52-9d1b-4c8e-8a47-6e2b5d90a1f7";
const START = Date.parse("2026-10-31T10:00:00.000Z");

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

export const REFS_DIRECTORY = "plans/export-chart--screens/";
export const REFS_GLOB = "clip-before-*.png";
export const REFS_OPTION_IMAGE = "plans/export-chart--screens/compare-1920x1080.png";
export const REFS_MISSING = "plans/export-chart--screens/never-saved.png";

const REPLY = [
	"I captured the export page before and after the width fix.",
	"",
	`The screens are in \`${REFS_DIRECTORY}\`:`,
	"",
	`- \`${REFS_GLOB}\` is the clipped chart at three widths`,
	`- \`${REFS_MISSING}\` was meant to be the dark theme, but that capture never saved`,
	"",
	"The bars now end inside the plot area at every width.",
].join("\n");

const QUESTIONS = {
	questions: [
		{
			question: "Which chart width should the export page use?",
			header: "Width",
			multiSelect: false,
			options: [
				{
					label: "Fill the column (Recommended)",
					description: `The chart follows the column, as in ${REFS_OPTION_IMAGE} (the right-hand edge is clear).`,
					preview: "",
				},
				{ label: "Fixed 640 px", description: "Same size at every window width.", preview: "" },
			],
		},
	],
};

export function refsEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		asked("r-1", "Check the export chart width fix."),
		lifecycle("r-1", "started"),
		say("msg_r1", REPLY),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 1, total_cost_usd: 0.02 }],
		lifecycle("r-1", "completed"),
		asked("r-2", "Which width should I keep?"),
		lifecycle("r-2", "started"),
		[
			"out",
			{
				type: "assistant",
				session_id: SESSION,
				message: {
					id: "msg_r2",
					content: [{ type: "tool_use", id: "t-ask", name: "AskUserQuestion", input: QUESTIONS }],
				},
			},
		],
		[
			"out",
			{
				type: "control_request",
				request_id: "req-ask",
				request: {
					subtype: "can_use_tool",
					tool_name: "AskUserQuestion",
					tool_use_id: "t-ask",
					input: QUESTIONS,
					permission_suggestions: [],
				},
			},
		],
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}

const lerp = (a: number, b: number, t: number) => Math.round(a + (b - a) * t);

/** A fictional screenshot: a dark window with bars cut off at `clipAt` pixels, so the three widths differ. */
export const refsPng = (width: number, clipAt: number) =>
	pngBase64(width, 200, (x, y): Rgb => {
		const bar = Math.floor((x - 20) / 36);
		const inBars = x >= 20 && x < clipAt && bar >= 0 && bar < 8 && (x - 20) % 36 < 26;
		const top = 180 - [50, 90, 70, 120, 100, 140, 110, 130][Math.min(Math.max(bar, 0), 7)];
		if (inBars && y >= top && y < 172) return [242, 249, 60];
		return [lerp(18, 40, y / 200), lerp(30, 58, y / 200), lerp(58, 52, x / width)];
	});
