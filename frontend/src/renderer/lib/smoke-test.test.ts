import { describe, expect, it } from "vitest";
import { progressFor, type SmokeCheck } from "./smoke-test";

const base = {
	sessionId: "w1",
	projectId: "p",
	why: "",
	steps: [],
	expected: "",
	prNum: 0,
	fileRef: "",
	note: "",
	evidence: [],
	agentEvidence: [],
	createdAt: "2026-08-20T00:00:00Z",
	updatedAt: "2026-08-20T00:00:00Z",
};

const check = (over: Partial<SmokeCheck> & { id: string }): SmokeCheck =>
	({ seq: 1, name: over.id, verdict: "pending", ...base, ...over }) as SmokeCheck;

describe("progressFor", () => {
	it("counts the user's verdicts, and only the user's", () => {
		// A machine pass is not a pass. If it were counted as one the checklist
		// would read complete with nobody having opened the app.
		const p = progressFor([
			check({ id: "a", verdict: "pass" }),
			check({ id: "b", verdict: "pending", agentVerdict: "pass" }),
		]);
		expect(p.total).toBe(2);
		expect(p.pass).toBe(1);
		expect(p.pending).toBe(1);
		expect(p.checked).toBe(1);
		expect(p.agentPass).toBe(1);
	});

	it("counts a machine's verdict only while the person has not decided", () => {
		// The person judged the case themselves; a machine's older answer must
		// stop being counted rather than going on shouting.
		const p = progressFor([check({ id: "a", verdict: "pass", agentVerdict: "fail" })]);
		expect(p.agentFail).toBe(0);
		expect(p.pass).toBe(1);
	});

	it("leaves retired cases out of the checklist it counts", () => {
		const p = progressFor([
			check({ id: "a", verdict: "pass" }),
			check({ id: "b", verdict: "fail", retiredAt: "2026-08-20T00:00:00Z", retiredReason: "covered by tests" }),
		]);
		expect(p.total).toBe(1);
		expect(p.retired).toBe(1);
		// The retired case's old failure is not the checklist's failure any more.
		expect(p.fail).toBe(0);
		expect(p.pending).toBe(0);
		expect(p.checked).toBe(1);
	});
});
