import { afterEach, describe, expect, it, vi } from "vitest";
import { createHttpTransport } from "./live-transport";

// What the daemon actually serves. `GET /api/v1/sessions` has NO `name` and no
// `projectName` — the board builds both itself, from `displayName`/`issueId` and
// from a join on the projects list. Reading a `name` that was never in the schema
// is why every Proc was labelled with its session id.

const SESSION = {
	id: "agent-orchestrator-141",
	projectId: "p1",
	status: "working",
	kind: "worker",
	isTerminated: false,
};

function serve(routes: Record<string, unknown>) {
	const fetchMock = vi.fn(async (url: string) => {
		const path = new URL(url).pathname;
		const body = routes[path];
		if (body === undefined) return { ok: false, status: 404, json: async () => ({}) } as unknown as Response;
		return { ok: true, status: 200, json: async () => body } as unknown as Response;
	});
	vi.stubGlobal("fetch", fetchMock);
	return fetchMock;
}

const PROJECTS = { projects: [{ id: "p1", name: "agent-orchestrator" }] };

afterEach(() => vi.unstubAllGlobals());

describe("reading the roster off the real sessions endpoint", () => {
	it("labels a Proc with the board's display name, not its session id", async () => {
		serve({
			"/api/v1/sessions": { sessions: [{ ...SESSION, displayName: "smoke to testiny" }] },
			"/api/v1/projects": PROJECTS,
		});

		const [session] = await createHttpTransport("http://localhost:4021").fetchSessions();

		expect(session.name).toBe("smoke to testiny");
	});

	it("shows the issue key when a session has no display name", async () => {
		serve({
			"/api/v1/sessions": { sessions: [{ ...SESSION, issueId: "jira:PROJ-2272" }] },
			"/api/v1/projects": PROJECTS,
		});

		const [session] = await createHttpTransport("http://localhost:4021").fetchSessions();

		expect(session.name).toBe("PROJ-2272");
	});

	it("falls back to the session id only when there is no other name at all", async () => {
		serve({ "/api/v1/sessions": { sessions: [SESSION] }, "/api/v1/projects": PROJECTS });

		const [session] = await createHttpTransport("http://localhost:4021").fetchSessions();

		expect(session.name).toBe("agent-orchestrator-141");
	});

	it("strips a leading issue key the board would strip too", async () => {
		serve({
			"/api/v1/sessions": { sessions: [{ ...SESSION, displayName: "PROJ-2272 App eligibility" }] },
			"/api/v1/projects": PROJECTS,
		});

		const [session] = await createHttpTransport("http://localhost:4021").fetchSessions();

		expect(session.name).toBe("App eligibility");
	});

	it("resolves the project name by joining projectId, because the session row has no project name", async () => {
		serve({ "/api/v1/sessions": { sessions: [SESSION] }, "/api/v1/projects": PROJECTS });

		const [session] = await createHttpTransport("http://localhost:4021").fetchSessions();

		expect(session.projectName).toBe("agent-orchestrator");
	});

	it("still returns the roster when the projects call fails — a missing project is not a missing pet", async () => {
		serve({ "/api/v1/sessions": { sessions: [{ ...SESSION, displayName: "coupon search ui" }] } });

		const [session] = await createHttpTransport("http://localhost:4021").fetchSessions();

		expect(session.name).toBe("coupon search ui");
		expect(session.projectName).toBeUndefined();
	});

	it("fails loudly when the sessions call itself fails, so the last good roster is kept", async () => {
		serve({ "/api/v1/projects": PROJECTS });

		await expect(createHttpTransport("http://localhost:4021").fetchSessions()).rejects.toThrow(/404/);
	});
});
