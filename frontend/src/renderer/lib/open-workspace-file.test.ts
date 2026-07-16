import { describe, expect, it, vi } from "vitest";
import { openWorkspaceFileRef } from "./open-workspace-file";

function harness(candidates: string[] | Error) {
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
		const h = harness(["pkg/a.go"]);
		await openWorkspaceFileRef({ sessionId: "s1", ref: "a.go", line: 12, ...h });
		expect(h.onOpen).toHaveBeenCalledWith({ path: "pkg/a.go", line: 12 });
		expect(h.onDisambiguate).not.toHaveBeenCalled();
		expect(h.onNotFound).not.toHaveBeenCalled();
	});

	it("asks for disambiguation when multiple candidates resolve", async () => {
		const h = harness(["a/x.go", "b/x.go"]);
		await openWorkspaceFileRef({ sessionId: "s1", ref: "x.go", ...h });
		expect(h.onDisambiguate).toHaveBeenCalledWith(["a/x.go", "b/x.go"], undefined);
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
});
