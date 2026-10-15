import { Fragment } from "react";
import { DW, type Timeline } from "../../lib/releases/layout";
import type { Lens } from "../../lib/releases/lens";
import {
	boardLabel,
	DEST_META,
	destKey,
	destLabel,
	destTone,
	leadKind,
	mrPills,
	type MrPill,
} from "../../lib/releases/sprint";
import type { Board, BoardApp, SprintCard, SprintDest, SprintRow } from "../../lib/releases/types";
import { useReleasesUi } from "../../stores/releases-ui";
import { CardLine, Chev } from "./CardGroups";
import type { PageActions } from "./ReleasesPage";

const SRC_TOP = 6;
const SUM_H = 26;
const BLOCK_HEAD = 28;
const ROW_H = 24;
const MIN_H = 76;
const INDENT = 44;

const GLYPH: Record<BoardApp["platform"], string> = {
	ios: "M11.3 8.6c0-1.7 1.4-2.5 1.5-2.5-.8-1.2-2.1-1.4-2.5-1.4-1-.1-2.1.6-2.6.6s-1.4-.6-2.3-.6C4.2 4.7 3 5.4 2.4 6.6c-1.3 2.2-.3 5.4.9 7.2.6.9 1.3 1.8 2.2 1.8.9 0 1.2-.6 2.3-.6s1.4.6 2.3.6c1 0 1.6-.9 2.1-1.8.7-1 1-1.9 1-1.9s-1.9-.8-1.9-3.3zM9.6 3.5c.5-.6.8-1.4.7-2.3-.7 0-1.6.5-2.1 1.1-.5.5-.9 1.3-.8 2.2.8.1 1.6-.4 2.2-1z",
	android: "M2.2 12.6a5.8 5.8 0 0 1 11.6 0z",
};

function Pills({ pills, card }: { pills: MrPill[]; card: SprintCard }) {
	return (
		<span className="mrs">
			{pills.map((p) =>
				p.apps.length ? (
					<span key={p.family} className="mrf">
						<b>{p.label}</b>
						{p.apps.map((a) => {
							const mrs = card.mrs.filter((m) => m.app === a.app);
							const tip = `${a.app} · ${DEST_META[a.kind].label}\n${mrs.map((m) => `!${m.iid} → ${m.target} · ${m.state}`).join("\n")}`;
							return (
								<a
									key={a.app}
									className="mrd c"
									data-st={a.tone}
									data-kind={a.kind}
									data-testid="mr-glyph"
									href={mrs[0]?.url}
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

function SprintCardRow({ card, apps, actions }: { card: SprintCard; apps: readonly BoardApp[]; actions: PageActions }) {
	const lead = DEST_META[leadKind(card)];
	const mismatch = card.routes.find((r) => r.mismatch);
	const via = card.routes.find((r) => r.viaLlb)?.viaLlb;
	return (
		<CardLine
			glyph={lead.glyph}
			glyphTone={lead.tone}
			glyphTitle={lead.label}
			person={card.assignee}
			k={card.key}
			url={card.url}
			className="sp"
			summary={
				<>
					{card.summary}
					{!card.onBoard && <span className="ob">not on the board</span>}
					{card.active && !card.activeWhy.includes("new this sprint") && (
						<span className="ob passive">carried over</span>
					)}
					{via && <span className="ob hf">via ⑂ {via.replace(/^feature\//, "")}</span>}
					{mismatch?.plan && (
						<span className="ob c" data-st="failed">
							page says #{mismatch.plan.replace(/-\d{4}$/, "")}
						</span>
					)}
				</>
			}
			extra={<Pills pills={mrPills(card, apps)} card={card} />}
			status={card.status}
			statusCategory={card.statusCategory}
			onOpen={() => actions.openSheet({ kind: "card", key: card.key }, "push")}
		/>
	);
}

type Block = {
	key: string;
	dest: SprintDest | null;
	cards: SprintCard[];
	x: number;
	top: number;
	tone: string;
	open: boolean;
};

function blocksOf(
	row: SprintRow,
	b: Board,
	cards: SprintCard[],
	tl: Timeline,
	x0: number,
	isOpen: (k: string) => boolean,
) {
	const byKey = new Map(cards.map((c) => [c.key, c]));
	const fk = `sp-${row.board.key}`;
	const blocks: Block[] = [];
	let y = SRC_TOP + SUM_H + 10;
	const push = (key: string, dest: SprintDest | null, list: SprintCard[], x: number, tone: string) => {
		if (!list.length) return;
		const open = isOpen(key);
		blocks.push({ key, dest, cards: list, x, top: y, tone, open });
		y += BLOCK_HEAD + (open ? list.length * ROW_H + 4 : 0) + 4;
	};
	for (const d of row.dests) {
		const list = d.keys.flatMap((k) => byKey.get(k) ?? []);
		const freeze =
			d.to.kind === "train" ? b.trains.find((t) => t.id === (d.to as { train: string }).train)?.freeze : undefined;
		const x = freeze ? Math.max(tl.x(freeze) + 2, x0 + INDENT) : x0 + INDENT;
		push(`${fk}:${destKey(d.to)}`, d, list, x, destTone(b, d.to));
	}
	push(
		`${fk}:carried`,
		null,
		cards.filter((c) => !c.active),
		x0 + INDENT,
		"todo",
	);
	return { blocks, bottom: y };
}

export function BoardSprintRow({
	row,
	board,
	tl,
	lens,
	apps,
	actions,
}: {
	row: SprintRow;
	board: Board;
	tl: Timeline;
	lens: Lens;
	apps: readonly BoardApp[];
	actions: PageActions;
}) {
	const open = useReleasesUi((s) => s.open);
	const toggle = useReleasesUi((s) => s.toggleFold);
	const fk = `sp-${row.board.key}`;
	const cards = lens.sprintCards(row);
	const active = cards.filter((c) => c.active);
	const label = boardLabel(row, board);
	const start = row.sprint.start ?? tl.start;
	const x0 = tl.x(start) + 2;
	const width = Math.max(4 * DW, tl.x(row.sprint.end ?? start) - tl.x(start) - 4);
	const L = open.has(fk) ? blocksOf(row, board, cards, tl, x0, (k) => open.has(k)) : { blocks: [], bottom: 0 };
	const h = Math.max(L.bottom + 4, SRC_TOP + SUM_H + 8, MIN_H);
	const sx = x0 + 13;
	let lastY = SRC_TOP + SUM_H;
	return (
		<div className="rel-row rel-sprow" data-board={row.board.key} data-testid={`sprint-row-${row.board.key}`}>
			<button
				type="button"
				className="rel-lab top"
				style={{ height: h }}
				onClick={() => actions.openSheet({ kind: "sprint", board: row.board.key })}
			>
				<span className="spn">
					<b>{label.title}</b>
					<small>{label.dates}</small>
				</span>
				<small className="sps" data-testid="sprint-scope">
					{label.scope}
				</small>
				<span className="spt">
					{label.dests.map((d) => (
						<span key={d.text} className="c" data-st={d.tone}>
							{d.text}
						</span>
					))}
				</span>
			</button>
			<div className="rel-trk" style={{ width: tl.width, height: h }}>
				<svg className="rel-wires" width={tl.width} height={h} aria-hidden="true">
					{L.blocks.map((bk) => {
						if (!bk.dest || bk.dest.to.kind === "none") return null;
						const ey = bk.top + 11;
						const ex = bk.x - 3;
						const d = `M${sx} ${lastY} V${ey - 8} Q${sx} ${ey} ${sx + 8} ${ey} H${ex}`;
						lastY = ey - 8;
						return (
							<g key={bk.key} data-st={bk.tone}>
								<path d={d} />
								<circle cx={ex} cy={ey} r={3} />
							</g>
						);
					})}
				</svg>
				<div className="rel-cg spsrc" style={{ left: x0, top: SRC_TOP, width }}>
					<button type="button" className="csum" aria-expanded={open.has(fk)} onClick={() => toggle(fk)}>
						<span className="sl" data-sticky>
							<Chev open={open.has(fk)} />
							<span>{label.title}</span>
							<b>
								{active.length} card{active.length === 1 ? "" : "s"}
								{lens.me ? " of mine" : ""}
							</b>
						</span>
					</button>
				</div>
				{L.blocks.map((bk) => (
					<div
						key={bk.key}
						className="rel-cg dest"
						data-st={bk.tone}
						style={{ left: bk.x, top: bk.top }}
						data-testid="sprint-dest-block"
					>
						<button
							type="button"
							className="dsum"
							data-st={bk.tone}
							aria-expanded={bk.open}
							onClick={() => toggle(bk.key)}
						>
							<Chev open={bk.open} />
							<span className="to">{bk.dest ? destLabel(board, bk.dest.to) : "↺ carried over"}</span>
							<b>
								{bk.cards.length} card{bk.cards.length === 1 ? "" : "s"}
							</b>
							{bk.dest?.to.kind === "llb" && (
								<span
									role="button"
									tabIndex={0}
									className="lbn"
									onClick={(e) => {
										e.stopPropagation();
										actions.openSheet({ kind: "llb", id: (bk.dest?.to as { group: string }).group });
									}}
									onKeyDown={() => undefined}
								>
									plan ›
								</span>
							)}
						</button>
						{bk.open && (
							<div className="crows">
								{bk.cards.map((c) => (
									<Fragment key={c.key}>
										<SprintCardRow card={c} apps={apps} actions={actions} />
									</Fragment>
								))}
							</div>
						)}
					</div>
				))}
			</div>
		</div>
	);
}
