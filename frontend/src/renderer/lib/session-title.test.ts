import { describe, expect, it } from "vitest";
import { displaySessionName, stripLeadingJiraKeyPrefix } from "./session-title";

describe("stripLeadingJiraKeyPrefix", () => {
	it("returns an empty string for nullish input", () => {
		expect(stripLeadingJiraKeyPrefix(null)).toBe("");
		expect(stripLeadingJiraKeyPrefix(undefined)).toBe("");
		expect(stripLeadingJiraKeyPrefix("")).toBe("");
	});

	it("strips a leading key + separator, leaving the summary", () => {
		expect(stripLeadingJiraKeyPrefix("PROJ-2272 — App eligibility")).toBe("App eligibility");
		expect(stripLeadingJiraKeyPrefix("PROJ-2272 - App eligibility")).toBe("App eligibility");
		expect(stripLeadingJiraKeyPrefix("PROJ-2272: App eligibility")).toBe("App eligibility");
		expect(stripLeadingJiraKeyPrefix("PROJ-2272 App eligibility")).toBe("App eligibility");
		expect(stripLeadingJiraKeyPrefix("DEMO-1 fix the thing")).toBe("fix the thing");
	});

	it("strips bracketed / hashed / jira-bound key prefixes", () => {
		expect(stripLeadingJiraKeyPrefix("[PROJ-2272] App eligibility")).toBe("App eligibility");
		expect(stripLeadingJiraKeyPrefix("#PROJ-2272 App eligibility")).toBe("App eligibility");
		expect(stripLeadingJiraKeyPrefix("jira:PROJ-2272 App eligibility")).toBe("App eligibility");
	});

	it("leaves a bare key (no trailing summary) intact", () => {
		expect(stripLeadingJiraKeyPrefix("PROJ-2272")).toBe("PROJ-2272");
	});

	it("leaves a normal title untouched, including a key that appears mid-string", () => {
		expect(stripLeadingJiraKeyPrefix("App eligibility")).toBe("App eligibility");
		expect(stripLeadingJiraKeyPrefix("Fix flaky test in PROJ-2272 flow")).toBe("Fix flaky test in PROJ-2272 flow");
	});

	it("does not treat a lowercase or malformed token as a key", () => {
		expect(stripLeadingJiraKeyPrefix("proj-2272 nope")).toBe("proj-2272 nope");
		expect(stripLeadingJiraKeyPrefix("v2-2 release")).toBe("v2-2 release");
	});
});

describe("displaySessionName", () => {
	it("uses the display name when it is a clean summary", () => {
		expect(displaySessionName({ displayName: "App eligibility", issueId: "jira:PROJ-2272", id: "s1" })).toBe(
			"App eligibility",
		);
	});

	it("strips an accidental key prefix from the display name", () => {
		expect(displaySessionName({ displayName: "PROJ-2272 App eligibility", issueId: "jira:PROJ-2272", id: "s1" })).toBe(
			"App eligibility",
		);
	});

	it("shows the bare key (not the raw binding) when there is no display name", () => {
		expect(displaySessionName({ displayName: null, issueId: "jira:PROJ-2272", id: "s1" })).toBe("PROJ-2272");
	});

	it("falls back to a non-jira issueId, then the session id", () => {
		expect(displaySessionName({ displayName: "", issueId: "Fix WebGL fallback", id: "s1" })).toBe("Fix WebGL fallback");
		expect(displaySessionName({ displayName: null, issueId: null, id: "agent-orchestrator-81" })).toBe(
			"agent-orchestrator-81",
		);
	});
});
