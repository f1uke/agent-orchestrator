import { fixFailure } from "./fix";
import { targetAfterPlan } from "./plan";
import type { AlertHit, Board, Fix, FixEvent, FixPlan, FixStep } from "./types";

/** A fake of the daemon's fix routes over the fictional Board, for the browser preview and Playwright. */

const GITLAB_API = "https://gitlab.example.com/api/v4/projects/1";
const ACTOR = "app";

export type PreviewFixStore = { fixes: Fix[]; removed: Record<string, AlertHit> };

function hash(s: string): string {
	let h = 5381;
	for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
	return (h >>> 0).toString(16).padStart(8, "0");
}

function blocked(title: string, note: string, need?: FixPlan["need"]): Omit<FixPlan, "hit" | "rule" | "fingerprint"> {
	return { title, note, mode: "blocked", steps: [], ...(need ? { need } : {}) };
}

function siteOf(url: string): string {
	return new URL(url).origin;
}

function versionOfHit(b: Board, h: AlertHit) {
	const t = b.trains.find((x) => x.id === h.train);
	return { t, v: t?.versions.find((x) => x.app === h.app) };
}

type Draft = Omit<FixPlan, "hit" | "rule" | "fingerprint">;

function planE1(b: Board, h: AlertHit): Draft {
	const { v } = versionOfHit(b, h);
	const due = v?.milestone.due;
	if (!v?.jira || !due) return blocked("Mark the Jira release released", "The Jira release is no longer on the Board.");
	const j = v.jira;
	const step: FixStep = {
		label: `Jira release ${j.name}`,
		before: `unreleased, release date ${j.releaseDate ?? "-"}`,
		after: `released, release date ${due}`,
		effect: {
			kind: "jira_version_update",
			site: siteOf(j.url),
			versionId: j.id,
			before: { released: j.released, ...(j.releaseDate ? { releaseDate: j.releaseDate } : {}) },
			after: { released: true, releaseDate: due },
		},
		request: {
			method: "PUT",
			url: `${siteOf(j.url)}/rest/api/3/version/${encodeURIComponent(j.id)}`,
			body: { released: true, releaseDate: due },
		},
	};
	return { title: `Mark ${j.name} released`, mode: "queue", steps: [step] };
}

function planC1(b: Board, h: AlertHit): Draft {
	const { v } = versionOfHit(b, h);
	const mr = v?.releaseMr?.mr;
	if (!v || !mr) return blocked("Retry the failed job", "The release MR's jobs are no longer on the Board.");
	const job = { id: 9100 + mr.iid, name: "build_test" };
	const step: FixStep = {
		label: `${v.app} pipeline`,
		before: `${job.name} failed`,
		after: `${job.name} retried once`,
		effect: {
			kind: "gitlab_job_retry",
			host: "gitlab.example.com",
			app: v.app,
			project: 1,
			jobId: job.id,
			name: job.name,
		},
		request: { method: "POST", url: `${GITLAB_API}/jobs/${job.id}/retry` },
	};
	return {
		title: `Retry the failed job on !${mr.iid}`,
		mode: "queue",
		note: "Retry once only. If it fails again it is a real failure, not a flaky one.",
		steps: [step],
	};
}

function planA5(b: Board, h: AlertHit): Draft {
	const { v } = versionOfHit(b, h);
	const j = v?.jira;
	const ms = v?.milestone;
	if (!v || !j?.releaseDate || !ms?.due)
		return blocked("Make the dates match", "The Jira release or the milestone is no longer on the Board.");
	const jiraDate = j.releaseDate;
	const msDue = ms.due;
	return {
		title: `Make ${j.name}'s date match the milestone`,
		mode: "choose",
		note: "Which side is right is a team call, so neither is picked for you.",
		steps: [],
		choices: [
			{
				id: "jira",
				label: `Jira release date ← milestone due (${msDue})`,
				steps: [
					{
						label: j.name,
						before: jiraDate,
						after: msDue,
						effect: {
							kind: "jira_version_update",
							site: siteOf(j.url),
							versionId: j.id,
							before: { released: j.released, releaseDate: jiraDate },
							after: { released: j.released, releaseDate: msDue },
						},
						request: {
							method: "PUT",
							url: `${siteOf(j.url)}/rest/api/3/version/${encodeURIComponent(j.id)}`,
							body: { releaseDate: msDue },
						},
					},
				],
			},
			{
				id: "gitlab",
				label: `Milestone due ← Jira release date (${jiraDate})`,
				steps: [
					{
						label: ms.title,
						before: msDue,
						after: jiraDate,
						effect: {
							kind: "gitlab_milestone_due",
							host: "gitlab.example.com",
							app: v.app,
							project: ms.project,
							milestoneId: ms.id,
							before: msDue,
							after: jiraDate,
						},
						request: { method: "PUT", url: `${GITLAB_API}/milestones/${ms.id}`, body: { due_date: jiraDate } },
					},
				],
			},
		],
	};
}

function planG5(b: Board, h: AlertHit): Draft {
	const t = b.trains.find((x) => x.id === h.train);
	const keys = (h.evidence.cards as string[] | undefined) ?? [];
	const pg = t?.pages.find((p) => p.id === h.evidence.page);
	if (!pg) return blocked("Add the cards to the page", "The release train page is no longer on the Board.");
	const text = keys.join("\n");
	return {
		title: `Add ${keys.length} card(s) to ${pg.title}`,
		mode: "copy",
		note: "Release train pages are edited by people, so this copies the keys to paste into the page's card table.",
		steps: [
			{
				label: pg.title,
				before: "missing",
				after: keys.join(", "),
				effect: { kind: "copy_text", target: pg.url, text },
				request: { method: "COPY", url: pg.url, body: text },
			},
		],
	};
}

function planL3(b: Board, h: AlertHit): Draft {
	const groupId = h.evidence.group as string;
	const train = h.train ?? "";
	const g = b.llbs.find((x) => x.id === groupId);
	const title = `Set ${groupId} to target ${train}`;
	if (!g) return blocked(title, "The long-lived branch group is no longer on the Board.");
	const p = g.plan;
	if (!p)
		return blocked(title, `${g.name} has no plan yet; save one with its branches, then this fix retargets it.`, {
			label: "Make a plan first",
			llb: g.id,
		});
	const before = { targetKind: p.targetKind, ...(p.targetTrain ? { targetTrain: p.targetTrain } : {}) };
	const after = { targetKind: "train" as const, targetTrain: train };
	return {
		title: `Set ${p.name} to target ${train}`,
		mode: "apply",
		note: "This is AO's own plan, so it applies now. Nothing is written to GitLab, Jira or Confluence.",
		steps: [
			{
				label: `Long-lived plan ${p.id}`,
				before: p.targetTrain ? `targets ${p.targetTrain}` : p.targetKind,
				after: `targets ${train}`,
				effect: { kind: "ao_plan_target", plan: p.id, planVersion: p.version, before, after },
				request: {
					method: "PUT",
					url: `/api/v1/release-trains/llb-plans/${encodeURIComponent(p.id)}`,
					body: { ...after, ifVersion: p.version },
				},
			},
		],
	};
}

const PLANNERS: Record<string, (b: Board, h: AlertHit) => Draft> = {
	A5: planA5,
	C1: planC1,
	E1: planE1,
	G5: planG5,
	L3: planL3,
};

export function previewPlan(b: Board, hitId: string, choice?: string): FixPlan {
	const h = b.alerts.hits.find((x) => x.id === hitId);
	const planner = h && PLANNERS[h.rule];
	if (!h || !planner) throw fixFailure(404, { code: "RELEASE_HIT_NOT_FOUND", message: `no alert ${hitId}` });
	const d = planner(b, h);
	let steps = d.steps;
	if (d.mode === "choose" && choice) {
		const c = d.choices?.find((x) => x.id === choice);
		if (!c) throw fixFailure(400, { code: "FIX_NOT_RECORDABLE", message: `no choice ${choice}` });
		steps = c.steps;
	}
	const fingerprint = hash(JSON.stringify({ hit: h.id, choice, effects: steps.map((s) => s.effect) }));
	return {
		...d,
		hit: h.id,
		rule: h.rule,
		...(h.app ? { app: h.app } : {}),
		...(d.mode === "choose" && choice ? { choice } : {}),
		steps,
		fingerprint,
	};
}

function recount(b: Board): void {
	b.alerts.badge = { ...b.alerts.badge, count: b.alerts.hits.filter((h) => h.bucket === "badge").length };
}

function event(f: Fix, kind: FixEvent["kind"], at: string, detail: unknown = {}): void {
	f.events.push({ seq: f.events.length + 1, at, actor: ACTOR, kind, detail });
	f.updatedAt = at;
}

function setOpen(b: Board, f: Fix): void {
	const h = b.alerts.hits.find((x) => x.id === f.hit);
	if (!h) return;
	if (f.state === "dry_run" || f.state === "applied") h.openFix = { id: f.id, state: f.state };
	else delete h.openFix;
}

export function previewConfirm(
	b: Board,
	store: PreviewFixStore,
	req: { hitId: string; choice?: string; planFingerprint: string },
	at: string,
): Fix {
	const live = store.fixes.find(
		(f) =>
			f.hit === req.hitId && f.plan.fingerprint === req.planFingerprint && ["dry_run", "applied"].includes(f.state),
	);
	if (live) return live;
	const plan = previewPlan(b, req.hitId, req.choice);
	if (plan.fingerprint !== req.planFingerprint)
		throw fixFailure(409, {
			code: "FIX_PLAN_CHANGED",
			message: "the data behind this fix changed; review the new plan and confirm again",
			details: { plan },
		});
	if (!plan.steps.length || plan.mode === "copy" || plan.mode === "blocked")
		throw fixFailure(400, { code: "FIX_NOT_RECORDABLE", message: `a ${plan.mode} plan has nothing to record` });
	const apply = plan.steps.every((s) => s.effect.kind === "ao_plan_target");
	const f: Fix = {
		id: `fx-${hash(plan.hit + at)}${store.fixes.length}`,
		hit: plan.hit,
		rule: plan.rule,
		...(plan.choice ? { choice: plan.choice } : {}),
		plan,
		state: apply ? "applied" : "dry_run",
		createdAt: at,
		updatedAt: at,
		events: [],
	};
	const shown = plan.steps.map((s) => ({ label: s.label, effect: s.effect.kind, request: s.request }));
	if (!apply) event(f, "dry_run_recorded", at, { dryRun: true, steps: shown });
	else {
		event(f, "confirmed", at, { dryRun: false, steps: shown });
		for (const s of plan.steps) applyTarget(b, s, 1);
		const h = b.alerts.hits.find((x) => x.id === f.hit);
		if (h) store.removed[f.id] = h;
		b.alerts.hits = b.alerts.hits.filter((x) => x.id !== f.hit);
		recount(b);
		event(f, "applied", at, { undo: plan.steps.map((s) => s.effect) });
	}
	store.fixes.push(f);
	setOpen(b, f);
	return f;
}

function applyTarget(b: Board, s: FixStep, dir: 1 | -1): void {
	if (s.effect.kind !== "ao_plan_target") return;
	const e = s.effect;
	const g = b.llbs.find((x) => x.id === e.plan);
	if (!g?.plan) return;
	const to = dir === 1 ? e.after : e.before;
	g.plan = { ...g.plan, targetKind: to.targetKind, targetTrain: to.targetTrain, version: g.plan.version + 1 };
	g.target = targetAfterPlan(g, to);
}

export function previewUndo(b: Board, store: PreviewFixStore, id: string, at: string): Fix {
	const f = store.fixes.find((x) => x.id === id);
	if (!f) throw fixFailure(404, { code: "RELEASE_FIX_NOT_FOUND", message: `no fix ${id}` });
	if (f.state === "dry_run") {
		f.state = "withdrawn";
		event(f, "withdrawn", at);
	} else if (f.state === "applied") {
		for (const s of f.plan.steps) {
			if (s.effect.kind !== "ao_plan_target") continue;
			const e = s.effect;
			const p = b.llbs.find((x) => x.id === e.plan)?.plan;
			const expected = e.planVersion + 1;
			if (p?.version !== expected) {
				const message = `plan ${e.plan} is at version ${p?.version ?? 0}; reload it and try again`;
				event(f, "undo_failed", at, { error: message });
				throw fixFailure(409, { code: "PLAN_VERSION_CONFLICT", message });
			}
		}
		for (const s of f.plan.steps) applyTarget(b, s, -1);
		const h = store.removed[f.id];
		if (h) {
			b.alerts.hits.push(h);
			delete store.removed[f.id];
			recount(b);
		}
		f.state = "undone";
		event(f, "undone", at, { steps: f.plan.steps.length });
	} else throw fixFailure(409, { code: "FIX_STATE_CONFLICT", message: `this fix cannot be undone: it is ${f.state}` });
	setOpen(b, f);
	return f;
}
