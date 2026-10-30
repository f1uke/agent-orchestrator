import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
	allowOnce,
	alwaysAllow,
	answerQuestions,
	approvePlan,
	deny,
	keepPlanning,
	questionAnswer,
	REPLY_IN_CHAT_MESSAGE,
	replyInChat,
} from "./chat-answers";
import type { ChatEntry } from "./chat-event";
import { foldEntries, type Pending } from "./chat-fold";
import { encodeAnswer } from "./chat-mock";
import { parseQuestions, type Question } from "./chat-tools";

function loadFixture(name: string): ChatEntry[] {
	const testdata = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
	return readFileSync(join(testdata, name), "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line) => JSON.parse(line) as ChatEntry);
}

const first = loadFixture("first_session.jsonl");
const resumed = loadFixture("resumed_session.jsonl");

function pendingAt(entries: ChatEntry[], seq: number): Pending {
	const pending = foldEntries(entries.filter((e) => e.seq <= seq)).pending;
	expect(pending).toHaveLength(1);
	return pending[0];
}

const accepted = (entries: ChatEntry[], seq: number) => {
	const entry = entries.find((e) => e.seq === seq)!;
	expect(entry.dir).toBe("in");
	return entry.payload;
};

describe("answer builders reproduce the payloads claude accepted", () => {
	it("Always allow a Write sends the request's setMode suggestion", () => {
		const request = pendingAt(first, 12);
		expect(encodeAnswer(alwaysAllow(request), request)).toEqual(accepted(first, 13));
	});

	it("answers both questions, the multi-select one joined with a comma", () => {
		const request = pendingAt(first, 28);
		const body = answerQuestions(request, parseQuestions(request.input), [
			{ picked: ["Red"], other: "" },
			{ picked: ["Chips", "Fruit"], other: "" },
		]);
		expect(encodeAnswer(body, request)).toEqual(accepted(first, 29));
	});

	it("Keep planning sends the feedback as a deny", () => {
		const request = pendingAt(resumed, 32);
		const body = keepPlanning(request, "Rejected: name the file goodbye.txt instead and present the revised plan.");
		expect(encodeAnswer(body, request)).toEqual(accepted(resumed, 33));
	});

	it.each(["acceptEdits", "default"] as const)("approving a plan for %s allows it, then switches mode", (mode) => {
		const request = pendingAt(resumed, 41);
		const approval = approvePlan(request, mode);
		expect(encodeAnswer(approval.answer, request)).toEqual(accepted(resumed, 42));
		expect(approval.mode).toBe(mode);
	});

	it("Allow once sends no updatedInput, so the host reuses the request's input", () => {
		const request = pendingAt(resumed, 48);
		const body = allowOnce(request);
		expect(body).not.toHaveProperty("updatedInput");
		expect(body).not.toHaveProperty("updatedPermissions");
		expect(encodeAnswer(body, request)).toEqual(accepted(resumed, 49));
	});

	it("Deny sends its message", () => {
		const request = pendingAt(resumed, 63);
		expect(encodeAnswer(deny(request, "The user denied rm from the chat UI."), request)).toEqual(accepted(resumed, 64));
	});
});

describe("question answers", () => {
	const q = (multiSelect: boolean): Question => ({ question: "Q?", header: "Q", multiSelect, options: [] });

	it.each([
		["a picked option", false, { picked: ["Red"], other: "" }, "Red"],
		["the Other text over nothing picked", false, { picked: [], other: "  Teal " }, "Teal"],
		["every picked option, in order", true, { picked: ["Chips", "Nuts"], other: "" }, "Chips, Nuts"],
		["picked options plus the Other text", true, { picked: ["Chips"], other: "Olives" }, "Chips, Olives"],
		["nothing while nothing is chosen", true, { picked: [], other: " " }, ""],
	])("a question's answer is %s", (_, multi, choice, answer) => {
		expect(questionAnswer(q(multi), choice)).toBe(answer);
	});

	it("keeps the rest of the input and adds the answers keyed by question text", () => {
		const request: Pending = {
			requestId: "r",
			toolName: "AskUserQuestion",
			toolUseId: "t",
			kind: "question",
			input: { questions: [{ question: "Q?" }], metadata: { source: "x" } },
			suggestions: [],
			defaultToNo: false,
		};
		expect(answerQuestions(request, [q(false)], [{ picked: [], other: "Mine" }])).toEqual({
			requestId: "r",
			behavior: "allow",
			updatedInput: { questions: [{ question: "Q?" }], metadata: { source: "x" }, answers: { "Q?": "Mine" } },
		});
	});

	it("Reply in chat instead declines with the fixed message", () => {
		const request = pendingAt(first, 28);
		expect(replyInChat(request)).toEqual({
			requestId: request.requestId,
			behavior: "deny",
			message: REPLY_IN_CHAT_MESSAGE,
		});
		expect(REPLY_IN_CHAT_MESSAGE).toBe("The user will answer in chat.");
	});
});
