import { describe, expect, it } from "vitest";
import type { ChatItem, UserItem } from "./chat-fold";
import { activeTurnIndex, railTurns } from "./chat-rail";
import type { ChatRow } from "./chat-rows";

const item = (key: string, it: ChatItem): ChatRow => ({ key, kind: "item", item: it, subagentTools: 0 });
const user = (key: string, text: string): ChatRow =>
	item(key, { kind: "user", uuid: key, text, images: 0, state: "completed", ts: "" } satisfies UserItem);
const reply = (key: string, text: string): ChatRow =>
	item(key, { kind: "assistant_text", messageId: key, text, parentToolUseId: "" });

describe("railTurns", () => {
	it("numbers each turn from its opening message and pairs it with the turn's last reply", () => {
		const turns = railTurns([
			user("i0", "Fix the ledger tests"),
			reply("i1", "Looking."),
			reply("i2", "Fixed **both** bugs in `ledger.go`."),
			user("i3", "Now add a test"),
		]);
		expect(turns).toEqual([
			{ turn: 1, key: "i0", prompt: "Fix the ledger tests", reply: "Fixed both bugs in ledger.go." },
			{ turn: 2, key: "i3", prompt: "Now add a test", reply: "" },
		]);
	});

	it("keeps the prompt to one line of about 50 characters", () => {
		const [turn] = railTurns([
			user("i0", "Fix the failing tests\nin the ledger package, then run the whole suite with -race"),
		]);
		expect(turn.prompt).toBe("Fix the failing tests in the ledger package, then…");
		expect(turn.prompt.length).toBeLessThanOrEqual(51);
	});

	it("starts a turn at a task message claude woke up to answer, and not at one inside a turn", () => {
		const message = (key: string, opensTurn: boolean): ChatRow =>
			item(key, {
				kind: "task_message",
				ts: "",
				taskId: key,
				toolUseId: "",
				status: "completed",
				summary: `Agent "${key}" finished`,
				result: "",
				opensTurn,
			});
		const turns = railTurns([user("i0", "go"), message("inside", false), reply("i2", "ok"), message("woke", true)]);
		expect(turns.map((t) => [t.key, t.prompt])).toEqual([
			["i0", "go"],
			["woke", 'Agent "woke" finished'],
		]);
	});
});

describe("activeTurnIndex", () => {
	it("is the last turn whose opening row starts at or above the probe line", () => {
		const starts = [0, 400, 1200];
		expect(activeTurnIndex(starts, 0)).toBe(0);
		expect(activeTurnIndex(starts, 399)).toBe(0);
		expect(activeTurnIndex(starts, 400)).toBe(1);
		expect(activeTurnIndex(starts, 5000)).toBe(2);
	});

	it("is the first turn when no opening row is above the probe yet", () => {
		expect(activeTurnIndex([300, 900], 100)).toBe(0);
	});
});
