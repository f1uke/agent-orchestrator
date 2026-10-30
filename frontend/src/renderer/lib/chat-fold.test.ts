import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { ChatEntry, Direction } from "./chat-event";
import {
	ChatFolder,
	type ChatItem,
	foldEntries,
	type NoticeKind,
	type ToolCallItem,
	type Transcript,
	type UserItem,
} from "./chat-fold";

function loadFixture(name: string): ChatEntry[] {
	const testdata = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
	return readFileSync(join(testdata, name), "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line) => JSON.parse(line) as ChatEntry);
}

const upTo = (entries: ChatEntry[], seq: number) => entries.filter((e) => e.seq <= seq);
const users = (tr: Transcript) => tr.items.filter((i): i is UserItem => i.kind === "user");
const toolCalls = (tr: Transcript, name: string) =>
	tr.items.filter((i): i is ToolCallItem => i.kind === "tool_call" && i.name === name);
const notices = (tr: Transcript) =>
	tr.items.filter((i): i is Extract<ChatItem, { kind: "notice" }> => i.kind === "notice");
const noticeKinds = (tr: Transcript): NoticeKind[] => notices(tr).map((n) => n.notice);
const synth = (seq: number, dir: Direction | string, payload: unknown): ChatEntry =>
	({ seq, ts: "", dir, payload }) as ChatEntry;
const json = (text: string) => JSON.parse(text) as unknown;

describe("chat fold parity: first_session.jsonl", () => {
	const entries = loadFixture("first_session.jsonl");
	const tr = foldEntries(entries);

	it("names the claude session and every user input, completed, with the ao send origin", () => {
		expect(tr.claudeSessionIds).toEqual(["12e38fc9-a4a5-4819-b046-4107629b2c8b"]);
		const want = [
			["ce40626b-c042-46c2-8d92-2588f30c93ef", "Create a file named hello.txt"],
			["80bc0dba-da1e-479f-9f4b-fa3e99a11697", "Use the AskUserQuestion tool"],
			["31cfce2e-3e77-425d-b572-5aabe70b411e", "[from @billing-portal-12] "],
			["ddecc06d-38b6-43e4-9756-8d15537e927e", "Count from 1 to 3"],
			["6b6a4d66-83ae-48a8-be55-3de01739cff8", "Now say the word queued."],
		];
		const got = users(tr);
		expect(got).toHaveLength(want.length);
		got.forEach((u, i) => {
			expect(u.uuid).toBe(want[i][0]);
			expect(u.text.startsWith(want[i][1])).toBe(true);
			expect(u.state).toBe("completed");
			expect(u.origin).toEqual(i === 2 ? { via: "ao_send", from: "billing-portal-12" } : undefined);
		});
	});

	it("attaches the Write permission, its suggestion and its result", () => {
		const writes = toolCalls(tr, "Write");
		expect(writes).toHaveLength(1);
		expect(writes[0].permission?.status).toBe("allowed");
		expect(writes[0].permission?.updatedPermissions).toEqual([
			{ type: "setMode", mode: "acceptEdits", destination: "session" },
		]);
		expect(writes[0].result?.isError).toBe(false);
	});

	it("records the AskUserQuestion answer as an allowed call with the chosen options", () => {
		const asks = toolCalls(tr, "AskUserQuestion");
		expect(asks).toHaveLength(1);
		expect(asks[0].permission?.status).toBe("allowed");
		expect(asks[0].result?.content).toEqual(expect.stringContaining('"Red"'));
		expect(asks[0].result?.content).toEqual(expect.stringContaining('"Chips, Fruit"'));
	});

	it("ends idle, with the last cost, mode, seq, notices and five turn ends", () => {
		expect(tr.pending).toEqual([]);
		expect(tr.queued).toEqual([]);
		expect(tr.busy).toBe(false);
		expect(tr.costUsd).toBe(0.04411749999999999);
		expect(tr.permissionMode).toBe("acceptEdits");
		expect(tr.lastSeq).toBe(67);
		expect(noticeKinds(tr)).toEqual(["spawn", "exit"]);
		const ends = tr.items.filter((i) => i.kind === "turn_end");
		expect(ends).toHaveLength(5);
		expect(ends[0]).toMatchObject({ isError: false });
	});

	const write = "ce40626b-c042-46c2-8d92-2588f30c93ef";
	const first = "ddecc06d-38b6-43e4-9756-8d15537e927e";
	const second = "6b6a4d66-83ae-48a8-be55-3de01739cff8";
	it.each([
		["input sent, not yet acknowledged", 4, [write], false, []],
		["input started", 6, [], true, []],
		["waiting on the Write permission", 12, [], true, ["Write"]],
		["permission answered", 13, [], true, []],
		["waiting on the question", 28, [], true, ["AskUserQuestion"]],
		["burst sent, first still running the earlier message", 49, [first, second], true, []],
		["first running, second queued", 53, [second], true, []],
		["first done, second not started", 59, [second], false, []],
		["second running", 60, [], true, []],
		["all done", 66, [], false, []],
	])("prefix: %s", (_name, seq, queued, busy, pending) => {
		const prefix = foldEntries(upTo(entries, seq as number));
		expect(prefix.queued).toEqual(queued);
		expect(prefix.busy).toBe(busy);
		expect(prefix.pending.map((p) => p.toolName)).toEqual(pending);
	});

	it("carries the pending permission's request onto both the pending list and the tool call", () => {
		const prefix = foldEntries(upTo(entries, 12));
		expect(prefix.pending).toHaveLength(1);
		const p = prefix.pending[0];
		expect(p).toMatchObject({
			kind: "permission",
			requestId: "a14f5ad2-62e1-4695-88c8-112417c012ac",
			toolUseId: "toolu_01Rcbbfzp9t7Z51pRQtCMHpY",
			input: { file_path: "/work/project/hello.txt", content: "hi" },
			suggestions: [{ type: "setMode", mode: "acceptEdits", destination: "session" }],
			defaultToNo: false,
		});
		const calls = toolCalls(prefix, "Write");
		expect(calls[0].permission).toMatchObject({ status: "pending", requestId: p.requestId });
	});

	it("classifies the AskUserQuestion request as a question", () => {
		const prefix = foldEntries(upTo(entries, 28));
		expect(prefix.pending.map((p) => p.kind)).toEqual(["question"]);
	});
});

describe("chat fold parity: resumed_session.jsonl", () => {
	const entries = loadFixture("resumed_session.jsonl");
	const tr = foldEntries(entries);

	it("marks the resume, the interrupt and the exit, naming the resumed session", () => {
		expect(noticeKinds(tr)).toEqual(["resumed", "interrupted", "exit"]);
		expect(notices(tr)[0].text).toBe("12e38fc9-a4a5-4819-b046-4107629b2c8b");
	});

	it("settles the two plans, denied with the reviewer's message then allowed", () => {
		const plans = toolCalls(tr, "ExitPlanMode");
		expect(plans).toHaveLength(2);
		expect(plans[0].permission).toMatchObject({
			status: "denied",
			message: "Rejected: name the file goodbye.txt instead and present the revised plan.",
		});
		expect(plans[1].permission?.status).toBe("allowed");
		expect(plans[0].result?.isError).toBe(true);
	});

	it("denies the Bash call with the chat UI message", () => {
		const bash = toolCalls(tr, "Bash");
		expect(bash).toHaveLength(1);
		expect(bash[0].permission).toMatchObject({ status: "denied", message: "The user denied rm from the chat UI." });
	});

	it("follows the interrupt with an errored turn end and cancels the interrupted input", () => {
		const at = tr.items.findIndex((i) => i.kind === "notice" && i.notice === "interrupted");
		expect(tr.items[at + 1]).toMatchObject({ kind: "turn_end", isError: true, subtype: "error_during_execution" });
		const all = users(tr);
		const essay = all[all.length - 2];
		expect(essay.text.startsWith("Write a 600-word essay")).toBe(true);
		expect(essay.state).toBe("cancelled");
	});

	it("ends idle on one claude session with the last cost", () => {
		expect(tr.claudeSessionIds).toHaveLength(1);
		expect(tr.pending).toEqual([]);
		expect(tr.queued).toEqual([]);
		expect(tr.busy).toBe(false);
		expect(tr.costUsd).toBe(0.09444599999999999);
	});

	it.each([
		[16, "default"],
		[17, "plan"],
		[43, "plan"],
		[44, "default"],
		[91, "default"],
	])("permission mode after seq %i is %s", (seq, mode) => {
		expect(foldEntries(upTo(entries, seq)).permissionMode).toBe(mode);
	});
});

describe("chat fold rules", () => {
	it.each(["first_session.jsonl", "resumed_session.jsonl"])("replaying %s changes nothing", (name) => {
		const entries = loadFixture(name);
		const want = foldEntries(entries);
		const folder = new ChatFolder();
		entries.forEach((e) => folder.apply(e));
		for (const replay of [entries, entries.slice(0, entries.length / 2), entries.slice(0, 1), entries.slice(-1)]) {
			replay.forEach((e) => folder.apply(e));
			expect(folder.transcript()).toEqual(want);
		}
	});

	it("keeps an earlier snapshot unchanged when later entries arrive", () => {
		const entries = loadFixture("first_session.jsonl");
		const folder = new ChatFolder();
		upTo(entries, 12).forEach((e) => folder.apply(e));
		const snap = folder.transcript();
		const frozen = structuredClone(snap);
		entries.slice(12).forEach((e) => folder.apply(e));
		expect(snap).toEqual(frozen);
		expect(folder.transcript()).not.toEqual(snap);
	});

	it("rejects bad entries without changing state or consuming their seq", () => {
		const folder = new ChatFolder();
		folder.apply(synth(1, "out", { type: "system", subtype: "init", session_id: "s1", permissionMode: "plan" }));
		const before = folder.transcript();
		for (const bad of [
			synth(2, "out", "{"),
			synth(2, "in", { uuid: "u" }),
			synth(2, "host", []),
			synth(2, "host", {}),
			synth(2, "sideways", { type: "x" }),
		]) {
			expect(() => folder.apply(bad)).toThrow();
		}
		expect(folder.transcript()).toEqual(before);
		folder.apply(synth(2, "out", { type: "hologram" }));
		expect(folder.transcript().lastSeq).toBe(2);
	});

	const toolUse = (name: string) =>
		synth(1, "out", {
			type: "assistant",
			uuid: "a1",
			message: { id: "m1", content: [{ type: "tool_use", id: "toolu_1", name, input: { x: 1 } }] },
		});
	const ask = (name: string) =>
		synth(2, "out", {
			type: "control_request",
			request_id: "req1",
			request: { subtype: "can_use_tool", tool_name: name, tool_use_id: "toolu_1", input: { x: 1 } },
		});
	const allow = json(
		`{"type":"control_response","response":{"subtype":"success","request_id":"req1","response":{"behavior":"allow","updatedInput":{"x":1}}}}`,
	);
	const deny = json(
		`{"type":"control_response","response":{"subtype":"success","request_id":"req1","response":{"behavior":"deny","message":"no thanks"}}}`,
	);
	const cancel = { type: "control_cancel_request", request_id: "req1" };
	const withTool = (name: string, rest: Array<[Direction, unknown]>) => [
		toolUse(name),
		ask(name),
		...rest.map(([dir, payload], i) => synth(3 + i, dir, payload)),
	];

	it.each<[string, Array<[Direction, unknown]>, string, string, number]>([
		["unanswered", [], "pending", "", 1],
		["host allows", [["in", allow]], "allowed", "", 0],
		["host denies", [["in", deny]], "denied", "no thanks", 0],
		[
			"echo after the host's answer changes nothing",
			[
				["in", deny],
				["out", deny],
			],
			"denied",
			"no thanks",
			0,
		],
		[
			"first answer wins over a conflicting later one",
			[
				["in", allow],
				["out", deny],
			],
			"allowed",
			"",
			0,
		],
		["answer seen only on stdout", [["out", deny]], "denied", "no thanks", 0],
		["cancelled by claude", [["out", cancel]], "cancelled", "", 0],
		[
			"cancel after an answer changes nothing",
			[
				["in", allow],
				["out", cancel],
			],
			"allowed",
			"",
			0,
		],
		[
			"answer after a cancel changes nothing",
			[
				["out", cancel],
				["in", allow],
			],
			"cancelled",
			"",
			0,
		],
		[
			"error response counts as denied",
			[
				[
					"in",
					json(
						`{"type":"control_response","response":{"subtype":"error","request_id":"req1","error":"handler crashed"}}`,
					),
				],
			],
			"denied",
			"handler crashed",
			0,
		],
		[
			"response to another request is ignored",
			[
				[
					"out",
					json(
						`{"type":"control_response","response":{"subtype":"success","request_id":"other","response":{"mode":"plan"}}}`,
					),
				],
			],
			"pending",
			"",
			1,
		],
	])("permission settlement: %s", (_name, rest, status, message, pending) => {
		const tr = foldEntries(withTool("Bash", rest));
		const calls = toolCalls(tr, "Bash");
		expect(calls).toHaveLength(1);
		expect(calls[0].permission).toMatchObject({ status, message, requestId: "req1" });
		expect(tr.pending).toHaveLength(pending);
	});

	it.each([
		["Bash", "permission"],
		["Write", "permission"],
		["AskUserQuestion", "question"],
		["ExitPlanMode", "plan"],
	])("a pending %s request is a %s", (tool, kind) => {
		const tr = foldEntries(withTool(tool, []));
		expect(tr.pending.map((p) => [p.toolName, p.kind])).toEqual([[tool, kind]]);
	});

	it("keeps pending requests oldest first", () => {
		const req = (seq: number, id: string) =>
			synth(seq, "out", {
				type: "control_request",
				request_id: id,
				request: { subtype: "can_use_tool", tool_name: "Bash", tool_use_id: `t-${id}`, input: {} },
			});
		const answer = (seq: number, id: string) =>
			synth(seq, "in", {
				type: "control_response",
				response: { subtype: "success", request_id: id, response: { behavior: "deny", message: "x" } },
			});
		const tr = foldEntries([req(1, "a"), req(2, "b"), req(3, "c"), answer(4, "b")]);
		expect(tr.pending.map((p) => p.requestId)).toEqual(["a", "c"]);
	});

	it("carries a request's default_to_no onto its pending entry", () => {
		const tr = foldEntries([
			synth(1, "out", {
				type: "control_request",
				request_id: "r",
				request: { subtype: "can_use_tool", tool_name: "Bash", tool_use_id: "t", input: {}, default_to_no: true },
			}),
		]);
		expect(tr.pending[0].defaultToNo).toBe(true);
	});

	it("follows the mode a status event reports", () => {
		const tr = foldEntries([
			synth(1, "out", { type: "system", subtype: "init", session_id: "s", permissionMode: "default" }),
			synth(2, "out", { type: "system", subtype: "status", status: null, permissionMode: "plan" }),
			synth(3, "out", { type: "system", subtype: "status", status: "compacting" }),
		]);
		expect(tr.permissionMode).toBe("plan");
	});

	it("attaches tool results by id and treats a repeated tool_use id as the same call", () => {
		const tr = foldEntries([
			synth(
				1,
				"out",
				json(
					`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]}}`,
				),
			),
			synth(
				2,
				"out",
				json(
					`{"type":"assistant","parent_tool_use_id":"toolu_1","message":{"id":"m2","content":[{"type":"tool_use","id":"toolu_2","name":"Grep","input":{}}]}}`,
				),
			),
			synth(
				3,
				"out",
				json(
					`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","content":[{"type":"text","text":"found"}]},{"type":"tool_result","tool_use_id":"toolu_missing","content":"x"}]}}`,
				),
			),
			synth(
				4,
				"out",
				json(
					`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"bad"}]}}`,
				),
			),
			synth(
				5,
				"out",
				json(
					`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]}}`,
				),
			),
		]);
		const calls = tr.items.filter((i): i is ToolCallItem => i.kind === "tool_call");
		expect(calls).toHaveLength(2);
		expect(calls[0].result).toEqual({ isError: true, content: "bad" });
		expect(calls[1]).toMatchObject({
			parentToolUseId: "toolu_1",
			result: { isError: false, content: [{ type: "text", text: "found" }] },
		});
	});

	it("turns assistant blocks into items and keeps the result's cost", () => {
		const tr = foldEntries([
			synth(
				1,
				"out",
				json(
					`{"type":"assistant","message":{"id":"m1","content":[{"type":"thinking","thinking":"","signature":"s"}]}}`,
				),
			),
			synth(2, "out", json(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hello"}]}}`)),
			synth(3, "out", json(`{"type":"result","subtype":"success","num_turns":1,"total_cost_usd":0.25}`)),
		]);
		expect(tr.items).toEqual([
			{ kind: "thinking", text: "" },
			{ kind: "assistant_text", messageId: "m1", text: "hello" },
			{ kind: "turn_end", subtype: "success", isError: false, numTurns: 1, costUsd: 0.25 },
		]);
		expect(tr.costUsd).toBe(0.25);
	});

	it.each([
		["[Request interrupted by user]", true],
		["[Request interrupted by user for tool use]", true],
		["please do not interrupt", false],
	])("interrupt marker %j is a notice: %s", (text, want) => {
		const tr = foldEntries([synth(1, "out", { type: "user", message: { content: [{ type: "text", text }] } })]);
		expect(tr.items.length === 1 && tr.items[0].kind === "notice" && tr.items[0].notice === "interrupted").toBe(want);
	});

	it("attaches a user origin in either order, and only to its own input", () => {
		const input = json(
			`{"type":"user","uuid":"u1","message":{"role":"user","content":"[from @w-1] hi"},"parent_tool_use_id":null,"session_id":""}`,
		);
		const note = { kind: "user_origin", uuid: "u1", origin: { via: "ao_send", from: "w-1" } };
		for (const entries of [
			[synth(1, "host", note), synth(2, "in", input)],
			[synth(1, "in", input), synth(2, "host", note)],
		]) {
			expect(users(foldEntries(entries))[0].origin).toEqual({ via: "ao_send", from: "w-1" });
		}
		const other = foldEntries([
			synth(1, "in", input),
			synth(2, "host", { kind: "user_origin", uuid: "other", origin: { via: "ao_send", from: "w-2" } }),
		]);
		expect(users(other)[0].origin).toBeUndefined();
	});

	it("deduplicates user input by uuid and counts images", () => {
		const input = json(
			`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA"}}]}}`,
		);
		const tr = foldEntries([
			synth(1, "in", input),
			synth(2, "out", json(`{"type":"user","uuid":"u1","isReplay":true,"message":{"content":"look"}}`)),
			synth(3, "in", input),
		]);
		expect(tr.items).toEqual([{ kind: "user", uuid: "u1", text: "look", images: 1, state: "sent" }]);
		expect(tr.queued).toEqual(["u1"]);
	});

	it("applies a lifecycle that arrives before its input", () => {
		const tr = foldEntries([
			synth(1, "out", { type: "command_lifecycle", command_uuid: "u1", state: "started" }),
			synth(2, "in", { type: "user", uuid: "u1", message: { content: "hi" } }),
		]);
		expect(users(tr)[0].state).toBe("started");
		expect(tr.busy).toBe(true);
		expect(tr.queued).toEqual([]);
	});

	describe("a process end settles what it left behind", () => {
		const entries = loadFixture("first_session.jsonl");
		const exit = synth(1000, "host", { kind: "exit", code: null, signal: "SIGKILL" });

		it("exit while waiting on a permission", () => {
			const tr = foldEntries([...upTo(entries, 12), exit]);
			expect([tr.busy, tr.pending, tr.queued]).toEqual([false, [], []]);
			expect(toolCalls(tr, "Write")[0].permission?.status).toBe("cancelled");
			expect(users(tr)[0].state).toBe("cancelled");
			expect(notices(tr).at(-1)).toEqual({ kind: "notice", notice: "exit", text: "terminated by signal" });
		});

		it("exit with queued input", () => {
			const tr = foldEntries([...upTo(entries, 53), exit]);
			expect([tr.busy, tr.queued]).toEqual([false, []]);
			const all = users(tr);
			expect(all.at(-1)?.state).toBe("cancelled");
			expect(all[1].state).toBe("completed");
		});

		it("exit cancels an input claude never acknowledged", () => {
			const tr = foldEntries([synth(1, "in", { type: "user", uuid: "u1", message: { content: "hi" } }), exit]);
			expect(users(tr)[0].state).toBe("cancelled");
			expect(tr.queued).toEqual([]);
		});

		it("a spawn after a crashed host", () => {
			const spawn = synth(1000, "host", {
				kind: "spawn",
				argv: ["claude"],
				resume: "12e38fc9-a4a5-4819-b046-4107629b2c8b",
				pid: 7,
			});
			const tr = foldEntries([...upTo(entries, 12), spawn]);
			expect([tr.busy, tr.pending, tr.queued]).toEqual([false, [], []]);
			expect(toolCalls(tr, "Write")[0].permission?.status).toBe("cancelled");
			expect(users(tr)[0].state).toBe("cancelled");
			expect(notices(tr).at(-1)).toEqual({
				kind: "notice",
				notice: "resumed",
				text: "12e38fc9-a4a5-4819-b046-4107629b2c8b",
			});
		});
	});

	it("turns a compact boundary into a notice and nothing else", () => {
		const tr = foldEntries([
			synth(1, "out", {
				type: "system",
				subtype: "compact_boundary",
				session_id: "s",
				compact_metadata: { trigger: "manual" },
			}),
			synth(2, "out", {
				type: "user",
				uuid: "c1",
				session_id: "s",
				message: { role: "user", content: "This session is being continued..." },
			}),
		]);
		expect(tr.items).toEqual([{ kind: "notice", notice: "compacted", text: "" }]);
	});

	it("lets unknown events change nothing but the seq", () => {
		const entries = upTo(loadFixture("first_session.jsonl"), 12);
		const base = foldEntries(entries);
		const folder = new ChatFolder();
		entries.forEach((e) => folder.apply(e));
		let seq = 100;
		for (const payload of [
			{ type: "hologram", x: 1 },
			{ type: "system", subtype: "future_thing", session_id: "s" },
			{ type: "system", subtype: "status", status: "compacting" },
			{ type: "rate_limit_event" },
			{ type: "stream_event", event: { type: "message_start" } },
			{ type: "control_request", request_id: "x", request: { subtype: "mcp_status" } },
			{ type: "user", uuid: "r", isReplay: true, message: { content: "replayed" } },
		]) {
			folder.apply(synth(++seq, "out", payload));
		}
		folder.apply(synth(++seq, "host", { kind: "future_note" }));
		expect({ ...folder.transcript(), lastSeq: base.lastSeq }).toEqual(base);
	});
});

describe("chat fold parity: commands, models and the model in use", () => {
	it.each(["first_session.jsonl", "resumed_session.jsonl"])("reads them from %s", (name) => {
		const entries = loadFixture(name);
		const before = foldEntries(upTo(entries, 2));
		expect(before).toMatchObject({ model: "sonnet", commands: [], models: [] });

		const tr = foldEntries(entries);
		expect(tr.commands).toEqual([
			{ name: "compact", description: "Clear history but keep a summary in context", argumentHint: "", builtin: true },
			{ name: "review", description: "Review a pull request", argumentHint: "", builtin: true },
			{ name: "tdd", description: "Write the failing test first", argumentHint: "", builtin: false },
		]);
		expect(tr.models).toHaveLength(13);
		expect(tr.models[1]).toEqual({
			value: "opus",
			resolvedModel: "claude-opus-5-5",
			displayName: "Opus 5.5",
			description: "For complex work and everyday tasks",
			supportsEffort: true,
			supportedEffortLevels: ["low", "medium", "high", "xhigh", "max"],
		});
		expect(tr.model).toBe("claude-sonnet-5-5");
	});

	it("never carries the account block claude sends with them", () => {
		const tr = foldEntries(loadFixture("first_session.jsonl"));
		expect(Object.keys(tr)).not.toContain("account");
		const text = JSON.stringify(tr);
		for (const secret of ["dev@example.com", "Example Org", "subscriptionType"]) expect(text).not.toContain(secret);
	});

	it("follows an accepted set_model and ignores a refused one", () => {
		const entries = loadFixture("set_model.jsonl");
		expect(foldEntries(upTo(entries, 2)).model).toBe("claude-sonnet-5-5");
		expect(foldEntries(entries).model).toBe("claude-sonnet-5-5");
		const unasked = foldEntries([
			synth(1, "out", {
				type: "control_response",
				response: { subtype: "success", request_id: "x", response: { model: "claude-haiku-5-5" } },
			}),
		]);
		expect(unasked.model).toBe("");
	});

	it("keeps the last accepted initialize over a failed one", () => {
		const initialize = (seq: number, id: string) =>
			synth(seq, "in", { type: "control_request", request_id: id, request: { subtype: "initialize" } });
		const tr = foldEntries([
			initialize(1, "a"),
			synth(2, "out", {
				type: "control_response",
				response: {
					subtype: "success",
					request_id: "a",
					response: {
						commands: [{ name: "compact", description: "d", argumentHint: "<what>" }],
						models: [{ value: "haiku", displayName: "Haiku" }],
						account: { email: "x@example.com" },
					},
				},
			}),
			initialize(3, "b"),
			synth(4, "out", { type: "control_response", response: { subtype: "error", request_id: "b", error: "boom" } }),
		]);
		expect(tr.commands).toEqual([{ name: "compact", description: "d", argumentHint: "<what>" }]);
		expect(tr.models.map((m) => m.value)).toEqual(["haiku"]);
	});

	it.each([
		[["claude", "--model", "opus"], "opus"],
		[["claude", "--model=haiku"], "haiku"],
		[["claude", "--", "--model", "opus"], ""],
		[["claude", "--model"], ""],
	])("a spawn with argv %j takes model %j", (argv, want) => {
		const tr = foldEntries([
			synth(1, "out", { type: "system", subtype: "init", session_id: "s", model: "claude-sonnet-5-5" }),
			synth(2, "host", { kind: "spawn", resume: "", argv }),
		]);
		expect(tr.model).toBe(want);
	});
});
