import { describe, expect, it } from "vitest";
import {
	codeStateOf,
	llbTargetOf,
	membershipOf,
	mineWhyOf,
	noMrOf,
	planSaysText,
	releasePage,
	ruleOf,
	sourceIndex,
	sprintCardOf,
	stageOf,
	tagStale,
	tagText,
	type Provenance,
} from "./sources";
import { board, train } from "./testkit";
import type { Board, CardUnit, LLBGroup, RuleInfo, SprintCard, SprintRow, Stage } from "./types";

const NOW = Date.parse("2026-10-14T03:00:00Z");
const fresh = (kind: string, system: string, label = kind) => ({
	kind,
	system,
	label,
	budgetSeconds: 300,
	resources: 1,
	asOf: "2026-10-14T02:57:00Z",
	issues: [],
});
const stale = (kind: string, system: string) => ({
	...fresh(kind, system),
	issues: [{ kind, scope: "v:1", failures: 2, errorClass: "unavailable" as const }],
});

function kinds(over: Board["kinds"] = []): Board["kinds"] {
	const base = [
		fresh("gitlab.milestones", "gitlab", "milestones"),
		fresh("gitlab.mrs", "gitlab", "MRs"),
		fresh("gitlab.mr_detail", "gitlab", "release MR detail"),
		fresh("gitlab.tags", "gitlab", "tags"),
		fresh("jira.versions", "jira", "releases (versions)"),
		fresh("jira.cards", "jira", "cards by fixVersion"),
		fresh("jira.page_cards", "jira", "page cards (Jira)"),
		fresh("jira.sprint_cards", "jira", "sprint cards"),
		fresh("confluence.pages", "confluence", "Release train pages"),
	];
	return base.map((k) => over.find((o) => o.kind === k.kind) ?? k);
}

function index(b: Partial<Board> = {}, fetchError: string | null = null) {
	const x = board({
		kinds: kinds(),
		sources: [
			{ system: "gitlab", resources: 4, asOf: "2026-10-14T02:59:00Z", issues: [] },
			{ system: "jira", resources: 4, asOf: "2026-10-14T02:57:00Z", issues: [] },
			{ system: "confluence", resources: 1, asOf: "2026-10-14T02:50:00Z", issues: [] },
		],
		...b,
	});
	return sourceIndex(x, NOW, fetchError);
}

function unit(over: Partial<CardUnit> = {}): CardUnit {
	return {
		key: "DEMO-1",
		app: "di",
		summary: "A card",
		status: "In Progress",
		statusCategory: "indeterminate",
		url: "https://jira.example.test/browse/DEMO-1",
		code: { kind: "open", mrs: [12] },
		why: [],
		evidence: {},
		factsFrom: "jira.cards",
		...over,
	};
}

const tag = (p: Provenance, wide = false) => tagText(p, wide, index());

describe("membershipOf", () => {
	it("names only the matched sources, in the order fixVersion, page, sprint MR, whatever the wire order", () => {
		const p = membershipOf(
			unit({
				why: ["sprint branch", "fixVersion"],
				evidence: {
					sprintMR: { branch: "sprint-6", mrs: [12, 14] },
					fixVersion: { id: "10", name: "Demo iOS 2.4.0", url: "" },
				},
			}),
			"#21",
		);
		expect(tag(p)).toBe("fixVersion + sprint MR");
		expect(p.lines.map((l) => [l.kind, l.on])).toEqual([
			["jira.cards", true],
			["confluence.pages", false],
			["gitlab.mrs", true],
		]);
		expect(p.lines[0].text).toContain("Demo iOS 2.4.0");
		expect(p.lines[2].text).toBe("!12 !14 into sprint-6");
	});

	it("says no match when no source matched", () => {
		expect(tag(membershipOf(unit({ why: [] }), "#21"))).toBe("no match");
	});

	it("names the page by its title when the page matched", () => {
		const p = membershipOf(
			unit({ why: ["release train page"], evidence: { page: { id: "9", title: "Demo train #21", url: "" } } }),
			"#21",
		);
		expect(tag(p)).toBe("page");
		expect(p.lines[1].text).toBe('listed on "Demo train #21"');
	});
});

describe("ruleOf", () => {
	const rule = (id: string, reads: string[]): RuleInfo => ({
		id,
		group: id[0],
		severity: "block",
		title: "Release MR has conflicts",
		on: true,
		defaultOn: true,
		reads,
		subjects: ["version"],
		minePolicy: "owner",
	});

	it("reads rule C5 on the page and C5 · GitLab in a sheet", () => {
		const p = ruleOf(rule("C5", ["gitlab.milestones", "gitlab.mrs", "gitlab.mr_detail"]));
		expect(tag(p)).toBe("rule C5");
		expect(tag(p, true)).toBe("C5 · GitLab");
	});

	it("names every system the rule reads, in the order it reads them", () => {
		expect(tag(ruleOf(rule("A6", ["gitlab.milestones", "confluence.pages"])), true)).toBe("A6 · GitLab + Confluence");
	});
});

describe("systemOf", () => {
	it("takes the system from Board.kinds, not from the kind's prefix", () => {
		const idx = index({ kinds: [...kinds(), fresh("x.page_keys", "confluence")] });
		expect(idx.systemOf("x.page_keys")).toBe("confluence");
		expect(idx.systemOf("jira.page_cards")).toBe("jira");
		expect(idx.systemOf("confluence.pages")).toBe("confluence");
	});

	it("falls back to the prefix for a kind the catalogue does not list", () => {
		expect(index().systemOf("gitlab.compare")).toBe("gitlab");
	});

	it("labels a kind from Board.kinds", () => {
		expect(index().labelOf("jira.page_cards")).toBe("page cards (Jira)");
	});
});

describe("mineWhyOf", () => {
	it("says AO derives the why line from the rule's mine policy", () => {
		const p = mineWhyOf("D3", "pending_approver");
		expect(p.how).toBe("derived");
		expect(p.lines).toEqual([{ kind: "ao.mine_policy", text: "D3: mine while your approval is pending" }]);
		expect(index().labelOf("ao.mine_policy")).toBe("mine policy");
		expect(tag(p)).toBe("AO");
	});

	it("says what each policy reads", () => {
		expect(mineWhyOf("C1", "owner").lines[0].text).toBe(
			"C1: mine when you own it: the release MR, the release ticket or a hotfix MR",
		);
		expect(mineWhyOf("G5", "listed_cards").lines[0].text).toBe("G5: mine through the cards it lists that are yours");
		expect(mineWhyOf("L9", "mr").lines[0].text).toBe("L9: mine when you wrote the MR or own its card");
	});
});

describe("stageOf", () => {
	const s = (over: Partial<Stage>): Stage => ({ key: "scan", status: "done", detail: "", reads: [], ...over });

	it("says a conventional date is an AO convention, and the tag still names the systems read", () => {
		const p = stageOf(s({ key: "scan", conventional: true, reads: ["gitlab.milestones", "gitlab.mrs"] }));
		const conv = p.lines.find((l) => l.kind === "ao.convention");
		expect(conv?.text).toBe("freeze + 1 day");
		expect(index().labelOf("ao.convention")).toBe("AO convention");
		expect(tag(p)).toBe("GitLab");
	});

	it("adds no convention line to a stage dated by a system", () => {
		const p = stageOf(s({ key: "freeze", reads: ["gitlab.milestones", "gitlab.mrs"] }));
		expect(p.lines.map((l) => l.kind)).toEqual(["gitlab.milestones", "gitlab.mrs"]);
	});

	it("reads both systems for approval", () => {
		expect(tag(stageOf(s({ key: "approve", conventional: true, reads: ["gitlab.milestones", "jira.versions"] })))).toBe(
			"GitLab + Jira",
		);
	});
});

describe("tag text", () => {
	it("is AO when only AO settings or conventions feed the value", () => {
		expect(tag({ title: "x", how: "derived", lines: [{ kind: "ao.today" }] })).toBe("AO");
	});

	it("drops AO settings beside a system", () => {
		expect(tag({ title: "x", how: "read", lines: [{ kind: "ao.config" }, { kind: "jira.sprint_cards" }] })).toBe(
			"Jira",
		);
	});
});

describe("amber dot", () => {
	const reads = (kind: string): Provenance => ({ title: "x", how: "read", lines: [{ kind }] });

	it("marks only the tags whose own kind is stale, not every tag of that system", () => {
		const k = stale("jira.versions", "jira");
		const idx = index({
			kinds: kinds([k]),
			sources: [{ system: "jira", resources: 4, asOf: "2026-10-14T02:57:00Z", issues: k.issues }],
		});
		expect(idx.read("jira")?.unknown).toBe(true);
		expect(tagStale(reads("jira.versions"), idx)).toBe(true);
		expect(tagStale(reads("jira.cards"), idx)).toBe(false);
	});

	it("falls back to the system's freshness for a kind the catalogue does not list", () => {
		const idx = index({
			sources: [
				{
					system: "gitlab",
					resources: 2,
					asOf: "2026-10-14T02:00:00Z",
					issues: [{ kind: "gitlab.compare", scope: "c", failures: 1 }],
				},
			],
		});
		expect(tagStale(reads("gitlab.compare"), idx)).toBe(true);
		expect(tagStale(reads("gitlab.mrs"), idx)).toBe(false);
	});

	it("looks at the matched sources of a merged value only", () => {
		const idx = index({ kinds: kinds([stale("confluence.pages", "confluence")]) });
		expect(tagStale(membershipOf(unit({ why: ["fixVersion"] }), "#21"), idx)).toBe(false);
		expect(tagStale(membershipOf(unit({ why: ["release train page"] }), "#21"), idx)).toBe(true);
	});

	it("marks every system tag, but no AO one, when the Board itself could not be read", () => {
		const idx = index({}, "the daemon did not answer");
		expect(tagStale(reads("jira.cards"), idx)).toBe(true);
		expect(tagStale(reads("ao.plans"), idx)).toBe(false);
	});
});

describe("freshness for the cards", () => {
	it("gives each system's age and says unknown for one with issues", () => {
		const idx = index({
			sources: [
				{ system: "jira", resources: 4, asOf: "2026-10-14T02:57:00Z", issues: [] },
				{
					system: "gitlab",
					resources: 4,
					asOf: "2026-10-14T02:40:00Z",
					issues: [{ kind: "gitlab.mrs", scope: "p", failures: 1 }],
				},
			],
		});
		expect(idx.read("jira")).toMatchObject({ age: "3m", unknown: false });
		expect(idx.read("gitlab")).toMatchObject({ age: "20m", unknown: true });
	});
});

describe("codeStateOf", () => {
	it("lists the MRs that name the card", () => {
		const p = codeStateOf(unit({ code: { kind: "parked", branch: "feature/demo-wallet", mrs: [301] } }));
		expect(tag(p)).toBe("GitLab");
		expect(p.lines[0].text).toBe("!301 into feature/demo-wallet");
	});
});

describe("llbTargetOf", () => {
	const group = (target: LLBGroup["target"], plan?: LLBGroup["plan"]): LLBGroup => ({
		id: "g",
		name: "demo-wallet",
		branches: [],
		target,
		plan,
	});
	const label = (t: string) => `#${t.split("-")[0]}`;

	it("says epic for an epic target and names the epic and its page", () => {
		const p = llbTargetOf(group({ kind: "epic", train: "22-2026", epic: "DEMO-90", covered: 3, total: 4 }), label);
		expect(tag(p)).toBe("epic");
		expect(p.lines.find((l) => l.on)?.text).toBe("epic DEMO-90 is listed on the #22 page (3 of 4 cards)");
	});

	it("says landed and names the branch whose landing chose the train", () => {
		const p = llbTargetOf(group({ kind: "landed", train: "21-2026", app: "di", branch: "feature/demo-search" }), label);
		expect(tag(p)).toBe("landed");
		expect(p.lines[0].text).toBe("feature/demo-search landed, rides #21");
	});

	it("says plan for a planned target", () => {
		const plan = {
			id: "g",
			name: "Wallet",
			note: "",
			targetKind: "train" as const,
			targetTrain: "22-2026",
			branches: [],
			accepted: [],
			updatedAt: "2026-10-13T08:00:00Z",
			updatedBy: "ada",
			version: 2,
		};
		expect(tag(llbTargetOf(group({ kind: "planned", train: "22-2026" }, plan), label))).toBe("plan");
	});

	it("says no match with no target", () => {
		expect(tag(llbTargetOf(group({ kind: "none" }), label))).toBe("no match");
	});
});

describe("sprintCardOf", () => {
	const row = { board: { id: 1, key: "DEMO", ownsSprintBranch: true } } as SprintRow;
	const card = (onBoard: boolean): SprintCard =>
		({
			key: "DEMO-7",
			onBoard,
			mrs: [{ app: "di", iid: 41, target: "sprint-6", dest: "sprint_open" }],
		}) as SprintCard;

	it("says sprint MR for a card in a row only through its MR", () => {
		expect(tag(sprintCardOf(card(false), row))).toBe("sprint MR");
	});

	it("says the board for a card on it", () => {
		expect(tag(sprintCardOf(card(true), row))).toBe("DEMO board");
	});
});

describe("planSaysText", () => {
	it("names the source that put the card on its plan train", () => {
		expect(planSaysText(["fixVersion"])).toBe("fixVersion");
		expect(planSaysText(["release train page", "sprint branch"])).toBe("page + sprint MR");
		expect(planSaysText(undefined)).toBe("page");
	});
});

describe("No MR needed provenance", () => {
	const mark = { note: "config only", markedBy: "app", markedAt: "2026-10-14T03:00:00Z" };

	it("tags a marked card's code as AO's own mark, not GitLab, and names who marked it", () => {
		const p = codeStateOf(unit({ code: { kind: "no_mr", ...mark } }));
		expect(tag(p)).toBe("AO mark");
		expect(p.lines[0]).toEqual({ kind: "ao.no_mr", text: 'marked by you · app · 2026-10-14 · "config only"' });
		expect(p.title).toBe("Code: no code: marked No MR needed");
	});

	it("drops the note when there is none", () => {
		expect(noMrOf("DEMO-1", { markedBy: "cli:demo-7", markedAt: mark.markedAt }).lines[0].text).toBe(
			"marked by you · cli:demo-7 · 2026-10-14",
		);
	});
});

describe("releasePage", () => {
	const page = (family: string) => ({
		id: `p-${family}`,
		family,
		number: 21,
		title: `Demo release train #21 ${family}`,
		url: `https://wiki.example.com/${family}`,
		created: "2026-10-01",
		keys: [],
		tasks: 0,
		ticked: 0,
	});
	const apps = [
		{ id: "di", name: "Demo iOS", family: "demo", platform: "ios" as const },
		{ id: "pi", name: "Pilot iOS", family: "pilot", platform: "ios" as const },
	];
	const t = train("21-2026", "2026-10-20", "2026-10-27", "current", [], { pages: [page("demo")] });
	const read = board({ apps, kinds: [fresh("confluence.pages", "confluence")] });

	it("links the train page of the app's own family", () => {
		expect(releasePage(read, t, "di")).toEqual({ page: page("demo") });
	});

	it("never lends another family's page", () => {
		expect(releasePage(read, t, "pi")).toEqual({ reason: "no pilot train page for #21 yet" });
	});

	it("says not connected when no Confluence page is read at all", () => {
		const off = board({ apps, kinds: [{ ...fresh("confluence.pages", "confluence"), resources: 0 }] });
		expect(releasePage(off, { ...t, pages: [] }, "di")).toEqual({ reason: "not connected" });
	});
});
