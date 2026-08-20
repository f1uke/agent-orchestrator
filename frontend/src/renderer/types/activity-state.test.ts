import { describe, expect, it } from "vitest";
import { attentionZone, toSessionActivity, type WorkspaceSession } from "./workspace";

// `parked` was added so AO can tell an agent BLOCKED on a permission prompt apart
// from one that simply finished its turn and is sitting at an ordinary prompt.
// Only the first may not be typed at, and folding them together is what made the
// daemon drop every CI / review / merge-conflict nudge for sessions that were
// perfectly able to act on them.
describe("toSessionActivity", () => {
	it("keeps parked as its own state rather than folding it into unknown", () => {
		expect(toSessionActivity({ state: "parked", lastActivityAt: "2026-06-05T12:00:00Z" })).toEqual({
			state: "parked",
			lastActivityAt: "2026-06-05T12:00:00Z",
		});
	});

	it("still round-trips every other state the daemon reports", () => {
		for (const state of ["active", "idle", "waiting_input", "exited"]) {
			expect(toSessionActivity({ state, lastActivityAt: "" })?.state).toBe(state);
		}
	});

	it("normalizes a state from a newer daemon to unknown instead of trusting it", () => {
		expect(toSessionActivity({ state: "hibernating", lastActivityAt: "" })?.state).toBe("unknown");
	});
});

// The board lane is derived from STATUS, never from the activity state, which is
// why a new activity state cannot silently fall into a default column. These pin
// that: a genuinely-waiting session and a parked one both reach the board as
// `needs_input` (the daemon derives parked as an already-aged idle) and land in
// the same "Needs you" lane they always have.
describe("attentionZone is unaffected by the activity split", () => {
	function boardSession(over: Partial<WorkspaceSession>): WorkspaceSession {
		return {
			id: "octo-1",
			name: "fix ci",
			projectId: "octo",
			kind: "worker",
			status: "needs_input",
			isTerminated: false,
			...over,
		} as WorkspaceSession;
	}

	it("puts a session at a real prompt in Needs you", () => {
		const s = boardSession({ statusReason: "waiting_input", activity: { state: "waiting_input", lastActivityAt: "" } });
		expect(attentionZone(s)).toBe("action");
	});

	it("puts a parked session in the same lane an aged idle has always used", () => {
		const parked = boardSession({ statusReason: "idle_aged", activity: { state: "parked", lastActivityAt: "" } });
		const agedIdle = boardSession({ statusReason: "idle_aged", activity: { state: "idle", lastActivityAt: "" } });
		expect(attentionZone(parked)).toBe(attentionZone(agedIdle));
		expect(attentionZone(parked)).toBe("action");
	});

	it("leaves a parked session with an open PR in its PR lane", () => {
		const s = boardSession({ status: "mergeable", statusReason: "pr_pipeline", activity: { state: "parked", lastActivityAt: "" } });
		expect(attentionZone(s)).toBe("merge");
	});
});
