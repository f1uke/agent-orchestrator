import { describe, expect, it } from "vitest";
import {
	findExternalRefLinks,
	githubRepoBaseFromUrl,
	gitlabProjectBaseFromUrl,
	resolveScmRemotes,
	type ScmRemotes,
} from "./terminal-scm-links";

const GH = "https://github.com/acme-inc/ao-demo";
const GL = "https://gitlab.example.com/team/group/webapp";
const BOTH: ScmRemotes = { githubRepoBase: GH, gitlabProjectBase: GL };

describe("githubRepoBaseFromUrl", () => {
	it("derives the repo base from a /pull/ URL", () => {
		expect(githubRepoBaseFromUrl(`${GH}/pull/63`)).toBe(GH);
	});

	it("derives the repo base from an /issues/ URL", () => {
		expect(githubRepoBaseFromUrl(`${GH}/issues/12`)).toBe(GH);
	});

	it("keeps the host for GitHub Enterprise (host never hardcoded)", () => {
		expect(githubRepoBaseFromUrl("https://ghe.corp.net/org/repo/pull/9")).toBe("https://ghe.corp.net/org/repo");
	});

	it("returns undefined for a GitLab MR URL", () => {
		expect(githubRepoBaseFromUrl(`${GL}/-/merge_requests/2`)).toBeUndefined();
	});

	it("returns undefined for a non-PR/issue URL or garbage", () => {
		expect(githubRepoBaseFromUrl(`${GH}/actions/runs/1`)).toBeUndefined();
		expect(githubRepoBaseFromUrl("not a url")).toBeUndefined();
	});
});

describe("gitlabProjectBaseFromUrl", () => {
	it("derives the project base from a nested-group MR URL", () => {
		expect(gitlabProjectBaseFromUrl(`${GL}/-/merge_requests/2961`)).toBe(GL);
	});

	it("keeps the self-hosted host (never hardcoded)", () => {
		expect(gitlabProjectBaseFromUrl("https://git.internal.io/g/p/-/merge_requests/7")).toBe(
			"https://git.internal.io/g/p",
		);
	});

	it("returns undefined for a GitHub URL or garbage", () => {
		expect(gitlabProjectBaseFromUrl(`${GH}/pull/63`)).toBeUndefined();
		expect(gitlabProjectBaseFromUrl("::::")).toBeUndefined();
	});
});

describe("resolveScmRemotes", () => {
	it("collects the first GitHub base and first GitLab base from mixed URLs", () => {
		const remotes = resolveScmRemotes([`${GH}/pull/1`, `${GL}/-/merge_requests/9`, `${GH}/pull/2`]);
		expect(remotes).toEqual({ githubRepoBase: GH, gitlabProjectBase: GL });
	});

	it("yields only the observed provider (gating source)", () => {
		expect(resolveScmRemotes([`${GH}/pull/1`])).toEqual({ githubRepoBase: GH });
		expect(resolveScmRemotes([`${GL}/-/merge_requests/1`])).toEqual({ gitlabProjectBase: GL });
	});

	it("yields nothing when no PR/MR URLs are observed", () => {
		expect(resolveScmRemotes([])).toEqual({});
		expect(resolveScmRemotes(["https://github.com/acme-inc/ao-demo/actions/runs/1"])).toEqual({});
	});
});

describe("findExternalRefLinks — resolution", () => {
	it("links #<num> to the GitHub pull URL", () => {
		const line = "opened #63 for review";
		const matches = findExternalRefLinks(line, BOTH);
		expect(matches).toHaveLength(1);
		expect(matches[0].url).toBe(`${GH}/pull/63`);
		expect(line.slice(matches[0].startIndex, matches[0].endIndex)).toBe("#63");
	});

	it("links !<num> to the GitLab merge-request URL", () => {
		const line = "see !2961 please";
		const matches = findExternalRefLinks(line, BOTH);
		expect(matches).toHaveLength(1);
		expect(matches[0].url).toBe(`${GL}/-/merge_requests/2961`);
		expect(line.slice(matches[0].startIndex, matches[0].endIndex)).toBe("!2961");
	});

	it("links both sigils on one line, ordered by position", () => {
		const matches = findExternalRefLinks("landed #7 and !8 today", BOTH);
		expect(matches.map((m) => m.url)).toEqual([`${GH}/pull/7`, `${GL}/-/merge_requests/8`]);
	});

	it("links a token at start of line and one in parentheses/quotes", () => {
		expect(findExternalRefLinks("#5 is merged", BOTH)[0].url).toBe(`${GH}/pull/5`);
		expect(findExternalRefLinks("(#5)", BOTH)[0].url).toBe(`${GH}/pull/5`);
		expect(findExternalRefLinks('"!5"', BOTH)[0].url).toBe(`${GL}/-/merge_requests/5`);
	});
});

describe("findExternalRefLinks — provider gating", () => {
	it("does not link #<num> when no GitHub remote is known", () => {
		expect(findExternalRefLinks("opened #63", { gitlabProjectBase: GL })).toHaveLength(0);
	});

	it("does not link !<num> when no GitLab project is known", () => {
		expect(findExternalRefLinks("see !2961", { githubRepoBase: GH })).toHaveLength(0);
	});

	it("links nothing when no remote is known", () => {
		expect(findExternalRefLinks("#63 and !2961", {})).toHaveLength(0);
	});
});

describe("findExternalRefLinks — no false positives", () => {
	it("ignores hex colors (letters make the token non-numeric)", () => {
		expect(findExternalRefLinks("color: #3b82f6; bg #0a0a0c;", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("border #1a2b3c", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("#fff shorthand", BOTH)).toHaveLength(0);
	});

	it("ignores a shebang / #! and markdown headings", () => {
		expect(findExternalRefLinks("#!/bin/sh", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("##123 heading", BOTH)).toHaveLength(0);
	});

	it("ignores a # glued to a preceding word or path (owner/repo#1, URL anchor)", () => {
		expect(findExternalRefLinks("acme/repo#1", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("https://x.io/page#123", BOTH)).toHaveLength(0);
	});

	it("ignores common ! usages that are not MR refs", () => {
		expect(findExternalRefLinks("if a != b then", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("use !important here", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("run [ ! -f x ]", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("foo!5 glued", BOTH)).toHaveLength(0);
	});

	it("does not extend a token past a trailing word char or hyphen", () => {
		expect(findExternalRefLinks("#12abc", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("#12-foo", BOTH)).toHaveLength(0);
		expect(findExternalRefLinks("!12x", BOTH)).toHaveLength(0);
	});
});
