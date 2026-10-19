import { EyeOff, Snowflake } from "lucide-react";
import type { RefObject } from "react";
import {
	DropdownMenu,
	DropdownMenuCheckboxItem,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { dayLabel } from "../../lib/releases/day";
import { DW, type Timeline } from "../../lib/releases/layout";
import type { Lens } from "../../lib/releases/lens";
import { trainLabel } from "../../lib/releases/llb";
import {
	boardLabel,
	carriedTo,
	DEST_META,
	destBlocks,
	destHead,
	destKey,
	destTally,
	hiddenBoardsText,
	isSplit,
	leadKind,
	mrPills,
	sprintFoldKey,
	type DestBlock,
	type MrPill,
} from "../../lib/releases/sprint";
import { derivedOf, planSaysText, sprintCardOf } from "../../lib/releases/sources";
import type { Board, BoardApp, Dest, SprintCard, SprintRow } from "../../lib/releases/types";
import { useReleasesUi } from "../../stores/releases-ui";
import { PlatformGlyph } from "./AppMark";
import { CardLine, Chev } from "./CardGroups";
import { Moving, Unfold, Wire, WireText } from "./ReleaseMotion";
import type { PageActions } from "./ReleasesPage";
import { SourceTag } from "./Sources";

const SRC_TOP = 6;
const SUM_H = 26;
const BLOCK_HEAD = 28;
const ROW_H = 24;
const HEAD_ROW_H = 20;
const MIN_H = 76;
const ROW_BORDER = 1;
const DESTS_TOP = SRC_TOP + SUM_H;
const INDENT = 44;
const BLOCK_W = 683;

const SCOPE_P = derivedOf(
	"New and carried",
	[
		{ kind: "jira.this_sprint", text: 'new = in the board\'s "This Sprint" set' },
		{ kind: "gitlab.mrs", text: "carried with MRs = an MR opened or merged since the sprint started" },
		{ kind: "jira.sprint_cards", text: "carried, done = the rest of the sprint's cards" },
	],
	undefined,
	"SprintCard.active, activeWhy",
);

const DESTS_P = derivedOf(
	"Where the cards ship",
	[
		{ kind: "gitlab.mrs", text: "each card's MR target: sprint branch, long-lived branch, release" },
		{ kind: "gitlab.milestones", text: "the train that branch ships in" },
	],
	"Sprint N branch ships in train N + 1.",
	"SprintCard.routes",
);

function Pills({ pills, card, here }: { pills: MrPill[]; card: SprintCard; here: string | null }) {
	return (
		<span className="mrs">
			{pills.map((p) =>
				p.apps.length ? (
					<span key={p.family} className="mrf">
						<b>{p.label}</b>
						{p.apps.map((a) => {
							const route = card.routes.find((r) => r.app === a.app);
							const away = here !== null && !!route && destKey(route.to) !== here;
							const mrs = card.mrs.filter((m) => m.app === a.app);
							const tip = away
								? `${a.app} goes elsewhere`
								: `${a.app} · ${DEST_META[a.kind].label}\n${mrs.map((m) => `!${m.iid} → ${m.target} · ${m.state}`).join("\n")}`;
							return (
								<a
									key={a.app}
									className={`mrd c${away ? " away" : ""}`}
									data-st={a.tone}
									data-kind={a.kind}
									data-app={a.app}
									data-testid="mr-glyph"
									href={a.url}
									target="_blank"
									rel="noreferrer"
									title={tip}
									aria-label={tip}
									onClick={(e) => e.stopPropagation()}
								>
									<PlatformGlyph platform={a.platform} />
								</a>
							);
						})}
					</span>
				) : (
					<span key={p.family} className="mrf off" />
				),
			)}
		</span>
	);
}

function SprintCardRow({
	card,
	board,
	apps,
	here,
	carried,
	row,
	actions,
}: {
	card: SprintCard;
	row: SprintRow;
	board: Board;
	apps: readonly BoardApp[];
	here: string | null;
	carried: boolean;
	actions: PageActions;
}) {
	const routes = here ? card.routes.filter((r) => destKey(r.to) === here) : card.routes;
	const lead = DEST_META[leadKind(routes)];
	const mismatch = routes.find((r) => r.mismatch);
	const via = routes.find((r) => r.viaLlb)?.viaLlb;
	const went = carried ? carriedTo(board, card) : null;
	return (
		<CardLine
			glyph={lead.glyph}
			glyphTone={lead.tone}
			glyphTitle={lead.label}
			person={card.assignee}
			k={card.key}
			url={card.url}
			className={`sp${isSplit(card) ? " split" : ""}${card.mine?.length ? " mine" : ""}`}
			summary={
				<>
					{card.summary}
					{!card.onBoard && (
						<span className="ob">
							not on the board
							<SourceTag p={sprintCardOf(card, row)} />
						</span>
					)}
					{!carried && card.active && !card.activeWhy.includes("new this sprint") && (
						<span className="ob passive">carried over</span>
					)}
					{went && (
						<span className="ob c" data-st={went.tone}>
							{went.text}
						</span>
					)}
					{via && <span className="ob hf">via ⑂ {via.replace(/^feature\//, "")}</span>}
					{mismatch?.plan && (
						<span className="ob c" data-st="failed">
							{planSaysText(mismatch.planFrom)} says {trainLabel(board, mismatch.plan)}
						</span>
					)}
				</>
			}
			extra={<Pills pills={mrPills(card, apps)} card={card} here={carried ? null : here} />}
			status={card.status}
			statusCategory={card.statusCategory}
			statusKind={card.onBoard ? "jira.sprint_cards" : "jira.branch_cards"}
			onOpen={() => actions.openSheet({ kind: "card", key: card.key })}
		/>
	);
}

function DestSummary({
	board,
	block,
	open,
	onToggle,
	actions,
}: {
	board: Board;
	block: DestBlock;
	open: boolean;
	onToggle: () => void;
	actions: PageActions;
}) {
	const h = destHead(board, block.to, block.cards);
	const tally = destTally(block.to, block.cards);
	const n = block.cards.length;
	const group = block.to?.kind === "llb" ? block.to.group : null;
	return (
		<button type="button" className="dsum" data-st={h.tone} aria-expanded={open} onClick={onToggle}>
			<Chev open={open} />
			<span className="to">{h.to}</span>
			{h.target && (
				<span className="tgt c" data-st={h.target.tone} title={h.target.title}>
					{h.target.text}
				</span>
			)}
			{(h.freeze || h.note) && (
				<span className="passive">
					{h.freeze && (
						<>
							<Snowflake className="snow" strokeWidth={2.8} aria-hidden="true" /> {dayLabel(h.freeze)}
						</>
					)}
					{h.freeze && h.note ? " · " : ""}
					{h.note}
				</span>
			)}
			<b>
				{n} card{n === 1 ? "" : "s"}
			</b>
			{h.tail && <span className="passive">{h.tail}</span>}
			{tally && <span>{tally}</span>}
			{group && (
				<span
					role="button"
					tabIndex={0}
					className="lbn"
					onClick={(e) => {
						e.stopPropagation();
						actions.openSheet({ kind: "llb", id: group });
					}}
					onKeyDown={(e) => {
						if (e.key !== "Enter" && e.key !== " ") return;
						e.stopPropagation();
						actions.openSheet({ kind: "llb", id: group });
					}}
				>
					plan ›
				</span>
			)}
		</button>
	);
}

type Placed = DestBlock & { x: number; top: number; open: boolean; tone: string };

function freezeOf(b: Board, to: Dest | null): string | undefined {
	return to?.kind === "train" ? b.trains.find((t) => t.id === to.train)?.freeze : undefined;
}

function place(b: Board, blocks: DestBlock[], tl: Timeline, x0: number, fk: string, isOpen: (k: string) => boolean) {
	let y = SRC_TOP + SUM_H + 10;
	const placed: Placed[] = blocks.map((bk) => {
		const freeze = freezeOf(b, bk.to);
		const open = isOpen(`${fk}:${bk.key}`);
		const top = y;
		y += BLOCK_HEAD + (open ? HEAD_ROW_H + bk.cards.length * ROW_H + 4 : 0) + 4;
		return {
			...bk,
			x: freeze ? Math.max(tl.x(freeze) + 2, x0 + INDENT) : x0 + INDENT,
			top,
			open,
			tone: destHead(b, bk.to, bk.cards).tone,
		};
	});
	return { placed, bottom: y };
}

function Wires({ board, placed, x0, tl }: { board: Board; placed: Placed[]; x0: number; tl: Timeline }) {
	const sx = x0 + 13;
	let lastY = SRC_TOP + SUM_H;
	return (
		<svg className="rel-wires" width={tl.width} height="100%" aria-hidden="true" data-testid="sprint-wires">
			{placed.map((bk) => {
				if (!bk.to || bk.to.kind === "none") return null;
				const ey = bk.top + 11;
				const ex = bk.x - 3;
				const d = `M${sx} ${lastY} V${ey - 8} Q${sx} ${ey} ${sx + 8} ${ey} H${ex}`;
				lastY = ey - 8;
				const via = bk.to.kind === "llb" ? bk.to.via : undefined;
				const viaFreeze = via ? board.trains.find((t) => t.id === via)?.freeze : undefined;
				const tx = viaFreeze ? tl.x(viaFreeze) + DW / 2 : 0;
				const lx = bk.x + 470;
				const g = via ? board.llbs.find((x) => bk.to?.kind === "llb" && x.id === bk.to.group) : undefined;
				const viaTone = g?.target.kind === "landed" ? "done" : bk.tone;
				return (
					<g key={bk.key} data-st={bk.tone} data-wire={bk.key}>
						<Wire d={d} dot={{ x: ex, y: ey, r: 3 }} />
						{via && tx > lx && (
							<g data-st={viaTone} data-testid="via-wire">
								<Wire className="via" d={`M${lx} ${ey} H${tx - 4}`} dot={{ x: tx, y: ey, r: 3.5 }} />
								<WireText x={tx + 8} y={ey + 4}>
									{trainLabel(board, via)}
								</WireText>
							</g>
						)}
					</g>
				);
			})}
		</svg>
	);
}

export function HiddenBoardsMenu({
	board,
	lens,
	actions,
	trigger,
}: {
	board: Board;
	lens: Lens;
	actions: PageActions;
	trigger: RefObject<HTMLButtonElement | null>;
}) {
	const text = hiddenBoardsText(board.sprints, lens.hiddenBoards);
	if (!text) return null;
	const show = (key: string, shown: boolean) => {
		const next = new Set(lens.hiddenBoards);
		if (shown) next.delete(key);
		else next.add(key);
		actions.setHiddenBoards(next);
	};
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<button ref={trigger} type="button" className="rel-lyr rel-hid" data-testid="hidden-boards">
					<EyeOff aria-hidden="true" />
					{text}
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" aria-label="Sprint rows">
				<DropdownMenuLabel>Sprint rows</DropdownMenuLabel>
				{board.sprints.map((r) => (
					<DropdownMenuCheckboxItem
						key={r.board.key}
						checked={!lens.hiddenBoards.has(r.board.key)}
						onCheckedChange={(on) => show(r.board.key, on === true)}
						onSelect={(e) => e.preventDefault()}
					>
						{r.board.key} {r.number}
					</DropdownMenuCheckboxItem>
				))}
				<DropdownMenuSeparator />
				<DropdownMenuItem onSelect={() => actions.setHiddenBoards(new Set())}>Show all</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

export function BoardSprintRow({
	row,
	board,
	tl,
	lens,
	actions,
	onHide,
}: {
	row: SprintRow;
	board: Board;
	tl: Timeline;
	lens: Lens;
	actions: PageActions;
	onHide: (board: string) => void;
}) {
	const open = useReleasesUi((s) => s.open);
	const toggle = useReleasesUi((s) => s.toggleFold);
	const fk = sprintFoldKey(row);
	const cards = lens.sprintCards(row);
	const active = cards.filter((c) => c.active);
	const label = boardLabel(row, board, cards);
	const start = row.sprint.start ?? tl.start;
	const x0 = tl.x(start) + 2;
	const width = Math.max(4 * DW, tl.x(row.sprint.end ?? start) - tl.x(start) - 4);
	const unfolded = open.has(fk);
	const L = unfolded ? place(board, destBlocks(row, cards), tl, x0, fk, (k) => open.has(k)) : { placed: [], bottom: 0 };
	const h = Math.max(L.bottom + 4, SRC_TOP + SUM_H + 8, MIN_H);
	return (
		<Moving
			track="rows"
			appear
			height={h + ROW_BORDER}
			className="rel-row rel-sprow"
			data={{ "data-board": row.board.key, "data-testid": `sprint-row-${row.board.key}` }}
		>
			<div className="rel-lab top">
				<button
					type="button"
					className="open"
					onClick={() => actions.openSheet({ kind: "sprint", board: row.board.key })}
				>
					<span className="spn">
						<b>{label.title}</b>
						<SourceTag
							p={{
								title: `${row.board.key} board`,
								how: "read",
								lines: [
									{
										kind: "ao.config",
										text: `board ${row.board.id} is followed${row.board.ownsSprintBranch ? ", and owns the sprint branch" : ""}`,
									},
									{ kind: "jira.board_sprints", text: `active sprint "${row.sprint.name}"` },
								],
								note: "One row per followed board with an active sprint. A board with no active sprint has no row.",
								field: "SprintRow.board, sprint",
							}}
						/>
						<small>{label.dates}</small>
					</span>
					<small className="sps" data-testid="sprint-scope">
						{label.scope}
						<SourceTag p={SCOPE_P} />
					</small>
					<span className="spt" data-testid="sprint-dests">
						{label.dests.length ? (
							label.dests.map((d) => (
								<span key={d.text} className="c" data-st={d.tone}>
									{d.text}
								</span>
							))
						) : (
							<span className="passive">{lens.me ? "nothing of mine" : "no cards"}</span>
						)}
						<SourceTag p={DESTS_P} />
					</span>
				</button>
				<button
					type="button"
					className="hide"
					aria-label={`Hide ${row.board.key} sprint`}
					title={`Hide ${row.board.key} sprint`}
					onClick={() => onHide(row.board.key)}
				>
					<EyeOff aria-hidden="true" />
				</button>
			</div>
			<div className="rel-trk" style={{ width: tl.width }}>
				<div className="rel-cg spsrc" style={{ left: x0, top: SRC_TOP, width }}>
					<button type="button" className="csum" aria-expanded={open.has(fk)} onClick={() => toggle(fk)}>
						<span className="sl" data-sticky>
							<Chev open={open.has(fk)} />
							<span>{label.title}</span>
							<b>
								{active.length} card{active.length === 1 ? "" : "s"}
								{lens.me ? " of mine" : ""}
							</b>
							<span className="routes">
								{label.routes.map((r) => (
									<span key={r.text} className="c" data-st={r.tone}>
										{r.text}
									</span>
								))}
							</span>
						</span>
					</button>
				</div>
				<Unfold open={unfolded} clipAt={{ top: DESTS_TOP, height: L.bottom - DESTS_TOP }}>
					{L.placed.length > 0 && <Wires board={board} placed={L.placed} x0={x0} tl={tl} />}
					{L.placed.map((bk) => (
						<Moving
							track="blocks"
							key={bk.key}
							className="rel-cg dest"
							data={{ "data-st": bk.tone, "data-dest": bk.key, "data-testid": "sprint-dest-block" }}
							style={{ left: bk.x, top: bk.top, width: BLOCK_W }}
						>
							<DestSummary
								board={board}
								block={bk}
								open={bk.open}
								onToggle={() => toggle(`${fk}:${bk.key}`)}
								actions={actions}
							/>
							<Unfold open={bk.open} className="crows">
								<div className="crow sp sph" aria-hidden="true">
									<span />
									<span />
									<span>Card</span>
									<span />
									<span>MRs</span>
									<span>Jira</span>
								</div>
								{bk.cards.map((c) => (
									<SprintCardRow
										key={c.key}
										card={c}
										board={board}
										apps={lens.apps}
										here={bk.to ? bk.key : null}
										carried={!bk.to}
										row={row}
										actions={actions}
									/>
								))}
							</Unfold>
						</Moving>
					))}
				</Unfold>
			</div>
		</Moving>
	);
}
