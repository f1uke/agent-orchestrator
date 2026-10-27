import { describe, expect, it } from "vitest";
import { boardFetcher, boardPath, personFetchers } from "./boardFetch";
import { demoBoard, demoResponse } from "./fixture";

type Call = { ifNoneMatch: string | null };

function server(responses: Response[]) {
	const calls: Call[] = [];
	const get = async (ifNoneMatch: string | null) => {
		calls.push({ ifNoneMatch });
		const r = responses.shift();
		if (!r) throw new Error("no more responses");
		return r;
	};
	return { calls, get };
}

const body = demoResponse(demoBoard("2026-10-14"));
const ok = (etag: string, b = body) =>
	new Response(JSON.stringify(b), { status: 200, headers: { ETag: etag, "Content-Type": "application/json" } });
const notModified = (etag: string) => new Response(null, { status: 304, headers: { ETag: etag } });

describe("boardFetcher", () => {
	it("sends no If-None-Match first, then the last ETag, and keeps the last body on 304", async () => {
		const s = server([ok('"a"'), notModified('"a"')]);
		const fetchBoard = boardFetcher(s.get);
		const first = await fetchBoard();
		const second = await fetchBoard();
		expect(s.calls).toEqual([{ ifNoneMatch: null }, { ifNoneMatch: '"a"' }]);
		expect(first.board?.today).toBe("2026-10-14");
		expect(second).toBe(first);
	});

	it("replaces the body and the ETag when the Board changed", async () => {
		const changed = demoResponse(demoBoard("2026-10-15"));
		const s = server([ok('"a"'), ok('"b"', changed), notModified('"b"')]);
		const fetchBoard = boardFetcher(s.get);
		await fetchBoard();
		const next = await fetchBoard();
		await fetchBoard();
		expect(next.board?.today).toBe("2026-10-15");
		expect(s.calls.map((c) => c.ifNoneMatch)).toEqual([null, '"a"', '"b"']);
	});

	it("asks for the whole Board again when a 304 arrives with nothing kept", async () => {
		const s = server([notModified('"a"'), ok('"a"')]);
		const out = await boardFetcher(s.get)();
		expect(out.board?.today).toBe("2026-10-14");
		expect(s.calls.map((c) => c.ifNoneMatch)).toEqual([null, null]);
	});

	it("throws the daemon's message on an error, keeping nothing", async () => {
		const s = server([new Response(JSON.stringify({ message: "store is locked", code: "X" }), { status: 500 })]);
		await expect(boardFetcher(s.get)()).rejects.toThrow("store is locked");
	});
});

describe("boardPath", () => {
	it("names no person for the monitor's own Board and percent-encodes any other id", () => {
		expect(boardPath(undefined)).toBe("/api/v1/release-trains/board");
		expect(boardPath("")).toBe("/api/v1/release-trains/board");
		expect(boardPath("ann")).toBe("/api/v1/release-trains/board?person=ann");
		expect(boardPath("jira:acc-ann")).toBe("/api/v1/release-trains/board?person=jira%3Aacc-ann");
		expect(boardPath("a&b=c d")).toBe("/api/v1/release-trains/board?person=a%26b%3Dc%20d");
	});
});

describe("personFetchers", () => {
	it("keeps one ETag per person, so a 304 never hands one person the other's Board", async () => {
		const asked: { person: string; ifNoneMatch: string | null }[] = [];
		const fetchFor = personFetchers((person) => async (ifNoneMatch) => {
			asked.push({ person, ifNoneMatch });
			return ifNoneMatch ? notModified(ifNoneMatch) : ok(`"${person || "own"}"`);
		});
		await fetchFor(undefined);
		await fetchFor("ann");
		await fetchFor(undefined);
		await fetchFor("ann");
		expect(asked).toEqual([
			{ person: "", ifNoneMatch: null },
			{ person: "ann", ifNoneMatch: null },
			{ person: "", ifNoneMatch: '"own"' },
			{ person: "ann", ifNoneMatch: '"ann"' },
		]);
	});

	it("forgets the person asked longest ago once it holds too many", async () => {
		const asked: { person: string; ifNoneMatch: string | null }[] = [];
		const fetchFor = personFetchers((person) => async (ifNoneMatch) => {
			asked.push({ person, ifNoneMatch });
			return ok(`"${person}"`);
		});
		for (const p of ["a", "b", "c", "d", "e"]) await fetchFor(p);
		asked.length = 0;
		await fetchFor("e");
		await fetchFor("a");
		expect(asked).toEqual([
			{ person: "e", ifNoneMatch: '"e"' },
			{ person: "a", ifNoneMatch: null },
		]);
	});
});
