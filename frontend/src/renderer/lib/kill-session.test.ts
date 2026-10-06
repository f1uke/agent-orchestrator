import { beforeEach, describe, expect, it, vi } from "vitest";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));

vi.mock("./api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error && typeof error === "object" && "message" in error)
			return String((error as { message?: unknown }).message);
		return fallback;
	},
}));

import { killSession, unmergedChildrenFrom, undeliveredWorkFrom, UndeliveredWorkError } from "./kill-session";

const refusalBody = {
	error: "conflict",
	code: "SESSION_HAS_UNDELIVERED_WORK",
	message: "sess-1 still holds 2 uncommitted files that no pull request carries",
	details: {
		reason: "workspace_dirty",
		files: [
			{ path: "src/main.go", status: "modified" },
			{ path: "NewFile.swift", status: "untracked" },
		],
	},
};

describe("killSession", () => {
	beforeEach(() => postMock.mockReset());

	it("reports what the kill DID, not just whether disk came back", async () => {
		postMock.mockResolvedValue({ data: { ok: true, sessionId: "sess-1", terminated: true, freed: false } });

		const result = await killSession("sess-1");

		// `freed:false, terminated:true` is a real state (a crewmate holds the
		// tree) and it is NOT the same as the session still running. Collapsing
		// the two is the bug this module exists to prevent.
		expect(result).toEqual({ terminated: true, freed: false, discarded: [], preservedRef: "" });
	});

	it("turns the daemon's refusal into an error carrying the files", async () => {
		postMock.mockResolvedValue({ error: refusalBody });

		await expect(killSession("sess-1")).rejects.toBeInstanceOf(UndeliveredWorkError);
		await expect(killSession("sess-1")).rejects.toMatchObject({
			files: [
				{ path: "src/main.go", status: "modified" },
				{ path: "NewFile.swift", status: "untracked" },
			],
		});
	});

	it("sends the discard opt-in only when asked", async () => {
		postMock.mockResolvedValue({ data: { ok: true, sessionId: "sess-1", terminated: true, freed: true } });

		await killSession("sess-1");
		expect(postMock).toHaveBeenLastCalledWith("/api/v1/sessions/{sessionId}/kill", {
			params: { path: { sessionId: "sess-1" } },
			body: { discardUncommitted: false },
		});

		await killSession("sess-1", { discardUncommitted: true });
		expect(postMock).toHaveBeenLastCalledWith("/api/v1/sessions/{sessionId}/kill", {
			params: { path: { sessionId: "sess-1" } },
			body: { discardUncommitted: true },
		});
	});

	it("leaves every other failure an ordinary error", async () => {
		postMock.mockResolvedValue({ error: { code: "SESSION_NOT_FOUND", message: "Unknown session" } });

		await expect(killSession("sess-1")).rejects.not.toBeInstanceOf(UndeliveredWorkError);
		await expect(killSession("sess-1")).rejects.toThrow("Unknown session");
	});
});

describe("undeliveredWorkFrom", () => {
	it("ignores anything that is not the refusal", () => {
		expect(undeliveredWorkFrom(null)).toBeNull();
		expect(undeliveredWorkFrom({ code: "SESSION_NOT_FOUND" })).toBeNull();
		expect(undeliveredWorkFrom(new Error("network"))).toBeNull();
	});

	// A refusal with unreadable details is still a refusal. Falling back to
	// "something went wrong" would hide a reason the daemon stated plainly.
	it("still recognises the refusal when its details are missing or junk", () => {
		expect(undeliveredWorkFrom({ code: "SESSION_HAS_UNDELIVERED_WORK" })).toEqual([]);
		expect(undeliveredWorkFrom({ code: "SESSION_HAS_UNDELIVERED_WORK", details: { files: "nope" } })).toEqual([]);
		expect(
			undeliveredWorkFrom({ code: "SESSION_HAS_UNDELIVERED_WORK", details: { files: [{ path: "a.go" }, 7, null] } }),
		).toEqual([{ path: "a.go", status: "changed" }]);
	});
});

describe("a refusal for the worker's subagents", () => {
	const childrenRefusal = {
		error: "conflict",
		code: "SESSION_HAS_UNMERGED_CHILDREN",
		message: "sess-1 has 1 child worktree(s) whose work is not on its branch yet",
		details: {
			children: [
				{ agentId: "a1", description: "Write the parser", state: "running", branch: "ao-child/sess-1/a1", detail: "" },
				{ description: "no id, dropped" },
			],
		},
	};

	beforeEach(() => postMock.mockReset());

	it("is read as undelivered work carrying the subagents, so the same dialog can offer to end it", async () => {
		postMock.mockResolvedValueOnce({ data: undefined, error: childrenRefusal });
		const err = await killSession("sess-1").catch((e: unknown) => e);
		expect(err).toBeInstanceOf(UndeliveredWorkError);
		const work = (err as UndeliveredWorkError).work;
		expect(work.files).toEqual([]);
		expect(work.subagents).toEqual([
			{ agentId: "a1", description: "Write the parser", state: "running", branch: "ao-child/sess-1/a1", detail: "" },
		]);
	});

	it("is not mistaken for any other failure", () => {
		expect(unmergedChildrenFrom({ code: "SOMETHING_ELSE" })).toBeNull();
		expect(unmergedChildrenFrom(new Error("boom"))).toBeNull();
		expect(undeliveredWorkFrom(childrenRefusal)).toBeNull();
	});

	it("rides along a files refusal, so the dialog shows both lists", async () => {
		postMock.mockResolvedValueOnce({
			data: undefined,
			error: { ...refusalBody, details: { ...refusalBody.details, children: childrenRefusal.details.children } },
		});
		const err = await killSession("sess-1").catch((e: unknown) => e);
		const work = (err as UndeliveredWorkError).work;
		expect(work.files.length).toBeGreaterThan(0);
		expect(work.subagents.map((c) => c.agentId)).toEqual(["a1"]);
	});
});
