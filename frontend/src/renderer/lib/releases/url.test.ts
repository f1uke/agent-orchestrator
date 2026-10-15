import { describe, expect, it } from "vitest";
import { DEFAULT_LAYERS, parseReleasesSearch, releasesSearch, type ReleasesSearch } from "./url";

const empty: ReleasesSearch = { focus: null, me: false, apps: null, hotfixes: true, sheet: null, layers: null };

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
		};
		const raw = releasesSearch(s);
		expect(raw).toEqual({
			focus: "20-1-2026",
			me: "1",
			apps: "di,pa",
			hotfixes: "0",
			sheet: "version:21-2026:di",
			layers: "sprint,cards",
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
		for (const k of ["attention", "rules", "key"] as const) {
			expect(parseReleasesSearch({ sheet: k }).sheet).toEqual({ kind: k });
		}
		for (const bad of ["version:21-2026", "train:", "nope:1", "", 7]) {
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

	it("defaults to long-lived and cards on, sprint cards and history off", () => {
		expect(DEFAULT_LAYERS).toEqual({ sprint: false, llb: true, cards: true, past: false });
	});
});
