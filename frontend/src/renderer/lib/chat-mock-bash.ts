import type { ChatEntry, Direction } from "./chat-event";

/** The preview session with a finished "!" run, a running one, and no turn open. */
export const MOCK_BASH_CHAT_SESSION = "demo-chat-bash";

const SESSION = "5b1d93e6-7c20-4a48-9f3e-0d6a82c41b97";
const START = Date.parse("2026-11-06T09:00:00.000Z");
const CWD = "/preview/worktree";

export type BashStep = [Direction, object];

const escapeXml = (text: string) => text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");

export const bashStart = (id: string, command: string): BashStep => [
	"host",
	{ kind: "bash_start", id, command, cwd: CWD },
];

export const bashOutput = (id: string, stream: "stdout" | "stderr", text: string): BashStep => [
	"host",
	{ kind: "bash_output", id, stream, text },
];

/** What the host logs once a run ends: the end note, the injected user message, and claude's empty result. */
export function bashFinish(
	id: string,
	command: string,
	{ stdout, stderr, code, stopped = false }: { stdout: string; stderr: string; code: number; stopped?: boolean },
): BashStep[] {
	return [
		["host", { kind: "bash_end", id, code: stopped ? null : code, signal: null, stopped, truncated: false }],
		[
			"in",
			{
				type: "user",
				uuid: id,
				shouldQuery: false,
				message: {
					role: "user",
					content: [
						{ type: "text", text: `<bash-input>${escapeXml(command)}</bash-input>` },
						{
							type: "text",
							text: `<bash-stdout>${escapeXml(stdout)}</bash-stdout><bash-stderr>${escapeXml(stderr)}</bash-stderr><bash-exit-code>${stopped ? 130 : code}</bash-exit-code>`,
						},
					],
				},
			},
		],
		["out", { type: "result", subtype: "success", is_error: false, result: "", num_turns: 0, session_id: SESSION }],
	];
}

const FINISHED = "demo-bash-1";
const RUNNING = "demo-bash-2";

const FINISHED_OUT =
	" M frontend/src/renderer/components/ChatComposer.tsx\n?? frontend/src/renderer/lib/chat-bash.ts\n";

export function bashEntries(): ChatEntry[] {
	const steps: BashStep[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		["in", { type: "user", uuid: "bs-1", session_id: "", message: { role: "user", content: "Add the retry helper." } }],
		["out", { type: "command_lifecycle", command_uuid: "bs-1", state: "started" }],
		[
			"out",
			{
				type: "assistant",
				session_id: SESSION,
				message: { id: "msg_bs1", content: [{ type: "text", text: "The retry helper is in place." }] },
			},
		],
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 1, total_cost_usd: 0.02 }],
		["out", { type: "command_lifecycle", command_uuid: "bs-1", state: "completed" }],
		bashStart(FINISHED, "git status --short"),
		bashOutput(FINISHED, "stdout", FINISHED_OUT),
		...bashFinish(FINISHED, "git status --short", { stdout: FINISHED_OUT, stderr: "", code: 0 }),
		bashStart(RUNNING, "npm test -- --run"),
		bashOutput(RUNNING, "stdout", "> app@1.0.0 test\n> vitest --run\n\n RUN  v3.2.4 /preview/worktree\n\n"),
		bashOutput(RUNNING, "stderr", "warn: 2 snapshots are obsolete\n"),
		bashOutput(RUNNING, "stdout", " ✓ src/retry.test.ts (6 tests) 12ms\n ✓ src/backoff.test.ts (4 tests) 9ms\n"),
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 3_000).toISOString(),
		dir,
		payload,
	}));
}
