import { ChevronRight, GitFork } from "lucide-react";
import type { ReactNode } from "react";
import { branchFoldKey, statusTone, type BranchCard, type CardGroup } from "../../lib/releases/cards";
import { shortDay } from "../../lib/releases/day";
import { aimedTrain, branchState, shortBranch, targetChip } from "../../lib/releases/llb";
import { mineText } from "../../lib/releases/sprint";
import type { Board, CardUnit, IdentityKnown, MineReason, Person } from "../../lib/releases/types";
import { CODE_META } from "../../lib/releases/vocab";
import { useReleasesUi } from "../../stores/releases-ui";
import { Avatar } from "./Avatar";
import { Unfold } from "./ReleaseMotion";
import type { PageActions } from "./ReleasesPage";
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
	status,
	statusCategory,
	extra,
	onOpen,
	className = "",
}: {
	glyph: string;
	glyphTone: string;
	glyphTitle: string;
	person?: Person;
	k: string;
	url?: string;
	summary?: ReactNode;
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
			<span className="cg c" data-st={glyphTone} title={glyphTitle}>
				{glyph}
			</span>
			<Face person={person} />
			<JiraKey k={k} url={url} />
			<span className="t">{summary}</span>
			{extra ?? <span />}
			<span className="spill" data-st={statusTone(status, statusCategory)}>
				{status ?? "-"}
			</span>
		</div>
	);
}

function UnitRow({ u, face, actions }: { u: CardUnit; face: IdentityKnown | null; actions: PageActions }) {
	const m = CODE_META[u.code.kind];
	const into = u.code.kind === "parked" || u.code.kind === "elsewhere" ? ` into ${u.code.branch}` : "";
	return (
		<CardLine
			glyph={m.glyph}
			glyphTone={m.tone}
			glyphTitle={`${m.label}${into}`}
			person={u.assignee}
			k={u.key}
			url={u.url}
			summary={
				<>
					{u.summary}
					{u.code.kind === "merged" && u.code.via && <span className="ob hf">via ⑂ {shortBranch(u.code.via)}</span>}
				</>
			}
			status={u.status}
			statusCategory={u.statusCategory}
			extra={<MineFace face={face} mine={u.mine} />}
			className={face && u.mine?.length ? "mine" : ""}
			onOpen={() => actions.openSheet({ kind: "card", key: u.key }, "push")}
		/>
	);
}

function BranchCardRow({
	c,
	landed,
	url,
	face,
	actions,
}: {
	c: BranchCard;
	landed: boolean;
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
			extra={<MineFace face={face} mine={c.mine} />}
			className={face && c.mine?.length ? "mine" : ""}
			onOpen={() => actions.openSheet({ kind: "card", key: c.key }, "push")}
		/>
	);
}

function GroupRows({
	g,
	board,
	face,
	actions,
	isOpen,
	jira,
}: {
	g: CardGroup;
	board: Board;
	face: IdentityKnown | null;
	actions: PageActions;
	isOpen: (k: string) => boolean;
	jira: (key: string) => string | undefined;
}) {
	const toggle = useReleasesUi((s) => s.toggleFold);
	return (
		<>
			{g.direct.map((u) => (
				<UnitRow key={u.key} u={u} face={face} actions={actions} />
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
										actions.openSheet({ kind: "llb", id: group.id }, "push");
									}}
								>
									{shortBranch(branch.branch)}
								</button>
								<span className="passive">
									{cards.length} card{cards.length === 1 ? "" : "s"}
								</span>
								{tgt && <TargetTag chip={tgt} />}
							</span>
							<span className="spill">
								{landed && st.label !== "accepted" ? `merged ${shortDay(branch.landing?.mergedOn ?? "")}` : st.label}
							</span>
						</div>
						<Unfold open={open} className="nest">
							{cards.map((c) => (
								<BranchCardRow key={c.key} c={c} landed={landed} url={jira(c.key)} face={face} actions={actions} />
							))}
						</Unfold>
					</div>
				);
			})}
		</>
	);
}

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
	top,
	focused,
	face,
	actions,
	jira,
}: {
	g: CardGroup;
	board: Board;
	left: number;
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
	return (
		<div
			className={`rel-cg${focused ? "" : " quiet"}`}
			style={{ left, top }}
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
				</span>
				<span className="passive">{g.ready} ready</span>
				{g.llbs.length > 0 && (
					<span className="llbn">
						<GitFork className="inline size-3" aria-hidden="true" /> {g.llbs.length} long-lived
					</span>
				)}
			</button>
			<Unfold open={open.has(g.key)} className="crows">
				<GroupRows g={g} board={board} face={face} actions={actions} isOpen={isOpen} jira={jira} />
			</Unfold>
		</div>
	);
}
