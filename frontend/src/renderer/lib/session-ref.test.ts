import { describe, expect, it } from "vitest";
import { findSessionLinks, resolveSessionToken, sessionRefLabel } from "./session-ref";

const KNOWN = new Set(["agent-orchestrator-54", "agent-orchestrator-60", "ao-demo-3", "docs-site-12"]);

describe("sessionRefLabel", () => {
	it("prefixes the canonical id with the @ sigil", () => {
		expect(sessionRefLabel("agent-orchestrator-60")).toBe("@agent-orchestrator-60");
	});
});

describe("resolveSessionToken", () => {
	const opts = { knownIds: KNOWN, currentProjectId: "agent-orchestrator" };

	it("resolves the bare canonical id", () => {
		expect(resolveSessionToken("agent-orchestrator-54", opts)).toBe("agent-orchestrator-54");
	});

	it("resolves the @<project>-<num> full form", () => {
		expect(resolveSessionToken("@agent-orchestrator-54", opts)).toBe("agent-orchestrator-54");
	});

	it("expands the @<num> short form within the current project", () => {
		expect(resolveSessionToken("@60", opts)).toBe("agent-orchestrator-60");
	});

	it("does not expand @<num> when the current project is unknown", () => {
		expect(resolveSessionToken("@60", { knownIds: KNOWN })).toBeNull();
	});

	it("returns null for an unknown id (false-positive gate)", () => {
		expect(resolveSessionToken("agent-orchestrator-999", opts)).toBeNull();
		expect(resolveSessionToken("proj-2272", opts)).toBeNull();
	});
});

describe("findSessionLinks", () => {
	const opts = { knownIds: KNOWN, currentProjectId: "agent-orchestrator" };

	it("finds a bare canonical id with the correct range", () => {
		const line = "attach to agent-orchestrator-54 now";
		const matches = findSessionLinks(line, opts);
		expect(matches).toHaveLength(1);
		const m = matches[0];
		expect(m.sessionId).toBe("agent-orchestrator-54");
		expect(line.slice(m.startIndex, m.endIndex)).toBe("agent-orchestrator-54");
	});

	it("linkifies the [from @<id>] report wrapper", () => {
		const line = "[from @agent-orchestrator-54] done";
		const matches = findSessionLinks(line, opts);
		expect(matches).toHaveLength(1);
		expect(matches[0].sessionId).toBe("agent-orchestrator-54");
		// The @ is part of the linked token.
		expect(line.slice(matches[0].startIndex, matches[0].endIndex)).toBe("@agent-orchestrator-54");
	});

	it("linkifies the @<num> short form", () => {
		const line = "see @60 for the fix";
		const matches = findSessionLinks(line, opts);
		expect(matches).toHaveLength(1);
		expect(matches[0].sessionId).toBe("agent-orchestrator-60");
		expect(line.slice(matches[0].startIndex, matches[0].endIndex)).toBe("@60");
	});

	it("finds multiple references on one line", () => {
		const line = "agent-orchestrator-54 pinged @60";
		const matches = findSessionLinks(line, opts);
		expect(matches.map((m) => m.sessionId)).toEqual(["agent-orchestrator-54", "agent-orchestrator-60"]);
	});

	it("resolves a full id from another project (cross-project)", () => {
		const line = "compare with docs-site-12";
		const matches = findSessionLinks(line, opts);
		expect(matches).toHaveLength(1);
		expect(matches[0].sessionId).toBe("docs-site-12");
	});

	it("does not linkify a bare number without the @ sigil", () => {
		expect(findSessionLinks("exited with code 60", opts)).toHaveLength(0);
	});

	it("does not linkify an unknown hyphen-number token (Jira key, unknown session)", () => {
		expect(findSessionLinks("blocked by PROJ-2272 and issue-42", opts)).toHaveLength(0);
	});

	it("does not linkify an id embedded in a longer word", () => {
		expect(findSessionLinks("xagent-orchestrator-54x", opts)).toHaveLength(0);
	});

	it("finds the id inside the ao send command form", () => {
		const line = "ao send --session agent-orchestrator-54 --message hi";
		const matches = findSessionLinks(line, opts);
		expect(matches).toHaveLength(1);
		expect(matches[0].sessionId).toBe("agent-orchestrator-54");
	});
});
