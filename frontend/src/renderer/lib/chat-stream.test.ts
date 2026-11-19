import { describe, expect, it } from "vitest";
import type { ChatEntry } from "./chat-event";
import { applyDelta, emptyOverlay, settleOverlay, type StreamOverlay } from "./chat-stream";

const ev = (event: object, parent: string | null = null) => ({
	type: "stream_event",
	parent_tool_use_id: parent,
	event,
});
const start = (id: string) => ev({ type: "message_start", message: { id } });
const text = (index: number, t: string) =>
	ev({ type: "content_block_delta", index, delta: { type: "text_delta", text: t } });
const think = (index: number, t: string) =>
	ev({ type: "content_block_delta", index, delta: { type: "thinking_delta", thinking: t } });
const toolStart = (index: number, name: string) =>
	ev({ type: "content_block_start", index, content_block: { type: "tool_use", id: "toolu_1", name, input: {} } });
const json = (index: number, partial: string) =>
	ev({ type: "content_block_delta", index, delta: { type: "input_json_delta", partial_json: partial } });
const out = (payload: object): ChatEntry => ({ seq: 1, ts: "", dir: "out", payload });

const run = (...deltas: unknown[]): StreamOverlay => deltas.reduce<StreamOverlay>(applyDelta, emptyOverlay);

describe("chat stream overlay", () => {
	it("accumulates text and thinking per message and block index", () => {
		const overlay = run(start("m1"), think(0, "Let me "), think(0, "look."), text(1, "Found "), text(1, "it."));
		expect(overlay.blocks).toEqual([
			{ messageId: "m1", index: 0, kind: "thinking", text: "Let me look." },
			{ messageId: "m1", index: 1, kind: "text", text: "Found it." },
		]);
	});

	it("drops each block as the log delivers it, keeping the one still streaming", () => {
		let overlay = run(start("m1"), think(0, "hmm"), text(1, "Partial"));
		overlay = settleOverlay(
			overlay,
			out({ type: "assistant", message: { id: "m1", content: [{ type: "thinking" }] } }),
		);
		expect(overlay.blocks.map((b) => b.index)).toEqual([1]);
		overlay = applyDelta(overlay, think(0, " more"));
		expect(overlay.blocks.map((b) => b.index)).toEqual([1]);
		overlay = settleOverlay(overlay, out({ type: "assistant", message: { id: "m1", content: [{ type: "text" }] } }));
		expect(overlay.blocks).toEqual([]);
	});

	it("clears everything at the turn's end or when the process ends", () => {
		const overlay = run(start("m1"), text(0, "cut off"));
		expect(settleOverlay(overlay, out({ type: "result", subtype: "success" })).blocks).toEqual([]);
		expect(settleOverlay(overlay, { seq: 2, ts: "", dir: "host", payload: { kind: "exit", code: 0 } }).blocks).toEqual(
			[],
		);
		expect(
			settleOverlay(overlay, { seq: 2, ts: "", dir: "host", payload: { kind: "user_origin", uuid: "u" } }).blocks,
		).toHaveLength(1);
	});

	it("leaves a subagent's stream out of the main column", () => {
		const overlay = run(
			start("m1"),
			ev({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "x" } }, "toolu_1"),
		);
		expect(overlay.blocks).toEqual([]);
	});

	describe("a streaming tool call", () => {
		const writing = () =>
			run(
				start("m1"),
				think(0, "plan"),
				toolStart(1, "Write"),
				json(1, '{"file_path":"/a/b.md",'),
				json(1, '"content":"hi"}'),
			);
		const logged = (count: number) =>
			out({ type: "assistant", message: { id: "m1", content: Array(count).fill({}) } });

		it("shows the tool's name and how much input has arrived", () => {
			const { tool } = writing();
			expect(tool).toMatchObject({ messageId: "m1", index: 1, name: "Write", chars: 38 });
			expect(tool?.head).toBe('{"file_path":"/a/b.md","content":"hi"}');
		});

		it("keeps only the first 512 characters of the input but counts all of them", () => {
			const { tool } = run(start("m1"), toolStart(0, "Write"), json(0, "x".repeat(400)), json(0, "y".repeat(400)));
			expect(tool?.chars).toBe(800);
			expect(tool?.head).toBe("x".repeat(400) + "y".repeat(112));
		});

		it("ends when the log delivers that tool_use", () => {
			expect(settleOverlay(writing(), logged(1)).tool).toBeDefined();
			expect(settleOverlay(writing(), logged(2)).tool).toBeUndefined();
		});

		it("ends at the turn's end or when the process ends", () => {
			expect(settleOverlay(writing(), out({ type: "result", subtype: "success" })).tool).toBeUndefined();
			const exit = { seq: 2, ts: "", dir: "host" as const, payload: { kind: "exit", code: 0 } };
			expect(settleOverlay(writing(), exit).tool).toBeUndefined();
		});

		it("ignores a subagent's tool call and input that arrives after its block was logged", () => {
			const sub = ev(
				{ type: "content_block_start", index: 0, content_block: { type: "tool_use", name: "Write" } },
				"toolu_1",
			);
			expect(run(start("m1"), sub).tool).toBeUndefined();
			expect(applyDelta(settleOverlay(writing(), logged(2)), json(1, "late")).tool).toBeUndefined();
		});

		it("does not start a tool for a text block", () => {
			const text = ev({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } });
			expect(run(start("m1"), text).tool).toBeUndefined();
		});
	});
});
