import { describe, expect, it } from "vitest";
import { demoBoard } from "./fixture";
import { subjectLabel, subjectSheet } from "./subject";
import { board, hotfix, train, version } from "./testkit";
import type { Board, SprintCard, SprintRow } from "./types";

function sprintCard(key: string, summary: string): SprintCard {
	return {
		key,
		summary,
		active: true,
		activeWhy: ["new this sprint"],
		mrs: [],
		onBoard: true,
		routes: [],
		status: "In Progress",
		statusCategory: "indeterminate",
		subtasks: [],
		url: "",
	};
}

function sprintRow(key: string, cards: SprintCard[]): SprintRow {
	return {
		board: { id: 1, key, ownsSprintBranch: true },
		cards,
		counts: { carriedDone: 0, carriedWithMrs: 0, new: cards.length },
		dests: [],
		number: 20,
		sprint: { id: 1, board: 1, name: `${key} 20`, url: "", start: "2026-10-06", end: "2026-10-16" },
		total: cards.length,
	};
}

const b: Board = board({
	trains: [
		train("21-2026", "2026-10-09", "2026-10-16", "current", [
			version("21-2026", "di", "3.5.0", "2026-10-09", "2026-10-16", {
				cards: [
					{
						key: "DEMO-3",
						summary: "Wallet receipts",
						url: "",
						status: "In Progress",
						statusCategory: "indeterminate",
						code: { kind: "none" },
						why: [],
					} as unknown as Board["trains"][number]["versions"][number]["cards"][number],
				],
			}),
		]),
		train("22-2026", "2026-10-16", "2026-10-23", "next", []),
	],
	hotfixes: [hotfix("da", "3.4.1", "2026-10-10", "2026-10-13")],
	sprints: [sprintRow("DEMO", [sprintCard("DEMO-4", "Rate card for the new fees")])],
});

describe("subjectLabel", () => {
	it("names a version by its app, version and train", () => {
		expect(subjectLabel({ kind: "version", train: "21-2026", app: "di" }, b)).toEqual({
			app: "di",
			text: "3.5.0 · #21",
		});
	});

	it("falls back to the train label when the version is gone", () => {
		expect(subjectLabel({ kind: "version", train: "22-2026", app: "di" }, b)).toEqual({ app: "di", text: "#22" });
	});

	it("names a version date by the date it is about", () => {
		const at = (field: "freeze" | "publish" | "page") =>
			subjectLabel({ kind: "versionDate", train: "21-2026", app: "di", field }, b).text;
		expect(at("freeze")).toBe("3.5.0 · #21 · freeze date");
		expect(at("publish")).toBe("3.5.0 · #21 · publish date");
		expect(at("page")).toBe("3.5.0 · #21 · train page");
	});

	it("names a Jira release and a milestone by their own names", () => {
		expect(subjectLabel({ kind: "jiraVersion", id: "7001", app: "da", name: "Android 3.6.0" }, b)).toEqual({
			app: "da",
			text: "Jira release Android 3.6.0",
		});
		expect(subjectLabel({ kind: "milestone", app: "pi", id: 9, title: "Release 1.2.0" }, b)).toEqual({
			app: "pi",
			text: "milestone Release 1.2.0",
		});
	});

	it("names a hotfix, and a hotfix inside a train", () => {
		expect(subjectLabel({ kind: "hotfix", app: "da", version: "3.4.1" }, b)).toEqual({
			app: "da",
			text: "hotfix 3.4.1",
		});
		expect(subjectLabel({ kind: "hotfixTrain", app: "da", version: "3.4.1", train: "21-2026" }, b)).toEqual({
			app: "da",
			text: "hotfix 3.4.1 in #21",
		});
	});

	it("names both trains of a pair", () => {
		expect(subjectLabel({ kind: "trainPair", a: "21-2026", b: "22-2026" }, b)).toEqual({
			app: null,
			text: "#21 and #22",
		});
	});

	it("names a card by its key and summary, looked up on the train or the sprint row", () => {
		expect(subjectLabel({ kind: "cardOnTrain", train: "21-2026", app: "di", key: "DEMO-3" }, b)).toEqual({
			app: "di",
			text: "DEMO-3 Wallet receipts · #21",
		});
		expect(subjectLabel({ kind: "card", key: "DEMO-4", board: "DEMO" }, b)).toEqual({
			app: null,
			text: "DEMO-4 Rate card for the new fees",
		});
	});

	it("falls back to the key when no list carries the card", () => {
		expect(subjectLabel({ kind: "card", key: "DEMO-9", board: "DEMO" }, b).text).toBe("DEMO-9");
	});

	it("names an MR by its number and both branches", () => {
		expect(
			subjectLabel({ kind: "mr", app: "da", iid: 415, source: "feat/DEMO-203", target: "feat/DEMO-201-base" }, b),
		).toEqual({ app: "da", text: "!415 feat/DEMO-203 → feat/DEMO-201-base" });
	});

	it("names a page family's missing cards by the train, the family and whether their code merged", () => {
		const s = { kind: "pageFamily" as const, train: "21-2026", family: "demo", keys: ["DEMO-1", "DEMO-2"] };
		expect(subjectLabel({ ...s, bucket: "merged" }, b).text).toBe("#21 demo page · 2 merged cards missing");
		expect(subjectLabel({ ...s, bucket: "open" }, b).text).toBe("#21 demo page · 2 cards with an open MR missing");
	});

	it("tells apart alerts whose rule text is the same, by the card each is about", () => {
		const twin = board({
			sprints: [
				sprintRow(
					"DEMO",
					["DEMO-4", "DEMO-5", "DEMO-6"].map((k) => sprintCard(k, `Card ${k}`)),
				),
			],
		});
		const text = "No MR yet, DEMO 20 ends Fri 16 Oct (2 working days left) · Jira In Progress";
		const lines = ["DEMO-4", "DEMO-5", "DEMO-6"].map(
			(key) => `${subjectLabel({ kind: "card", key, board: "DEMO" }, twin).text} ${text}`,
		);
		expect(new Set(lines).size).toBe(3);
	});

	it("gives every alert on the demo board a line of its own", () => {
		const d = demoBoard("2026-10-14");
		const lines = d.alerts.hits.map((h) => {
			const l = subjectLabel(h.subject, d);
			return `${l.app ?? ""} ${l.text} ${h.text}`;
		});
		expect(new Set(lines).size).toBe(lines.length);
	});
});

describe("subjectSheet", () => {
	it("opens the sheet that holds the subject", () => {
		expect(subjectSheet({ kind: "version", train: "21-2026", app: "di" }, "C1~v/21-2026/di")).toEqual({
			kind: "version",
			train: "21-2026",
			app: "di",
		});
		expect(subjectSheet({ kind: "hotfix", app: "da", version: "3.4.1" }, "E1~h/da/3.4.1")).toEqual({
			kind: "hotfix",
			app: "da",
			version: "3.4.1",
		});
		expect(subjectSheet({ kind: "card", key: "DEMO-4", board: "DEMO" }, "G2~c/DEMO-4")).toEqual({
			kind: "card",
			key: "DEMO-4",
		});
		expect(subjectSheet({ kind: "trainPair", a: "21-2026", b: "22-2026" }, "F1~tt/21-2026/22-2026")).toEqual({
			kind: "train",
			train: "22-2026",
		});
	});

	it("opens the mismatch sheet for a Jira release or milestone that joined nothing", () => {
		expect(subjectSheet({ kind: "jiraVersion", id: "7", app: "da", name: "x" }, "A2~jv/7")).toEqual({
			kind: "mismatch",
			hit: "A2~jv/7",
		});
		expect(subjectSheet({ kind: "milestone", app: "pi", id: 9, title: "x" }, "A3~m/pi/9")).toEqual({
			kind: "mismatch",
			hit: "A3~m/pi/9",
		});
	});
});
