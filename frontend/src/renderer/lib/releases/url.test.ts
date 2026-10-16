import { describe, expect, it } from "vitest";
import { DEFAULT_LAYERS, parseReleasesSearch, releasesSearch, type ReleasesSearch } from "./url";

const empty: ReleasesSearch = {
	focus: null,
	me: false,
	apps: null,
	hotfixes: true,
	sheet: null,
	layers: null,
	hideBoards: null,
};

describe("releases search params", () => {
	it("reads an empty query as the default view", () => {
		expect(parseReleasesSearch({})).toEqual(empty);
		expect(releasesSearch(empty)).toEqual({});
	});

	it("round-trips every field", () => {
		const s: ReleasesSearch = {
			focus: "20-1-2026",
			me: true,
			apps: new Set(["di", "pa"]),
			hotfixes: false,
			sheet: { kind: "version", train: "21-2026", app: "di" },
			layers: { sprint: true, llb: false, cards: true, past: false },
			hideBoards: new Set(["PILOT", "DEMO"]),
		};
		const raw = releasesSearch(s);
		expect(raw).toEqual({
			focus: "20-1-2026",
			me: "1",
			apps: "di,pa",
			hotfixes: "0",
			sheet: "version:21-2026:di",
			layers: "sprint,cards",
			hideBoards: "DEMO,PILOT",
		});
		expect(parseReleasesSearch(raw)).toEqual(s);
	});

	it("accepts the values the router's JSON search parser produces", () => {
		expect(parseReleasesSearch({ me: 1, hotfixes: 0, focus: 22 })).toMatchObject({
			me: true,
			hotfixes: false,
			focus: "22",
		});
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
		expect(parseReleasesSearch({ sheet: "llb:demo-wallet.2" }).sheet).toEqual({ kind: "llb", id: "demo-wallet.2" });
		for (const s of ["card:DEMO-12", "sprint:DEMO", "llb:demo-wallet.2", "llbs"]) {
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
		});
		expect(parseReleasesSearch({ layers: "" }).layers).toEqual({
			sprint: false,
			llb: false,
			cards: false,
			past: false,
		});
		expect(releasesSearch({ ...empty, layers: { sprint: false, llb: false, cards: false, past: false } }).layers).toBe(
			"",
		);
	});

	it("hides exactly the named boards, and an empty list hides none over the saved set", () => {
		expect(parseReleasesSearch({}).hideBoards).toBeNull();
		expect([...(parseReleasesSearch({ hideBoards: "PILOT,,DEMO" }).hideBoards ?? [])]).toEqual(["PILOT", "DEMO"]);
		expect(parseReleasesSearch({ hideBoards: "" }).hideBoards).toEqual(new Set());
		expect(releasesSearch({ ...empty, hideBoards: new Set() }).hideBoards).toBe("");
		expect(releasesSearch({ ...empty, hideBoards: new Set(["ZED", "ALPHA"]) }).hideBoards).toBe("ALPHA,ZED");
	});

	it("defaults to long-lived and cards on, sprint cards and history off", () => {
		expect(DEFAULT_LAYERS).toEqual({ sprint: false, llb: true, cards: true, past: false });
	});
});
