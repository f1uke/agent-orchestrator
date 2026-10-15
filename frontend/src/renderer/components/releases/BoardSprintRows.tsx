import { Snowflake } from "lucide-react";
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
	isSplit,
	leadKind,
	mrPills,
	sprintFoldKey,
	type DestBlock,
	type MrPill,
} from "../../lib/releases/sprint";
import type { Board, BoardApp, Dest, SprintCard, SprintRow } from "../../lib/releases/types";
import { useReleasesUi } from "../../stores/releases-ui";
import { CardLine, Chev } from "./CardGroups";
import { Moving, Unfold, Wire, WireText } from "./ReleaseMotion";
import type { PageActions } from "./ReleasesPage";

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

const GLYPH: Record<BoardApp["platform"], string> = {
	ios: "M11.3 8.6c0-1.7 1.4-2.5 1.5-2.5-.8-1.2-2.1-1.4-2.5-1.4-1-.1-2.1.6-2.6.6s-1.4-.6-2.3-.6C4.2 4.7 3 5.4 2.4 6.6c-1.3 2.2-.3 5.4.9 7.2.6.9 1.3 1.8 2.2 1.8.9 0 1.2-.6 2.3-.6s1.4.6 2.3.6c1 0 1.6-.9 2.1-1.8.7-1 1-1.9 1-1.9s-1.9-.8-1.9-3.3zM9.6 3.5c.5-.6.8-1.4.7-2.3-.7 0-1.6.5-2.1 1.1-.5.5-.9 1.3-.8 2.2.8.1 1.6-.4 2.2-1z",
	android: "M2.2 12.6a5.8 5.8 0 0 1 11.6 0z",
};

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
									<svg viewBox="0 0 16 16" aria-hidden="true">
										<path d={GLYPH[a.platform]} />
									</svg>
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
	actions,
}: {
	card: SprintCard;
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
					{!card.onBoard && <span className="ob">not on the board</span>}
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
							page says {trainLabel(board, mismatch.plan)}
						</span>
					)}
				</>
			}
			extra={<Pills pills={mrPills(card, apps)} card={card} here={carried ? null : here} />}
			status={card.status}
			statusCategory={card.statusCategory}
			onOpen={() => actions.openSheet({ kind: "card", key: card.key }, "push")}
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
				<span className="tgt c" data-st={h.target.tone}>
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

export function BoardSprintRow({
	row,
	board,
	tl,
	lens,
	actions,
}: {
	row: SprintRow;
	board: Board;
	tl: Timeline;
	lens: Lens;
	actions: PageActions;
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
			<button
				type="button"
				className="rel-lab top"
				onClick={() => actions.openSheet({ kind: "sprint", board: row.board.key })}
			>
				<span className="spn">
					<b>{label.title}</b>
					<small>{label.dates}</small>
				</span>
				<small className="sps" data-testid="sprint-scope">
					{label.scope}
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
				</span>
			</button>
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
				<Unfold open={unfolded} at={{ top: DESTS_TOP, height: L.bottom - DESTS_TOP }}>
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
