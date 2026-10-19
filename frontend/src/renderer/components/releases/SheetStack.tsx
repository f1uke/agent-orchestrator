import { ArrowLeft, ChevronRight, X } from "lucide-react";
import { AnimatePresence, m } from "motion/react";
import { useEffect, type ReactNode } from "react";
import { useClearStaleHides, useSetRule } from "../../hooks/useReleaseBoard";
import { FIX_STATE_META } from "../../lib/releases/fix";
import { dayLabel } from "../../lib/releases/day";
import { hotfixStages } from "../../lib/releases/layout";
import {
	approvals,
	agoText,
	badgeLabel,
	nowStage,
	owner,
	trainApprovers,
	unknownText,
	versionOf,
	type Lens,
	type Owner,
} from "../../lib/releases/lens";
import { derivedOf, readOf, stageOf, type Provenance } from "../../lib/releases/sources";
import type { AppVersion, Board, FixState, RuleOutcome, Stage } from "../../lib/releases/types";
import type { ReleasesSearch, SheetEntry } from "../../lib/releases/url";
import {
	RULE_GROUPS,
	SEVERITY_META,
	SEVERITY_ORDER,
	STAGE_META,
	STATUS_TEXT,
	stageTone,
} from "../../lib/releases/vocab";
import { useReleasesUi } from "../../stores/releases-ui";
import { Switch } from "../ui/switch";
import { appMarkOf } from "../../lib/releases/appMark";
import { AlertList, AlertRow, Ext, HitLinks, HitWho, Sec } from "./Alerts";
import { AppMark } from "./AppMark";
import { Avatar } from "./Avatar";
import { cardSheet, sprintSheet } from "./CardSheets";
import { fixesSheet, fixSheet } from "./FixSheets";
import { llbSheet, llbsSheet } from "./LlbSheet";
import { useReleaseMotion } from "./ReleaseMotion";
import type { PageActions } from "./ReleasesPage";
import { Stepper } from "./StickyAppLabel";
import { phaseTone } from "./SummaryStrip";
import { StageMark } from "./VersionLanes";
import { SourceTag } from "./Sources";

type Ctx = { board: Board; lens: Lens; actions: PageActions; now: number };
type Sheet = { title: ReactNode; body: ReactNode; tabs?: ReactNode };

export function SheetStack({
	board,
	url,
	lens,
	actions,
}: {
	board: Board;
	url: ReleasesSearch;
	lens: Lens;
	actions: PageActions;
}) {
	const s = url.sheet;
	useEffect(() => {
		if (!s) return;
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && actions.closeSheet();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [s, actions]);
	const ctx: Ctx = { board, lens, actions, now: Date.now() };
	const sheet = s ? render(s, ctx) : null;
	const canBack = useReleasesUi((st) => st.back.length > 0);
	const { sheet: mo } = useReleaseMotion();
	return (
		<AnimatePresence>
			{sheet && (
				<m.aside
					key="sheet"
					className="rel-sheet"
					role="dialog"
					aria-modal="false"
					aria-label="Release details"
					initial={mo.initial}
					animate={mo.animate}
					exit={{ ...mo.exit, transition: mo.exitTransition ?? mo.transition }}
					transition={mo.transition}
				>
					<div className="sh-h">
						{canBack && (
							<button type="button" className="x" aria-label="Back" onClick={actions.back}>
								<ArrowLeft className="size-4" />
							</button>
						)}
						<h2>{sheet.title}</h2>
						<button type="button" className="x" aria-label="Close" onClick={actions.closeSheet}>
							<X className="size-4" />
						</button>
					</div>
					<div className="sh-rest">
						{sheet.tabs}
						<div className="sh-b">
							{sheet.body}
							<div className="rel-halos" aria-hidden="true" />
						</div>
					</div>
				</m.aside>
			)}
		</AnimatePresence>
	);
}

function render(s: SheetEntry, c: Ctx): Sheet | null {
	switch (s.kind) {
		case "train":
			return trainSheet(s.train, c);
		case "version":
			return versionSheet(s.train, s.app, c);
		case "hotfix":
			return hotfixSheet(s.app, s.version, c);
		case "mismatch":
			return mismatchSheet(s.hit, c);
		case "attention":
		case "rules":
			return panelSheet(s.kind, c);
		case "fixes":
			return { ...fixesSheet(s.fix, c), tabs: panelTabs("fixes", c) };
		case "fix":
			return fixSheet(s.hit, s.choice, c);
		case "key":
			return keySheet(c);
		case "llb":
			return llbSheet(s.id, c.board, c.actions);
		case "llbs":
			return llbsSheet(c.board, c.actions, () => true);
		case "card":
			return cardSheet(s.key, c);
		case "sprint":
			return sprintSheet(s.board, c);
	}
}

function StageRows({ stages, now }: { stages: Stage[]; now: number }) {
	return (
		<>
			{stages.map((st) => (
				<div key={st.key} className="srow" data-testid="stage-row" data-stage={st.key}>
					<StageMark stage={st} className="static" now={now} />
					<span>
						{STAGE_META[st.key].label}
						<SourceTag p={stageOf(st)} wide />
					</span>
					<span className="muted">{st.date ? dayLabel(st.date) : "-"}</span>
					<span className="d">
						<span className="c" data-st={stageTone(st.status)}>
							{st.status === "unknown" ? unknownText(st.missing, now) : STATUS_TEXT[st.status]}
						</span>
						{st.detail ? ` · ${st.detail}` : ""}
					</span>
				</div>
			))}
		</>
	);
}

const TRAIN_FREEZE_P = derivedOf(
	"Freeze",
	[{ kind: "gitlab.milestones", text: "earliest milestone start over the train's apps" }],
	"The page's freeze cell only feeds rule A6.",
	"Train.freeze",
);
const TRAIN_PUBLISH_P = derivedOf(
	"Publish",
	[{ kind: "gitlab.milestones", text: "latest milestone due over the train's apps" }],
	"The page's publish cell only feeds rule A6.",
	"Train.publish",
);
const TRAIN_PAGE_P = readOf(
	"Train page",
	"confluence.pages",
	"newest page per family, created 0-60 days before freeze",
	"Train.pages",
);
const VERSION_MILESTONE_P = readOf(
	"Milestone",
	"gitlab.milestones",
	"milestone with the train label in its description",
	"Version.milestone",
);
const RELEASE_MR_P: Provenance = {
	title: "Release MR",
	how: "read",
	lines: [
		{ kind: "gitlab.mrs", text: "oldest MR from release/* into main in this milestone" },
		{ kind: "gitlab.mr_detail", text: "conflicts, head pipeline" },
	],
	field: "releaseMr",
};
const JIRA_RELEASE_P = readOf(
	"Jira release",
	"jira.versions",
	"version with the train label and this version's name",
	"Version.jira",
);
const HOTFIX_MILESTONE_P = readOf(
	"Milestone",
	"gitlab.milestones",
	"milestone without a train label",
	"Hotfix.milestone",
);

function ownerOf(own: Owner, v: AppVersion): Provenance {
	return own.from === "release MR author"
		? readOf("Owner", "gitlab.mrs", `release MR !${v.releaseMr?.mr.iid} author`, "releaseMr.mr.author")
		: readOf("Owner", "jira.cards", "release ticket assignee", "ticket.assignee");
}

const APPROVERS_P: Provenance = {
	title: "Approvers",
	how: "read",
	lines: [
		{ kind: "jira.versions", text: "release approvers and status" },
		{ kind: "jira.users", text: "names and faces" },
	],
	field: "Version.jira.approvers",
};

function trainSheet(id: string, c: Ctx): Sheet {
	const t = c.board.trains.find((x) => x.id === id);
	if (!t) return { title: `Train ${id}`, body: <div className="sec empty">This train has no milestone yet.</div> };
	const people = trainApprovers(
		t,
		c.board.apps.map((a) => a.id),
	);
	const hits = c.lens.attention.filter((h) => h.train === t.id);
	return {
		title: (
			<>
				Train {t.label}
				<span className="badge" data-st={phaseTone(t)}>
					{t.phase}
				</span>
			</>
		),
		body: (
			<>
				<div className="sec kv">
					<span>Freeze</span>
					<span>
						{t.freeze ? dayLabel(t.freeze) : "-"}
						<SourceTag p={TRAIN_FREEZE_P} wide />
					</span>
					<span>Publish</span>
					<span>
						{t.publish ? dayLabel(t.publish) : "-"}
						<SourceTag p={TRAIN_PUBLISH_P} wide />
					</span>
					<span>Train page</span>
					<span>
						{t.pages.length ? (
							t.pages.map((p) => (
								<div key={p.id}>
									<Ext url={p.url}>{p.title}</Ext>
								</div>
							))
						) : (
							<span className="passive">none</span>
						)}
						<SourceTag p={TRAIN_PAGE_P} wide />
					</span>
				</div>
				{t.id !== c.lens.focus?.id && (
					<div className="sec">
						<button
							type="button"
							className="rel-chip"
							aria-pressed="true"
							onClick={() => c.actions.setSearch({ focus: t.id })}
						>
							Focus the page on {t.label}
						</button>
					</div>
				)}
				<Sec title="Apps">
					{c.board.apps.map((a) => {
						const v = versionOf(t, a.id);
						if (!v) return null;
						const now = nowStage(v.stages);
						const ap = approvals(v);
						return (
							<button
								key={a.id}
								type="button"
								className="tapp"
								onClick={() => c.actions.openSheet({ kind: "version", train: t.id, app: a.id }, "push")}
							>
								<b>
									<AppMark mark={appMarkOf(c.board.apps, a.id)} /> <span className="mono muted">{v.version}</span>
								</b>
								<span className="muted text-[12px]">
									approvals {ap.ok}/{ap.total}
								</span>
								<span className="s2">
									<Stepper stages={v.stages} big />
									<span className="c" data-st={now ? stageTone(now.status) : "done"}>
										{now ? `${STAGE_META[now.key].short} · ${STATUS_TEXT[now.status]}` : "shipped"}
									</span>
								</span>
							</button>
						);
					})}
				</Sec>
				<Sec title="Approvers" source={APPROVERS_P}>
					{people.length ? (
						people.map((p) => (
							<div key={p.accountId} className="prow" data-testid="approver-row">
								<Avatar accountId={p.accountId} name={p.name} />
								{p.name}
								<span className="st">
									{p.apps.map((x) => (
										<span
											key={x.app}
											className="spill"
											data-st={x.approved ? "done" : "waiting"}
											data-testid="approver-chip"
										>
											{x.approved ? "✓" : "◷"} <AppMark mark={appMarkOf(c.board.apps, x.app)} size="sm" />
										</span>
									))}
								</span>
							</div>
						))
					) : (
						<div className="empty">No approvers set on any Jira release of {t.label}.</div>
					)}
				</Sec>
				<AlertList title="Needs attention" hits={hits} c={c} />
			</>
		),
	};
}

function versionSheet(trainId: string, app: string, c: Ctx): Sheet {
	const t = c.board.trains.find((x) => x.id === trainId);
	const v = t && versionOf(t, app);
	if (!t || !v)
		return {
			title: <AppMark mark={appMarkOf(c.board.apps, app)} />,
			body: <div className="sec empty">This version is no longer on the Board.</div>,
		};
	const own = owner(v);
	const mr = v.releaseMr;
	const ap = approvals(v);
	const hits = c.lens.attention.filter((h) => h.train === t.id && h.app === app);
	return {
		title: (
			<>
				<AppMark mark={appMarkOf(c.board.apps, app)} /> <span className="mono">{v.version}</span>
				<span className="badge" data-st="accent">
					{t.label}
				</span>
			</>
		),
		body: (
			<>
				<div className="sec kv">
					<span>Owner</span>
					<span>
						{own ? `${own.name} (${own.from})` : "-"}
						<SourceTag p={own && ownerOf(own, v)} wide />
					</span>
					<span>Milestone</span>
					<span>
						<Ext url={v.milestone.url}>{v.milestone.title}</Ext>
						{t.datesSplit.includes(app) && <span className="c" data-st="failed">{` · not ${t.label}'s dates`}</span>}
						<SourceTag p={VERSION_MILESTONE_P} wide />
					</span>
					<span>Release MR</span>
					<span data-part="mr">
						{mr ? (
							<>
								<Ext url={mr.mr.url}>!{mr.mr.iid}</Ext> · {mr.mr.state}
								{mr.detail?.pipeline && (
									<>
										{" "}
										· pipeline{" "}
										<span
											className="c"
											data-st={
												mr.detail.pipeline.status === "success"
													? "done"
													: mr.detail.pipeline.status === "failed"
														? "failed"
														: "running"
											}
										>
											{mr.detail.pipeline.status}
										</span>
									</>
								)}
								{mr.detail?.conflicts && (
									<span className="c" data-st="failed">
										{" "}
										· conflicts
									</span>
								)}
							</>
						) : (
							<span className="passive">not open</span>
						)}
						<SourceTag p={RELEASE_MR_P} wide />
					</span>
					<span>Jira release</span>
					<span data-part="jira">
						{v.jira ? <Ext url={v.jira.url}>{v.jira.name + (v.jira.released ? " · released" : "")}</Ext> : "-"}
						<SourceTag p={JIRA_RELEASE_P} wide />
					</span>
					<span>Tag</span>
					<span data-part="tag">
						{v.tag ? <Ext url={v.tag.url}>{v.tag.name}</Ext> : "-"}
						<SourceTag p={readOf("Tag", "gitlab.tags", `tag named ${v.version}`, "Version.tag")} wide />
					</span>
				</div>
				<AlertList title="Needs attention" hits={hits} c={c} />
				<Sec title="Stages">
					<StageRows stages={v.stages} now={c.now} />
				</Sec>
				<Sec
					title="Approvers"
					part="approvers"
					source={APPROVERS_P}
					extra={
						ap.total > 0 && (
							<span className="badge" data-st={ap.ok === ap.total ? "done" : "waiting"}>
								{ap.ok}/{ap.total}
							</span>
						)
					}
				>
					{!v.jira ? (
						<div className="empty">No Jira release found.</div>
					) : v.jira.approvers.length === 0 ? (
						<div className="empty">No approvers set on {v.jira.name} yet.</div>
					) : (
						[...v.jira.approvers]
							.sort((a, b) => Number(a.status === "APPROVED") - Number(b.status === "APPROVED"))
							.map((p) => (
								<div key={p.accountId} className="prow" data-name={p.name}>
									<Avatar accountId={p.accountId} name={p.name} />
									{p.name}
									<span className="st c" data-st={p.status === "APPROVED" ? "done" : "waiting"}>
										{p.status === "APPROVED" ? "✓ approved" : "◷ pending"}
									</span>
								</div>
							))
					)}
				</Sec>
			</>
		),
	};
}

function hotfixSheet(app: string, version: string, c: Ctx): Sheet {
	const h = c.board.hotfixes.find((x) => x.app === app && x.version === version);
	if (!h)
		return {
			title: `Hotfix ${version}`,
			body: <div className="sec empty">This hotfix is no longer on the Board.</div>,
		};
	return {
		title: (
			<>
				<AppMark mark={appMarkOf(c.board.apps, app)} /> <span className="mono">{version}</span>
				<span className="badge" data-st="hotfix">
					hotfix
				</span>
			</>
		),
		body: (
			<>
				<div className="sec kv">
					<span>Milestone</span>
					<span>
						<Ext url={h.milestone.url}>{h.milestone.title}</Ext>
						<SourceTag p={HOTFIX_MILESTONE_P} wide />
					</span>
					<span>Dates</span>
					<span>
						{h.milestone.start ? dayLabel(h.milestone.start) : "-"} →{" "}
						{h.milestone.due ? dayLabel(h.milestone.due) : "-"}
						<SourceTag p={readOf("Dates", "gitlab.milestones", "start and due dates", "Hotfix.milestone")} wide />
					</span>
					<span>Jira release</span>
					<span>
						{h.jira ? <Ext url={h.jira.url}>{h.jira.name}</Ext> : "-"}
						<SourceTag p={readOf("Jira release", "jira.versions", "version by name", "Hotfix.jira")} wide />
					</span>
					<span>Tag</span>
					<span>
						{h.tag ? <Ext url={h.tag.url}>{h.tag.name}</Ext> : <span className="passive">no tag yet</span>}
						<SourceTag p={readOf("Tag", "gitlab.tags", "tag named after the version", "Hotfix.tag")} wide />
					</span>
				</div>
				<Sec title="Stages">
					<StageRows stages={hotfixStages(h, c.board.today)} now={c.now} />
				</Sec>
			</>
		),
	};
}

function mismatchSheet(hitId: string, c: Ctx): Sheet {
	const h = c.board.alerts.hits.find((x) => x.id === hitId);
	const kind = h?.evidence.kind;
	const m =
		h && c.board.mismatches.find((x) => x.app === h.app && (x.train ?? null) === (h.train ?? null) && x.kind === kind);
	return {
		title: "Data mismatch",
		body: h ? (
			<div className="sec kv">
				<span>App</span>
				<span>
					<AppMark mark={appMarkOf(c.board.apps, h.app)} />
				</span>
				<span>Train</span>
				<span>{h.train ? (c.board.trains.find((t) => t.id === h.train)?.label ?? h.train) : "none"}</span>
				<span>Problem</span>
				<span>{h.text}</span>
				{m?.milestone && (
					<>
						<span>Milestone</span>
						<span>
							<Ext url={m.milestone.url}>{m.milestone.title}</Ext>
						</span>
					</>
				)}
				{m?.jira && (
					<>
						<span>Jira release</span>
						<span>
							<Ext url={m.jira.url}>{m.jira.name}</Ext>
						</span>
					</>
				)}
				{h.links.some((l) => l.url) && (
					<>
						<span>Open</span>
						<span className="links">
							<HitLinks h={h} />
						</span>
					</>
				)}
			</div>
		) : (
			<div className="sec empty">This mismatch no longer shows.</div>
		),
	};
}

function Fold({ label, children, testid }: { label: string; children: ReactNode; testid: string }) {
	const open = useReleasesUi((s) => s.open.has(`sheet:${testid}`));
	const toggle = useReleasesUi((s) => s.toggleFold);
	return (
		<div data-testid={testid}>
			<button type="button" className="fold" aria-expanded={open} onClick={() => toggle(`sheet:${testid}`)}>
				<ChevronRight className="size-3" style={{ rotate: open ? "90deg" : "0deg" }} aria-hidden="true" />
				{label}
			</button>
			{open && children}
		</div>
	);
}

function UnknownCells({ rules, c }: { rules: RuleOutcome[]; c: Ctx }) {
	if (!rules.length) return null;
	return (
		<Sec
			title="Unknown"
			extra={
				<span className="badge" data-st="unknown">
					{rules.length}
				</span>
			}
		>
			{rules.map((r) => (
				<div key={`${r.rule}:${r.app ?? ""}`} className="al" data-st="unknown" data-testid="unknown-cell">
					<div>
						<div className="who">
							<span className="sev">Unknown</span>
							<HitWho board={c.board} app={r.app} />
							<span className="rid">{r.rule}</span>
						</div>
						<div className="tx">{r.outcome.kind === "unknown" ? unknownText(r.outcome.missing, c.now) : ""}</div>
					</div>
					<span />
				</div>
			))}
		</Sec>
	);
}

function AttentionBody({ c }: { c: Ctx }) {
	const clear = useClearStaleHides();
	const groups = SEVERITY_ORDER.map((s) => [s, c.lens.attention.filter((h) => h.severity === s)] as const).filter(
		([, l]) => l.length,
	);
	const shownApps = new Set(c.lens.apps.map((a) => a.id));
	const unknown = c.board.alerts.rules.filter(
		(r) => r.on && r.outcome.kind === "unknown" && (!r.app || shownApps.has(r.app)),
	);
	const stale = c.board.alerts.staleHides;
	return (
		<>
			<div className="passive mt-2.5 text-[11.5px]">
				Following the app filter:{" "}
				{c.lens.apps.length === c.board.apps.length ? "all apps" : c.lens.apps.map((a) => a.name).join(", ") || "none"}.{" "}
				{c.board.catalogue.filter((r) => !r.on).length} rule(s) off.
			</div>
			{groups.length ? (
				groups.map(([s, list]) => (
					<div key={s}>
						<div className="grp" data-st={SEVERITY_META[s].tone}>
							{SEVERITY_META[s].label}
							<span className="n">{list.length}</span>
						</div>
						{list.map((h) => (
							<AlertRow key={h.id} h={h} c={c} />
						))}
					</div>
				))
			) : (
				<div className="sec empty">Nothing needs attention{unknown.length ? " that the monitor can see." : "."}</div>
			)}
			<UnknownCells rules={unknown} c={c} />
			{c.lens.hidden.length > 0 && (
				<Fold label={`${c.lens.hidden.length} hidden`} testid="hidden-fold">
					{c.lens.hidden.map((h) => (
						<AlertRow key={h.id} h={h} c={c} hidden />
					))}
				</Fold>
			)}
			{stale.length > 0 && (
				<Fold label={`${stale.length} hide(s) no longer matching`} testid="stale-fold">
					{stale.map((s) => (
						<div key={s.id} className="al dim">
							<div>
								<div className="who mono">{s.id}</div>
								<div className="tx">
									Hidden {agoText(c.now - Date.parse(s.hiddenAt))}
									{s.reason ? `: ${s.reason}` : ""}
								</div>
							</div>
							<span />
						</div>
					))}
					<button type="button" className="link mt-2" onClick={() => clear.mutate()}>
						Clear stale hides
					</button>
				</Fold>
			)}
			{c.lens.history.length > 0 && (
				<Fold label={`History · ${c.lens.history.length} on trains published a while ago`} testid="history-fold">
					{c.lens.history.map((h) => (
						<AlertRow key={h.id} h={h} c={c} />
					))}
				</Fold>
			)}
		</>
	);
}

function RulesBody({ c }: { c: Ctx }) {
	const setRule = useSetRule();
	const outcomes = (id: string) => c.board.alerts.rules.filter((r) => r.rule === id);
	const groups = [...new Set(c.board.catalogue.map((r) => r.group))];
	return (
		<>
			{groups.map((g) => (
				<div key={g}>
					<div className="grp" style={{ color: "var(--fg-muted)" }}>
						{RULE_GROUPS[g] ?? g}
					</div>
					{c.board.catalogue
						.filter((r) => r.group === g)
						.map((r) => {
							const os = outcomes(r.id);
							const hits = os.reduce((n, o) => n + (o.outcome.kind === "fired" ? o.outcome.hits.length : 0), 0);
							const unknown = os.filter((o) => o.outcome.kind === "unknown").length;
							return (
								<div key={r.id} className="rule" data-rule={r.id}>
									<Switch
										checked={r.on}
										aria-label={`${r.id} ${r.on ? "on" : "off"}`}
										onCheckedChange={(on) => setRule.mutate({ id: r.id, on })}
									/>
									<div>
										<div className="ti">{r.title}</div>
										<div className="hn">
											<span className="mono">{r.id}</span> ·{" "}
											<span className="c" data-st={SEVERITY_META[r.severity].tone}>
												{SEVERITY_META[r.severity].label}
											</span>
											{r.hint ? ` · ${r.hint}` : ""}
											{r.on !== r.defaultOn ? " · changed from its default" : ""}
										</div>
									</div>
									<span className="cnt">
										{hits} hit{hits === 1 ? "" : "s"}
										{unknown > 0 && (
											<span className="c" data-st="unknown">
												{" "}
												· {unknown} unknown
											</span>
										)}
									</span>
								</div>
							);
						})}
				</div>
			))}
		</>
	);
}

const PANEL_TABS = [
	["attention", "Alerts"],
	["rules", "Rules"],
	["fixes", "Fixes"],
] as const;

function panelTabs(tab: (typeof PANEL_TABS)[number][0], c: Ctx) {
	return (
		<div className="tabs" role="tablist">
			{PANEL_TABS.map(([kind, label]) => (
				<button
					key={kind}
					type="button"
					role="tab"
					aria-selected={tab === kind}
					onClick={() => c.actions.setSearch({ sheet: { kind } })}
				>
					{label}
				</button>
			))}
		</div>
	);
}

function panelSheet(tab: "attention" | "rules", c: Ctx): Sheet {
	return {
		title:
			tab === "rules" ? (
				"Alert rules"
			) : (
				<>
					Needs attention
					<span className="badge" data-st={c.lens.badge.worst ? SEVERITY_META[c.lens.badge.worst].tone : "todo"}>
						{badgeLabel(c.lens.badge)}
					</span>
				</>
			),
		tabs: panelTabs(tab, c),
		body: tab === "rules" ? <RulesBody c={c} /> : <AttentionBody c={c} />,
	};
}

const KEY_FIX_STATES: readonly FixState[] = ["dry_run", "applied", "withdrawn", "undone", "superseded"];

const KEY_STATUSES = ["done", "running", "waiting", "failed", "unknown", "todo"] as const;

function keySheet(c: Ctx): Sheet {
	const sample = (key: Stage["key"]): Stage => ({ key, status: "done", detail: "", reads: [] });
	return {
		title: "Key",
		body: (
			<>
				<Sec title="Bars">
					<div className="kr">
						<span className="kbar" />
						train in focus, with its stage markers
					</div>
					<div className="kr">
						<span className="kbar quiet" />
						another train: dimmer, click it for its stages
					</div>
					<div className="kr">
						<span className="kbar next" />
						next train (expected dates)
					</div>
					<div className="kr">
						<span className="kbar off" />
						an app on other dates than the rest of its train (rule A7)
					</div>
					<div className="kr">
						<span className="kbar hf" />
						hotfix
					</div>
					<div className="kr">
						Line 2 of a bar: release MR state · next stage not done and how far · tag. A narrow bar drops the tag first,
						then the next stage, the dates and the train.
					</div>
				</Sec>
				<Sec title="Stages">
					{(Object.keys(STAGE_META) as Stage["key"][]).map((k) => (
						<div key={k} className="kr">
							<StageMark stage={sample(k)} className="static" now={c.now} />
							{STAGE_META[k].label}
						</div>
					))}
					<div className="kr">A dashed marker is a conventional date, not one a system records.</div>
				</Sec>
				<Sec title="Sprint rows">
					<div className="kr">
						The Sprint cards layer adds one row per board. Hide a board&apos;s row with the eye on its label; &quot;N
						hidden&quot; on the Sprint line lists every board to show it again. A hidden board&apos;s cards leave the
						sprint counts, but its alerts stay in Needs attention: hiding a row is a view choice, not &quot;not
						mine&quot;.
					</div>
				</Sec>
				<Sec title="Fixes">
					<div className="kr">
						Fix › on an alert shows the exact request a fix would send. Every fix except L3 is a dry run: AO records it
						and sends nothing to Jira, GitLab or Confluence. L3 applies to AO's own long-lived plan.
					</div>
					{KEY_FIX_STATES.map((k) => (
						<div key={k} className="kr">
							<span className="dot" data-st={FIX_STATE_META[k].tone} />
							{FIX_STATE_META[k].text}
						</div>
					))}
					<button
						type="button"
						className="lnk mt-1 text-[12.5px]"
						onClick={() => c.actions.openSheet({ kind: "fixes" }, "push")}
					>
						Open the Fixes list ›
					</button>
				</Sec>
				<Sec title="Status colours">
					{KEY_STATUSES.map((k) => (
						<div key={k} className="kr">
							<span className="dot" data-st={k} />
							{k === "failed"
								? "failed / late"
								: k === "unknown"
									? "unknown: the read failed or is stale, with its age"
									: STATUS_TEXT[k]}
						</div>
					))}
					<div className="kr">
						<span className="dot" data-st="passive" />
						{STATUS_TEXT.not_observable}: no system records it yet
					</div>
				</Sec>
				<Sec title="Sources (Show → Sources)">
					<div className="kr" data-testid="key-sources">
						J Jira · G GitLab · C Confluence in the top bar. Hover one for when AO last read it; an unknown one says so
						in amber.
					</div>
					<div className="kr">
						<span className="kstag">Jira</span>
						read straight from one system
					</div>
					<div className="kr">
						<span className="kstag" data-how="derived">
							GitLab + Jira
						</span>
						worked out by AO from these systems
					</div>
					<div className="kr">
						<span className="kstag" data-how="merged">
							page + sprint MR
						</span>
						a card in a train: only the sources that matched, in the order fixVersion, page, sprint MR
					</div>
					<div className="kr">
						<span className="kstag" data-how="rule">
							rule C5
						</span>
						an alert: the rule that fired and what it reads
					</div>
					<div className="kr">
						<span className="kstag" data-unknown="">
							Jira
						</span>
						amber dot: what it reads is not fresh, so the value may be old
					</div>
					<div className="kr">
						Click a tag, or hover a value for a moment, to see each source, its fact kind and when AO last read it.
					</div>
				</Sec>
			</>
		),
	};
}
