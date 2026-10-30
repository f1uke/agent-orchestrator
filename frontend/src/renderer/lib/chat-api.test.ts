import { beforeEach, describe, expect, it, vi } from "vitest";

const { POST, PUT } = vi.hoisted(() => ({ POST: vi.fn(), PUT: vi.fn() }));
vi.mock("./api-client", () => ({ apiClient: { POST, PUT } }));

import { chatError, httpChatApi } from "./chat-api";

const reply = (status: number, error?: object) => ({ error, response: new Response(null, { status }) });

beforeEach(() => {
	POST.mockReset();
	PUT.mockReset();
});

describe("chatError", () => {
	it.each([
		[409, "SESSION_HOST_NOT_RUNNING", "host_not_running"],
		[409, "CHAT_REQUEST_REJECTED", "rejected"],
		[409, "NOT_CHAT_SESSION", "not_chat"],
		[400, "TEXT_REQUIRED", "invalid"],
		[400, "INVALID_MODE", "invalid"],
		[500, "INTERNAL", "failed"],
	])("maps %i %s to %s and keeps the host's message", (status, code, kind) => {
		expect(chatError(status, { error: "x", code, message: "request r1 is not pending", requestId: "q" })).toEqual({
			kind,
			message: "request r1 is not pending",
		});
	});

	it("names the status when the body has no message", () => {
		expect(chatError(503, undefined)).toEqual({ kind: "failed", message: "Request failed (HTTP 503)" });
	});
});

describe("httpChatApi", () => {
	it("posts a message, with images only when there are some", async () => {
		POST.mockResolvedValue(reply(200));
		await httpChatApi.send("s1", "hi", []);
		await httpChatApi.send("s1", "look", [{ mediaType: "image/png", base64: "AAAA" }]);
		expect(POST.mock.calls).toEqual([
			["/api/v1/sessions/{sessionId}/chat/messages", { params: { path: { sessionId: "s1" } }, body: { text: "hi" } }],
			[
				"/api/v1/sessions/{sessionId}/chat/messages",
				{
					params: { path: { sessionId: "s1" } },
					body: { text: "look", images: [{ mediaType: "image/png", base64: "AAAA" }] },
				},
			],
		]);
	});

	it("answers, interrupts and sets the mode on their own routes", async () => {
		POST.mockResolvedValue(reply(200));
		PUT.mockResolvedValue(reply(200));
		expect(await httpChatApi.answer("s1", { requestId: "r", behavior: "allow" })).toEqual({ ok: true });
		expect(await httpChatApi.interrupt("s1")).toEqual({ ok: true });
		expect(await httpChatApi.setMode("s1", "plan")).toEqual({ ok: true });
		expect(POST.mock.calls.map((c) => c[0])).toEqual([
			"/api/v1/sessions/{sessionId}/chat/answers",
			"/api/v1/sessions/{sessionId}/chat/interrupt",
		]);
		expect(PUT).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/chat/mode", {
			params: { path: { sessionId: "s1" } },
			body: { mode: "plan" },
		});
	});

	it("returns the typed error from the envelope, and a thrown fetch as failed", async () => {
		POST.mockResolvedValueOnce(reply(409, { code: "CHAT_REQUEST_REJECTED", message: "already answered" }));
		expect(await httpChatApi.answer("s1", { requestId: "r", behavior: "deny" })).toEqual({
			ok: false,
			error: { kind: "rejected", message: "already answered" },
		});
		POST.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		expect(await httpChatApi.interrupt("s1")).toEqual({
			ok: false,
			error: { kind: "failed", message: "Failed to fetch" },
		});
	});
});
