import type { components } from "../../api/schema";
import { isObject } from "./chat-event";
import type { Pending } from "./chat-fold";
import type { Question } from "./chat-tools";

export type AnswerBody = components["schemas"]["AnswerChatRequest"];
export type PermissionMode = components["schemas"]["SetChatModeRequest"]["mode"];

export type PlanApproval = { answer: AnswerBody; mode: PermissionMode };

export type QuestionChoice = { picked: readonly string[]; other: string };

export const REPLY_IN_CHAT_MESSAGE = "The user will answer in chat.";

export function allowOnce(request: Pending): AnswerBody {
	return { requestId: request.requestId, behavior: "allow" };
}

export function alwaysAllow(request: Pending): AnswerBody {
	return { requestId: request.requestId, behavior: "allow", updatedPermissions: [...request.suggestions] };
}

export function deny(request: Pending, message = ""): AnswerBody {
	return { requestId: request.requestId, behavior: "deny", message: message.trim() };
}

export function questionAnswer(question: Question, choice: QuestionChoice | undefined): string {
	if (!choice) return "";
	const other = choice.other.trim();
	if (!question.multiSelect) return other || (choice.picked[0] ?? "");
	return [...choice.picked, ...(other ? [other] : [])].join(", ");
}

export function answerQuestions(
	request: Pending,
	questions: readonly Question[],
	choices: readonly (QuestionChoice | undefined)[],
): AnswerBody {
	const answers: Record<string, string> = {};
	questions.forEach((q, i) => {
		answers[q.question] = questionAnswer(q, choices[i]);
	});
	const input = isObject(request.input) ? request.input : {};
	return { requestId: request.requestId, behavior: "allow", updatedInput: { ...input, answers } };
}

export function replyInChat(request: Pending): AnswerBody {
	return deny(request, REPLY_IN_CHAT_MESSAGE);
}

export function approvePlan(request: Pending, mode: "acceptEdits" | "default"): PlanApproval {
	return { answer: allowOnce(request), mode };
}

export function keepPlanning(request: Pending, feedback: string): AnswerBody {
	return deny(request, feedback);
}
