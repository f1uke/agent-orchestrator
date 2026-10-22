import { describe, expect, it } from "vitest";
import {
	actorLabel,
	fixAction,
	fixFailure,
	FixPlanChangedError,
	fixStateText,
	newestFirst,
	planView,
	requestText,
	stepNote,
	undoVerb,
} from "./fix";
import type { AlertHit, Fix, FixPlan, FixStep } from "./types";

function hit(id: string, over: Partial<AlertHit> = {}): AlertHit {
	return {
		id,
		rule: id.slice(0, id.indexOf("~")),
		subject: { kind: "version", train: "21-2026", app: "di" },
		severity: "risk",
		bucket: "badge",
		text: "",
		evidence: {},
		links: [],
		asOf: "2026-10-15T00:00:00Z",
		...over,
	};
}

const e1Step: FixStep = {
	label: "Jira release di 2.3.0",
	before: "unreleased",
	after: "released",
	effect: {
		kind: "jira_version_update",
		site: "https://demo.example.com",
		versionId: "10",
		before: { released: false },
		after: { released: true, releaseDate: "2026-10-08" },
	},
	request: {
		method: "PUT",
		url: "https://demo.example.com/rest/api/3/version/10",
		body: { released: true, releaseDate: "2026-10-08" },
	},
};

const l3Step: FixStep = {
	label: "Long-lived plan demo-chat",
	before: "none",
	after: "targets 21-2026",
	effect: {
		kind: "ao_plan_target",
		plan: "demo-chat",
		planVersion: 1,
		before: { targetKind: "unplanned" },
		after: { targetKind: "train", targetTrain: "21-2026" },
	},
	request: { method: "PUT", url: "/api/v1/release-trains/llb-plans/demo-chat", body: { targetKind: "train" } },
};

const g5Step: FixStep = {
	label: "Demo #21 page",
	before: "missing",
	after: "DEMO-101, DEMO-102",
	effect: { kind: "copy_text", target: "https://wiki.example.com/p/1", text: "DEMO-101\nDEMO-102" },
	request: { method: "COPY", url: "https://wiki.example.com/p/1", body: "DEMO-101\nDEMO-102" },
};

function plan(over: Partial<FixPlan>): FixPlan {
	return { hit: "E1~v/20-2026/di", rule: "E1", title: "t", mode: "queue", steps: [], fingerprint: "fp", ...over };
}

describe("fix state labels", () => {
	it("says a dry run is recorded, not sent", () => {
		expect(fixStateText("dry_run")).toBe("fix recorded (dry run)");
		expect(fixStateText("applied")).toBe("applied");
		expect(fixStateText("withdrawn")).toBe("withdrawn");
	});
});

describe("which hits get a Fix button", () => {
	it("a fixable hit gets a button, a hit with no fix gets none", () => {
		expect(fixAction(hit("E1~v/20-2026/di", { fix: "queue" }), false)).toEqual({ kind: "fix" });
		expect(fixAction(hit("C5~v/21-2026/di"), false)).toBeNull();
	});

	it("a hit with an open fix shows that fix instead of a button", () => {
		const h = hit("E1~v/20-2026/di", { fix: "queue", openFix: { id: "fx-1", state: "dry_run" } });
		expect(fixAction(h, false)).toEqual({ kind: "open", ref: { id: "fx-1", state: "dry_run" } });
	});

	it("a hidden hit gets no button", () => {
		expect(fixAction(hit("E1~v/20-2026/di", { fix: "queue" }), true)).toBeNull();
	});
});

describe("the request block", () => {
	it("shows the method, URL and the pretty JSON body", () => {
		expect(requestText(e1Step.request)).toBe(
			'PUT https://demo.example.com/rest/api/3/version/10\n{\n  "released": true,\n  "releaseDate": "2026-10-08"\n}',
		);
	});

	it("leaves out an absent body", () => {
		expect(requestText({ method: "POST", url: "https://gitlab.example.com/api/v4/projects/1/jobs/9/retry" })).toBe(
			"POST https://gitlab.example.com/api/v4/projects/1/jobs/9/retry",
		);
	});

	it("shows copy text as the text itself, not a JSON string", () => {
		expect(requestText(g5Step.request)).toBe("COPY https://wiki.example.com/p/1\nDEMO-101\nDEMO-102");
	});
});

describe("what each step says it does", () => {
	it("a step for Jira or GitLab is a dry run", () => {
		expect(stepNote(e1Step)).toBe("dry run: nothing is sent");
	});

	it("an AO plan step applies to AO's own data", () => {
		expect(stepNote(l3Step)).toBe("applies now to AO's own plan; nothing goes to GitLab, Jira or Confluence");
	});

	it("a copy step is pasted by hand", () => {
		expect(stepNote(g5Step)).toBe("nothing is sent: paste it by hand");
	});
});

describe("plan view", () => {
	it("a queue plan confirms as a dry run", () => {
		expect(planView(plan({ steps: [e1Step] }))).toMatchObject({ confirm: "Record as a dry run", copy: null });
	});

	it("an apply plan confirms as Apply", () => {
		expect(planView(plan({ mode: "apply", steps: [l3Step] })).confirm).toBe("Apply");
	});

	it("a copy plan has no confirm and copies the text", () => {
		expect(planView(plan({ mode: "copy", steps: [g5Step] }))).toEqual({
			steps: [g5Step],
			confirm: null,
			copy: "DEMO-101\nDEMO-102",
		});
	});

	it("a blocked plan and a choose plan with no choice have no confirm", () => {
		expect(planView(plan({ mode: "blocked" })).confirm).toBeNull();
		const choices = [{ id: "jira", label: "Jira", steps: [e1Step] }];
		expect(planView(plan({ mode: "choose", choices })).confirm).toBeNull();
		expect(planView(plan({ mode: "choose", choices, choice: "jira", steps: [e1Step] })).confirm).toBe(
			"Record as a dry run",
		);
	});
});

describe("undo", () => {
	it("a dry run is withdrawn, an applied fix is undone, the rest have neither", () => {
		expect(undoVerb("dry_run")).toBe("Withdraw");
		expect(undoVerb("applied")).toBe("Undo");
		expect(undoVerb("withdrawn")).toBeNull();
		expect(undoVerb("undone")).toBeNull();
	});
});

describe("actor", () => {
	it("names the surface and the session", () => {
		expect(actorLabel("app")).toBe("the app");
		expect(actorLabel("cli")).toBe("the CLI");
		expect(actorLabel("api")).toBe("an API call");
		expect(actorLabel("cli:agent-orchestrator-412")).toBe("the CLI (agent-orchestrator-412)");
		expect(actorLabel("poller")).toBe("poller");
	});
});

describe("fixes list order", () => {
	it("puts the newest first", () => {
		const f = (id: string, createdAt: string) => ({ id, createdAt }) as Fix;
		expect(
			newestFirst([f("a", "2026-10-15T01:00:00Z"), f("b", "2026-10-15T03:00:00Z"), f("c", "2026-10-15T02:00:00Z")]).map(
				(x) => x.id,
			),
		).toEqual(["b", "c", "a"]);
	});
});

describe("confirm failures", () => {
	it("a 409 FIX_PLAN_CHANGED carries the new plan", () => {
		const next = plan({ fingerprint: "new" });
		const err = fixFailure(409, { code: "FIX_PLAN_CHANGED", message: "changed", details: { plan: next } });
		expect(err).toBeInstanceOf(FixPlanChangedError);
		expect((err as FixPlanChangedError).plan).toBe(next);
	});

	it("any other error keeps the daemon's message", () => {
		const err = fixFailure(409, {
			code: "PLAN_VERSION_CONFLICT",
			message: "plan demo-chat is at version 3; reload it and try again",
		});
		expect(err).not.toBeInstanceOf(FixPlanChangedError);
		expect(err.message).toBe("plan demo-chat is at version 3; reload it and try again (PLAN_VERSION_CONFLICT)");
	});
});
