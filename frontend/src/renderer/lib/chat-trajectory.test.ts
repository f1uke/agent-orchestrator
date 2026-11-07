import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { ChatEntry } from "./chat-event";
import { foldEntries } from "./chat-fold";
import {
	buildTrajectory,
	capText,
	filterRows,
	formatSpan,
	fullText,
	layoutBars,
	rawEntries,
	type Trajectory,
	visibleRows,
} from "./chat-trajectory";

const dir = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
const loadFixture = (name: string): ChatEntry[] =>
	readFileSync(join(dir, name), "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line) => JSON.parse(line) as ChatEntry);

const NOW = Date.parse("2030-01-01T00:00:00Z");
const trajectoryOf = (entries: ChatEntry[], root = ""): Trajectory =>
	buildTrajectory(foldEntries(entries), entries, { root });

describe("buildTrajectory on the recorded long session", () => {
	const entries = loadFixture("long_session.jsonl");
	const t = trajectoryOf(entries, "/work/project");
	const tools = t.rows.filter((r) => r.kind === "tool");

	it("has one row per tool call, a row per user prompt and per turn end, and no empty thinking", () => {
		expect(tools).toHaveLength(27);
		expect(t.calls).toBe(27);
		expect(t.rows.filter((r) => r.kind === "user")).toHaveLength(2);
		expect(t.rows.filter((r) => r.kind === "turn_end")).toHaveLength(2);
		expect(t.rows.filter((r) => r.kind === "thinking")).toHaveLength(0);
	});

	it("summarises a tool by its name, input and first result line, with paths shortened to the worktree", () => {
		const bash = tools.find((r) => r.title === "Bash")!;
		expect(bash.detail).toBe("scripts/slow-check.sh");
		const edit = tools.find((r) => r.title === "Edit")!;
		expect(edit.detail).toBe("ledger/ledger.go");
		expect(edit.outcome).not.toContain("\n");
		expect(edit.outcome).not.toBe("");
	});

	it("flags the failed command and the failed background task as errors, and nothing else", () => {
		const errors = t.rows.filter((r) => r.tone === "error");
		expect(errors.map((r) => [r.kind, r.title])).toEqual([
			["task", "failed"],
			["tool", "Bash"],
		]);
		expect(errors[0].detail).toMatch(/failed with exit code 1/);
		expect(errors[1].detail).toBe("go test -count=1 ./ledger");
		expect(errors[1].outcome).toMatch(/^Exit code/);
		expect(t.errors).toBe(2);
	});

	it("numbers the two turns and marks where each starts", () => {
		expect(t.turns).toBe(2);
		const starts = t.rows.filter((r) => r.startsTurn);
		expect(starts.map((r) => [r.kind, r.turn])).toEqual([
			["user", 1],
			["user", 2],
		]);
		expect(t.rows.find((r) => r.kind === "notice")?.turn).toBe(0);
		expect(t.rows.filter((r) => r.kind === "tool" && r.turn === 2)).toHaveLength(4);
	});

	it("nests the sub-agent calls under their Agent row, collapsed with a count", () => {
		const agent = tools.find((r) => r.title === "Agent")!;
		expect(agent.children).toBe(2);
		const kids = t.rows.filter((r) => r.parent === agent.key);
		expect(kids).toHaveLength(2);
		expect(kids.every((k) => k.kind === "tool" && k.title === "Bash")).toBe(true);
		const at = t.rows.indexOf(agent);
		expect(t.rows.slice(at + 1, at + 3)).toEqual(kids);

		const collapsed = visibleRows(t.rows, new Set());
		expect(collapsed.some((r) => r.parent !== undefined)).toBe(false);
		const open = visibleRows(t.rows, new Set([agent.key]));
		expect(open.filter((r) => r.parent === agent.key)).toHaveLength(2);
	});

	it("ends each turn with its duration, call count and the cost of that turn alone", () => {
		const [first, second] = t.rows.filter((r) => r.kind === "turn_end");
		expect(first.title).toBe("Completed");
		expect(first.detail).toMatch(/1m 21s/);
		expect(first.detail).toMatch(/23 calls/);
		expect(first.detail).toMatch(/\$0\.142/);
		expect(second.detail).toMatch(/4 calls/);
		expect(second.detail).toMatch(/\$0\.028/);
	});

	it("draws one Tools bar per call in both modes, inside the strip", () => {
		for (const axis of ["steps", "duration"] as const) {
			const { bars } = layoutBars(t, axis, NOW);
			expect(bars.filter((b) => b.lane === "tools")).toHaveLength(27);
			expect(bars.filter((b) => b.lane === "input")).toHaveLength(2);
			expect(bars.length).toBeGreaterThan(27 + 2);
			for (const b of bars) {
				expect(b.start).toBeGreaterThanOrEqual(0);
				expect(b.end).toBeLessThanOrEqual(1.0000001);
				expect(b.end).toBeGreaterThanOrEqual(b.start);
			}
		}
	});
});

describe("buildTrajectory on a crafted session", () => {
	let seq = 0;
	const at = (sec: number) => new Date(Date.UTC(2026, 9, 30, 10, 0, sec)).toISOString();
	const e = (sec: number, dir: ChatEntry["dir"], payload: object): ChatEntry => ({
		seq: ++seq,
		ts: at(sec),
		dir,
		payload,
	});
	const use = (sec: number, id: string, name: string, input: object, parent: string | null = null) =>
		e(sec, "out", {
			type: "assistant",
			parent_tool_use_id: parent,
			message: { id: `m-${id}`, content: [{ type: "tool_use", id, name, input }] },
		});
	const done = (sec: number, id: string, content: string, isError = false, parent: string | null = null) =>
		e(sec, "out", {
			type: "user",
			parent_tool_use_id: parent,
			message: { content: [{ type: "tool_result", tool_use_id: id, content, is_error: isError }] },
		});
	const say = (sec: number, id: string, text: string) =>
		e(sec, "out", { type: "assistant", message: { id, content: [{ type: "text", text }] } });
	const asked = (sec: number, uuid: string, text: string) =>
		e(sec, "in", { type: "user", uuid, message: { content: text } });
	const lifecycle = (sec: number, uuid: string, state: string) =>
		e(sec, "out", { type: "command_lifecycle", command_uuid: uuid, state });
	const ask = (sec: number, uuid: string, text: string) => [asked(sec, uuid, text), lifecycle(sec, uuid, "started")];
	const result = (sec: number, cost: number) =>
		e(sec, "out", { type: "result", subtype: "success", is_error: false, num_turns: 1, total_cost_usd: cost });

	const log = (): ChatEntry[] => {
		seq = 0;
		return [
			...ask(0, "u1", "Fix the bug\nsecond line"),
			say(1, "m1", "Looking at it now\nmore"),
			use(2, "t1", "Read", { file_path: "/w/a.go" }),
			done(3, "t1", "package a\nline two"),
			use(4, "t2", "Agent", { subagent_type: "Explore", description: "scan" }),
			use(5, "t3", "Grep", { pattern: "foo", path: "/w" }, "t2"),
			done(6, "t3", "Found 2 files", false, "t2"),
			done(8, "t2", "done scanning"),
			use(9, "t4", "Bash", { command: "go  test\n./..." }),
			done(10, "t4", "Exit code 1\nFAIL", true),
			result(11, 0.5),
			...ask(12, "u2", "again"),
			e(13, "out", {
				type: "system",
				subtype: "compact_boundary",
				compact_metadata: { pre_tokens: 200000, post_tokens: 20000 },
			}),
			use(14, "t5", "Bash", { command: "sleep 9" }),
		];
	};

	it("gives each kind of item a labelled row", () => {
		const t = trajectoryOf(log());
		expect(t.rows.map((r) => [r.kind, r.title, r.detail])).toEqual([
			["user", "", "Fix the bug"],
			["assistant", "", "Looking at it now"],
			["tool", "Read", "/w/a.go"],
			["tool", "Agent", 'Explore · "scan"'],
			["tool", "Grep", '"foo" in /w'],
			["tool", "Bash", "go test ./..."],
			["turn_end", "Completed", expect.stringContaining("4 calls")],
			["user", "", "again"],
			["notice", "Conversation compacted", "200k → 20k tokens"],
			["tool", "Bash", "sleep 9"],
		]);
	});

	it("carries the first result line, an error tone and a running flag", () => {
		const rows = trajectoryOf(log()).rows.filter((r) => r.kind === "tool");
		expect(rows.map((r) => [r.title, r.outcome, r.tone, r.running])).toEqual([
			["Read", "package a", "normal", false],
			["Agent", "done scanning", "normal", false],
			["Grep", "Found 2 files", "normal", false],
			["Bash", "Exit code 1", "error", false],
			["Bash", "", "normal", true],
		]);
	});

	it("says compacted with the tokens when it knows them and plainly when it does not", () => {
		seq = 0;
		const plain = trajectoryOf([e(1, "out", { type: "system", subtype: "compact_boundary" })]);
		expect(plain.rows.map((r) => [r.title, r.detail])).toEqual([["Conversation compacted", ""]]);
		seq = 0;
		const pre = trajectoryOf([
			e(1, "out", { type: "system", subtype: "compact_boundary", compact_metadata: { pre_tokens: 12000 } }),
		]);
		expect(pre.rows[0].detail).toBe("from 12k tokens");
	});

	it("shows interrupted and a failed turn in a warning or danger tone", () => {
		seq = 0;
		const t = trajectoryOf([
			...ask(0, "u1", "go"),
			e(1, "out", { type: "user", message: { content: [{ type: "text", text: "[Request interrupted by user]" }] } }),
			e(2, "out", { type: "result", subtype: "error_max_turns", is_error: true, total_cost_usd: 0.1 }),
		]);
		expect(t.rows.map((r) => [r.kind, r.title, r.tone])).toEqual([
			["user", "", "normal"],
			["notice", "Interrupted", "warn"],
			["turn_end", "Turn limit reached", "error"],
		]);
		expect(t.errors).toBe(1);
	});

	it("narrows by search over the tool name, input summary and first result line", () => {
		const t = trajectoryOf(log());
		const titles = (q: string) => filterRows(t.rows, { query: q, kind: "all" }).map((r) => r.title || r.kind);
		expect(titles("grep")).toEqual(["Grep"]);
		expect(titles("go test")).toEqual(["Bash"]);
		expect(titles("EXIT CODE")).toEqual(["Bash"]);
		expect(titles("no such text")).toEqual([]);
		expect(titles("  ")).toHaveLength(t.rows.length);
	});

	it("narrows by kind: tools, errors, turns", () => {
		const t = trajectoryOf(log());
		const kinds = (kind: "all" | "tools" | "errors" | "turns") =>
			filterRows(t.rows, { query: "", kind }).map((r) => r.kind);
		expect(kinds("tools")).toEqual(["tool", "tool", "tool", "tool", "tool"]);
		expect(kinds("errors")).toEqual(["tool"]);
		expect(kinds("turns")).toEqual(["user", "turn_end", "user"]);
		expect(kinds("all")).toHaveLength(t.rows.length);
	});

	it("counts the calls and errors the toolbar shows", () => {
		const t = trajectoryOf(log());
		expect([t.calls, t.errors, t.turns]).toEqual([5, 1, 2]);
	});

	it("places Input, Model and Tools bars by real time in duration mode, with a divider where turn 2 starts", () => {
		const t = trajectoryOf(log());
		const { bars, dividers } = layoutBars(t, "duration", Date.UTC(2026, 9, 30, 10, 0, 20));
		const total = 20;
		const lane = (name: string) => bars.filter((b) => b.lane === name);
		expect(lane("input").map((b) => b.start)).toEqual([0, 12 / total]);
		expect(dividers).toEqual([12 / total]);
		const read = lane("tools").find((b) => b.label.startsWith("Read"))!;
		expect(read.start).toBeCloseTo(2 / total);
		expect(read.end).toBeCloseTo(3 / total);
		const running = lane("tools").at(-1)!;
		expect(running.running).toBe(true);
		expect(running.end).toBeCloseTo(1);
		const failed = lane("tools").find((b) => b.tone === "error")!;
		expect(failed.label).toMatch(/^Bash/);
		expect(lane("model").length).toBeGreaterThan(0);
	});

	it("gives every step its own equal column in steps mode, in order, with a divider where each turn starts", () => {
		const t = trajectoryOf(log());
		const { bars, dividers } = layoutBars(t, "steps", NOW);
		const n = bars.length;
		expect(new Set(bars.map((b) => b.start)).size).toBe(n);
		bars.forEach((b, i) => {
			expect(b.start).toBeCloseTo(i / n);
			expect(b.end).toBeCloseTo((i + 1) / n);
		});
		const secondTurn = bars.findIndex((b) => b.turn === 2);
		expect(dividers).toHaveLength(1);
		expect(dividers[0]).toBeCloseTo(secondTurn / n);
		expect(bars.slice(0, secondTurn).every((b) => b.turn === 1)).toBe(true);
	});

	it("colours typed input blue, the model purple, a tool orange and a failed tool red", () => {
		const t = trajectoryOf(log());
		const { bars } = layoutBars(t, "steps", NOW);
		const tones = (lane: string) => new Set(bars.filter((b) => b.lane === lane).map((b) => b.tone));
		expect(tones("input")).toEqual(new Set(["input"]));
		expect(tones("model")).toEqual(new Set(["model"]));
		expect(tones("tools")).toEqual(new Set(["tool", "error"]));
		expect(bars.find((b) => b.label.startsWith("Bash go"))!.tone).toBe("error");
		expect(bars.find((b) => b.label.startsWith("Read"))!.tone).toBe("tool");
	});

	it("colours injected context green: a task brief, a peer message and a background task that opens a turn", () => {
		seq = 0;
		const note = (sec: number, payload: object) => e(sec, "host" as ChatEntry["dir"], payload);
		const entries = [
			note(0, { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }),
			...ask(1, "b", "/pstack:poteto-mode # Goal"),
			result(2, 0.1),
			note(3, { kind: "user_origin", uuid: "p", origin: { via: "ao_send", from: "agent-orchestrator-orchestrator" } }),
			...ask(3, "p", "[from @agent-orchestrator-orchestrator] hello"),
			result(4, 0.2),
			e(5, "out", {
				type: "user",
				isReplay: true,
				message: {
					role: "user",
					content:
						"<task-notification>\n<task-id>k1</task-id>\n<status>completed</status>\n<summary>done</summary>\n</task-notification>",
				},
			}),
			result(6, 0.3),
			...ask(7, "u", "typed by a person"),
		];
		const { bars } = layoutBars(trajectoryOf(entries), "steps", NOW);
		expect(bars.filter((b) => b.lane === "input").map((b) => [b.label, b.tone])).toEqual([
			["Task brief", "context"],
			["orchestrator", "context"],
			["completed", "context"],
			["Input", "input"],
		]);
	});

	it("points every bar at a row, and a sub-agent call's bar at its nested row", () => {
		const t = trajectoryOf(log());
		const keys = new Set(t.rows.map((r) => r.key));
		const { bars } = layoutBars(t, "duration", NOW);
		expect(bars.every((b) => keys.has(b.key))).toBe(true);
		const grep = bars.find((b) => b.label.startsWith("Grep"))!;
		expect(t.rows.find((r) => r.key === grep.key)?.parent).toBeDefined();
	});

	it("has no bars and no dividers for an empty log", () => {
		expect(layoutBars(trajectoryOf([]), "steps", NOW)).toEqual({ bars: [], dividers: [] });
	});

	it("finds the log entries a row came from: a tool call's request, result and nothing else", () => {
		const entries = log();
		const t = trajectoryOf(entries);
		const read = t.rows.find((r) => r.title === "Read")!;
		expect(rawEntries(read, entries).map((x) => x.seq)).toEqual([4, 5]);
		const user = t.rows.find((r) => r.kind === "user")!;
		expect(rawEntries(user, entries).map((x) => x.seq)).toEqual([1]);
	});

	it("shows a queued message dim, and not as the start of a turn", () => {
		seq = 0;
		const entries = [...ask(0, "u1", "first"), asked(1, "u2", "second"), lifecycle(1.5, "u2", "queued")];
		const t = trajectoryOf(entries);
		expect(t.rows.map((r) => [r.detail, r.title, r.startsTurn, r.tone])).toEqual([
			["first", "", true, "normal"],
			["second", "queued", false, "dim"],
		]);
		expect(t.turns).toBe(1);
	});
});

describe("fullText, capText and formatSpan", () => {
	it("opens a tool row to its whole input and result, and a command to the command itself", () => {
		const t = trajectoryOf(loadFixture("long_session.jsonl"), "/work/project");
		const edit = t.rows.find((r) => r.title === "Edit")!;
		const text = fullText(edit);
		expect(JSON.parse(text.input).file_path).toBe("/work/project/ledger/ledger.go");
		expect(text.result).not.toBe("");
		const bash = t.rows.find((r) => r.title === "Bash")!;
		expect(fullText(bash).input).toBe("scripts/slow-check.sh");
		const failed = t.rows.find((r) => r.tone === "error" && r.kind === "tool")!;
		expect(fullText(failed).resultIsError).toBe(true);
		const user = t.rows.find((r) => r.kind === "user")!;
		expect(fullText(user).input).toMatch(/^The tests in this repo fail/);
		const end = t.rows.find((r) => r.kind === "turn_end")!;
		expect(fullText(end).input).toMatch(/in · .* out/);
	});

	it("caps by lines and by characters and says how many lines it left out", () => {
		const text = Array.from({ length: 10 }, (_, i) => `line ${i}`).join("\n");
		expect(capText(text, 4, 1000)).toEqual({ text: "line 0\nline 1\nline 2\nline 3", hidden: 6 });
		expect(capText(text, 100, 1000)).toEqual({ text, hidden: 0 });
		const long = capText("x".repeat(50), 5, 10);
		expect(long.text).toBe("x".repeat(10));
		expect(long.hidden).toBe(1);
	});

	it("writes milliseconds below a second and the chat's duration above it", () => {
		expect([formatSpan(0), formatSpan(412), formatSpan(1500), formatSpan(81000)]).toEqual([
			"0ms",
			"412ms",
			"1s",
			"1m 21s",
		]);
	});
});
