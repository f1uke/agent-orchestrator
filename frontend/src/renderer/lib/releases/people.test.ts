import { describe, expect, it } from "vitest";
import { demoBoard, unknownIdentityBoard, viewBoardAs } from "./fixture";
import { makeLens } from "./lens";
import { pickerPeople, searchPeople, SEARCH_FROM, viewAsState } from "./people";
import { parseReleasesSearch } from "./url";

const TODAY = "2026-10-14";

describe("the person picker's list", () => {
	const people = demoBoard(TODAY).people;

	it("puts the monitor's own person first and leaves the rest in the API's order", () => {
		const { self, others } = pickerPeople(people);
		expect(self?.id).toBe("ada");
		expect(others.map((p) => p.name)).toEqual(people.filter((p) => !p.self).map((p) => p.name));
		expect(others).not.toContain(self);
	});

	it("searches a name, a GitLab username or a Jira id, ignoring case", () => {
		expect(searchPeople(people, "BEN").map((p) => p.id)).toEqual(["bsample"]);
		expect(searchPeople(people, "bsample").map((p) => p.id)).toEqual(["bsample"]);
		expect(searchPeople(people, "acc-dot").map((p) => p.id)).toEqual(["jira:acc-dot"]);
		expect(searchPeople(people, "  ")).toHaveLength(people.length);
		expect(searchPeople(people, "nobody at all")).toEqual([]);
	});

	it("is long enough in the fixture to need the search box", () => {
		expect(people.length).toBeGreaterThan(SEARCH_FROM);
	});
});

describe("what the picker shows for ?person=", () => {
	const b = demoBoard(TODAY);

	it("says Everyone with no person, and the person once the Board lists them in any id form", () => {
		expect(viewAsState(b, undefined)).toEqual({ kind: "everyone" });
		for (const id of ["bsample", "jira:acc-ben"])
			expect(viewAsState(b, id)).toMatchObject({ kind: "person", person: { name: "Ben Sample" } });
	});

	it("keeps an id the Board does not list, with the reason the daemon gave", () => {
		const other = demoBoard(TODAY);
		viewBoardAs(other, "zed");
		expect(viewAsState(other, "zed")).toEqual({ kind: "unknown", id: "zed", reason: 'no person "zed" on the Board' });
		expect(viewAsState(b, "zed")).toMatchObject({ kind: "unknown", id: "zed" });
	});
});

describe("the fixture's Board for another person", () => {
	const mineOfCards = (b: ReturnType<typeof demoBoard>) =>
		b.trains.flatMap((t) =>
			t.versions.flatMap((v) => v.cards.filter((u) => u.mine?.length).map((u) => u.assignee?.name)),
		);

	it("carries that person's Mine in place of the monitor's own, and filters to it", () => {
		const own = demoBoard(TODAY);
		const ben = demoBoard(TODAY);
		viewBoardAs(ben, "bsample");
		expect(ben.viewer).toMatchObject({ kind: "person", person: { id: "bsample" } });
		expect(mineOfCards(own)).toEqual(["Ada Example"]);
		expect(mineOfCards(ben).length).toBeGreaterThan(0);
		expect(new Set(mineOfCards(ben))).toEqual(new Set(["Ben Sample"]));
		expect(makeLens(ben, parseReleasesSearch({ person: "bsample" }), new Set()).me).toBe(true);
		expect(makeLens(own, parseReleasesSearch({ person: "bsample" }), new Set()).me).toBe(false);
	});

	it("gives the monitor's own Board back for the monitor's own person, and none for an unknown one", () => {
		const own = demoBoard(TODAY);
		const asAda = demoBoard(TODAY);
		viewBoardAs(asAda, "jira:acc-ada");
		expect(asAda).toEqual(own);
		const stranger = demoBoard(TODAY);
		viewBoardAs(stranger, "zed");
		expect(stranger.viewer.kind).toBe("unknown");
		expect(JSON.stringify(stranger)).not.toContain('"mine"');
	});

	it("keeps the monitor's own viewer unknown while its identity is, and still serves any listed person", () => {
		const own = unknownIdentityBoard(TODAY);
		viewBoardAs(own, "");
		expect(own.viewer).toEqual({ kind: "unknown", reason: "gitlab.user has not been read yet" });
		const ben = unknownIdentityBoard(TODAY);
		viewBoardAs(ben, "bsample");
		expect(ben.viewer).toMatchObject({ kind: "person", person: { id: "bsample" } });
	});
});
