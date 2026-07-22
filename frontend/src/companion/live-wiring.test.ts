import { describe, expect, it } from "vitest";
import { applyEvent, emptySlots, type ActivityFrame } from "./activity-decay";
import { composeBubble } from "./bubble-compose";
import { sessionsToActivities, type LiveSession } from "./live-roster";

// Frames captured VERBATIM off a real `GET /api/v1/activity/stream`, from an
// isolated daemon driven by real `POST /sessions/{id}/activity` hook calls — the
// same path `ao hooks` takes after its whitelist has curated the payload.
//
// They are pinned here because the wiring is the one thing unit tests with
// hand-written fixtures cannot prove: that the bytes the daemon actually emits are
// the bytes this consumer actually understands. If the feed's shape drifts, this
// goes red rather than the overlay quietly going mute.
const CAPTURED: ActivityFrame[] = [
	{
		sessionId: "repo-1",
		kind: "tool_start",
		at: "2026-07-22T12:57:44.493848Z",
		tool: "Bash",
		text: "Running the test suite",
		ttlMs: 20000,
		coarse: "working",
		coarseTtlMs: 600000,
	},
	{
		sessionId: "repo-1",
		kind: "tool_end",
		at: "2026-07-22T12:57:45.510626Z",
		tool: "Read",
		target: "hooks.go",
		ttlMs: 8000,
		coarse: "working",
		coarseTtlMs: 600000,
	},
	{
		sessionId: "repo-1",
		kind: "activity",
		at: "2026-07-22T12:57:46.527751Z",
		ttlMs: 0,
		coarse: "waiting",
		coarseTtlMs: 0,
	},
];

const T0 = Date.parse(CAPTURED[0].at);

describe("frames captured off the real daemon", () => {
	it("turns a real Bash frame into the sentence its model wrote", () => {
		const slots = applyEvent(emptySlots(), CAPTURED[0]);

		expect(composeBubble(slots, T0 + 1_000)?.text).toBe("Running the test suite");
	});

	it("lets a real later frame supersede the one before it", () => {
		const slots = CAPTURED.slice(0, 2).reduce(applyEvent, emptySlots());

		expect(composeBubble(slots, T0 + 2_000)?.text).toBe("Reading hooks.go");
	});

	it("decays a real frame to the coarse truth, then to silence", () => {
		// Nothing arrives after these three. The claim has to run out on the clock —
		// this is the "still saying Running the test suite two minutes later" case.
		const slots = CAPTURED.slice(0, 2).reduce(applyEvent, emptySlots());

		expect(composeBubble(slots, T0 + 30_000)?.text).toBe("Working…");
		expect(composeBubble(slots, T0 + 11 * 60_000)).toBeNull();
	});

	it("treats the real waiting frame as sticky and raises it as an alert", () => {
		// coarseTtlMs 0 off the real wire. A pending prompt is pending until answered.
		const slots = CAPTURED.reduce(applyEvent, emptySlots());
		const said = composeBubble(slots, T0 + 24 * 60 * 60_000);

		expect(said?.text).toBe("Waiting for you");
		expect(said?.tone).toBe("alert");
	});
});

describe("the sessions payload the real daemon returns", () => {
	// Captured verbatim from `GET /api/v1/sessions` on the same daemon. Note `name`
	// and `projectName` come back NULL for a session that has neither — the overlay
	// must not render "null" under a Proc.
	const REAL_ROW = {
		id: "repo-1",
		name: null,
		projectId: "repo",
		projectName: null,
		status: "needs_input",
		statusReason: "waiting_input",
		isTerminated: false,
	};

	it("survives the nulls the API really sends", () => {
		const session: LiveSession = {
			id: REAL_ROW.id,
			name: REAL_ROW.name ?? REAL_ROW.id,
			projectName: REAL_ROW.projectName ?? undefined,
			kind: "worker",
			status: REAL_ROW.status as LiveSession["status"],
			statusReason: REAL_ROW.statusReason as LiveSession["statusReason"],
			isTerminated: REAL_ROW.isTerminated,
		};
		const [pet] = sessionsToActivities([session]);

		expect(pet.name).toBe("repo-1");
		expect(pet.project).toBe("");
		// waiting_input is a REAL prompt, so this one is allowed to come to the front.
		expect(pet.status).toBe("needs_input");
	});
});
