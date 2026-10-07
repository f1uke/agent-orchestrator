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

import { killSession, scriptsStoreFrom, UndeliveredWorkError } from "./kill-session";

const store = {
	path: "/data/store-worktrees/mobile-ui-scripts/nter-7",
	branch: "ao/nter-7",
	baseBranch: "main",
	store: "/scripts",
	uncommitted: ["projects/nter/draft.yaml"],
	publish: {
		outcome: "refused",
		hold: "publish_conflict",
		detail: "ao/nter-7 conflicts with main",
		files: ["projects/nter/login.yaml"],
	},
};

const scriptsRefusal = {
	error: "conflict",
	code: "SESSION_HAS_UNPUBLISHED_SCRIPTS",
	message: "nter-7's scripts store worktree holds 1 uncommitted file(s)",
	details: { reason: "scripts_store_dirty", scriptsStore: store },
};

describe("a refusal for the scripts store worktree", () => {
	beforeEach(() => postMock.mockReset());

	it("is read as undelivered work carrying the store's files and the refused publish", async () => {
		postMock.mockResolvedValueOnce({ data: undefined, error: scriptsRefusal });
		const err = await killSession("nter-7").catch((e: unknown) => e);
		expect(err).toBeInstanceOf(UndeliveredWorkError);
		const work = (err as UndeliveredWorkError).work;
		expect(work.files).toEqual([]);
		expect(work.subagents).toEqual([]);
		expect(work.scriptsStore).toEqual({
			path: store.path,
			branch: "ao/nter-7",
			baseBranch: "main",
			uncommitted: ["projects/nter/draft.yaml"],
			publishRefused: {
				hold: "publish_conflict",
				detail: "ao/nter-7 conflicts with main",
				files: ["projects/nter/login.yaml"],
			},
		});
	});

	it("rides along a files refusal, so the dialog shows every list", async () => {
		postMock.mockResolvedValueOnce({
			data: undefined,
			error: {
				code: "SESSION_HAS_UNDELIVERED_WORK",
				details: {
					files: [{ path: "src/main.go", status: "modified" }],
					scriptsStore: { ...store, publish: { outcome: "fast_forward" } },
				},
			},
		});
		const work = ((await killSession("nter-7").catch((e: unknown) => e)) as UndeliveredWorkError).work;
		expect(work.files.map((f) => f.path)).toEqual(["src/main.go"]);
		expect(work.scriptsStore?.uncommitted).toEqual(["projects/nter/draft.yaml"]);
		expect(work.scriptsStore?.publishRefused).toBeUndefined();
	});

	it("is not mistaken for any other failure, and survives missing details", () => {
		expect(scriptsStoreFrom({ code: "SOMETHING_ELSE", details: { scriptsStore: store } })).toBeNull();
		expect(scriptsStoreFrom({ code: "SESSION_HAS_UNDELIVERED_WORK", details: { files: [] } })).toBeNull();
		expect(scriptsStoreFrom({ code: "SESSION_HAS_UNPUBLISHED_SCRIPTS" })).toEqual({
			path: "",
			branch: "",
			baseBranch: "",
			uncommitted: [],
		});
	});
});
