import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { GET, POST, PUT } = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), PUT: vi.fn() }));
const baseUrl = vi.hoisted(() => ({ value: "" }));
vi.mock("./api-client", () => ({ apiClient: { GET, POST, PUT }, getApiBaseUrl: () => baseUrl.value }));

import { chatError, httpChatApi } from "./chat-api";

const reply = (status: number, error?: object) => ({ error, response: new Response(null, { status }) });

beforeEach(() => {
	GET.mockReset();
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

	it("puts the Remote Control switch on its own route and reports a refusal", async () => {
		PUT.mockResolvedValueOnce(reply(200)).mockResolvedValueOnce(
			reply(409, {
				error: "x",
				code: "CHAT_REQUEST_REJECTED",
				message: "not available for this account",
				requestId: "q",
			}),
		);
		expect(await httpChatApi.setRemoteControl("s1", true)).toEqual({ ok: true });
		expect(await httpChatApi.setRemoteControl("s1", false)).toEqual({
			ok: false,
			error: { kind: "rejected", message: "not available for this account" },
		});
		expect(PUT.mock.calls.map((c) => [c[0], c[1].body])).toEqual([
			["/api/v1/sessions/{sessionId}/chat/remote-control", { enabled: true }],
			["/api/v1/sessions/{sessionId}/chat/remote-control", { enabled: false }],
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

describe("httpChatApi.taskOutput", () => {
	const output = { text: "line 1\n", size: 7, truncated: false, modifiedAt: "2026-10-30T10:56:00Z" };

	it("reads a task's output by session and task id only", async () => {
		GET.mockResolvedValue({ data: output, response: new Response(null, { status: 200 }) });
		expect(await httpChatApi.taskOutput("s1", "b7x")).toEqual(output);
		expect(GET).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/chat/tasks/{taskId}/output", {
			params: { path: { sessionId: "s1", taskId: "b7x" } },
		});
	});

	it("is null when the daemon has no output for the task, or cannot be reached", async () => {
		GET.mockResolvedValueOnce({
			error: { code: "TASK_OUTPUT_NOT_FOUND" },
			response: new Response(null, { status: 404 }),
		});
		expect(await httpChatApi.taskOutput("s1", "b7x")).toBeNull();
		GET.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		expect(await httpChatApi.taskOutput("s1", "b7x")).toBeNull();
	});
});

describe("httpChatApi.stopTask", () => {
	it("posts to the task's own stop route by session and task id, with no body", async () => {
		POST.mockResolvedValue(reply(200));
		expect(await httpChatApi.stopTask("s1", "b7x")).toEqual({ ok: true });
		expect(POST).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/chat/tasks/{taskId}/stop", {
			params: { path: { sessionId: "s1", taskId: "b7x" } },
		});
	});

	it("keeps the answer's message when claude refuses, and names a thrown fetch as failed", async () => {
		POST.mockResolvedValueOnce(
			reply(409, { code: "CHAT_REQUEST_REJECTED", message: "stop_task is not supported in this context" }),
		);
		expect(await httpChatApi.stopTask("s1", "b7x")).toEqual({
			ok: false,
			error: { kind: "rejected", message: "stop_task is not supported in this context" },
		});
		POST.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		expect(await httpChatApi.stopTask("s1", "b7x")).toEqual({
			ok: false,
			error: { kind: "failed", message: "Failed to fetch" },
		});
	});
});

describe("httpChatApi.upload", () => {
	afterEach(() => {
		vi.unstubAllGlobals();
		baseUrl.value = "";
	});

	const answer = (status: number, body: object) =>
		vi.fn(
			async (..._args: unknown[]) =>
				new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
		);

	it("posts the raw bytes with the encoded name to the daemon the other routes reach", async () => {
		baseUrl.value = "http://127.0.0.1:4100";
		const fetchMock = answer(200, { path: "/d/attachments/ab/my report.pdf", name: "my report.pdf", size: 4 });
		vi.stubGlobal("fetch", fetchMock);
		const file = new File(["%PDF"], "my report.pdf", { type: "application/pdf" });

		const result = await httpChatApi.upload("s 1", file);

		expect(result).toEqual({
			ok: true,
			file: { path: "/d/attachments/ab/my report.pdf", name: "my report.pdf", size: 4 },
		});
		expect(fetchMock).toHaveBeenCalledWith(
			"http://127.0.0.1:4100/api/v1/sessions/s%201/chat/attachments?name=my%20report.pdf",
			{ method: "POST", headers: { "Content-Type": "application/octet-stream" }, body: file },
		);
	});

	it("names a file that has no name", async () => {
		const fetchMock = answer(200, { path: "/d/x", name: "pasted file", size: 1 });
		vi.stubGlobal("fetch", fetchMock);
		await httpChatApi.upload("s1", new File(["x"], ""));
		expect(String(fetchMock.mock.calls[0][0])).toBe("/api/v1/sessions/s1/chat/attachments?name=pasted%20file");
	});

	it("returns the daemon's message when it refuses the file", async () => {
		vi.stubGlobal("fetch", answer(413, { code: "FILE_TOO_LARGE", message: "file is larger than 50 MiB" }));
		expect(await httpChatApi.upload("s1", new File(["x"], "big.bin"))).toEqual({
			ok: false,
			error: { kind: "failed", message: "file is larger than 50 MiB" },
		});
		vi.stubGlobal("fetch", answer(400, { code: "INVALID_NAME", message: "bad name" }));
		expect(await httpChatApi.upload("s1", new File(["x"], "a"))).toEqual({
			ok: false,
			error: { kind: "invalid", message: "bad name" },
		});
	});

	it("returns a thrown fetch as failed, and an answer without a path as failed", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => Promise.reject(new TypeError("Failed to fetch"))),
		);
		expect(await httpChatApi.upload("s1", new File(["x"], "a"))).toEqual({
			ok: false,
			error: { kind: "failed", message: "Failed to fetch" },
		});
		vi.stubGlobal("fetch", answer(200, { name: "a" }));
		expect(await httpChatApi.upload("s1", new File(["x"], "a"))).toMatchObject({ ok: false });
	});
});
