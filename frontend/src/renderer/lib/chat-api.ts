import type { components } from "../../api/schema";
import { apiClient, getApiBaseUrl } from "./api-client";
import type { AnswerBody, PermissionMode } from "./chat-answers";
import { isObject } from "./chat-event";

export type ChatImage = components["schemas"]["ChatImage"];

export type TaskOutput = components["schemas"]["ControllersChatTaskOutputResponse"];

export type RewindPreview = components["schemas"]["ControllersRewindChatPreviewResponse"];

/** What the agent was given: each system prompt and tool set it sent, and the context blocks its CLI injected. */
export type ModelContext = components["schemas"]["ChatModelContextResponse"];

export type ModelShape = components["schemas"]["ChatModelShape"];

export type ContextInjection = components["schemas"]["ChatContextInjection"];

export type ChatErrorKind = "host_not_running" | "rejected" | "not_chat" | "invalid" | "failed";

export type ChatActionError = { kind: ChatErrorKind; message: string };

export type ChatResult = { ok: true } | { ok: false; error: ChatActionError };

export type UploadedFile = components["schemas"]["ControllersChatAttachment"];

export type UploadResult = { ok: true; file: UploadedFile } | { ok: false; error: ChatActionError };

export type BashResult = { ok: true; id: string } | { ok: false; error: ChatActionError };

export type ChatApi = {
	send: (sessionId: string, text: string, images: readonly ChatImage[]) => Promise<ChatResult>;
	answer: (sessionId: string, body: AnswerBody) => Promise<ChatResult>;
	interrupt: (sessionId: string) => Promise<ChatResult>;
	setMode: (sessionId: string, mode: PermissionMode) => Promise<ChatResult>;
	setModel: (sessionId: string, model: string) => Promise<ChatResult>;
	/** Starts or stops Remote Control. The state and the session URL come back in the chat log, not in the result. */
	setRemoteControl: (sessionId: string, enabled: boolean) => Promise<ChatResult>;
	setChrome: (sessionId: string, enabled: boolean) => Promise<ChatResult>;
	/** Sets the reasoning effort for the next requests; the level in use comes back in the chat log, not in the result. */
	setEffort: (sessionId: string, level: string) => Promise<ChatResult>;
	/** Runs a "!" command in the session's worktree as the person. The output comes back in the chat log, not in the result. */
	runBash: (sessionId: string, command: string) => Promise<BashResult>;
	/** Stops the running "!" command with this id. A finished or unknown id is a no-op. */
	stopBash: (sessionId: string, id: string) => Promise<ChatResult>;
	/** Saves a file where claude can read it; the answered path goes into the message. */
	upload: (sessionId: string, file: File) => Promise<UploadResult>;
	/** Ends a background task: a subagent or a background Bash. Resolves once claude has answered. */
	stopTask: (sessionId: string, taskId: string) => Promise<ChatResult>;
	/** The tail of a background task's output; null when the daemon has none for it. */
	taskOutput: (sessionId: string, taskId: string) => Promise<TaskOutput | null>;
	/** Drops the user message uuid and everything after it from claude's conversation; restoreFiles also puts the files back. */
	rewind: (sessionId: string, uuid: string, restoreFiles: boolean) => Promise<ChatResult>;
	/** What restoring files to before uuid would change; null when the daemon could not say. */
	rewindPreview: (sessionId: string, uuid: string) => Promise<RewindPreview | null>;
	/** What the agent was given, read from its own transcript; null when its harness writes none or the daemon could not say. */
	modelContext: (sessionId: string) => Promise<ModelContext | null>;
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

const path = (sessionId: string) => ({ params: { path: { sessionId } } });

async function settle(call: Promise<{ error?: unknown; response: Response }>): Promise<ChatResult> {
	try {
		const { error, response } = await call;
		return response.ok ? { ok: true } : { ok: false, error: chatError(response.status, error) };
	} catch (err) {
		return { ok: false, error: { kind: "failed", message: err instanceof Error ? err.message : String(err) } };
	}
}

async function runBash(sessionId: string, command: string): Promise<BashResult> {
	try {
		const { data, error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/chat/bash", {
			...path(sessionId),
			body: { command },
		});
		if (!response.ok) return { ok: false, error: chatError(response.status, error) };
		if (!data?.id) return { ok: false, error: { kind: "failed", message: "The daemon did not return the run's id." } };
		return { ok: true, id: data.id };
	} catch (err) {
		return { ok: false, error: { kind: "failed", message: err instanceof Error ? err.message : String(err) } };
	}
}

async function upload(sessionId: string, file: File): Promise<UploadResult> {
	const name = file.name || "pasted file";
	try {
		const response = await fetch(
			`${getApiBaseUrl()}/api/v1/sessions/${encodeURIComponent(sessionId)}/chat/attachments?name=${encodeURIComponent(name)}`,
			{ method: "POST", headers: { "Content-Type": "application/octet-stream" }, body: file },
		);
		const body: unknown = await response.json().catch(() => undefined);
		if (!response.ok) return { ok: false, error: chatError(response.status, body) };
		if (!isObject(body) || typeof body.path !== "string" || body.path === "") {
			return { ok: false, error: { kind: "failed", message: "The daemon did not say where it saved the file." } };
		}
		return { ok: true, file: body as UploadedFile };
	} catch (err) {
		return { ok: false, error: { kind: "failed", message: err instanceof Error ? err.message : String(err) } };
	}
}

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
	setModel: (sessionId, model) =>
		settle(apiClient.PUT("/api/v1/sessions/{sessionId}/chat/model", { ...path(sessionId), body: { model } })),
	setRemoteControl: (sessionId, enabled) =>
		settle(
			apiClient.PUT("/api/v1/sessions/{sessionId}/chat/remote-control", { ...path(sessionId), body: { enabled } }),
		),
	setChrome: (sessionId, enabled) =>
		settle(apiClient.PUT("/api/v1/sessions/{sessionId}/chat/chrome", { ...path(sessionId), body: { enabled } })),
	setEffort: (sessionId, level) =>
		settle(apiClient.PUT("/api/v1/sessions/{sessionId}/chat/effort", { ...path(sessionId), body: { level } })),
	runBash,
	stopBash: (sessionId, id) =>
		settle(apiClient.POST("/api/v1/sessions/{sessionId}/chat/bash/stop", { ...path(sessionId), body: { id } })),
	upload,
	stopTask: (sessionId, taskId) =>
		settle(
			apiClient.POST("/api/v1/sessions/{sessionId}/chat/tasks/{taskId}/stop", {
				params: { path: { sessionId, taskId } },
			}),
		),
	rewind: (sessionId, uuid, restoreFiles) =>
		settle(
			apiClient.POST("/api/v1/sessions/{sessionId}/chat/rewind", { ...path(sessionId), body: { uuid, restoreFiles } }),
		),
	rewindPreview: async (sessionId, uuid) => {
		try {
			const { data } = await apiClient.POST("/api/v1/sessions/{sessionId}/chat/rewind/preview", {
				...path(sessionId),
				body: { uuid },
			});
			return data ?? null;
		} catch {
			return null;
		}
	},
	modelContext: async (sessionId) => {
		try {
			const { data, response } = await apiClient.GET(
				"/api/v1/sessions/{sessionId}/chat/model-context",
				path(sessionId),
			);
			return response.status === 200 && data ? data : null;
		} catch {
			return null;
		}
	},
	taskOutput: async (sessionId, taskId) => {
		try {
			const { data } = await apiClient.GET("/api/v1/sessions/{sessionId}/chat/tasks/{taskId}/output", {
				params: { path: { sessionId, taskId } },
			});
			return data ?? null;
		} catch {
			return null;
		}
	},
};
