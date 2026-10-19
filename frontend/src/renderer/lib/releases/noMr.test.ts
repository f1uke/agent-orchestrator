import { describe, expect, it } from "vitest";
import { demoBoard, subjectsBoard } from "./fixture";
import { applyNoMr, canMarkNoMr, markedCards, noMrOfCard } from "./noMr";

const mark = { markedBy: "app", markedAt: "2026-10-19T03:00:00Z" };
const row = (b: ReturnType<typeof demoBoard>, key: string) => b.sprints.find((r) => r.board.key === key)!;
const dest = (b: ReturnType<typeof demoBoard>, kind: string) =>
	row(b, "DEMO").dests.find((d) => d.to.kind === kind)?.keys ?? [];

describe("applyNoMr", () => {
	it("moves a sprint card from not routed to no code, drops its G2, and undo restores both", () => {
		const b = subjectsBoard("2026-10-19");
		expect(dest(b, "none")).toEqual(["DEMO-304"]);
		const g2 = b.alerts.hits.filter((h) => h.id === "G2~c/DEMO-304");
		expect(g2).toHaveLength(1);
		const badge = b.alerts.badge.count;

		applyNoMr(b, "DEMO-304", mark);
		expect(dest(b, "none")).toEqual([]);
		expect(dest(b, "nomr")).toEqual(["DEMO-304"]);
		expect(b.alerts.hits.some((h) => h.id === "G2~c/DEMO-304")).toBe(false);
		expect(b.alerts.badge.count).toBe(badge - 1);
		expect(noMrOfCard(b, "DEMO-304")).toEqual(mark);
		expect(row(b, "DEMO").cards.find((c) => c.key === "DEMO-304")?.mine).toEqual([{ why: "no_mr" }]);

		applyNoMr(b, "DEMO-304", null);
		expect(dest(b, "none")).toEqual(["DEMO-304"]);
		expect(dest(b, "nomr")).toEqual([]);
		expect(b.alerts.hits.filter((h) => h.id === "G2~c/DEMO-304")).toEqual(g2);
		expect(b.alerts.badge.count).toBe(badge);
		expect(noMrOfCard(b, "DEMO-304")).toBeNull();
	});

	it("puts a train card with no MR in the no-code state, and leaves one with code alone", () => {
		const b = demoBoard("2026-10-19");
		const di21 = () => b.trains.find((t) => t.id === "21-2026")!.versions.find((v) => v.app === "di")!;
		applyNoMr(b, "DEMO-105", mark);
		applyNoMr(b, "DEMO-101", mark);
		const code = (k: string) => di21().cards.find((u) => u.key === k)?.code.kind;
		expect(code("DEMO-105")).toBe("no_mr");
		expect(code("DEMO-101")).toBe("merged");
		expect(markedCards(b).map((m) => m.key)).toEqual(["DEMO-105"]);
		applyNoMr(b, "DEMO-105", null);
		expect(code("DEMO-105")).toBe("none");
	});

	it("offers the mark only on a card with no MR, or one already marked", () => {
		const b = demoBoard("2026-10-19");
		expect(canMarkNoMr(b, "DEMO-304")).toBe(true);
		expect(canMarkNoMr(b, "DEMO-105")).toBe(true);
		expect(canMarkNoMr(b, "DEMO-301")).toBe(false);
		expect(canMarkNoMr(b, "DEMO-101")).toBe(false);
	});
});
