import { describe, expect, it } from "vitest";
import { formatAliasLines, parseAliasLines, splitRefLinks, type RefLinkSettings, type RefPart } from "./ref-links";

const JIRA = "https://jira.example.com";
const GITLAB = "https://gitlab.example.com";

const full: RefLinkSettings = {
	jiraBaseUrl: JIRA,
	gitlabBaseUrl: GITLAB,
	gitlabDefaultRepo: "group/app",
	gitlabRepoAliases: { XYZ: "group/xyz-service", web: "group/sub/web-client" },
};

const empty: RefLinkSettings = { jiraBaseUrl: "", gitlabBaseUrl: "", gitlabDefaultRepo: "", gitlabRepoAliases: {} };

/** Just the links, as [text, url] pairs. */
function links(text: string, settings: RefLinkSettings = full): [string, string][] {
	return splitRefLinks(text, settings)
		.filter((p): p is Extract<RefPart, { kind: "ref" }> => p.kind === "ref")
		.map((p) => [p.value, p.url]);
}

function joined(parts: RefPart[]): string {
	return parts.map((p) => p.value).join("");
}

describe("splitRefLinks", () => {
	it("links a Jira key to its browse URL", () => {
		expect(links("ABC-123: ship the thing")).toEqual([["ABC-123", `${JIRA}/browse/ABC-123`]]);
	});

	it("links a bare !N to the default repo", () => {
		expect(links("review !1234 today")).toEqual([["!1234", `${GITLAB}/group/app/-/merge_requests/1234`]]);
	});

	it("links an aliased MR, alias included", () => {
		expect(links("waiting on XYZ !187 to land")).toEqual([
			["XYZ !187", `${GITLAB}/group/xyz-service/-/merge_requests/187`],
		]);
	});

	it("matches aliases case-insensitively", () => {
		expect(links("see WEB !9")).toEqual([["WEB !9", `${GITLAB}/group/sub/web-client/-/merge_requests/9`]]);
	});

	it("links refs inside inline code", () => {
		expect(links("fix (`ABC-7`) and `!42` and `XYZ !5`")).toEqual([
			["ABC-7", `${JIRA}/browse/ABC-7`],
			["!42", `${GITLAB}/group/app/-/merge_requests/42`],
			["XYZ !5", `${GITLAB}/group/xyz-service/-/merge_requests/5`],
		]);
	});

	it("links only !N when the alias sits outside the inline code", () => {
		expect(links("follow XYZ `!187` up")).toEqual([["!187", `${GITLAB}/group/xyz-service/-/merge_requests/187`]]);
	});

	it("links GitLab's glued alias!N and group/project!N forms", () => {
		expect(links("`XYZ!12` and `other/proj!3`")).toEqual([
			["XYZ!12", `${GITLAB}/group/xyz-service/-/merge_requests/12`],
			["other/proj!3", `${GITLAB}/other/proj/-/merge_requests/3`],
		]);
	});

	it("leaves an unknown uppercase alias unlinked instead of guessing the default repo", () => {
		expect(links("ping QQQ !187 please")).toEqual([]);
		expect(links("ping QQQ `!187` please")).toEqual([]);
		expect(links("`unknown-repo!99`")).toEqual([]);
	});

	it("treats MR / PR and ordinary words as a bare reference", () => {
		expect(links("MR !7")).toEqual([["!7", `${GITLAB}/group/app/-/merge_requests/7`]]);
		expect(links("fix: `!8`")).toEqual([["!8", `${GITLAB}/group/app/-/merge_requests/8`]]);
	});

	it("does not link look-alikes", () => {
		expect(links("a!=b, #!1, !!2, x/!3, !4a, PROJ-12-branch, feature/ABC-1")).toEqual([]);
		expect(links("./x!3 ../y!4")).toEqual([]);
	});

	it("leaves everything plain when nothing is configured", () => {
		const text = "ABC-123 XYZ !187 `!1234`";
		expect(splitRefLinks(text, empty)).toEqual([{ kind: "text", value: text }]);
		expect(splitRefLinks(text, undefined)).toEqual([{ kind: "text", value: text }]);
	});

	it("needs the GitLab address for any MR, and the default repo for a bare one", () => {
		expect(links("XYZ !1 and !2", { ...full, gitlabBaseUrl: "" })).toEqual([]);
		expect(links("XYZ !1 and !2", { ...full, gitlabDefaultRepo: "" })).toEqual([
			["XYZ !1", `${GITLAB}/group/xyz-service/-/merge_requests/1`],
		]);
		expect(links("ABC-1, !2", { ...full, jiraBaseUrl: "" })).toEqual([
			["!2", `${GITLAB}/group/app/-/merge_requests/2`],
		]);
	});

	it("keeps every character of the text, in order", () => {
		const text = "ABC-1 then XYZ `!2` then !3 (QQQ !4) ไทย `DEF-5`.";
		expect(joined(splitRefLinks(text, full))).toBe(text);
	});

	it("never lets text choose the host", () => {
		for (const [, url] of links("evil.com/x!1 https://evil.com/g/p!2 ABC-3 !4 XYZ !5")) {
			expect(url.startsWith(`${GITLAB}/`) || url.startsWith(`${JIRA}/`)).toBe(true);
		}
	});
});

describe("alias lines", () => {
	it("round-trips a map, sorted", () => {
		const text = formatAliasLines({ web: "group/web", XYZ: "group/xyz" });
		expect(text).toBe("web = group/web\nXYZ = group/xyz");
		expect(parseAliasLines(text)).toEqual({ web: "group/web", XYZ: "group/xyz" });
	});

	it("skips blank lines and trims", () => {
		expect(parseAliasLines("\n  XYZ=group/xyz  \n\n")).toEqual({ XYZ: "group/xyz" });
		expect(parseAliasLines("")).toEqual({});
	});

	it("refuses a line it cannot read, naming it", () => {
		expect(() => parseAliasLines("XYZ = group/xyz\nweb group/web")).toThrow(/line 2/);
		expect(() => parseAliasLines("= group/xyz")).toThrow(/line 1/);
	});
});
