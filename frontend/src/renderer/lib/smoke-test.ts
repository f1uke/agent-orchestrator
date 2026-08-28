// Pure helpers + palette for the Tests-tab "Smoke test" checklist. The tab is
// pixel-matched to a hand-authored design (Tests.dc.html), but each value below
// resolves to a CSS custom property (defined per theme in `styles.css`) rather
// than a raw hex — otherwise the tab stays dark when the app is in light mode.
// Surfaces, borders and the text ramp come from the --inbox-* tokens the sibling
// Comments tab uses (the two tabs share one design language); only the
// checklist-specific roles carry --smoke-* tokens. The dark values behind these
// tokens are still the verbatim handoff hexes; see the block in `styles.css`.

import type { components } from "../../api/schema";
import { ACCENT, MONO, accentMix, tint } from "./comment-inbox";

/** Re-exported so the Tests-tab components keep a single import site. The accent
 * is the app-wide token — this tab used to carry its own `#3b82f6` copy. */
export { ACCENT, MONO, accentMix };

export type SmokeCheck = components["schemas"]["SmokeCheck"];
export type SmokeStandDown = components["schemas"]["DomainSmokeStandDown"];
export type SmokeEvidence = components["schemas"]["SmokeEvidence"];
export type SmokeRun = components["schemas"]["DomainSmokeRun"];
export type SmokeVerdict = "pending" | "pass" | "fail" | "skip";

/** Design palette, by role. Each value is a themed token, not a literal. */
export const PALETTE = {
	rail: "var(--inbox-rail)",
	cardBg: "var(--smoke-card-bg)",
	cardBgOpen: "var(--inbox-card-bg)",
	pillBg: "var(--inbox-pill-bg)",
	whyBg: "var(--smoke-why-bg)",
	trackBg: "var(--smoke-track-bg)",
	reportBg: "var(--inbox-batch-bg)",
	// borders
	divider: "var(--inbox-divider)",
	borderCard: "var(--inbox-border-card)",
	borderCardOpen: "var(--smoke-card-border-open)",
	borderPill: "var(--inbox-border-pill)",
	borderExpand: "var(--smoke-divider-expand)",
	borderReport: "var(--inbox-border-batch)",
	// text
	textStrong: "var(--inbox-text-strong)",
	text: "var(--inbox-text)",
	body: "var(--inbox-body)",
	secondary: "var(--inbox-secondary)",
	secondary2: "var(--inbox-secondary-2)",
	muted: "var(--inbox-muted)",
	muted2: "var(--inbox-muted-2)",
	/** Monospace PR/file-ref chips — same ramp as the viewer's path chrome. */
	refChip: "var(--viewer-chrome-fg)",
	/** Load-error copy, shared with the Failed verdict hue. */
	danger: "var(--smoke-fail-fg)",
	/** "· by you · 2h ago" — sits on the verdict pill's tint, so not `muted`. */
	caption: "var(--smoke-caption)",
	/** Accent label on an accent-tinted fill (Post to Jira), so not `ACCENT`. */
	accentText: "var(--smoke-accent-text)",
	// progress segments
	segPass: "var(--smoke-pass)",
	segFail: "var(--smoke-fail)",
	segSkip: "var(--smoke-skip)",
	// expected box
	expectedBg: tint("var(--smoke-pass)", 6),
	expectedBorder: tint("var(--smoke-pass)", 28),
	expectedBody: "var(--smoke-expected-body)",
	evidenceOn: "var(--smoke-evidence-on)",
	/** The machine's lane: a flat, neutral block. Deliberately not accent-tinted
	 * (that is the app's own voice) and never a verdict hue. */
	qaBg: "var(--smoke-qa-bg)",
	qaBorder: "var(--smoke-qa-border)",
	qaFg: "var(--smoke-qa-fg)",
	// Pass/Fail decision buttons (softer than the verdict pills' fills).
	passBtnBg: tint("var(--smoke-pass)", 12),
	failBtnBg: tint("var(--smoke-fail)", 12),
} as const;

export type VerdictMeta = {
	label: string;
	color: string;
	icon: string;
	pillBg: string;
	pillBorder: string;
};

/** Authoritative per-verdict colors/labels/icons (Tests.dc.html §2 table). */
export const VERDICT_META: Record<SmokeVerdict, VerdictMeta> = {
	pass: {
		label: "Passed",
		color: "var(--smoke-pass-fg)",
		icon: "✓",
		pillBg: tint("var(--smoke-pass)", 14),
		pillBorder: tint("var(--smoke-pass)", 40),
	},
	fail: {
		label: "Failed",
		color: "var(--smoke-fail-fg)",
		icon: "✗",
		pillBg: tint("var(--smoke-fail)", 14),
		pillBorder: tint("var(--smoke-fail)", 45),
	},
	pending: {
		label: "To check",
		color: "var(--inbox-secondary)",
		icon: "○",
		pillBg: "var(--inbox-pill-bg)",
		pillBorder: "var(--inbox-border-menu)",
	},
	skip: {
		label: "Skipped",
		color: "var(--inbox-secondary)",
		icon: "⊘",
		pillBg: "var(--inbox-pill-bg)",
		pillBorder: "var(--inbox-border-menu)",
	},
};

export function verdictMeta(v: string): VerdictMeta {
	return VERDICT_META[(v as SmokeVerdict) in VERDICT_META ? (v as SmokeVerdict) : "pending"];
}

export type SmokeProgress = {
	/** Active cases only - a retired case is no longer one the user is asked to play. */
	total: number;
	pass: number;
	fail: number;
	skip: number;
	pending: number;
	checked: number;
	/** How many cases were retired out of the checklist (excluded from `total`). */
	retired: number;
	/**
	 * The MACHINE's verdicts, counted ONLY over cases the user has not decided.
	 * They are deliberately not folded into pass/fail: a machine answers "did the
	 * steps run", a person answers "does this work for a person", and a card must
	 * never read confirmed with nobody having touched it. Restricting the count
	 * to undecided cases is what keeps a stale machine failure from going on
	 * blocking after the person has judged the case themselves.
	 */
	agentPass: number;
	agentFail: number;
	/**
	 * Cases the machine RAN but deliberately did not judge (`agentRanAt` set,
	 * `agentVerdict` empty). Counted separately from a verdict because it is not
	 * a weaker verdict - it is captured evidence waiting for a person's eye.
	 */
	agentCaptured: number;
	/**
	 * Cases qa DECLARED it could not drive, each with the reason it gave. A
	 * machine skip answers nothing about the app - it is a fact about the
	 * machine's reach - so it never touches the person's counts, and it is the
	 * one thing that tells "cannot be driven" apart from "nobody looked".
	 */
	agentSkip: number;
	/**
	 * Cases still open for the person that carry NOTHING from any machine.
	 *
	 * This is the number that used to be unreadable. An untouched case and an
	 * undriveable one rendered identically, so a run that ended with cases
	 * neglected looked exactly like one that ended with cases nothing could
	 * reach. Now the second state has to be said out loud (agentSkip), and
	 * whatever is left over is this.
	 */
	agentNotDriven: number;
};

/** Counts for the progress bar + counts row. */
export function progressFor(checks: SmokeCheck[]): SmokeProgress {
	const p: SmokeProgress = {
		total: 0,
		pass: 0,
		fail: 0,
		skip: 0,
		pending: 0,
		checked: 0,
		retired: 0,
		agentPass: 0,
		agentFail: 0,
		agentCaptured: 0,
		agentSkip: 0,
		agentNotDriven: 0,
	};
	for (const c of checks) {
		if (c.retiredAt) {
			p.retired += 1;
			continue;
		}
		p.total += 1;
		switch (c.verdict) {
			case "pass":
				p.pass += 1;
				break;
			case "fail":
				p.fail += 1;
				break;
			case "skip":
				p.skip += 1;
				break;
			default:
				p.pending += 1;
				switch (agentState(c)) {
					case "pass":
						p.agentPass += 1;
						break;
					case "fail":
						p.agentFail += 1;
						break;
					case "captured":
						p.agentCaptured += 1;
						break;
					case "skip":
						p.agentSkip += 1;
						break;
					case "none":
						p.agentNotDriven += 1;
						break;
				}
		}
	}
	p.checked = p.total - p.pending;
	return p;
}

/** Ordered progress-bar segments (pass, fail, skip); pending shows as the track. */
export function progressSegments(p: SmokeProgress): { color: string; count: number }[] {
	return [
		{ color: PALETTE.segPass, count: p.pass },
		{ color: PALETTE.segFail, count: p.fail },
		{ color: PALETTE.segSkip, count: p.skip },
	];
}

/** "CHECK N" tag derived from a case's 1-based seq. */
export function checkTag(seq: number): string {
	return `CHECK ${seq}`;
}

/**
 * Compact relative time ("just now", "5m ago", "2h ago", "3d ago") for the
 * "by you · <when>" verdict caption. NOTE: approximate, like the Comments tab.
 */
export function relativeTime(iso: string | null | undefined, now: number): string {
	if (!iso) return "";
	const t = Date.parse(iso);
	if (Number.isNaN(t)) return "";
	const s = Math.max(0, Math.floor((now - t) / 1000));
	if (s < 60) return "just now";
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m ago`;
	const h = Math.floor(m / 60);
	if (h < 24) return `${h}h ago`;
	const d = Math.floor(h / 24);
	if (d < 30) return `${d}d ago`;
	const mo = Math.floor(d / 30);
	if (mo < 12) return `${mo}mo ago`;
	return `${Math.floor(mo / 12)}y ago`;
}

/** Whether a MIME type is a video we accept as evidence. */
export function isVideoMime(mime: string): boolean {
	return mime.startsWith("video/");
}

// ---------------------------------------------------------------------------
// The machine's lane.
//
// A case carries TWO results that are never merged: the human's
// (verdict/note/evidence) and the machine's (agentVerdict/agentNote/
// agentEvidence/agentRanAt/agentSha). They answer different questions - the
// machine's is "did the steps execute", the human's is "does this work for a
// person" - and every regression a person has caught by hand (recording
// latency, dead drag-scroll, keystrokes never arriving, a tab pausing when
// unfocused, control lost after a lease lapse) lives in the gap between them.
//
// So the screen keeps them in separate lanes, and the machine's lane is
// deliberately MONOCHROME: the pass/fail hues on this tab belong to the human's
// verdict alone, and a machine result must never render as a completed case.
// The one hue the machine may borrow is the fail red, because an agent failure
// only ever makes the picture stricter.

/**
 * What the machine did to a case.
 *
 * `captured` is the state this vocabulary exists for: `agentRanAt` set with an
 * EMPTY `agentVerdict` is deliberate, not missing data. qa drove the case and
 * photographed it, and concluded that what it captured does not settle the
 * question the case asks - so the call is a person's. Rendering that as an empty
 * circle would read "qa hasn't got to it yet" and send them to the wrong thing.
 */
export type AgentState = "none" | "pass" | "fail" | "skip" | "captured";

export function agentState(check: SmokeCheck): AgentState {
	const v = check.agentVerdict ?? "";
	if (v === "pass" || v === "fail" || v === "skip") return v;
	return check.agentRanAt ? "captured" : "none";
}

export type AgentMeta = {
	/** Chip text. Always prefixed `qa ·` so the actor is named, never inferred. */
	label: string;
	/**
	 * The one word the expanded case leads with, so what qa concluded is readable
	 * without reading a paragraph. It carries the meaning ALONE: the machine's
	 * lane is monochrome by design (see the note above), so prominence here comes
	 * from size, weight and the glyph beside it - never from a colour that would
	 * both break that rule and make a machine result look like a played case.
	 */
	stamp: string;
	/** What the machine did, in its own row of the expanded case. */
	headline: string;
	/** What that result does and does NOT mean: the sentence that keeps a
	 * machine pass from being read as a played case. */
	caption: string;
	color: string;
};

/** The machine's ink. Neutral by design; see the note above. */
const QA_FG = "var(--smoke-qa-fg)";

export const AGENT_META: Record<Exclude<AgentState, "none">, AgentMeta> = {
	pass: {
		label: "qa · ran",
		stamp: "PASS",
		headline: "qa ran the steps and they passed",
		caption: "That is not a verdict on how it behaves. The call is yours - agree with it, or play the case.",
		color: QA_FG,
	},
	fail: {
		label: "qa · failed",
		stamp: "FAIL",
		headline: "qa ran the steps and hit a failure",
		caption: "Worth reading before you play it, but the call is still yours.",
		color: "var(--smoke-fail-fg)",
	},
	skip: {
		label: "qa · skipped",
		stamp: "SKIP",
		headline: "qa could not run this one",
		caption: "Nothing was exercised, so there is nothing here to lean on.",
		color: QA_FG,
	},
	captured: {
		label: "qa · evidence only",
		stamp: "NO VERDICT",
		headline: "qa captured the screen and left the judgement to you",
		caption:
			"It recorded what it saw and said the capture does not settle this one, so the call is yours. Judge it from what qa captured instead of driving the app yourself.",
		color: QA_FG,
	},
};

export function agentMeta(state: AgentState): AgentMeta | null {
	return state === "none" ? null : AGENT_META[state];
}

/**
 * The machine run the user may confirm in ONE click, or null when there is
 * nothing to confirm. This is the whole "agree" rule in one place, and every
 * refusal in it is deliberate:
 *
 * - **Already decided** - nothing to offer; the case shows their verdict and a
 *   Change button instead.
 * - **`skip`** - qa's skip means "I could not run this one, nothing was
 *   exercised"; the user's skip means "this check does not apply". Different
 *   claims, so there is nothing to agree WITH, and a button here would put words
 *   in their mouth. The service refuses it too, so this is not the only guard.
 * - **Evidence-only** - qa ran it and deliberately did not judge. There is no
 *   verdict to agree with, and an agree button could only ever mean "pass" -
 *   exactly the disguised pass this state exists to avoid.
 * - **An open run** - a round that never concluded is not a result.
 *
 * It resolves to the LATEST RECORDED run, which is the one the block's stamp
 * shows. Since a case can have failed at one commit and passed at another,
 * "agree with qa" would be ambiguous the moment two runs disagree; agreeing
 * always means this run, and the id is stored so the record says which.
 * Earlier runs get no button of their own: confirming a superseded round is a
 * judgement, not a confirmation, and belongs in the user's own Pass/Fail.
 */
export function agreeableRun(check: SmokeCheck): SmokeRun | null {
	if (check.verdict !== "pending") return null;
	const run = latestRun(check);
	if (!run) return null;
	const verdict = run.verdict ?? "";
	return verdict === "pass" || verdict === "fail" ? run : null;
}

/** The run a decided case's verdict was reached by agreeing with, if any. */
export function agreedRun(check: SmokeCheck): SmokeRun | null {
	const id = check.agreedRunId ?? "";
	if (!id) return null;
	return (check.runs ?? []).find((r) => r.id === id) ?? null;
}

/** Head-of-branch facts the staleness rule needs, structurally typed so the
 * Tests tab does not have to import the whole PR summary shape. */
export type HeadRef = { number: number; headSha: string };

/** First 7 of a sha, the length every git surface in the app already shows. */
export function shortSha(sha: string | null | undefined): string {
	return (sha ?? "").slice(0, 7);
}

/**
 * Same commit? Prefix-tolerant in both directions: `ao smoke record --sha` may
 * be handed an abbreviation, and calling an abbreviated match "stale" would cry
 * wolf. Below 7 characters nothing is comparable, so nothing matches.
 */
export function shaMatches(a: string | null | undefined, b: string | null | undefined): boolean {
	const x = (a ?? "").trim().toLowerCase();
	const y = (b ?? "").trim().toLowerCase();
	if (x.length < 7 || y.length < 7) return false;
	return x.startsWith(y) || y.startsWith(x);
}

/** The head commit this case's machine result should be compared against: the
 * PR the case names, else the most actionable one (the list arrives sorted). */
export function headShaFor(check: SmokeCheck, heads: HeadRef[]): string {
	const named = check.prNum > 0 ? heads.find((h) => h.number === check.prNum) : undefined;
	return (named ?? heads[0])?.headSha ?? "";
}

/**
 * Stale = the machine ran against a commit that is no longer head.
 *
 * Silence is not staleness: with no machine run, no recorded sha, or no head to
 * compare to, the answer is "cannot tell" and the case renders normally. Only a
 * head we know AND a recorded sha that differs earns the mark.
 */
export function isAgentStale(check: SmokeCheck, heads: HeadRef[]): boolean {
	if (agentState(check) === "none") return false;
	const ran = check.agentSha ?? "";
	const head = headShaFor(check, heads);
	if (!ran || !head) return false;
	return !shaMatches(ran, head);
}

// ---------------------------------------------------------------------------
// The machine's run history.
//
// A case's machine result used to be four fields that the next `ao smoke record`
// overwrote, so a re-run destroyed the round before it and its screenshots were
// left pooled under whatever verdict was newest. The runs below are what that
// became: one entry per round, each owning the captures it took.

/**
 * A case's machine rounds, NEWEST FIRST - the order the tab reads them in. The
 * API sends them chronologically because that is how they happened; the screen
 * puts the current result on top because that is the one being asked about.
 */
export function runsNewestFirst(check: SmokeCheck): SmokeRun[] {
	return [...(check.runs ?? [])].reverse();
}

/**
 * The run whose result the case currently carries: the latest RECORDED one.
 *
 * A run with no `recordedAt` is skipped deliberately. It is a round the machine
 * opened, captured into and never concluded - what a crashed or abandoned run
 * leaves behind - and showing it as the headline result would put an empty
 * conclusion where a real one used to be.
 */
export function latestRun(check: SmokeCheck): SmokeRun | null {
	return runsNewestFirst(check).find((r) => Boolean(r.recordedAt)) ?? null;
}

/** The machine artifacts captured during one run. */
export function evidenceForRun(check: SmokeCheck, runId: string): SmokeEvidence[] {
	return (check.agentEvidence ?? []).filter((ev) => (ev.runId ?? "") === runId);
}

/**
 * Machine captures that belong to NO run: taken before AO kept run history, when
 * the result they were taken for could be overwritten out of existence - and
 * often was. They are shown apart and labelled, never folded into the newest
 * run: the verdict they were captured for may be the opposite of the one showing
 * now, and a stale image read as current evidence is the failure this whole
 * shape exists to remove.
 */
export function unknownRunEvidence(check: SmokeCheck): SmokeEvidence[] {
	return evidenceForRun(check, "");
}

/** "RUN 3" tag from a run's 1-based seq, mirroring `checkTag`. */
export function runTag(seq: number): string {
	return `RUN ${seq}`;
}

/**
 * What one run concluded, in the same vocabulary as the case-level lane so the
 * history reads with the headline rather than beside it. A run with no verdict
 * that HAS concluded is `captured`; one still open is `open`.
 */
export type RunState = AgentState | "open";

/**
 * What ONE round concluded, in past tense and without the `qa ·` actor prefix -
 * the chip's wording ("ran") is a sentence opener that reads as "it executed"
 * when it stands alone in a history row, exactly where the verdict is the thing
 * being read.
 */
export const RUN_LABEL: Record<RunState, string> = {
	none: "",
	pass: "passed",
	fail: "failed",
	skip: "skipped",
	captured: "evidence only",
	open: "never concluded",
};

export function runState(run: SmokeRun): RunState {
	if (!run.recordedAt) return "open";
	const v = run.verdict ?? "";
	if (v === "pass" || v === "fail" || v === "skip") return v;
	return "captured";
}

/** The cases the user is asked to play: retired ones are out of the checklist. */
export function activeChecks(checks: SmokeCheck[]): SmokeCheck[] {
	return checks.filter((c) => !c.retiredAt);
}

/** Retired cases, newest retirement first - the audit trail of a shrinking
 * checklist ("3 retired, now covered by tests"), never a silent vanishing. */
export function retiredChecks(checks: SmokeCheck[]): SmokeCheck[] {
	return checks
		.filter((c) => Boolean(c.retiredAt))
		.sort((a, b) => Date.parse(b.retiredAt ?? "") - Date.parse(a.retiredAt ?? ""));
}

// ---------------------------------------------------------------------------
// Who wrote a case.
//
// The checklist is authored by BOTH crew members - dev knows what the change
// actually touched, qa sees it the way a user will - so which of them a case
// came from is part of reading the list, not decoration. The human asked for it
// in those words.
//
// It is deliberately low-emphasis, and it sits nowhere near the machine's lane:
// a "qa" author chip beside a "qa · ran" machine chip would read as one fact
// about qa when they are two different ones.

/** "written by dev" / "written by qa" / "written by @mer-2", or "" when AO could
 * not identify the author. An unattributed case shows no author rather than a
 * guessed one. */
export function authorLabel(check: SmokeCheck): string {
	const role = (check.authoredByRole ?? "").trim();
	if (role) return `written by ${role}`;
	const by = (check.authoredBy ?? "").trim();
	return by ? `written by @${by}` : "";
}

// ---------------------------------------------------------------------------
// Empty, and stood down.
//
// An empty checklist used to say two opposite things at once - nobody has
// decided what a person should look at, or somebody decided and there is nothing
// worth your eyes - and the tab rendered both as the same grey panel. A member
// can now record the second (`ao smoke stand-down`), so the two states are told
// apart here rather than left to the reader.

export type ChecklistState = "loading" | "cases" | "stood-down" | "all-retired" | "undecided";

export function checklistState(checks: SmokeCheck[], standDown: SmokeStandDown | null | undefined): ChecklistState {
	if (activeChecks(checks).length > 0) return "cases";
	if (standDown) return "stood-down";
	return checks.length > 0 ? "all-retired" : "undecided";
}

/** "qa stood down" / "dev stood down" / "the worker stood down" - the actor is
 * named because "somebody decided this needs no human" is only useful if you can
 * see who decided it. */
export function standDownActor(standDown: SmokeStandDown): string {
	const role = (standDown.byRole ?? "").trim();
	if (role) return role;
	const by = (standDown.by ?? "").trim();
	return by ? `@${by}` : "the worker";
}
