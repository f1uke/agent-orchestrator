import { useRef } from "react";
import { mergedOf } from "../../lib/releases/cards";
import { approvals, nowStage, owner, unknownText, versionOf, type Lens } from "../../lib/releases/lens";
import { derivedOf, readOf, ruleTagOf, stageOf } from "../../lib/releases/sources";
import type { BoardApp, RuleInfo, Stage } from "../../lib/releases/types";
import { SourceTag, SourceValue } from "./Sources";
import { SEVERITY_META, STAGE_META, STATUS_TEXT, stageTone } from "../../lib/releases/vocab";
import { versionMineText } from "../../lib/releases/sprint";
import { appMark } from "../../lib/releases/appMark";
import { AppMark } from "./AppMark";
import { useLabelStagger } from "./ReleaseMotion";
import type { PageActions } from "./ReleasesPage";

export function Stepper({ stages, big = false }: { stages: Stage[]; big?: boolean }) {
	return (
		<span className={`rel-step${big ? " big" : ""}`}>
			{stages.map((s, i) => (
				<span key={s.key} className="contents">
					{i > 0 && <u />}
					<i data-st={stageTone(s.status)} title={`${STAGE_META[s.key].label}: ${STATUS_TEXT[s.status]}`} />
				</span>
			))}
		</span>
	);
}

function MineLine({ app, lens }: { app: string; lens: Lens }) {
	const t = lens.focus;
	const v = t ? versionOf(t, app) : undefined;
	const units = (v?.cards ?? []).filter((u) => u.mine?.length);
	const bits = [
		v ? versionMineText(v, lens.identity) : "",
		units.length ? `${units.length} of my card${units.length === 1 ? "" : "s"}` : "",
	].filter(Boolean);
	return bits.length ? (
		<div className="meline" data-testid="label-mine">
			{bits.join(" · ")}
			<SourceTag p={MINE_P} />
		</div>
	) : (
		<div className="meline off" data-testid="label-mine">
			Nothing of mine in {t?.label ?? "this train"}
		</div>
	);
}

const STAGES_P = derivedOf(
	"Stages",
	[
		{ kind: "gitlab.milestones", text: "freeze and publish dates" },
		{ kind: "gitlab.mrs", text: "release MR" },
		{ kind: "gitlab.jobs", text: "pipeline" },
		{ kind: "jira.versions", text: "approvers" },
		{ kind: "gitlab.tags", text: "tag" },
	],
	"One dot per stage. The version sheet tags each stage with its own source.",
	"Version.stages[].reads",
);

const MERGED_P = derivedOf(
	"Cards merged",
	[{ kind: "gitlab.mrs", text: "each card's MRs: merged into the release or not" }],
	"Out of the cards in the train (fixVersion, page or sprint-branch MR), less those marked No MR needed in AO.",
	"Version.cards[].code",
);

const MINE_P = derivedOf(
	"Mine",
	[
		{ kind: "gitlab.mrs", text: "release MR or card MRs you authored" },
		{ kind: "jira.versions", text: "you are an approver" },
		{ kind: "jira.cards", text: "cards assigned to you" },
		{ kind: "jira.my_subtasks", text: "subtasks assigned to you" },
	],
	undefined,
	"Version.mine, cards[].mine",
);

export function StickyAppLabel({
	app,
	lens,
	catalogue,
	actions,
}: {
	app: BoardApp;
	lens: Lens;
	catalogue: readonly RuleInfo[];
	actions: PageActions;
}) {
	const t = lens.focus;
	const v = t ? versionOf(t, app.id) : undefined;
	const root = useRef<HTMLElement>(null);
	useLabelStagger(root, t?.id ?? "");
	const hits = lens.appHits(app.id);
	const top = hits[0];
	const head = (
		<div className="nm">
			<AppMark mark={appMark(app)} />
			{hits.length > 0 && top && (
				<span
					className="flag"
					data-st={SEVERITY_META[top.severity].tone}
					title={`${hits.length} alert(s) on ${app.name} in ${t?.label}`}
				>
					{hits.length}
				</span>
			)}
		</div>
	);
	if (!t || !v) {
		return (
			<div className="rel-lab" data-testid={`label-${app.id}`} ref={(el) => void (root.current = el)}>
				{head}
				<div className="passive text-[12px]">Not in train {t?.label ?? ""}</div>
			</div>
		);
	}
	const now = nowStage(v.stages);
	const own = owner(v);
	const ap = approvals(v);
	const { merged, needCode } = mergedOf(v.cards);
	const nowAt = Date.now();
	const jiraName = v.jira?.name ?? "the Jira release (none found)";
	const ownerP =
		own &&
		(own.from === "release MR author"
			? readOf(`Owner: ${own.name}`, "gitlab.mrs", `release MR !${v.releaseMr?.mr.iid} author`, "releaseMr.mr.author")
			: readOf(`Owner: ${own.name}`, "jira.cards", "release ticket assignee", "ticket.assignee"));
	return (
		<button
			type="button"
			className="rel-lab"
			data-testid={`label-${app.id}`}
			ref={(el) => void (root.current = el)}
			onClick={() => actions.openSheet({ kind: "version", train: t.id, app: app.id })}
		>
			{head}
			<div className="ver">
				<span className="mono">{v.version}</span>
				<SourceTag
					p={derivedOf(
						`Version ${v.version}`,
						[
							{ kind: "gitlab.milestones", text: `milestone "${v.milestone.title}"` },
							{ kind: "jira.versions", text: `release ${jiraName}` },
						],
						"Rule A1 fires when the two name different versions.",
						"Version.version",
					)}
				/>
				<span className="passive mono">{t.label}</span>
				<SourceValue p={STAGES_P}>
					<span className="inline-flex">
						<Stepper stages={v.stages} />
					</span>
				</SourceValue>
			</div>
			<div data-testid="label-stage">
				{now ? (
					<span className="now" data-st={stageTone(now.status)}>
						{STAGE_META[now.key].short} ·{" "}
						{now.status === "unknown" ? unknownText(now.missing, nowAt) : STATUS_TEXT[now.status]}
						<SourceTag p={stageOf(now)} />
					</span>
				) : (
					<span className="now" data-st="done">
						shipped
					</span>
				)}
			</div>
			<div className="why" data-testid="label-blocker" data-st={top ? SEVERITY_META[top.severity].tone : undefined}>
				{top ? (
					<>
						<SourceTag p={ruleTagOf(catalogue, top.rule)} />
						<b>{SEVERITY_META[top.severity].label}</b> {top.text}
					</>
				) : now?.detail ? (
					now.detail
				) : (
					<span className="passive">Nothing blocking</span>
				)}
			</div>
			{lens.me ? (
				<MineLine app={app.id} lens={lens} />
			) : (
				<div className="meta">
					{needCode > 0 && (
						<span>
							<b>
								{merged}/{needCode}
							</b>{" "}
							merged
							<SourceTag p={MERGED_P} />
						</span>
					)}
					<span>
						<b>
							{ap.ok}/{ap.total}
						</b>{" "}
						approved
						<SourceTag p={readOf("Approvals", "jira.versions", `approvers on ${jiraName}`, "Version.jira.approvers")} />
					</span>
					{own && (
						<span title={`${own.name} (${own.from})`}>
							{own.name.split(" ")[0]}
							<SourceTag p={ownerP} />
						</span>
					)}
				</div>
			)}
		</button>
	);
}
