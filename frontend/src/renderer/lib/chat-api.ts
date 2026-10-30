import type { components } from "../../api/schema";
import { apiClient } from "./api-client";
import type { AnswerBody, PermissionMode } from "./chat-answers";
import { isObject } from "./chat-event";

export type ChatImage = components["schemas"]["ChatImage"];

export type ChatErrorKind = "host_not_running" | "rejected" | "not_chat" | "invalid" | "failed";

export type ChatActionError = { kind: ChatErrorKind; message: string };

export type ChatResult = { ok: true } | { ok: false; error: ChatActionError };

export type ChatApi = {
	send: (sessionId: string, text: string, images: readonly ChatImage[]) => Promise<ChatResult>;
	answer: (sessionId: string, body: AnswerBody) => Promise<ChatResult>;
	interrupt: (sessionId: string) => Promise<ChatResult>;
	setMode: (sessionId: string, mode: PermissionMode) => Promise<ChatResult>;
};

const KIND_BY_CODE: Record<string, ChatErrorKind> = {
	SESSION_HOST_NOT_RUNNING: "host_not_running",
	CHAT_REQUEST_REJECTED: "rejected",
	NOT_CHAT_SESSION: "not_chat",
};

/** Reads the daemon's error envelope ({ error, code, message, requestId }) into a typed error. */
export function chatError(status: number, body: unknown): ChatActionError {
	const code = isObject(body) && typeof body.code === "string" ? body.code : "";
	const message =
		isObject(body) && typeof body.message === "string" && body.message !== ""
			? body.message
			: `Request failed (HTTP ${status})`;
	return { kind: KIND_BY_CODE[code] ?? (status === 400 ? "invalid" : "failed"), message };
}

async function settle(call: Promise<{ error?: unknown; response: Response }>): Promise<ChatResult> {
	try {
		const { error, response } = await call;
		return response.ok ? { ok: true } : { ok: false, error: chatError(response.status, error) };
	} catch (err) {
		return { ok: false, error: { kind: "failed", message: err instanceof Error ? err.message : String(err) } };
	}
}

const path = (sessionId: string) => ({ params: { path: { sessionId } } });

export const httpChatApi: ChatApi = {
	send: (sessionId, text, images) =>
		settle(
			apiClient.POST("/api/v1/sessions/{sessionId}/chat/messages", {
				...path(sessionId),
				body: images.length ? { text, images: [...images] } : { text },
			}),
		),
	answer: (sessionId, body) =>
		settle(apiClient.POST("/api/v1/sessions/{sessionId}/chat/answers", { ...path(sessionId), body })),
	interrupt: (sessionId) => settle(apiClient.POST("/api/v1/sessions/{sessionId}/chat/interrupt", path(sessionId))),
	setMode: (sessionId, mode) =>
		settle(apiClient.PUT("/api/v1/sessions/{sessionId}/chat/mode", { ...path(sessionId), body: { mode } })),
};
