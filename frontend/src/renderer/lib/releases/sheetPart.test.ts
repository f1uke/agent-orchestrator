import { describe, expect, it } from "vitest";
import { sheetPart } from "./sheetPart";
import type { AlertHit } from "./types";

const hit = (rule: string, subject: AlertHit["subject"], evidence: AlertHit["evidence"] = {}): AlertHit => ({
	id: `${rule}~x`,
	rule,
	subject,
	evidence,
	severity: "risk",
	bucket: "badge",
	text: "",
	links: [],
	asOf: "2026-10-14T03:00:00Z",
});
const v = { kind: "version", train: "21-2026", app: "di" } as const;

describe("sheetPart", () => {
	it("points an A6 hit at the stage row of the date it names, and a page hit nowhere", () => {
		const at = (field: "freeze" | "publish" | "page") =>
			sheetPart(hit("A6", { kind: "versionDate", train: "21-2026", app: "di", field }));
		expect(at("freeze")).toBe('[data-testid="stage-row"][data-stage="freeze"]');
		expect(at("publish")).toBe('[data-testid="stage-row"][data-stage="publish"]');
		expect(at("page")).toBeNull();
	});

	it("points an approval hit at the approvers it names, or at the Approvers section", () => {
		expect(sheetPart(hit("D3", v, { pending: ["Ann Example", "Bob Sample"] }))).toBe(
			'[data-part="approvers"] [data-name="Ann Example"], [data-part="approvers"] [data-name="Bob Sample"]',
		);
		expect(sheetPart(hit("D1", v))).toBe('[data-part="approvers"]');
		expect(sheetPart(hit("D2", v))).toBe('[data-part="jira"]');
	});

	it("points a date or version disagreement at the Jira release row", () => {
		expect(sheetPart(hit("A1", v))).toBe('[data-part="jira"]');
		expect(sheetPart(hit("A5", v))).toBe('[data-part="jira"]');
	});

	it("points a release MR hit at the release MR row", () => {
		for (const rule of ["C1", "C2", "C3", "C5", "C6", "B1"])
			expect(sheetPart(hit(rule, v)), rule).toBe('[data-part="mr"]');
	});

	it("has no part for a hit its sheet holds whole", () => {
		expect(sheetPart(hit("G2", { kind: "card", key: "DEMO-1", board: "DEMO" }))).toBeNull();
	});
});
