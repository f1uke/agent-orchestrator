import { describe, expect, it } from "vitest";
import { DEFAULT_LAYERS, parseReleasesSearch, releasesSearch, type ReleasesSearch } from "./url";

const empty: ReleasesSearch = {
	focus: null,
	apps: null,
	hotfixes: true,
	sheet: null,
	layers: null,
	hideBoards: null,
	mobile: false,
};

describe("releases search params", () => {
	it("reads an empty query as the default view", () => {
		expect(parseReleasesSearch({})).toEqual(empty);
		expect(releasesSearch(empty)).toEqual({});
	});

	it("round-trips every field", () => {
		const s: ReleasesSearch = {
			focus: "20-1-2026",
			person: "jira:acc-ann",
			apps: new Set(["di", "pa"]),
			hotfixes: false,
			sheet: { kind: "version", train: "21-2026", app: "di" },
			layers: { sprint: true, llb: false, cards: true, past: false, src: false },
			hideBoards: new Set(["PILOT", "DEMO"]),
			mobile: true,
		};
		const raw = releasesSearch(s);
		expect(raw).toEqual({
			focus: "20-1-2026",
			person: "jira:acc-ann",
			apps: "di,pa",
			hotfixes: "0",
			sheet: "version:21-2026:di",
			layers: "sprint,cards",
			hideBoards: "DEMO,PILOT",
			mobile: "1",
		});
		expect(parseReleasesSearch(raw)).toEqual(s);
	});

	it("keeps Mobile only off unless asked, and writes nothing for off", () => {
		expect(parseReleasesSearch({}).mobile).toBe(false);
		expect(parseReleasesSearch({ mobile: "1" }).mobile).toBe(true);
		expect(parseReleasesSearch({ mobile: 1 }).mobile).toBe(true);
		expect(releasesSearch({ ...empty, mobile: false }).mobile).toBeUndefined();
	});

	it("accepts the values the router's JSON search parser produces", () => {
		expect(parseReleasesSearch({ person: 7, hotfixes: 0, focus: 22 })).toMatchObject({
			person: "7",
			hotfixes: false,
			focus: "22",
		});
	});

	it("names a person by the id the API gives, whatever it holds, and writes nothing for everyone", () => {
		for (const id of ["ann", "jira:acc-ann", "ann.b-c_d", "jira:557058:ab12"]) {
			expect(parseReleasesSearch({ person: id }).person).toBe(id);
			expect(releasesSearch({ ...empty, person: id })).toEqual({ person: id });
		}
		expect(parseReleasesSearch({ person: "" }).person).toBeUndefined();
		expect(releasesSearch(parseReleasesSearch({}))).toEqual({});
	});

	it("ignores the Only me flag an old link or a saved search still carries", () => {
		const parsed = parseReleasesSearch({ me: "1", apps: "di" });
		expect(parsed.person).toBeUndefined();
		expect(releasesSearch(parsed)).toEqual({ apps: "di" });
	});

	it("keeps a hit id whole, slashes, tildes and colons included", () => {
		const hit = "A1~v/21-2026/di:x";
		const s = parseReleasesSearch({ sheet: `mismatch:${hit}` });
		expect(s.sheet).toEqual({ kind: "mismatch", hit });
		expect(releasesSearch(s).sheet).toBe(`mismatch:${hit}`);
	});

	it("parses each sheet kind this page opens and drops the rest", () => {
		expect(parseReleasesSearch({ sheet: "train:21-2026" }).sheet).toEqual({ kind: "train", train: "21-2026" });
		expect(parseReleasesSearch({ sheet: "hotfix:pa:1.0.2" }).sheet).toEqual({
			kind: "hotfix",
			app: "pa",
			version: "1.0.2",
		});
		expect(parseReleasesSearch({ sheet: "card:DEMO-12" }).sheet).toEqual({ kind: "card", key: "DEMO-12" });
		expect(parseReleasesSearch({ sheet: "sprint:DEMO" }).sheet).toEqual({ kind: "sprint", board: "DEMO" });
		expect(parseReleasesSearch({ sheet: "sprint:DEMO:next" }).sheet).toEqual({
			kind: "sprint",
			board: "DEMO",
			next: true,
		});
		expect(parseReleasesSearch({ sheet: "llb:demo-wallet.2" }).sheet).toEqual({ kind: "llb", id: "demo-wallet.2" });
		for (const s of ["card:DEMO-12", "sprint:DEMO", "sprint:DEMO:next", "llb:demo-wallet.2", "llbs"]) {
			expect(releasesSearch(parseReleasesSearch({ sheet: s })).sheet).toBe(s);
		}
		for (const k of ["attention", "rules", "key", "llbs"] as const) {
			expect(parseReleasesSearch({ sheet: k }).sheet).toEqual({ kind: k });
		}
		for (const bad of ["version:21-2026", "train:", "nope:1", "", 7, "card:", "llb:", "sprint:"]) {
			expect(parseReleasesSearch({ sheet: bad }).sheet).toBeNull();
		}
	});

	it("round-trips a fix sheet with and without a choice, and the fixes list", () => {
		const hit = "A5~v/21-2026/di:x";
		for (const sheet of [
			{ kind: "fix" as const, hit },
			{ kind: "fix" as const, hit, choice: "gitlab" },
			{ kind: "fixes" as const },
			{ kind: "fixes" as const, fix: "fx-0a1b2c3d4e5f6a7b" },
		]) {
			const raw = releasesSearch({ ...empty, sheet });
			expect(parseReleasesSearch(raw).sheet).toEqual(sheet);
		}
		expect(releasesSearch({ ...empty, sheet: { kind: "fix", hit, choice: "gitlab" } }).sheet).toBe(`fix.gitlab:${hit}`);
		for (const bad of ["fix:", "fix.:A5~v/21-2026/di", "fix.jira:"]) {
			expect(parseReleasesSearch({ sheet: bad }).sheet).toBeNull();
		}
	});

	it("sets exactly the named layers, and an empty list turns every layer off", () => {
		expect(parseReleasesSearch({ layers: "past" }).layers).toEqual({
			sprint: false,
			llb: false,
			cards: false,
			past: true,
			src: false,
		});
		expect(parseReleasesSearch({ layers: "cards,src" }).layers).toEqual({
			sprint: false,
			llb: false,
			cards: true,
			past: false,
			src: true,
		});
		expect(parseReleasesSearch({ layers: "" }).layers).toEqual({
			sprint: false,
			llb: false,
			cards: false,
			past: false,
			src: false,
		});
		expect(
			releasesSearch({ ...empty, layers: { sprint: false, llb: false, cards: false, past: false, src: false } }).layers,
		).toBe("");
		expect(
			releasesSearch({ ...empty, layers: { sprint: false, llb: true, cards: true, past: false, src: true } }).layers,
		).toBe("llb,cards,src");
	});

	it("hides exactly the named boards, and an empty list hides none over the saved set", () => {
		expect(parseReleasesSearch({}).hideBoards).toBeNull();
		expect([...(parseReleasesSearch({ hideBoards: "PILOT,,DEMO" }).hideBoards ?? [])]).toEqual(["PILOT", "DEMO"]);
		expect(parseReleasesSearch({ hideBoards: "" }).hideBoards).toEqual(new Set());
		expect(releasesSearch({ ...empty, hideBoards: new Set() }).hideBoards).toBe("");
		expect(releasesSearch({ ...empty, hideBoards: new Set(["ZED", "ALPHA"]) }).hideBoards).toBe("ALPHA,ZED");
	});

	it("defaults to long-lived and cards on, sprint cards, history and sources off", () => {
		expect(DEFAULT_LAYERS).toEqual({ sprint: false, llb: true, cards: true, past: false, src: false });
	});
});
