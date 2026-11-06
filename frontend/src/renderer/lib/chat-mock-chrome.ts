import type { ChatEntry, Direction } from "./chat-event";
import { CHROME_FLAG } from "./chat-fold";
import { pngBase64, type Rgb } from "./chat-mock-images";

export const MOCK_CHROME_CHAT_SESSION = "demo-chat-chrome";

const SESSION = "9e4c7a21-5d3b-4f80-b6a2-1c8e0f7d3a54";
const START = Date.parse("2026-11-05T09:00:00.000Z");
const CHROME = "mcp__claude-in-chrome__";

type Step = [Direction, object];

const PAGE_PNG = pngBase64(480, 300, (x, y): Rgb => {
	if (y < 36) return [236, 239, 244];
	if (y >= 72 && y < 96 && x >= 40 && x < 300) return [40, 48, 64];
	if (y >= 120 && y < 260 && x >= 40 && x < 440) return (x + y) % 32 < 16 ? [230, 236, 246] : [214, 224, 240];
	if (y >= 200 && y < 228 && x >= 300 && x < 420) return [59, 130, 246];
	return [250, 251, 252];
});

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
const used = (id: string, messageId: string, action: string, input: object): Step => [
	"out",
	{
		type: "assistant",
		session_id: SESSION,
		message: { id: messageId, content: [{ type: "tool_use", id, name: CHROME + action, input }] },
	},
];
const returned = (id: string, content: unknown, isError = false): Step => [
	"out",
	{
		type: "user",
		session_id: SESSION,
		message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content, is_error: isError }] },
	},
];
const text = (t: string) => [{ type: "text", text: t }];

export function chromeEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude", CHROME_FLAG], resume: "", pid: 1 }],
		asked("cr-1", "Open the example signup page and check that the submit button is visible."),
		lifecycle("cr-1", "started"),
		used("t-cr-1", "msg_cr1", "tabs_context_mcp", {}),
		returned("t-cr-1", text("Tab group: 1 tab. tabId 101: New Tab")),
		used("t-cr-2", "msg_cr2", "navigate", { url: "https://example.com/signup", tabId: 101 }),
		returned("t-cr-2", text("Navigated to https://example.com/signup")),
		used("t-cr-3", "msg_cr3", "find", { query: "submit button", tabId: 101 }),
		returned("t-cr-3", text('ref_12 button "Create account" at (360, 214)')),
		used("t-cr-4", "msg_cr4", "computer", { action: "screenshot", tabId: 101 }),
		returned("t-cr-4", [{ type: "image", source: { type: "base64", media_type: "image/png", data: PAGE_PNG } }]),
		used("t-cr-5", "msg_cr5", "computer", { action: "left_click", coordinate: [360, 214], tabId: 101 }),
		returned("t-cr-5", "No browser is connected", true),
		said(
			"msg_cr6",
			"The submit button is visible below the form, at the right. The click failed because the browser disconnected.",
		),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 6, total_cost_usd: 0.04 }],
		lifecycle("cr-1", "completed"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 4_000).toISOString(),
		dir,
		payload,
	}));
}
