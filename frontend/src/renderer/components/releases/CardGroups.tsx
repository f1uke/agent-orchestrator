import { ChevronRight, GitFork } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { branchFoldKey, statusTone, type BranchCard, type CardGroup } from "../../lib/releases/cards";
import { shortDay } from "../../lib/releases/day";
import { aimedTrain, branchState, shortBranch, targetChip, trainLabel } from "../../lib/releases/llb";
import {
	codeStateOf,
	derivedOf,
	llbTargetOf,
	membershipOf,
	readOf,
	statusOf,
	type Provenance,
} from "../../lib/releases/sources";
import { mineText } from "../../lib/releases/sprint";
import type { Board, CardUnit, IdentityKnown, MineReason, Person } from "../../lib/releases/types";
import { CODE_META } from "../../lib/releases/vocab";
import { useReleasesUi } from "../../stores/releases-ui";
import { Avatar } from "./Avatar";
import { Unfold } from "./ReleaseMotion";
import type { PageActions } from "./ReleasesPage";
import { SourceCell, SourceTag, SourceValue } from "./Sources";
import { NoMrButton } from "./NoMr";
import { TargetTag } from "./TargetTag";

export function Chev({ open }: { open: boolean }) {
	return <ChevronRight className="chev" style={{ rotate: open ? "90deg" : "0deg" }} aria-hidden="true" />;
}

export function Face({ person, size = 18 }: { person?: Person; size?: number }) {
	return person ? (
		<Avatar accountId={person.accountId} name={person.name} size={size} />
	) : (
		<span className="av" style={{ width: size, height: size }} aria-hidden="true" />
	);
}

export function JiraKey({ k, url }: { k: string; url?: string }) {
	return url ? (
		<a
			className="k"
			href={url}
			target="_blank"
			rel="noreferrer"
			title={`Open ${k} in Jira`}
			onClick={(e) => e.stopPropagation()}
		>
			{k}
		</a>
	) : (
		<span className="k">{k}</span>
	);
}

export function MineFace({ face, mine }: { face: IdentityKnown | null; mine?: readonly MineReason[] }) {
	if (!face || !mine?.length) return <span />;
	const why = `Yours: ${mineText(mine)}`;
	return (
		<span className="mecell" title={why} aria-label={why} data-testid="mine-face">
			<Avatar accountId={face.jiraAccountId} name={face.displayName} size={18} />
		</span>
	);
}

export function CardLine({
	glyph,
	glyphTone,
	glyphTitle,
	person,
	k,
	url,
	summary,
	chips,
	status,
	statusCategory,
	extra,
	source,
	glyphSource,
	statusKind = "jira.cards",
	onOpen,
	action,
	className = "",
}: {
	action?: ReactNode;
	source?: ReactNode;
	glyphSource?: Provenance;
	statusKind?: string;
	glyph: string;
	glyphTone: string;
	glyphTitle: string;
	person?: Person;
	k: string;
	url?: string;
	summary?: string;
	chips?: ReactNode;
	status?: string;
	statusCategory?: string;
	extra?: ReactNode;
	onOpen?: () => void;
	className?: string;
}) {
	return (
		<div
			role={onOpen ? "button" : undefined}
			tabIndex={onOpen ? 0 : undefined}
			className={`crow ${className}`}
			data-key={k}
			onClick={onOpen}
			onKeyDown={(e) => onOpen && (e.key === "Enter" || e.key === " ") && onOpen()}
		>
			<SourceValue p={glyphSource ?? null}>
				<span className="cg c" data-st={glyphTone} title={glyphTitle}>
					{glyph}
				</span>
			</SourceValue>
			<Face person={person} />
			<JiraKey k={k} url={url} />
			<span className="t">
				<span className="tt" title={summary}>
					{summary}
				</span>
				<span className="tc">
					{chips}
					{action}
				</span>
			</span>
			{extra ?? <span />}
			<SourceValue p={status ? statusOf(status, statusKind) : null}>
				<span className="spill" data-st={statusTone(status, statusCategory)}>
					{status ?? "-"}
				</span>
			</SourceValue>
			{source}
		</div>
	);
}

function UnitRow({
	u,
	board,
	train,
	tagged,
	face,
	actions,
}: {
	u: CardUnit;
	board: Board;
	train: string;
	tagged: boolean;
	face: IdentityKnown | null;
	actions: PageActions;
}) {
	const m = CODE_META[u.code.kind];
	const into = u.code.kind === "parked" || u.code.kind === "elsewhere" ? ` into ${u.code.branch}` : "";
	return (
		<CardLine
			glyph={m.glyph}
			glyphTone={m.tone}
			glyphTitle={`${m.label}${into}`}
			glyphSource={codeStateOf(u)}
			person={u.assignee}
			k={u.key}
			url={u.url}
			summary={u.summary}
			chips={u.code.kind === "merged" && u.code.via && <span className="ob hf">via ⑂ {shortBranch(u.code.via)}</span>}
			status={u.status}
			statusCategory={u.statusCategory}
			statusKind={u.factsFrom}
			source={
				tagged && (
					<SourceCell>
						<SourceTag p={membershipOf(u, train)} />
					</SourceCell>
				)
			}
			extra={<MineFace face={face} mine={u.mine} />}
			action={
				(u.code.kind === "none" || u.code.kind === "no_mr") && <NoMrButton board={board} k={u.key} variant="row" />
			}
			className={`${face && u.mine?.length ? "mine" : ""}${u.code.kind === "no_mr" ? " nomr" : ""}`}
			onOpen={() => actions.openSheet({ kind: "card", key: u.key })}
		/>
	);
}

function BranchCardRow({
	c,
	landed,
	url,
	tagged,
	face,
	actions,
}: {
	c: BranchCard;
	landed: boolean;
	tagged: boolean;
	url?: string;
	face: IdentityKnown | null;
	actions: PageActions;
}) {
	const [glyph, tone, title] = c.pending
		? ["◐", "running", "MR into the long-lived branch is still open"]
		: landed
			? ["✓", "done", "merged with the long-lived branch"]
			: ["⑂", "hotfix", "merged into the long-lived branch, waiting there"];
	return (
		<CardLine
			glyph={glyph}
			glyphTone={tone}
			glyphTitle={title}
			person={c.assignee}
			k={c.key}
			url={url}
			summary={c.summary}
			status={c.status}
			statusCategory={c.statusCategory}
			statusKind="jira.llb_cards"
			source={
				tagged && (
					<SourceCell>
						<SourceTag p={BRANCH_CARD_P} />
					</SourceCell>
				)
			}
			extra={<MineFace face={face} mine={c.mine} />}
			className={face && c.mine?.length ? "mine" : ""}
			onOpen={() => actions.openSheet({ kind: "card", key: c.key })}
		/>
	);
}

const BRANCH_CARD_P = derivedOf(
	"Card in the branch",
	[
		{ kind: "gitlab.mrs", text: "an MR into the branch names the card" },
		{ kind: "jira.llb_cards", text: "status, title, assignee" },
	],
	undefined,
	"LongLivedBranch.cards",
);

function GroupRows({
	g,
	board,
	tagged,
	face,
	actions,
	isOpen,
	jira,
}: {
	g: CardGroup;
	board: Board;
	tagged: boolean;
	face: IdentityKnown | null;
	actions: PageActions;
	isOpen: (k: string) => boolean;
	jira: (key: string) => string | undefined;
}) {
	const toggle = useReleasesUi((s) => s.toggleFold);
	return (
		<>
			{g.direct.map((u) => (
				<UnitRow key={u.key} u={u} board={board} train={g.train.label} tagged={tagged} face={face} actions={actions} />
			))}
			{g.llbs.map(({ group, branch, cards }) => {
				const fk = branchFoldKey(g, branch);
				const open = isOpen(fk);
				const st = branchState(group, branch);
				const landed = !!branch.landing?.mergedOn;
				const tgt = aimedTrain(group) ? targetChip(group, board) : null;
				return (
					<div key={fk} className="llbgrp" data-st={st.tone}>
						<div
							role="button"
							tabIndex={0}
							className="crow lbr"
							aria-expanded={open}
							onClick={() => toggle(fk)}
							onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && toggle(fk)}
						>
							<Chev open={open} />
							<GitFork className="cg size-3" aria-hidden="true" />
							<span className="t">
								<button
									type="button"
									className="lbn"
									onClick={(e) => {
										e.stopPropagation();
										actions.openSheet({ kind: "llb", id: group.id });
									}}
								>
									{shortBranch(branch.branch)}
								</button>
								<span className="passive">
									{cards.length} card{cards.length === 1 ? "" : "s"}
								</span>
								{tgt && <TargetTag chip={tgt} />}
								{tgt && tagged && <SourceTag p={llbTargetOf(group, (t) => trainLabel(board, t))} />}
							</span>
							<span className="spill">
								{landed && st.label !== "accepted" ? `merged ${shortDay(branch.landing?.mergedOn ?? "")}` : st.label}
							</span>
						</div>
						<Unfold open={open} className="nest">
							{cards.map((c) => (
								<BranchCardRow
									key={c.key}
									c={c}
									landed={landed}
									url={jira(c.key)}
									tagged={tagged}
									face={face}
									actions={actions}
								/>
							))}
						</Unfold>
					</div>
				);
			})}
		</>
	);
}

const CODE_TALLY_P = derivedOf(
	"Code state",
	[{ kind: "gitlab.mrs", text: "MRs whose title or source branch names the card" }],
	"One bucket per card: merged + MR open + parked + elsewhere + no MR + no code = cards. No code is a No MR needed mark set in AO.",
	"CardUnit.code",
);

const READY_P = readOf(
	"Ready",
	"jira.cards",
	"card status in the ready set or Jira's Done category",
	"CardUnit.status",
);

function CodeMix({ g }: { g: CardGroup }) {
	const n = g.units.length || 1;
	return (
		<span className="cmix" aria-hidden="true">
			{(Object.keys(CODE_META) as (keyof typeof CODE_META)[]).map((k) =>
				g.tally[k] ? <i key={k} data-st={CODE_META[k].tone} style={{ width: `${(g.tally[k] / n) * 100}%` }} /> : null,
			)}
		</span>
	);
}

export function CardGroupBox({
	g,
	board,
	left,
	room,
	top,
	focused,
	face,
	actions,
	jira,
}: {
	g: CardGroup;
	board: Board;
	left: number;
	room: number;
	top: number;
	focused: boolean;
	face: IdentityKnown | null;
	actions: PageActions;
	jira: (key: string) => string | undefined;
}) {
	const open = useReleasesUi((s) => s.open);
	const toggle = useReleasesUi((s) => s.toggleFold);
	const isOpen = (k: string) => open.has(k);
	const t = g.tally;
	const n = g.units.length;
	const tagged = focused || g.train.phase !== "past";
	return (
		<div
			className={`rel-cg${focused ? "" : " quiet"}`}
			style={{ left, top, ...(Number.isFinite(room) ? { "--room": `${room}px` } : {}) } as CSSProperties}
			data-testid={`cards-${g.key}`}
			data-group={g.key}
		>
			<button type="button" className="csum" aria-expanded={open.has(g.key)} onClick={() => toggle(g.key)}>
				<Chev open={open.has(g.key)} />
				<span>{g.train.label}</span>
				<b>
					{n} card{n === 1 ? "" : "s"}
				</b>
				<CodeMix g={g} />
				<span>
					{t.merged} merged
					{t.open ? ` · ${t.open} MR open` : ""}
					{t.parked ? ` · ${t.parked} parked` : ""}
					{t.elsewhere ? ` · ${t.elsewhere} elsewhere` : ""}
					{t.no_mr ? ` · ${t.no_mr} no code` : ""}
					{tagged && <SourceTag p={CODE_TALLY_P} />}
				</span>
				<span className="passive">
					{g.ready} ready
					{tagged && <SourceTag p={READY_P} />}
				</span>
				{g.llbs.length > 0 && (
					<span className="llbn">
						<GitFork className="inline size-3" aria-hidden="true" /> {g.llbs.length} long-lived
					</span>
				)}
			</button>
			<Unfold open={open.has(g.key)} className="crows">
				<GroupRows g={g} board={board} tagged={tagged} face={face} actions={actions} isOpen={isOpen} jira={jira} />
			</Unfold>
		</div>
	);
}
