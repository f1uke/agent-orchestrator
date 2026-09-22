import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { doneAt, doneDisposition, doneSearchText, matchesDoneQuery, sortDoneRecentFirst } from "./done-lane";

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

function ended(at: string) {
	return { source: "ao" as const, reason: "kill", at };
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

describe("doneAt", () => {
	it("is when the daemon recorded the ending, not the row's last update", () => {
		const s = session({ updatedAt: "2026-06-12T00:00:00Z", termination: ended("2026-06-10T08:00:00Z") });
		expect(doneAt(s)).toBe("2026-06-10T08:00:00Z");
	});

	it("falls back to updatedAt for a session that ended before AO kept an account", () => {
		expect(doneAt(session({ updatedAt: "2026-06-12T00:00:00Z" }))).toBe("2026-06-12T00:00:00Z");
	});
});

describe("sortDoneRecentFirst", () => {
	it("orders sessions by when they ended, most recent first", () => {
		const older = session({ id: "old", updatedAt: "2026-06-10T00:00:00Z" });
		const newer = session({ id: "new", updatedAt: "2026-06-12T00:00:00Z" });
		const middle = session({ id: "mid", updatedAt: "2026-06-11T00:00:00Z" });

		expect(sortDoneRecentFirst([older, newer, middle]).map((s) => s.id)).toEqual(["new", "mid", "old"]);
	});

	it("ranks by the recorded ending even when a later update touched the row", () => {
		// Ended first, but its row was bumped afterwards - updatedAt would put it on top.
		const touched = session({
			id: "touched",
			updatedAt: "2026-06-13T00:00:00Z",
			termination: ended("2026-06-10T00:00:00Z"),
		});
		const recent = session({
			id: "recent",
			updatedAt: "2026-06-12T00:00:00Z",
			termination: ended("2026-06-12T00:00:00Z"),
		});

		expect(sortDoneRecentFirst([touched, recent]).map((s) => s.id)).toEqual(["recent", "touched"]);
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

	it("sinks an unparseable timestamp to the bottom", () => {
		const bad = session({ id: "bad", updatedAt: "not a date" });
		const good = session({ id: "good", updatedAt: "2026-06-10T00:00:00Z" });

		expect(sortDoneRecentFirst([bad, good]).map((s) => s.id)).toEqual(["good", "bad"]);
	});
});

describe("done search", () => {
	const text = doneSearchText({
		id: "agent-orchestrator-212",
		title: "Kill runaway notification retry loop",
		branch: "feat/notif-retry",
		issueId: "jira:STAR-1234",
		prs: [{ number: 318 }],
	});

	it.each([
		["the display name, in any case", "RUNAWAY"],
		["the session id", "orchestrator-212"],
		["the branch", "feat/notif"],
		["the bare Jira key", "star-1234"],
		["a PR number as #N", "#318"],
		["an MR number as !N", "!318"],
		["several terms in any order", "retry kill"],
	])("matches on %s", (_label, query) => {
		expect(matchesDoneQuery(text, query)).toBe(true);
	});

	it("misses when any one term is absent", () => {
		expect(matchesDoneQuery(text, "retry websocket")).toBe(false);
	});

	it("matches everything on a blank query", () => {
		expect(matchesDoneQuery(text, "   ")).toBe(true);
	});
});
