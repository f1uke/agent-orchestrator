import { addDays, dayLabel } from "./day";
import type {
	AlertHit,
	AppVersion,
	Board,
	BoardResponse,
	FixKind,
	MineReason,
	MinePolicy,
	RuleInfo,
	Severity,
	Stage,
	StageStatus,
	Subject,
	SubjectKind,
	Train,
} from "./types";

/** A fictional Board for the browser preview and Playwright: DEMO keys, made-up people, versions and hosts. */

const HOST = "https://gitlab.example.com/demo";
const JIRA = "https://demo.example.com";

const PEOPLE = [
	["acc-ada", "Ada Example"],
	["acc-ben", "Ben Sample"],
	["acc-cy", "Cy Placeholder"],
	["acc-dot", "Dot Fiction"],
	["acc-eli", "Eli Mock"],
	["acc-fay", "Fay Pretend"],
	["acc-gus", "Gus Invented"],
	["acc-hal", "Hal Madeup"],
	["acc-ivy", "Ivy Dummy"],
] as const;

const APPS = [
	{ id: "di", name: "Demo iOS", family: "demo", platform: "ios" },
	{ id: "da", name: "Demo Android", family: "demo", platform: "android" },
	{ id: "pi", name: "Pilot iOS", family: "pilot", platform: "ios" },
	{ id: "pa", name: "Pilot Android", family: "pilot", platform: "android" },
] as const;

function st(
	key: Stage["key"],
	date: string | undefined,
	status: StageStatus,
	detail = "",
	conventional = false,
): Stage {
	const dated = date !== undefined && (key === "scan" || key === "approve");
	return {
		key,
		date,
		status,
		detail,
		reads: STAGE_READS[key],
		...(conventional || dated ? { conventional: true } : {}),
	};
}

const STAGE_READS: Readonly<Record<Stage["key"], string[]>> = {
	freeze: ["gitlab.milestones", "gitlab.mrs"],
	scan: ["gitlab.milestones", "gitlab.mrs", "gitlab.mr_detail", "gitlab.jobs"],
	approve: ["gitlab.milestones", "jira.versions"],
	tag: ["gitlab.milestones", "gitlab.mrs", "gitlab.tags"],
	publish: ["gitlab.milestones", "jira.versions", "gitlab.tags"],
	force: ["gitlab.milestones", "jira.techreq"],
};

function approvers(n: number, ok: number) {
	return PEOPLE.slice(0, n).map(([accountId, name], i) => ({
		accountId,
		name,
		status: i < ok ? "APPROVED" : "PENDING",
	}));
}

type Spec = {
	app: string;
	v: string;
	start: string;
	due: string;
	stages: Stage[];
	mr?: { iid: number; state?: string; conflicts?: boolean; pipeline?: string; author: string };
	ok?: number;
	tag?: string;
};

function appVersion(train: string, s: Spec): AppVersion {
	return {
		train,
		app: s.app,
		version: s.v,
		milestone: {
			id: 1,
			project: 1,
			title: `v ${s.v}`,
			description: `Release Train #${train}`,
			state: "active",
			url: `${HOST}/milestones/1`,
			start: s.start,
			due: s.due,
		},
		jira: {
			id: `${train}-${s.app}`,
			name: `${s.app} ${s.v}`,
			description: "",
			released: false,
			url: `${JIRA}/versions/1`,
			approvers: approvers(9, s.ok ?? 0),
		},
		releaseMr: s.mr
			? {
					mr: {
						iid: s.mr.iid,
						project: 1,
						title: `Release ${s.v}`,
						state: s.mr.state ?? "opened",
						author: s.mr.author,
						sourceBranch: `release/${s.v}`,
						targetBranch: "main",
						createdOn: s.start,
						url: `${HOST}/merge_requests/${s.mr.iid}`,
					},
					detail: {
						iid: s.mr.iid,
						project: 1,
						conflicts: s.mr.conflicts ?? false,
						pipeline: s.mr.pipeline ? { id: 1, status: s.mr.pipeline, url: `${HOST}/pipelines/1` } : undefined,
					},
				}
			: undefined,
		tag: s.tag
			? { name: s.tag, project: 1, url: `${HOST}/tags/${s.tag}`, commitOn: s.due, newest: true, mergesAfter: [] }
			: undefined,
		stages: s.stages,
		cards: [],
		issues: [],
	};
}

function trainOf(
	id: string,
	phase: Train["phase"],
	freeze: string,
	publish: string,
	specs: Spec[],
	datesSplit: string[] = [],
): Train {
	return {
		id,
		label: `#${id.replace(/-\d{4}$/, "")}`,
		phase,
		freeze,
		publish,
		families: ["demo", "pilot"],
		versions: specs.map((s) => appVersion(id, s)),
		datesSplit,
		pages:
			phase === "past"
				? []
				: [
						{
							id: `page-${id}`,
							family: "demo",
							number: Number(id.split("-")[0]),
							title: `Demo release train #${id.split("-")[0]}`,
							url: `https://wiki.example.com/pages/${id}`,
							created: addDays(freeze, -10),
							keys: [],
							tasks: 0,
							ticked: 0,
						},
					],
	};
}

function done(f: string, p: string, tag: string): Stage[] {
	return [
		st("freeze", f, "done"),
		st("scan", addDays(f, 1), "done"),
		st("approve", addDays(f, 2), "done"),
		st("tag", addDays(p, -1), "done"),
		st("publish", p, "done"),
		st("force", undefined, "not_observable", "No force-update ticket matches this version"),
	].map((x) => (x.key === "tag" ? { ...x, detail: `Tag ${tag}` } : x));
}

function current(f: string, p: string, scan: StageStatus, approveDetail: string): Stage[] {
	return [
		st("freeze", f, "done", "Release MR opened"),
		st("scan", addDays(f, 1), scan, scan === "failed" ? "build_test failed" : "Pipeline passed"),
		st("approve", addDays(f, 2), "waiting", approveDetail),
		st("tag", addDays(p, -1), "todo", "", true),
		st("publish", p, "todo"),
		st("force", undefined, "not_observable", "No force-update ticket matches this version"),
	];
}

const RULES: [string, Severity, string][] = [
	["A1", "data", "Milestone and Jira release name different versions"],
	["A5", "data", "Jira release date differs from milestone due date"],
	["A6", "data", "Release train page dates differ from the milestone"],
	["A7", "data", "An app's milestone dates differ from the rest of its train"],
	["B1", "risk", "Freeze day passed and no release MR is open"],
	["C1", "block", "Release MR pipeline failed on a required job"],
	["C5", "block", "Release MR has conflicts"],
	["D1", "risk", "No approvers set on the Jira release"],
	["D3", "risk", "Not every approver approved before publish"],
	["E1", "hygiene", "Published but the Jira release is not marked released"],
	["F1", "info", "Two trains overlap"],
	["L9", "risk", "MR into a branch that is not a sprint branch, a long-lived branch or develop"],
];

const KINDS: readonly [string, string, number][] = [
	["confluence.pages", "train pages", 15],
	["gitlab.compare", "branch compares", 30],
	["gitlab.jobs", "pipeline jobs", 2],
	["gitlab.milestones", "milestones", 10],
	["gitlab.mr_detail", "release MR detail", 2],
	["gitlab.mrs", "merge requests", 2],
	["gitlab.tags", "tags", 10],
	["gitlab.user", "my GitLab account", 1440],
	["jira.board_sprints", "active sprints", 30],
	["jira.branch_cards", "sprint branch cards", 5],
	["jira.cards", "fixVersion cards", 5],
	["jira.llb_cards", "long-lived branch cards", 10],
	["jira.my_subtasks", "my subtasks", 5],
	["jira.myself", "my Jira account", 1440],
	["jira.page_cards", "page cards (Jira)", 5],
	["jira.relatedwork", "related work", 15],
	["jira.sprint_cards", "sprint cards", 5],
	["jira.techreq", "force-update tickets", 30],
	["jira.this_sprint", "This Sprint set", 10],
	["jira.users", "people", 1440],
	["jira.versions", "Jira releases", 5],
];

const MS = "gitlab.milestones";
const CARD_READS = [
	MS,
	"jira.versions",
	"jira.cards",
	"gitlab.mrs",
	"confluence.pages",
	"jira.page_cards",
	"jira.llb_cards",
];
const READS: Readonly<Record<string, string[]>> = {
	A1: [MS, "jira.versions"],
	A5: [MS, "jira.versions"],
	A6: [MS, "confluence.pages"],
	A7: [MS],
	B1: [MS, "gitlab.mrs"],
	C1: [MS, "gitlab.mrs", "gitlab.mr_detail", "gitlab.jobs"],
	C5: [MS, "gitlab.mrs", "gitlab.mr_detail"],
	D1: [MS, "jira.versions"],
	D3: [MS, "jira.versions", "gitlab.tags"],
	E1: [MS, "jira.versions", "gitlab.tags"],
	F1: [MS],
	G5: [...CARD_READS, "jira.branch_cards"],
	L3: CARD_READS,
	L9: ["gitlab.mrs"],
};

const MINE_POLICY: Readonly<Record<string, MinePolicy>> = {
	A1: "never",
	A2: "never",
	A3: "never",
	A5: "never",
	A6: "never",
	A7: "never",
	F1: "never",
	B1: "owner",
	C1: "owner",
	C5: "owner",
	D1: "owner",
	E1: "owner",
	F2: "owner",
	D3: "pending_approver",
	G2: "listed_cards",
	G5: "listed_cards",
	L3: "listed_cards",
	L1: "mr",
	L8: "mr",
	L9: "mr",
};

const SUBJECTS: Readonly<Record<string, SubjectKind[]>> = {
	A1: ["version"],
	A5: ["version"],
	A6: ["versionDate"],
	A7: ["version"],
	B1: ["version"],
	C1: ["version"],
	C5: ["version"],
	D1: ["version"],
	D3: ["version"],
	E1: ["version", "hotfix"],
	F1: ["trainPair"],
	G5: ["pageFamily"],
	L3: ["cardOnTrain"],
	L9: ["mr"],
};

const FIX_KINDS: Readonly<Record<string, FixKind>> = {
	A5: "choose",
	C1: "queue",
	D1: "queue",
	E1: "queue",
	G5: "copy",
	L3: "apply",
};

function withCards(b: Board): Board {
	attachCards(b);
	b.llbs = demoLlbs(b);
	b.sprints = demoSprints(b);
	markMine(b);
	return b;
}

export function demoBoard(today: string): Board {
	return markHitMine(withCards(baseBoard(today)));
}

function baseBoard(today: string): Board {
	const f20 = addDays(today, -16);
	const f21 = addDays(today, -1);
	const f22 = addDays(today, 13);
	const p20 = addDays(f20, 7);
	const p21 = addDays(f21, 7);
	const p22 = addDays(f22, 7);
	const t20 = trainOf(
		"20-2026",
		"past",
		f20,
		p20,
		APPS.map((a) => ({
			app: a.id,
			v: a.family === "demo" ? "2.3.0" : "1.0.0",
			start: f20,
			due: p20,
			stages: done(f20, p20, a.family === "demo" ? "2.3.0" : "1.0.0"),
			ok: 9,
			tag: a.family === "demo" ? "2.3.0" : "1.0.0",
		})),
	);
	const t21 = trainOf(
		"21-2026",
		"current",
		f21,
		p21,
		[
			{
				app: "di",
				v: "2.4.0",
				start: f21,
				due: p21,
				stages: current(f21, p21, "failed", "3 of 9 approved"),
				mr: { iid: 77, conflicts: true, pipeline: "failed", author: "Ada Example" },
				ok: 3,
			},
			{
				app: "da",
				v: "2.4.0",
				start: f21,
				due: p21,
				stages: current(f21, p21, "done", "0 of 9 approved"),
				mr: { iid: 41, pipeline: "success", author: "Ben Sample" },
				ok: 0,
			},
			{
				app: "pi",
				v: "1.0.1",
				start: f21,
				due: p21,
				stages: current(f21, p21, "done", "2 of 9 approved"),
				mr: { iid: 9, pipeline: "success", author: "Cy Placeholder" },
				ok: 2,
			},
			{
				app: "pa",
				v: "1.0.1",
				start: addDays(f21, 2),
				due: addDays(p21, 2),
				stages: current(addDays(f21, 2), addDays(p21, 2), "done", "0 of 9 approved"),
				mr: { iid: 12, author: "Dot Fiction" },
				ok: 0,
			},
		],
		["pa"],
	);
	const t22 = trainOf(
		"22-2026",
		"next",
		f22,
		p22,
		APPS.map((a) => ({
			app: a.id,
			v: a.family === "demo" ? "2.5.0" : "1.0.2",
			start: f22,
			due: p22,
			stages: [
				st("freeze", f22, "todo"),
				st("scan", addDays(f22, 1), "todo"),
				st("approve", addDays(f22, 2), "todo"),
				st("tag", addDays(p22, -1), "todo", "", true),
				st("publish", p22, "todo"),
			],
		})),
	);
	const hit = (
		id: string,
		rule: string,
		severity: Severity,
		text: string,
		subject: Subject,
		over: Partial<AlertHit> = {},
	): AlertHit => ({
		id,
		rule,
		subject,
		severity,
		bucket: "badge",
		text,
		evidence: {},
		links: [{ label: "Open", url: `${HOST}/x` }],
		asOf: `${today}T02:00:00Z`,
		...over,
	});
	const hits: AlertHit[] = [
		hit(
			"C5~v/21-2026/di",
			"C5",
			"block",
			"!77 has merge conflicts",
			{ kind: "version", train: "21-2026", app: "di" },
			{ app: "di", train: "21-2026" },
		),
		hit(
			"C1~v/21-2026/di",
			"C1",
			"block",
			"!77 failed: build_test",
			{ kind: "version", train: "21-2026", app: "di" },
			{ app: "di", train: "21-2026", fix: "queue" },
		),
		hit(
			"A7~v/21-2026/pa",
			"A7",
			"data",
			`Milestone ${dayLabel(addDays(f21, 2))} → ${dayLabel(addDays(p21, 2))}, the rest of #21 runs ${dayLabel(f21)} → ${dayLabel(p21)}. Give it its own train or move the milestone`,
			{ kind: "version", train: "21-2026", app: "pa" },
			{ app: "pa", train: "21-2026" },
		),
		hit(
			"A1~v/21-2026/pi",
			"A1",
			"data",
			"Milestone says 1.0.1, Jira release says 1.0.2",
			{ kind: "version", train: "21-2026", app: "pi" },
			{
				app: "pi",
				train: "21-2026",
				evidence: { kind: "version", version: "1.0.2", milestone: "v 1.0.1", jira: "pi 1.0.2" },
				links: [
					{ label: "Open Jira release", url: `${JIRA}/versions/1` },
					{ label: "Open milestone", url: `${HOST}/milestones/1` },
				],
			},
		),
		hit(
			"A6~vd/21-2026/di/freeze",
			"A6",
			"data",
			`Page says freeze ${dayLabel(addDays(f21, 1))}, milestone starts ${dayLabel(f21)}`,
			{ kind: "versionDate", train: "21-2026", app: "di", field: "freeze" },
			{
				app: "di",
				train: "21-2026",
				evidence: { page: addDays(f21, 1), milestone: f21 },
				links: [
					{ label: "Open page", url: "https://wiki.example.com/pages/21-demo" },
					{ label: "Open milestone", url: `${HOST}/milestones/1` },
				],
			},
		),
		hit(
			"D3~v/21-2026/da",
			"D3",
			"risk",
			`9 of 9 not approved: ${PEOPLE.map(([, name]) => name).join(", ")}`,
			{ kind: "version", train: "21-2026", app: "da" },
			{
				app: "da",
				train: "21-2026",
				evidence: { pending: PEOPLE.map(([, name]) => name), total: PEOPLE.length },
			},
		),
		hit(
			"F1~tt/21-2026/22-2026",
			"F1",
			"info",
			"#22 freezes before #21 publishes",
			{ kind: "trainPair", a: "21-2026", b: "22-2026" },
			{ train: "22-2026" },
		),
		hit(
			"L9~mr/da/415",
			"L9",
			"risk",
			"!415 feat/DEMO-203 → feat/DEMO-201-base: not a sprint branch, a long-lived branch or develop",
			{ kind: "mr", app: "da", iid: 415, source: "feat/DEMO-203", target: "feat/DEMO-201-base" },
			{
				app: "da",
				evidence: { mr: 415, source: "feat/DEMO-203", target: "feat/DEMO-201-base", state: "opened" },
				links: [{ label: "Open MR", url: `${HOST}/merge_requests/415` }],
			},
		),
		hit(
			"E1~v/20-2026/di",
			"E1",
			"hygiene",
			"Published but the Jira release is not marked released",
			{ kind: "version", train: "20-2026", app: "di" },
			{
				app: "di",
				train: "20-2026",
				bucket: "history",
				fix: "queue",
			},
		),
		hit(
			"D1~v/22-2026/da",
			"D1",
			"risk",
			"No approvers set on the Jira release",
			{ kind: "version", train: "22-2026", app: "da" },
			{
				app: "da",
				train: "22-2026",
				bucket: "hidden",
			},
		),
	];
	const catalogue: RuleInfo[] = RULES.map(([id, severity, title]) => ({
		id,
		group: id[0],
		severity,
		title,
		on: id !== "F1",
		defaultOn: true,
		reads: READS[id],
		subjects: SUBJECTS[id],
		minePolicy: MINE_POLICY[id],
		...(FIX_KINDS[id] ? { fix: FIX_KINDS[id] } : {}),
	}));
	const badge = hits.filter((h) => h.bucket === "badge");
	return {
		today,
		apps: APPS.map((a) => ({ ...a })),
		trains: [t20, t21, t22],
		hotfixes: [
			{
				app: "da",
				version: "2.3.1",
				milestone: {
					id: 3,
					project: 1,
					title: "Hotfix 2.3.1 login crash",
					description: "hotfix",
					state: "active",
					url: `${HOST}/milestones/3`,
					start: addDays(today, 1),
					due: addDays(today, 2),
				},
			},
		],
		sprints: [],
		llbs: [],
		mismatches: [{ kind: "version", app: "pi", train: "21-2026", version: "1.0.2" }],
		sources: [
			{ system: "gitlab", resources: 40, asOf: `${today}T02:58:00Z`, issues: [] },
			{ system: "jira", resources: 30, asOf: `${today}T02:57:00Z`, issues: [] },
			{ system: "confluence", resources: 2, asOf: `${today}T02:50:00Z`, issues: [] },
		],
		kinds: KINDS.map(([kind, label, budget]) => {
			const system = kind.startsWith("confluence.") ? "confluence" : kind.split(".")[0];
			const minute = { gitlab: 58, jira: 57, confluence: 50 }[system] ?? 50;
			return {
				kind,
				system,
				label,
				budgetSeconds: budget * 60,
				resources: 2,
				asOf: `${today}T02:${minute}:00Z`,
				issues: [],
			};
		}),
		cutover: null,
		identity: { kind: "known", jiraAccountId: "acc-ada", gitlabUsername: "ada", displayName: "Ada Example" },
		alerts: {
			badge: { count: badge.length, unknown: 0, worst: "block" },
			hits,
			rules: catalogue.map((r) => ({
				rule: r.id,
				on: r.on,
				outcome: hits.some((h) => h.rule === r.id && h.bucket === "badge")
					? {
							kind: "fired" as const,
							asOf: `${today}T02:00:00Z`,
							hits: hits.filter((h) => h.rule === r.id).map((h) => h.id),
						}
					: { kind: "clean" as const, checkedAt: `${today}T02:00:00Z` },
			})),
			staleHides: [{ id: "B3~v/19-2026/di", hiddenAt: `${today}T01:00:00Z`, reason: "" }],
		},
		catalogue,
	};
}

type Unit = Board["trains"][number]["versions"][number]["cards"][number];

const CARD_SPECS: [string, string, Unit["code"]["kind"], string, string, number][] = [
	["DEMO-101", "Sign-in with a passkey", "merged", "Ready for UAT", "indeterminate", 0],
	["DEMO-102", "Dark mode for the statement page", "merged", "Done", "done", 1],
	["DEMO-103", "Show the fee before the order", "open", "Pull Request", "indeterminate", 2],
	["DEMO-104", "Wallet top-up limits", "parked", "In Progress", "indeterminate", 3],
	["DEMO-105", "Fix the chart tooltip on small phones", "none", "To Do", "new", 4],
	["DEMO-106", "Portfolio export to CSV", "merged", "Ready for UAT", "indeterminate", 5],
];

function unitOf(
	key: string,
	summary: string,
	v: Board["trains"][number]["versions"][number],
	kind: Unit["code"]["kind"],
	status: string,
	cat: string,
	who: number,
): Unit {
	const [accountId, name] = PEOPLE[who];
	const code: Unit["code"] =
		kind === "parked"
			? { kind, branch: "feature/demo-wallet", mrs: [301] }
			: kind === "merged"
				? { kind, mrs: [200 + who] }
				: kind === "open"
					? { kind, mrs: [250 + who] }
					: kind === "elsewhere"
						? { kind, branch: "feature/demo-other", on: "2026-01-01" }
						: kind === "no_mr"
							? { kind, markedBy: "app", markedAt: "2026-01-01T03:00:00Z" }
							: { kind };
	const jira = v.jira;
	return {
		key,
		summary,
		app: v.app,
		status,
		statusCategory: cat,
		url: `${JIRA}/browse/${key}`,
		assignee: { accountId, name },
		why: ["fixVersion"],
		evidence: jira ? { fixVersion: { id: jira.id, name: jira.name, url: jira.url } } : {},
		factsFrom: "jira.cards",
		code,
	};
}

/** Membership reasons, in wire order, so the page has to put them in the fixed order itself. */
const ALSO_IN: Readonly<Record<string, Unit["why"]>> = {
	"DEMO-103": ["sprint branch", "fixVersion"],
	"DEMO-104": ["release train page"],
	"DEMO-106": ["fixVersion", "release train page", "sprint branch"],
};

function withReasons(u: Unit, t: Train): Unit {
	const why = ALSO_IN[u.key];
	if (!why) return u;
	const page = t.pages[0];
	const fv = u.evidence.fixVersion;
	return {
		...u,
		why,
		factsFrom: why.includes("fixVersion") ? "jira.cards" : "jira.page_cards",
		evidence: {
			...(why.includes("fixVersion") && fv ? { fixVersion: fv } : {}),
			...(why.includes("release train page") && page
				? { page: { id: page.id, title: page.title, url: page.url } }
				: {}),
			...(why.includes("sprint branch")
				? { sprintMR: { branch: "feature/sprint-2026-20", mrs: [400 + u.key.length] } }
				: {}),
		},
	};
}

function attachCards(b: Board): void {
	const t21 = b.trains.find((t) => t.id === "21-2026");
	const t22 = b.trains.find((t) => t.id === "22-2026");
	for (const v of t21?.versions ?? []) {
		const n = v.app === "di" ? CARD_SPECS.length : v.app === "da" ? 4 : 2;
		v.cards = CARD_SPECS.slice(0, n).map(([k, s, kind, st, cat, who]) => {
			const u = unitOf(k, s, v, v.app === "di" || kind !== "parked" ? kind : "merged", st, cat, who);
			return t21 ? withReasons(u, t21) : u;
		});
	}
	for (const v of t22?.versions ?? []) {
		if (v.app === "di" || v.app === "da")
			v.cards = [
				unitOf("DEMO-201", "Price alerts on the watchlist", v, "open", "In Progress", "indeterminate", 6),
				unitOf("DEMO-202", "New onboarding checklist", v, "none", "To Do", "new", 7),
			];
	}
}

function llbCardOf(
	key: string,
	summary: string,
	branch: string,
	iid: number,
	created: string,
	merged: string | undefined,
	who: number,
) {
	const [accountId, name] = PEOPLE[who];
	return {
		key,
		summary,
		status: merged ? "Ready for UAT" : "Pull Request",
		statusCategory: "indeterminate",
		assignee: { accountId, name },
		mr: {
			iid,
			project: 1,
			title: `${key} ${summary}`,
			state: merged ? "merged" : "opened",
			author: name,
			sourceBranch: `feat/${key}`,
			targetBranch: branch,
			createdOn: created,
			mergedOn: merged,
			url: `${HOST}/merge_requests/${iid}`,
		},
	};
}

function demoLlbs(b: Board): Board["llbs"] {
	const today = b.today;
	const f21 = b.trains.find((t) => t.id === "21-2026")?.freeze ?? today;
	const landedOn = addDays(f21, -3);
	return [
		{
			id: "demo-wallet",
			name: "Wallet",
			branches: [
				{
					app: "di",
					branch: "feature/demo-wallet",
					openedOn: addDays(today, -40),
					lastIn: addDays(today, -3),
					state: "open",
					ahead: 14,
					cards: [
						llbCardOf(
							"DEMO-104",
							"Wallet top-up limits",
							"feature/demo-wallet",
							301,
							addDays(today, -30),
							addDays(today, -28),
							3,
						),
						llbCardOf(
							"DEMO-107",
							"Wallet history screen",
							"feature/demo-wallet",
							302,
							addDays(today, -12),
							addDays(today, -10),
							4,
						),
						llbCardOf("DEMO-108", "Wallet receipts", "feature/demo-wallet", 303, addDays(today, -3), undefined, 5),
					],
				},
			],
			target: { kind: "planned", train: "22-2026" },
			plan: {
				id: "demo-wallet",
				name: "Wallet",
				targetKind: "train",
				targetTrain: "22-2026",
				note: "Waits for the payments review.",
				branches: [],
				accepted: [],
				version: 1,
				updatedAt: `${addDays(today, -2)}T03:00:00Z`,
				updatedBy: "app",
			},
		},
		{
			id: "demo-search",
			name: "demo-search",
			branches: [
				{
					app: "di",
					branch: "feature/demo-search",
					openedOn: addDays(today, -30),
					lastIn: addDays(landedOn, -2),
					state: "landed",
					train: "21-2026",
					cards: [
						llbCardOf(
							"DEMO-109",
							"Search by fund code",
							"feature/demo-search",
							304,
							addDays(today, -25),
							addDays(today, -24),
							6,
						),
					],
					landing: {
						iid: 320,
						project: 1,
						title: "Merge demo-search",
						state: "merged",
						author: "Gus Invented",
						sourceBranch: "feature/demo-search",
						targetBranch: "sprint-20-2026",
						createdOn: addDays(landedOn, -1),
						mergedOn: landedOn,
						url: `${HOST}/merge_requests/320`,
					},
				},
			],
			target: { kind: "landed", train: "21-2026", app: "di", branch: "feature/demo-search" },
		},
		{
			id: "demo-chat",
			name: "demo-chat",
			branches: [
				{
					app: "di",
					branch: "feature/demo-chat",
					openedOn: addDays(today, -20),
					lastIn: addDays(today, -5),
					state: "open",
					cards: [
						llbCardOf(
							"DEMO-110",
							"Chat with an advisor",
							"feature/demo-chat",
							305,
							addDays(today, -18),
							addDays(today, -17),
							7,
						),
					],
				},
			],
			target: { kind: "none" },
		},
		{
			id: "demo-pay",
			name: "demo-pay",
			branches: [
				{
					app: "di",
					branch: "feature/demo-pay",
					openedOn: addDays(today, -26),
					lastIn: addDays(today, -4),
					state: "open",
					cards: [
						["DEMO-120", "Pay by QR", 306, -24, 1],
						["DEMO-121", "Pay by QR receipts", 307, -18, 2],
						["DEMO-122", "Saved payees", 308, -11, 3],
						["DEMO-123", "Payment limits copy", 309, -4, 4],
					].map(([key, summary, iid, days, who], i) => ({
						...llbCardOf(
							key as string,
							summary as string,
							"feature/demo-pay",
							iid as number,
							addDays(today, days as number),
							addDays(today, (days as number) + 1),
							who as number,
						),
						...(i < 3 ? { epic: "DEMO-500" } : {}),
					})),
				},
			],
			target: { kind: "epic", train: "22-2026", epic: "DEMO-500", covered: 3, total: 4 },
		},
	];
}

type SprintCard = Board["sprints"][number]["cards"][number];
type SprintMR = SprintCard["mrs"][number];
type Route = SprintCard["routes"][number];
type Dest = Route["to"];

function sprintMr(
	app: string,
	iid: number,
	target: string,
	dest: SprintMR["dest"],
	who: number,
	merged?: string,
): SprintMR {
	return {
		app,
		iid,
		url: `${HOST}/merge_requests/${iid}`,
		title: `MR ${iid}`,
		state: merged ? "merged" : "opened",
		source: `feat/demo-${iid}`,
		target,
		author: PEOPLE[who][1],
		created: merged ?? "2026-01-01",
		...(merged ? { merged } : {}),
		dest,
	};
}

function sprintCard(
	key: string,
	summary: string,
	who: number,
	status: [string, string],
	why: SprintCard["activeWhy"],
	mrs: SprintMR[],
	routes: Route[],
	over: Partial<SprintCard> = {},
): SprintCard {
	const [accountId, name] = PEOPLE[who];
	return {
		key,
		summary,
		url: `${JIRA}/browse/${key}`,
		status: status[0],
		statusCategory: status[1],
		assignee: { accountId, name },
		subtasks: [],
		mrs,
		routes,
		onBoard: true,
		active: why.length > 0,
		activeWhy: why,
		...over,
	};
}

const destId = (d: Dest) =>
	d.kind === "train" ? `t:${d.train}` : d.kind === "llb" ? `l:${d.group}` : d.kind === "before" ? "early" : d.kind;
const DEST_RANK: Readonly<Record<Dest["kind"], number>> = { before: 0, train: 1, llb: 2, off: 3, none: 4, nomr: 5 };

function sprintRow(board: Board["sprints"][number]["board"], start: string, end: string, cards: SprintCard[]) {
	const dests = new Map<string, { to: Dest; count: number; keys: string[] }>();
	for (const c of cards.filter((x) => x.active))
		for (const to of c.routes.length ? c.routes.map((r) => r.to) : [{ kind: c.noMr ? "nomr" : "none" } as Dest]) {
			const d = dests.get(destId(to)) ?? { to, count: 0, keys: [] as string[] };
			if (!d.keys.includes(c.key)) {
				d.keys.push(c.key);
				d.count++;
			}
			dests.set(destId(to), d);
		}
	const active = cards.filter((c) => c.active);
	const isNew = active.filter((c) => c.activeWhy[0] === "new this sprint").length;
	return {
		board,
		number: 20,
		sprint: {
			board: board.id,
			id: board.id * 100 + 20,
			name: `${board.key} 20`,
			url: `${JIRA}/sprint/${board.id}`,
			start,
			end,
		},
		cards,
		dests: [...dests.values()].sort((a, b) => DEST_RANK[a.to.kind] - DEST_RANK[b.to.kind]),
		counts: { new: isNew, carriedWithMrs: active.length - isNew, carriedDone: cards.length - active.length },
		total: cards.length + 9,
	};
}

function demoSprints(b: Board): Board["sprints"] {
	const f21 = b.trains.find((t) => t.id === "21-2026")?.freeze ?? b.today;
	const start = addDays(f21, -8);
	const end = addDays(f21, 6);
	const day = (n: number) => addDays(start, n);
	const sprintBranch = "feature/sprint-2026-20";
	const t21: Dest = { kind: "train", train: "21-2026" };
	const wallet: Dest = { kind: "llb", group: "demo-wallet", branch: "feature/demo-wallet", via: "22-2026" };
	const route = (app: string, kind: Route["kind"], to: Dest, mr: number, over: Partial<Route> = {}): Route => ({
		app,
		kind,
		to,
		mr,
		...over,
	});
	const ready: [string, string] = ["Ready for UAT", "indeterminate"];
	const doing: [string, string] = ["In Progress", "indeterminate"];
	const review: [string, string] = ["Pull Request", "indeterminate"];
	const todo: [string, string] = ["To Do", "new"];
	const done: [string, string] = ["Done", "done"];
	const demo = [
		sprintCard(
			"DEMO-301",
			"Filter the fund list by risk level",
			0,
			review,
			["new this sprint"],
			[sprintMr("di", 401, sprintBranch, "sprint", 0, day(2)), sprintMr("da", 402, sprintBranch, "sprint_open", 0)],
			[route("di", "sprint", t21, 401), route("da", "sprint_open", t21, 402)],
			{
				mine: [{ why: "assignee" }],
				subtasks: [
					{
						key: "DEMO-311",
						summary: "iOS filter sheet",
						status: "Done",
						statusCategory: "done",
						mobile: true,
						assignee: { accountId: "acc-ada", name: "Ada Example" },
					},
					{
						key: "DEMO-312",
						summary: "Android filter sheet",
						status: "In Progress",
						statusCategory: "indeterminate",
						mobile: true,
						assignee: { accountId: "acc-ben", name: "Ben Sample" },
					},
				],
			},
		),
		sprintCard(
			"DEMO-302",
			"Statement as PDF",
			1,
			doing,
			["new this sprint"],
			[
				sprintMr("di", 403, "feature/demo-wallet", "llb", 1, day(3)),
				sprintMr("da", 404, sprintBranch, "sprint", 1, day(4)),
			],
			[route("di", "llb", wallet, 403), route("da", "sprint", t21, 404)],
		),
		sprintCard(
			"DEMO-303",
			"Fix the login copy",
			2,
			ready,
			["MR this sprint"],
			[sprintMr("pi", 405, "develop", "develop", 2, day(1))],
			[route("pi", "develop", { kind: "off" }, 405)],
		),
		sprintCard("DEMO-304", "Rate card for the new fees", 3, todo, ["new this sprint"], [], [], {
			subtasks: [{ key: "DEMO-314", summary: "iOS rate card", status: "To Do", statusCategory: "new", mobile: true }],
		}),
		sprintCard(
			"DEMO-305",
			"Price alert sound",
			4,
			review,
			["new this sprint"],
			[sprintMr("di", 406, sprintBranch, "sprint", 4, day(5))],
			[route("di", "sprint", t21, 406, { plan: "22-2026", planFrom: ["fixVersion"], mismatch: true })],
		),
		sprintCard(
			"DEMO-306",
			"Onboarding copy",
			5,
			ready,
			["MR this sprint"],
			[sprintMr("pa", 407, "develop", "before", 5, addDays(start, -40))],
			[route("pa", "before", { kind: "before", train: "20-2026" }, 407)],
		),
		sprintCard(
			"DEMO-307",
			"Dark mode for charts",
			6,
			done,
			[],
			[sprintMr("di", 408, "feature/sprint-2026-19", "prev", 6, addDays(start, -3))],
			[route("di", "prev", t21, 408)],
		),
		sprintCard(
			"DEMO-308",
			"Wallet receipt email",
			7,
			done,
			[],
			[sprintMr("da", 409, "feature/demo-wallet", "llb", 7, addDays(start, -5))],
			[route("da", "llb", wallet, 409)],
			{ mine: [{ why: "subtask", ref: "DEMO-318" }] },
		),
	];
	const pilot = [
		sprintCard(
			"PIL-11",
			"Chart colours for the pilot app",
			8,
			doing,
			["new this sprint"],
			[sprintMr("pi", 410, sprintBranch, "sprint", 8, day(3)), sprintMr("pa", 411, sprintBranch, "sprint_open", 0)],
			[route("pi", "sprint", t21, 410), route("pa", "sprint_open", t21, 411)],
			{ mine: [{ why: "mr_author", ref: "!411" }] },
		),
		sprintCard(
			"PIL-12",
			"Pilot settings icon",
			2,
			review,
			["MR this sprint"],
			[sprintMr("pa", 412, sprintBranch, "sprint_open", 2)],
			[route("pa", "sprint_open", t21, 412)],
			{ onBoard: false },
		),
	];
	return [
		sprintRow({ id: 1, key: "DEMO", ownsSprintBranch: true }, start, end, demo),
		sprintRow({ id: 2, key: "PILOT", ownsSprintBranch: false }, start, end, pilot),
	];
}

function markMine(b: Board): void {
	const di21 = b.trains.find((t) => t.id === "21-2026")?.versions.find((v) => v.app === "di");
	if (di21) {
		di21.mine = [{ why: "release_mr_author", ref: "!77" }];
		for (const u of di21.cards) if (u.assignee?.accountId === "acc-ada") u.mine = [{ why: "assignee" }];
	}
	for (const h of b.hotfixes) h.mine = [{ why: "mr_author", ref: "!88" }];
	const wallet = b.llbs.find((g) => g.id === "demo-wallet");
	if (wallet) wallet.mine = [{ why: "card", ref: "DEMO-108" }];
	for (const c of wallet?.branches[0]?.cards ?? [])
		if (c.key === "DEMO-108") c.mine = [{ why: "mr_author", ref: "!303" }];
}

const ME = "acc-ada";
const myName = (b: Board) => (b.identity.kind === "known" ? b.identity.displayName : "");

function myCards(b: Board): Set<string> {
	const owns = (m?: readonly MineReason[]) => m?.some((x) => x.why !== "no_mr") ?? false;
	const keys = [
		...b.trains.flatMap((t) => t.versions.flatMap((v) => v.cards.filter((u) => owns(u.mine)))),
		...b.sprints.flatMap((r) => r.cards.filter((c) => owns(c.mine))),
		...b.llbs.flatMap((g) => g.branches.flatMap((br) => br.cards.filter((c) => owns(c.mine)))),
	].map((c) => c.key);
	return new Set(keys);
}

function listedCards(h: AlertHit): string[] {
	const s = h.subject;
	if (s.kind === "pageFamily") return s.keys;
	if (s.kind === "card" || s.kind === "cardOnTrain") return [s.key];
	const cards = h.evidence.cards;
	return Array.isArray(cards) ? cards.filter((k): k is string => typeof k === "string") : [];
}

function mineAsTheBackendPolicyComputes(b: Board, h: AlertHit): MineReason[] {
	const cards = myCards(b);
	const byCard = listedCards(h)
		.filter((k) => cards.has(k))
		.map((ref) => ({ why: "card" as const, ref }));
	const s = h.subject;
	switch (MINE_POLICY[h.rule]) {
		case "owner": {
			if (s.kind === "hotfix" || s.kind === "hotfixTrain")
				return b.hotfixes.find((x) => x.app === s.app && x.version === s.version)?.mine ?? [];
			if (s.kind !== "version") return [];
			const v = b.trains.find((t) => t.id === s.train)?.versions.find((x) => x.app === s.app);
			return (v?.mine ?? []).filter((m) => m.why === "release_mr_author" || m.why === "release_ticket_assignee");
		}
		case "pending_approver": {
			if (s.kind !== "version") return [];
			const v = b.trains.find((t) => t.id === s.train)?.versions.find((x) => x.app === s.app);
			const pending = v?.jira?.approvers.some((a) => a.accountId === ME && a.status !== "APPROVED");
			return pending ? [{ why: "approver" }] : [];
		}
		case "listed_cards":
			return byCard;
		case "mr": {
			if (s.kind !== "mr") return byCard;
			const mine = b.sprints
				.flatMap((r) => r.cards.flatMap((c) => c.mrs))
				.some((m) => m.app === s.app && m.iid === s.iid && m.author === myName(b));
			return mine ? [{ why: "mr_author", ref: `!${s.iid}` }] : byCard;
		}
	}
	return [];
}

function markHitMine(b: Board): Board {
	if (b.identity.kind !== "known") return b;
	for (const h of b.alerts.hits) {
		const mine = mineAsTheBackendPolicyComputes(b, h);
		if (mine.length) h.mine = mine;
		else delete h.mine;
	}
	return b;
}

function stripMine(v: unknown): void {
	if (Array.isArray(v)) v.forEach(stripMine);
	else if (v && typeof v === "object") {
		delete (v as { mine?: unknown }).mine;
		Object.values(v).forEach(stripMine);
	}
}

export function unknownIdentityBoard(today: string): Board {
	const b = demoBoard(today);
	stripMine(b);
	b.identity = { kind: "unknown", reason: "gitlab.user has not been read yet" };
	return b;
}

export function failedSourceBoard(today: string): Board {
	const b = demoBoard(today);
	const lastRead = new Date(Date.now() - 20 * 60_000).toISOString();
	const missing = [
		{
			kind: "gitlab.mrs",
			scope: "p:1",
			app: "di",
			failures: 4,
			errorClass: "unavailable" as const,
			completeAt: lastRead,
		},
	];
	b.sources = b.sources.map((s) => (s.system === "gitlab" ? { ...s, asOf: lastRead, issues: missing } : s));
	b.alerts.rules = [...b.alerts.rules, { rule: "B1", app: "di", on: true, outcome: { kind: "unknown", missing } }];
	b.alerts.badge = { ...b.alerts.badge, unknown: 1 };
	const t21 = b.trains[1];
	t21.versions[0].stages = t21.versions[0].stages.map((s) =>
		s.key === "scan" ? { ...s, status: "unknown", detail: "", missing } : s,
	);
	return b;
}

export function staleKindBoard(today: string): Board {
	const b = demoBoard(today);
	const lastRead = new Date(Date.now() - 47 * 60_000).toISOString();
	const issue = {
		kind: "jira.versions",
		scope: "global",
		failures: 3,
		errorClass: "unavailable" as const,
		completeAt: lastRead,
	};
	b.kinds = b.kinds.map((k) => (k.kind === "jira.versions" ? { ...k, asOf: lastRead, issues: [issue] } : k));
	b.sources = b.sources.map((s) => (s.system === "jira" ? { ...s, asOf: lastRead, issues: [issue] } : s));
	return b;
}

/** One fixable hit per plan mode: E1 queue, A5 choose, G5 copy, L3 apply (blocked until demo-chat has a plan). */
export function fixesBoard(today: string): Board {
	const b = demoBoard(today);
	const t20 = b.trains[0];
	const t21 = b.trains[1];
	const da21 = t21.versions.find((v) => v.app === "da");
	if (da21?.jira && da21.milestone.due) da21.jira.releaseDate = addDays(da21.milestone.due, 2);
	t21.pages = [
		{
			id: "page-21-demo",
			family: "demo",
			title: "Release Train #21 Demo",
			url: "https://wiki.example.com/pages/21-demo",
			number: 21,
			created: t21.freeze ?? today,
			keys: ["DEMO-101", "DEMO-102"],
			tasks: 2,
			ticked: 0,
		},
	];
	const at = `${today}T02:00:00Z`;
	const fixable = (
		id: string,
		severity: Severity,
		text: string,
		subject: Subject,
		over: Partial<AlertHit>,
	): AlertHit => ({
		id,
		subject,
		rule: id.slice(0, id.indexOf("~")),
		severity,
		bucket: "badge",
		text,
		evidence: {},
		links: [{ label: "Open", url: `${HOST}/x` }],
		asOf: at,
		fix: FIX_KINDS[id.slice(0, id.indexOf("~"))],
		...over,
	});
	const added = [
		fixable(
			"A5~v/21-2026/da",
			"data",
			"Jira release date is 2 days after the milestone due date",
			{ kind: "version", train: "21-2026", app: "da" },
			{
				app: "da",
				train: "21-2026",
			},
		),
		fixable(
			"L3~ct/21-2026/di/DEMO-110",
			"risk",
			"DEMO-110 is on train 21-2026, but its code sits in feature/demo-chat (no target train)",
			{ kind: "cardOnTrain", train: "21-2026", app: "di", key: "DEMO-110" },
			{
				app: "di",
				train: "21-2026",
				evidence: { card: "DEMO-110", group: "demo-chat", branch: "feature/demo-chat" },
				links: [{ label: "Open card", url: `${JIRA}/browse/DEMO-110` }],
			},
		),
		fixable(
			"G5~pf/21-2026/demo/merged",
			"risk",
			"2 card(s) ship with #21 but are not on Release Train #21 Demo: DEMO-103, DEMO-106",
			{ kind: "pageFamily", train: "21-2026", family: "demo", bucket: "merged", keys: ["DEMO-103", "DEMO-106"] },
			{
				train: "21-2026",
				evidence: { page: "page-21-demo", cards: ["DEMO-103", "DEMO-106"], bucket: "merged" },
			},
		),
	];
	const e1 = b.alerts.hits.find((h) => h.id === "E1~v/20-2026/di");
	if (e1) e1.bucket = "badge";
	if (t20.versions[0].jira) t20.versions[0].jira.released = false;
	b.alerts.hits.push(...added);
	for (const [id, severity, title] of [
		["G5", "risk", "Card ships with a train but is not on its release train page"],
		["L3", "risk", "Card is on a train page but parked in a long-lived branch that does not target it"],
	] as const)
		b.catalogue.push({
			id,
			group: "G",
			severity,
			title,
			on: true,
			defaultOn: true,
			fix: FIX_KINDS[id],
			reads: READS[id],
			subjects: SUBJECTS[id],
			minePolicy: MINE_POLICY[id],
		});
	b.alerts.rules = b.catalogue.map((r) => {
		const ids = b.alerts.hits.filter((h) => h.rule === r.id && h.bucket === "badge").map((h) => h.id);
		return {
			rule: r.id,
			on: r.on,
			outcome: ids.length ? { kind: "fired" as const, asOf: at, hits: ids } : { kind: "clean" as const, checkedAt: at },
		};
	});
	b.alerts.badge = { ...b.alerts.badge, count: b.alerts.hits.filter((h) => h.bucket === "badge").length };
	return markHitMine(b);
}

const SUBJECT_HITS: [string, Severity, string, Subject][] = [
	[
		"A2~jv/7001",
		"data",
		"Jira release Android 2.5.0 says train #22, but no milestone has that label",
		{
			kind: "jiraVersion",
			id: "7001",
			app: "da",
			name: "Android 2.5.0",
		},
	],
	[
		"A3~m/pi/9001",
		"data",
		"Milestone Release 1.0.2 has no Jira release with the train #21 label",
		{
			kind: "milestone",
			app: "pi",
			id: 9001,
			title: "Release 1.0.2",
		},
	],
	[
		"E1~h/da/2.3.1",
		"hygiene",
		"Hotfix shipped, Jira release still unreleased",
		{
			kind: "hotfix",
			app: "da",
			version: "2.3.1",
		},
	],
	[
		"F2~ht/da/2.3.1/21-2026",
		"risk",
		"Hotfix 2.3.1 runs inside train #21",
		{
			kind: "hotfixTrain",
			app: "da",
			version: "2.3.1",
			train: "21-2026",
		},
	],
	[
		"L3~ct/21-2026/di/DEMO-109",
		"risk",
		"DEMO-109 is on train #21, but its code sits in feature/demo-search",
		{
			kind: "cardOnTrain",
			train: "21-2026",
			app: "di",
			key: "DEMO-109",
		},
	],
	[
		"G2~c/DEMO-304",
		"risk",
		"No MR yet, DEMO 20 ends soon · Jira To Do",
		{ kind: "card", key: "DEMO-304", board: "DEMO" },
	],
	[
		"G2~c/PIL-12",
		"risk",
		"No MR yet, PILOT 20 ends soon · Jira Pull Request",
		{
			kind: "card",
			key: "PIL-12",
			board: "PILOT",
		},
	],
	[
		"L1~mr/di/330",
		"block",
		"!330 feature/demo-wallet → develop is open; land it in the sprint branch instead",
		{
			kind: "mr",
			app: "di",
			iid: 330,
			source: "feature/demo-wallet",
			target: "develop",
		},
	],
	[
		"L8~mr/pi/405",
		"risk",
		"Pilot iOS !405 merged into develop; it should go to the sprint branch",
		{
			kind: "mr",
			app: "pi",
			iid: 405,
			source: "feat/DEMO-303",
			target: "develop",
		},
	],
	[
		"G5~pf/21-2026/demo/open",
		"info",
		"4 card(s) not on the #21 Demo page: DEMO-103, DEMO-104, DEMO-105, DEMO-106",
		{
			kind: "pageFamily",
			train: "21-2026",
			family: "demo",
			bucket: "open",
			keys: ["DEMO-103", "DEMO-104", "DEMO-105", "DEMO-106"],
		},
	],
];

/** One alert per subject kind, for Show. */
export function subjectsBoard(today: string): Board {
	const b = demoBoard(today);
	const at = `${today}T02:00:00Z`;
	const f1 = b.alerts.hits.find((h) => h.rule === "F1");
	if (f1) f1.bucket = "badge";
	for (const [id, severity, text, subject] of SUBJECT_HITS)
		b.alerts.hits.push({
			id,
			rule: id.slice(0, id.indexOf("~")),
			subject,
			severity,
			bucket: "badge",
			text,
			evidence: {},
			links: [{ label: "Open", url: `${HOST}/x` }],
			asOf: at,
			...("app" in subject ? { app: subject.app } : {}),
		});
	for (const r of b.catalogue) r.on = true;
	for (const v of b.trains.find((t) => t.id === "21-2026")?.versions ?? [])
		for (const u of v.cards)
			if (u.key === "DEMO-104" || u.key === "DEMO-106") u.mine = [{ why: "subtask", ref: u.key.replace("-10", "-11") }];
	b.alerts.badge = { ...b.alerts.badge, count: b.alerts.hits.filter((h) => h.bucket === "badge").length };
	return markHitMine(b);
}

export function demoResponse(board: Board): BoardResponse {
	return { config: { state: "ok", path: "~/.ao/data/release-train.json" }, board };
}
