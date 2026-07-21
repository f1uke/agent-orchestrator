import { describe, expect, it, vi } from "vitest";
import { type ResolvedCandidate, openWorkspaceFileRef } from "./open-workspace-file";

/** Candidates default to in-workspace; the reveal cases opt out explicitly. */
function inWs(paths: string[]): ResolvedCandidate[] {
	return paths.map((path) => ({ path, inWorkspace: true }));
}

function harness(candidates: ResolvedCandidate[] | Error) {
	const onOpen = vi.fn();
	const onDisambiguate = vi.fn();
	const onNotFound = vi.fn();
	const resolve = vi.fn(async () => {
		if (candidates instanceof Error) throw candidates;
		return candidates;
	});
	return { onOpen, onDisambiguate, onNotFound, resolve };
}

describe("openWorkspaceFileRef", () => {
	it("opens directly when exactly one candidate resolves", async () => {
		const h = harness(inWs(["pkg/a.go"]));
		await openWorkspaceFileRef({ sessionId: "s1", ref: "a.go", line: 12, ...h });
		expect(h.onOpen).toHaveBeenCalledWith({ path: "pkg/a.go", line: 12, inWorkspace: true });
		expect(h.onDisambiguate).not.toHaveBeenCalled();
		expect(h.onNotFound).not.toHaveBeenCalled();
	});

	it("asks for disambiguation when multiple candidates resolve", async () => {
		const h = harness(inWs(["a/x.go", "b/x.go"]));
		await openWorkspaceFileRef({ sessionId: "s1", ref: "x.go", ...h });
		expect(h.onDisambiguate).toHaveBeenCalledWith(inWs(["a/x.go", "b/x.go"]), undefined);
		expect(h.onOpen).not.toHaveBeenCalled();
	});

	it("reports not-found when no candidate resolves", async () => {
		const h = harness([]);
		await openWorkspaceFileRef({ sessionId: "s1", ref: "ghost.go", ...h });
		expect(h.onNotFound).toHaveBeenCalledWith("ghost.go");
		expect(h.onOpen).not.toHaveBeenCalled();
	});

	it("degrades to not-found (never throws) when resolution errors", async () => {
		const h = harness(new Error("network"));
		await expect(openWorkspaceFileRef({ sessionId: "s1", ref: "a.go", ...h })).resolves.toBeUndefined();
		expect(h.onNotFound).toHaveBeenCalledWith("a.go");
	});

	// The reveal-in-tree gate. A file outside the project must reach onOpen with
	// inWorkspace false so the caller keeps the standalone viewer instead of
	// switching the rail to a Files tab that cannot contain it.
	it("carries the out-of-workspace verdict through to onOpen", async () => {
		const h = harness([{ path: "/etc/hosts", inWorkspace: false }]);
		await openWorkspaceFileRef({ sessionId: "s1", ref: "/etc/hosts", ...h });
		expect(h.onOpen).toHaveBeenCalledWith({ path: "/etc/hosts", line: undefined, inWorkspace: false });
	});

	// The verdict is per candidate, not per request: a bare ref can match one file
	// inside the workspace and nothing else, and the picker must not flatten that.
	it("keeps the verdict per candidate through disambiguation", async () => {
		const mixed: ResolvedCandidate[] = [
			{ path: "a/x.go", inWorkspace: true },
			{ path: "/elsewhere/x.go", inWorkspace: false },
		];
		const h = harness(mixed);
		await openWorkspaceFileRef({ sessionId: "s1", ref: "x.go", ...h });
		expect(h.onDisambiguate).toHaveBeenCalledWith(mixed, undefined);
	});
});
