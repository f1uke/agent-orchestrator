import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { ChatEntry } from "./chat-event";
import { foldEntries } from "./chat-fold";

const testdata = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
const recorded = JSON.parse(readFileSync(join(testdata, "context_usage.jsonl"), "utf8")) as ChatEntry;

let seq = 0;
const out = (payload: object): ChatEntry => ({ seq: ++seq, ts: "2026-10-31T12:00:00.000Z", dir: "out", payload });
const answer = (requestId: string, response: unknown, ok = true) =>
	out({
		type: "control_response",
		response: ok
			? { subtype: "success", request_id: requestId, response }
			: { subtype: "error", request_id: requestId, error: response },
	});
const recordedBody = (recorded.payload as { response: { response: Record<string, unknown> } }).response.response;
const fold = (...entries: ChatEntry[]) => {
	const tr = foldEntries(entries);
	seq = 0;
	return tr;
};

describe("the context breakdown", () => {
	it("folds the recorded get_context_usage answer into categories and totals", () => {
		const tr = fold({ ...recorded, seq: 1 });
		expect(tr.contextBreakdown).toEqual({
			totalTokens: 13_123,
			maxTokens: 1_000_000,
			percentage: 1,
			autoCompactThreshold: 967_000,
			categories: [
				{ id: "system_prompt", name: "System prompt", tokens: 2456, kind: "used" },
				{ id: "system_tools", name: "System tools", tokens: 8376, kind: "used" },
				{ id: "system_tools_deferred", name: "System tools (deferred)", tokens: 13_289, kind: "deferred" },
				{ id: "skills", name: "Skills", tokens: 2281, kind: "used" },
				{ id: "messages", name: "Messages", tokens: 10, kind: "used" },
				{ id: "autocompact_buffer", name: "Autocompact buffer", tokens: 33_000, kind: "buffer" },
				{ id: "free_space", name: "Free space", tokens: 953_877, kind: "free" },
			],
		});
	});

	it("keeps the latest answer", () => {
		const later = { ...recordedBody, totalTokens: 30_700, percentage: 3 };
		expect(fold(answer("ctx-1", recordedBody), answer("ctx-2", later)).contextBreakdown?.totalTokens).toBe(30_700);
	});

	it("reads only the host's ctx- requests", () => {
		expect(fold(answer("init-1", recordedBody)).contextBreakdown).toBeUndefined();
	});

	it.each<[string, () => ChatEntry]>([
		["an error answer", () => answer("ctx-2", "Unknown request subtype: get_context_usage", false)],
		["no categories", () => answer("ctx-2", { ...recordedBody, categories: undefined })],
		[
			"tokens that are not numbers",
			() => answer("ctx-2", { ...recordedBody, categories: [{ id: "x", name: "X", tokens: "12", kind: "used" }] }),
		],
		[
			"an unknown category kind",
			() => answer("ctx-2", { ...recordedBody, categories: [{ id: "x", name: "X", tokens: 1, kind: "mystery" }] }),
		],
		["no window size", () => answer("ctx-2", { ...recordedBody, maxTokens: null })],
		["a body that is not an object", () => answer("ctx-2", "nope")],
	])("falls back quietly on %s, dropping an older breakdown", (_, bad) => {
		const good = answer("ctx-1", recordedBody);
		expect(fold(good, bad()).contextBreakdown).toBeUndefined();
	});

	it("has none before any answer", () => {
		expect(
			fold(out({ type: "control_request", request_id: "ctx-1", request: { subtype: "get_context_usage" } }))
				.contextBreakdown,
		).toBeUndefined();
	});
});
