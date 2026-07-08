import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { doneDisposition, formatMovedAgo, sortDoneRecentFirst } from "./done-chip";

function session(overrides: Partial<WorkspaceSession>): WorkspaceSession {
	return {
		id: "s",
		workspaceId: "p",
		workspaceName: "w",
		title: "t",
		provider: "claude-code",
		branch: "b",
		status: "terminated",
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
		...overrides,
	};
}

describe("doneDisposition", () => {
	it("resolves a terminated session to 'terminated'", () => {
		expect(doneDisposition(session({ status: "terminated" }))).toBe("terminated");
	});

	it("resolves a merged session to 'done'", () => {
		expect(doneDisposition(session({ status: "merged" }))).toBe("done");
	});

	it("treats any other done-bucket status as 'done'", () => {
		// Defensive: the done bucket only ever holds merged/terminated, but a stray
		// status must still read as done rather than mislabel as terminated.
		expect(doneDisposition(session({ status: "unknown" }))).toBe("done");
	});
});

describe("formatMovedAgo", () => {
	afterEach(() => vi.useRealTimers());

	it("prefixes the compact relative time with 'moved'", () => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-06-10T05:00:00Z"));
		expect(formatMovedAgo("2026-06-10T03:00:00Z")).toBe("moved 2h ago");
	});

	it("reads 'moved 3d ago' for a multi-day gap", () => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-06-13T00:00:00Z"));
		expect(formatMovedAgo("2026-06-10T00:00:00Z")).toBe("moved 3d ago");
	});

	it("reads 'moved just now' for a missing timestamp", () => {
		expect(formatMovedAgo(undefined)).toBe("moved just now");
	});
});

describe("sortDoneRecentFirst", () => {
	it("orders sessions by updatedAt, most recently moved first", () => {
		const older = session({ id: "old", updatedAt: "2026-06-10T00:00:00Z" });
		const newer = session({ id: "new", updatedAt: "2026-06-12T00:00:00Z" });
		const middle = session({ id: "mid", updatedAt: "2026-06-11T00:00:00Z" });

		expect(sortDoneRecentFirst([older, newer, middle]).map((s) => s.id)).toEqual(["new", "mid", "old"]);
	});

	it("does not mutate the input array", () => {
		const input = [
			session({ id: "a", updatedAt: "2026-06-10T00:00:00Z" }),
			session({ id: "b", updatedAt: "2026-06-12T00:00:00Z" }),
		];

		sortDoneRecentFirst(input);

		expect(input.map((s) => s.id)).toEqual(["a", "b"]);
	});

	it("keeps input order for equal timestamps (stable)", () => {
		const a = session({ id: "a", updatedAt: "2026-06-10T00:00:00Z" });
		const b = session({ id: "b", updatedAt: "2026-06-10T00:00:00Z" });

		expect(sortDoneRecentFirst([a, b]).map((s) => s.id)).toEqual(["a", "b"]);
	});
});
