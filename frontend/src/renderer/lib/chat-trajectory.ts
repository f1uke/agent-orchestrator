import { type ChatEntry, isObject, isPeerOrigin } from "./chat-event";
import type { BashItem, ChatItem, ToolCallItem, Transcript } from "./chat-fold";
import { formatCost, formatTokens } from "./chat-inspect";
import { noticeText } from "./chat-notice";
import { formatDuration } from "./chat-time";
import { inputSummary, resultText, toolFailed, toolRunning } from "./chat-tools";

export type RowKind = "user" | "assistant" | "thinking" | "tool" | "task" | "bash" | "turn_end" | "notice";

export type Tone = "normal" | "dim" | "warn" | "error";

export type TrajectoryRow = {
	/** The item's key in the transcript: stable as older entries are folded in. */
	key: string;
	seq: number;
	order: number;
	kind: RowKind;
	title: string;
	detail: string;
	outcome: string;
	tone: Tone;
	running: boolean;
	/** 1-based; 0 before the first turn. */
	turn: number;
	startsTurn: boolean;
	parent?: string;
	children: number;
	/** Epoch ms of the row, NaN when the log does not say. */
	t0: number;
	/** Epoch ms the row ended: a tool's result, NaN while it runs or when unknown. */
	t1: number;
	item: ChatItem;
};

export type Trajectory = {
	rows: TrajectoryRow[];
	turns: number;
	calls: number;
	errors: number;
	live: boolean;
};

const LINE_CHARS = 300;

const firstLine = (text: string): string => {
	const line = text.split("\n").find((l) => l.trim() !== "") ?? "";
	const trimmed = line.trim();
	return trimmed.length > LINE_CHARS ? `${trimmed.slice(0, LINE_CHARS)}…` : trimmed;
};

const SLASH_COMMAND = /^\/([\w:.-]+)\s*/;
const PEER_PREFIX = /^\[from @[^\]]*\]\s*/;

function userLabel(item: Extract<ChatItem, { kind: "user" }>): { title: string; detail: string } {
	const text = item.text.replace(PEER_PREFIX, "");
	if (isPeerOrigin(item.origin)) {
		const from = item.origin.from;
		return { title: from.endsWith("-orchestrator") ? "orchestrator" : `@${from}`, detail: firstLine(text) };
	}
	if (item.brief) {
		const command = SLASH_COMMAND.exec(text);
		return { title: "Task brief", detail: firstLine(command ? text.slice(command[0].length) : text) };
	}
	return { title: "", detail: firstLine(text) };
}

function turnEndFigures(took: number | undefined, calls: number, cost: number): string {
	const parts: string[] = [];
	if (took !== undefined) parts.push(formatDuration(took));
	parts.push(`${calls} ${calls === 1 ? "call" : "calls"}`);
	if (cost > 0) parts.push(formatCost(cost));
	return parts.join(" · ");
}

const ms = (ts: string | undefined) => (ts === undefined || ts === "" ? Number.NaN : Date.parse(ts));

function bashOutcome(item: BashItem): string {
	if (item.state === "stopped") return "stopped";
	if (item.state === "running") return "";
	return item.signal ? item.signal : `exit ${item.code ?? 0}`;
}

function toolOutcome(call: ToolCallItem, live: boolean): { outcome: string; tone: Tone; running: boolean } {
	const status = call.permission?.status;
	if (status === "pending") return { outcome: "Waiting for approval", tone: "warn", running: false };
	if (status === "denied") return { outcome: "Denied", tone: "error", running: false };
	if (status === "cancelled") return { outcome: "Cancelled", tone: "dim", running: false };
	if (!call.result) {
		return live
			? { outcome: "", tone: "normal", running: toolRunning(call) }
			: { outcome: "No result", tone: "dim", running: false };
	}
	return { outcome: firstLine(resultText(call.result)), tone: toolFailed(call) ? "error" : "normal", running: false };
}

// An assistant message has no time of its own in the transcript, so its row takes the time of its log entry.
export function buildTrajectory(
	transcript: Transcript,
	entries: readonly ChatEntry[],
	{ root }: { root: string },
): Trajectory {
	const tsOfSeq = new Map(entries.map((e) => [e.seq, e.ts]));
	const { items, itemKeys } = transcript;
	const live = transcript.busy;

	const toolById = new Set(items.flatMap((i) => (i.kind === "tool_call" ? [i.id] : [])));
	const rows: TrajectoryRow[] = [];
	let turn = 0;
	let calls = 0;
	let errors = 0;
	let turnCalls = 0;
	let lastCost = 0;
	let turnStartMs = Number.NaN;

	items.forEach((item, order) => {
		const key = itemKeys[order];
		const seq = Number.parseInt(key, 10);
		const seqTs = tsOfSeq.get(seq);
		const base = {
			key,
			seq,
			order,
			item,
			running: false,
			tone: "normal" as Tone,
			outcome: "",
			startsTurn: false,
			children: 0,
			t0: ms(seqTs),
			t1: Number.NaN,
		};
		const push = (row: Omit<TrajectoryRow, keyof typeof base | "turn"> & Partial<TrajectoryRow>) =>
			rows.push({ ...base, turn, ...row });

		switch (item.kind) {
			case "user": {
				const queued = item.state === "sent" || item.state === "queued";
				const { title, detail } = userLabel(item);
				if (!queued) {
					turn += 1;
					turnCalls = 0;
					turnStartMs = ms(item.ts);
				}
				push({
					kind: "user",
					title: queued ? "queued" : title,
					detail,
					tone: queued ? "dim" : "normal",
					startsTurn: !queued,
					t0: ms(item.ts),
				});
				return;
			}
			case "task_message": {
				if (item.opensTurn) {
					turn += 1;
					turnCalls = 0;
					turnStartMs = ms(item.ts);
				}
				push({
					kind: "task",
					title: item.status || "task",
					detail: firstLine(item.summary),
					tone: item.status === "failed" || item.status === "error" ? "error" : "normal",
					startsTurn: item.opensTurn,
					t0: ms(item.ts),
				});
				return;
			}
			case "bash":
				push({
					kind: "bash",
					title: "!",
					detail: firstLine(item.command),
					outcome: bashOutcome(item),
					tone: item.state === "running" ? "normal" : item.state === "stopped" || item.code ? "error" : "normal",
					running: item.state === "running",
					t0: ms(item.ts),
				});
				return;
			case "assistant_text":
			case "thinking":
				if (item.parentToolUseId !== "" || item.text.trim() === "") return;
				push({
					kind: item.kind === "thinking" ? "thinking" : "assistant",
					title: "",
					detail: firstLine(item.text),
					tone: item.kind === "thinking" ? "dim" : "normal",
				});
				return;
			case "tool_call": {
				calls += 1;
				turnCalls += 1;
				const outcome = toolOutcome(item, live);
				push({
					kind: "tool",
					title: item.name,
					detail: inputSummary(item, root),
					...outcome,
					t0: ms(item.startedTs),
					t1: ms(item.endedTs),
					parent: item.parentToolUseId !== "" && toolById.has(item.parentToolUseId) ? item.parentToolUseId : undefined,
				});
				return;
			}
			case "turn_end": {
				const label = item.error?.label ?? "Completed";
				const cost = item.costUsd >= lastCost ? item.costUsd - lastCost : item.costUsd;
				lastCost = item.costUsd;
				const t0 = ms(item.ts);
				push({
					kind: "turn_end",
					title: label,
					detail: turnEndFigures(item.durationMs ?? (t0 - turnStartMs || undefined), turnCalls, cost),
					tone: !item.isError ? "normal" : item.subtype === "error_during_execution" ? "warn" : "error",
					t0,
				});
				return;
			}
			case "notice": {
				const tokens = item.tokens;
				push({
					kind: "notice",
					title: noticeText(item),
					detail: tokens
						? tokens.post === undefined
							? `from ${formatTokens(tokens.pre)} tokens`
							: `${formatTokens(tokens.pre)} → ${formatTokens(tokens.post)} tokens`
						: "",
					tone: item.notice === "interrupted" ? "warn" : item.notice === "api_error" ? "error" : "normal",
					t0: ms(item.ts),
				});
				return;
			}
		}
	});

	const keyOfTool = new Map(rows.flatMap((r) => (r.item.kind === "tool_call" ? [[r.item.id, r.key] as const] : [])));
	const nested = new Map<string, TrajectoryRow[]>();
	const top: TrajectoryRow[] = [];
	for (const row of rows) {
		const parentKey = row.item.kind === "tool_call" && row.parent !== undefined ? keyOfTool.get(row.parent) : undefined;
		if (parentKey === undefined) {
			top.push({ ...row, parent: undefined });
		} else {
			const kids = nested.get(parentKey) ?? [];
			kids.push({ ...row, parent: parentKey });
			nested.set(parentKey, kids);
		}
	}
	const ordered = top.flatMap((row) => {
		const kids = nested.get(row.key) ?? [];
		for (const kid of kids) kid.turn = row.turn;
		return [{ ...row, children: kids.length }, ...kids];
	});
	for (const row of ordered) if (row.tone === "error") errors += 1;

	return { rows: ordered, turns: turn, calls, errors, live };
}

export type KindFilter = "all" | "tools" | "errors" | "turns";

export type RowFilter = { query: string; kind: KindFilter };

export const isFiltering = (filter: RowFilter) => filter.query.trim() !== "" || filter.kind !== "all";

const KIND_TEST: Record<KindFilter, (row: TrajectoryRow) => boolean> = {
	all: () => true,
	tools: (row) => row.kind === "tool",
	errors: (row) => row.tone === "error",
	turns: (row) => row.startsTurn || row.kind === "turn_end",
};

export function filterRows(rows: readonly TrajectoryRow[], { query, kind }: RowFilter): TrajectoryRow[] {
	const needle = query.trim().toLowerCase();
	const kindTest = KIND_TEST[kind];
	return rows.filter(
		(row) =>
			kindTest(row) && (needle === "" || `${row.title} ${row.detail} ${row.outcome}`.toLowerCase().includes(needle)),
	);
}

export function visibleRows(rows: readonly TrajectoryRow[], open: ReadonlySet<string>): TrajectoryRow[] {
	return rows.filter((row) => row.parent === undefined || open.has(row.parent));
}

export type Axis = "steps" | "duration";

export type Lane = "input" | "model" | "tools";

export const LANES: readonly Lane[] = ["input", "model", "tools"];

export type StepTone = "context" | "input" | "model" | "tool" | "error";

export type Step = {
	key: string;
	lane: Lane;
	tone: StepTone;
	turn: number;
	t0: number;
	t1: number;
	label: string;
	running: boolean;
};

export type Bar = {
	key: string;
	lane: Lane;
	tone: StepTone;
	start: number;
	end: number;
	label: string;
	turn: number;
	ms: number;
	running: boolean;
};

export type Layout = {
	bars: Bar[];
	dividers: number[];
};

function inputTone(row: TrajectoryRow): StepTone {
	const item = row.item;
	return item.kind === "user" && !item.brief && !isPeerOrigin(item.origin) ? "input" : "context";
}

const laneTone: Record<Exclude<Lane, "input">, (row: TrajectoryRow) => StepTone> = {
	model: () => "model",
	tools: (row) => (row.tone === "error" ? "error" : "tool"),
};

function stepsOf(rows: readonly TrajectoryRow[], now: number): Step[] {
	const chrono = [...rows].sort((a, b) => a.order - b.order);
	const steps: Step[] = [];
	const inputs: number[] = [];
	for (const row of chrono) {
		if (row.startsTurn && !Number.isNaN(row.t0)) inputs.push(row.t0);
		if (row.kind === "tool" && row.parent === undefined && !Number.isNaN(row.t1)) inputs.push(row.t1);
	}
	inputs.sort((a, b) => a - b);

	let modelEnd = Number.NEGATIVE_INFINITY;
	let modelSeq = -1;
	for (const row of chrono) {
		const turn = Math.max(1, row.turn);
		if (row.startsTurn && !Number.isNaN(row.t0)) {
			steps.push({
				key: row.key,
				lane: "input",
				tone: inputTone(row),
				turn,
				t0: row.t0,
				t1: row.t0,
				label: row.title || "Input",
				running: false,
			});
		}
		const modelRow =
			row.parent === undefined && (row.kind === "assistant" || row.kind === "thinking" || row.kind === "tool");
		if (modelRow && !Number.isNaN(row.t0) && row.seq !== modelSeq) {
			modelSeq = row.seq;
			const before = inputs.filter((t) => t <= row.t0).at(-1) ?? row.t0;
			const start = Math.min(row.t0, Math.max(before, modelEnd));
			modelEnd = row.t0;
			steps.push({
				key: row.key,
				lane: "model",
				tone: laneTone.model(row),
				turn,
				t0: start,
				t1: row.t0,
				label: "Model",
				running: false,
			});
		}
		if (row.kind === "tool" && !Number.isNaN(row.t0)) {
			steps.push({
				key: row.key,
				lane: "tools",
				tone: laneTone.tools(row),
				turn,
				t0: row.t0,
				t1: Number.isNaN(row.t1) ? (row.running ? Math.max(now, row.t0) : row.t0) : row.t1,
				label: `${row.title} ${row.detail}`.trim(),
				running: row.running,
			});
		}
	}
	return steps;
}

export function layoutBars(trajectory: Trajectory, axis: Axis, now: number): Layout {
	const steps = stepsOf(trajectory.rows, now);
	if (steps.length === 0) return { bars: [], dividers: [] };
	const make = (s: Step, start: number, end: number): Bar => ({
		key: s.key,
		lane: s.lane,
		tone: s.tone,
		start,
		end,
		label: s.label,
		turn: s.turn,
		ms: s.t1 - s.t0,
		running: s.running,
	});

	if (axis === "duration") {
		const from = Math.min(...steps.map((s) => s.t0));
		const to = Math.max(...steps.map((s) => s.t1));
		const total = Math.max(to - from, 1);
		const turnStart = new Map<number, number>();
		for (const s of steps) turnStart.set(s.turn, Math.min(turnStart.get(s.turn) ?? s.t0, s.t0));
		const dividers = [...turnStart]
			.sort(([a], [b]) => a - b)
			.slice(1)
			.map(([, t]) => (t - from) / total);
		return { bars: steps.map((s) => make(s, (s.t0 - from) / total, (s.t1 - from) / total)), dividers };
	}

	const n = steps.length;
	return {
		bars: steps.map((s, i) => make(s, i / n, (i + 1) / n)),
		dividers: steps.flatMap((s, i) => (i > 0 && s.turn !== steps[i - 1].turn ? [i / n] : [])),
	};
}

export function rawEntries(row: TrajectoryRow, entries: readonly ChatEntry[]): ChatEntry[] {
	if (row.item.kind !== "tool_call") return entries.filter((e) => e.seq === row.seq);
	const id = row.item.id;
	return entries.filter((e) => e.seq === row.seq || JSON.stringify(e.payload).includes(id));
}

export type RowText = { input: string; result: string; resultIsError: boolean };

const pretty = (value: unknown): string => (value === undefined ? "" : JSON.stringify(value, null, 2));

export function fullText(row: TrajectoryRow): RowText {
	const item = row.item;
	switch (item.kind) {
		case "tool_call": {
			const input = isObject(item.input) ? item.input : undefined;
			const text = resultText(item.result);
			return {
				input: typeof input?.command === "string" ? input.command : pretty(item.input),
				result: text !== "" ? text : item.result?.content === undefined ? "" : pretty(item.result.content),
				resultIsError: row.tone === "error",
			};
		}
		case "user":
			return { input: item.text, result: "", resultIsError: false };
		case "assistant_text":
		case "thinking":
			return { input: item.text, result: "", resultIsError: false };
		case "task_message":
			return { input: item.summary, result: item.result, resultIsError: row.tone === "error" };
		case "bash":
			return {
				input: item.command,
				result: item.chunks.map((c) => c.text).join(""),
				resultIsError: row.tone === "error",
			};
		case "turn_end": {
			const u = item.usage;
			const lines = [
				`${formatTokens(u.inputTokens)} in · ${formatTokens(u.outputTokens)} out`,
				`${formatTokens(u.cacheReadTokens)} cache read · ${formatTokens(u.cacheCreationTokens)} cache write`,
			];
			if (item.error?.detail) lines.push(item.error.detail);
			return { input: lines.join("\n"), result: item.error?.raw ?? "", resultIsError: item.isError };
		}
		case "notice":
			return { input: item.text, result: "", resultIsError: row.tone === "error" };
	}
}

export type Capped = { text: string; hidden: number };

export function capText(text: string, lines: number, chars: number): Capped {
	const all = text.split("\n");
	let kept = all.slice(0, lines).join("\n");
	let dropped = Math.max(0, all.length - lines);
	if (kept.length > chars) {
		kept = kept.slice(0, chars);
		dropped = Math.max(dropped, all.length - kept.split("\n").length + 1, 1);
	}
	return { text: kept, hidden: dropped };
}

export const formatSpan = (ms: number): string => (ms < 1000 ? `${Math.max(0, Math.round(ms))}ms` : formatDuration(ms));
