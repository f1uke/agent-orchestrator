import { EyeOff, Snowflake } from "lucide-react";
import { Fragment, type ReactElement, type RefObject } from "react";
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
	cardCount,
	carriedTo,
	DEST_META,
	destBlocks,
	destHead,
	destKey,
	destTally,
	barSpans,
	chipFit,
	emptyCountText,
	hiddenBoardsText,
	isSplit,
	leadKind,
	MOBILE_RULE,
	mrPills,
	sprintFoldKey,
	stackTop,
	type BoardLabel,
	type DestBlock,
	type Extent,
	type MrPill,
} from "../../lib/releases/sprint";
import { statusTone } from "../../lib/releases/cards";
import { derivedOf, planSaysText, sprintCardOf } from "../../lib/releases/sources";
import type { Board, BoardApp, Dest, SprintCard, SprintRow } from "../../lib/releases/types";
import { CODE_META } from "../../lib/releases/vocab";
import { useReleasesUi } from "../../stores/releases-ui";
import { PlatformGlyph } from "./AppMark";
import { NoMrButton } from "./NoMr";
import { CardLine, Chev } from "./CardGroups";
import { Moving, Unfold, Wire, WireText } from "./ReleaseMotion";
import type { PageActions } from "./ReleasesPage";
import { SourceTag, useSources } from "./Sources";

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

function Pills({
	pills,
	card,
	here,
	copy = false,
}: {
	pills: MrPill[];
	card: SprintCard;
	here: string | null;
	copy?: boolean;
}) {
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
									data-testid={copy ? undefined : "mr-glyph"}
									tabIndex={copy ? -1 : undefined}
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

type FitChip = { text: string; node: ReactElement; tag?: boolean };

const CHIP_GAP = 6;
const CHIP_ROOM = 669 - 12 - (16 + 20 + 96 + 126 + 110) - 5 * CHIP_GAP - 40;
const TAG_W = 56;

let chipCtx: CanvasRenderingContext2D | null = null;

function chipWidth(text: string): number {
	if (!chipCtx) {
		chipCtx = document.createElement("canvas").getContext("2d");
		if (!chipCtx) return text.length * 6 + CHIP_GAP;
		chipCtx.font = `10.5px ${getComputedStyle(document.documentElement).getPropertyValue("--font-sans")}`;
	}
	return Math.ceil(chipCtx.measureText(text).width) + CHIP_GAP;
}

function FitChips({ chips }: { chips: FitChip[] }) {
	const sources = !!useSources();
	const widths = chips.map((c) => chipWidth(c.text) + (sources && c.tag ? TAG_W : 0));
	const shown = chipFit(widths, CHIP_ROOM, chipWidth(`+${chips.length}`));
	const over = chips.slice(shown);
	return (
		<>
			{chips.map((c, i) =>
				i < shown ? (
					<Fragment key={c.text}>{c.node}</Fragment>
				) : (
					<span key={c.text} className="over">
						{c.node}
					</span>
				),
			)}
			{over.length > 0 && (
				<span className="ob passive more" data-testid="more-chips" title={over.map((c) => c.text).join("\n")}>
					+{over.length}
				</span>
			)}
		</>
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
	const lead = !routes.length && card.noMr ? CODE_META.no_mr : DEST_META[leadKind(routes)];
	const mismatch = routes.find((r) => r.mismatch);
	const via = routes.find((r) => r.viaLlb)?.viaLlb;
	const went = carried && card.mobile ? carriedTo(board, card) : null;
	const pills = mrPills(card, apps);
	const away = carried ? null : here;
	const branch = via?.replace(/^feature\//, "");
	const says = mismatch?.plan && `${planSaysText(mismatch.planFrom)} says ${trainLabel(board, mismatch.plan)}`;
	const chips: FitChip[] = [
		!card.mobile && {
			text: "web",
			node: (
				<span className="ob passive" data-testid="web-tag">
					web
				</span>
			),
		},
		!card.onBoard && {
			text: "not on the board",
			tag: true,
			node: (
				<span className="ob">
					not on the board
					<SourceTag p={sprintCardOf(card, row)} />
				</span>
			),
		},
		card.mobile &&
			!carried &&
			card.active &&
			!card.activeWhy.includes("new this sprint") && {
				text: "carried over",
				node: <span className="ob passive">carried over</span>,
			},
		went && {
			text: went.text,
			node: (
				<span className="ob c" data-st={went.tone}>
					{went.text}
				</span>
			),
		},
		branch && { text: `via ⑂ ${branch}`, node: <span className="ob hf">via ⑂ {branch}</span> },
		says && {
			text: says,
			node: (
				<span className="ob c" data-st="failed">
					{says}
				</span>
			),
		},
	].filter((c): c is FitChip => !!c);
	return (
		<CardLine
			glyph={lead.glyph}
			glyphTone={lead.tone}
			glyphTitle={lead.label}
			person={card.assignee}
			k={card.key}
			url={card.url}
			className={`sp${isSplit(card) ? " split" : ""}${card.mine?.length ? " mine" : ""}${card.noMr ? " nomr" : ""}`}
			action={card.mobile && !card.mrs.length && <NoMrButton board={board} k={card.key} variant="row" />}
			summary={card.summary}
			chips={<FitChips chips={chips} />}
			extra={<Pills pills={pills} card={card} here={away} />}
			tail={
				<>
					<Pills pills={pills} card={card} here={away} copy />
					<span className="spill" data-st={statusTone(card.status, card.statusCategory)}>
						{card.status ?? "-"}
					</span>
				</>
			}
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
	const h = destHead(board, block.to, block.cards, block.key);
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

function place(
	b: Board,
	blocks: DestBlock[],
	tl: Timeline,
	x0: number,
	fk: string,
	isOpen: (k: string) => boolean,
	top0: number,
) {
	let y = top0;
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
			tone: destHead(b, bk.to, bk.cards, bk.key).tone,
		};
	});
	return { placed, bottom: y };
}

function Wires({
	board,
	placed,
	x0,
	tl,
	className = "",
}: {
	board: Board;
	placed: Placed[];
	x0: number;
	tl: Timeline;
	className?: string;
}) {
	const sx = x0 + 13;
	let lastY = SRC_TOP + SUM_H;
	return (
		<svg
			className={`rel-wires${className}`}
			width={tl.width}
			height="100%"
			aria-hidden="true"
			data-testid="sprint-wires"
		>
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
				{board.sprints
					.filter((r) => !r.next)
					.map((r) => (
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

type Bar = {
	row: SprintRow;
	cards: SprintCard[];
	fk: string;
	x0: number;
	width: number;
	unfolded: boolean;
	label: BoardLabel;
	placed: Placed[];
	bottom: number;
};

function layBars(rows: readonly SprintRow[], board: Board, tl: Timeline, lens: Lens, open: ReadonlySet<string>): Bar[] {
	const spans = barSpans(rows, tl);
	const taken: Extent[] = [];
	return rows.map((row, i) => {
		const cards = lens.sprintCards(row);
		const fk = sprintFoldKey(row);
		const { x0, width } = spans[i];
		const unfolded = open.has(fk);
		const top0 = stackTop(taken, x0 + INDENT, SRC_TOP + SUM_H + 10);
		const L = unfolded
			? place(board, destBlocks(row, cards), tl, x0, fk, (k) => open.has(k), top0)
			: { placed: [], bottom: 0 };
		if (unfolded) taken.push({ right: Math.max(x0, ...L.placed.map((b) => b.x + BLOCK_W)), bottom: L.bottom });
		return { row, cards, fk, x0, width, unfolded, label: boardLabel(row, board, cards), ...L };
	});
}

const sprintSource = (row: SprintRow) => ({
	title: `${row.board.key} board`,
	how: "read" as const,
	lines: [
		{
			kind: "jira.board_sprints",
			text: `${row.next ? "next" : "active"} sprint "${row.sprint.name}"`,
		},
	],
	note: `${
		row.next
			? row.estimated
				? "The board's next sprint, dimmed because it has not started. Jira has no dates for it yet, so they are estimated from the active sprint's length, starting the day after it ends."
				: "The board's next sprint, dimmed because it has not started, on the same row as the active one. A board with no next sprint has none."
			: "One row per followed board with an active sprint. A board with no active sprint has no row."
	} ${MOBILE_RULE}`,
	field: "SprintRow.board, sprint",
});

function SprintBar({
	bar,
	lens,
	toggle,
	actions,
}: {
	bar: Bar;
	lens: Lens;
	toggle: (k: string) => void;
	actions: PageActions;
}) {
	const { row, label, fk, x0, width, unfolded, cards } = bar;
	const next = row.next;
	const count = cardCount(cards, lens);
	const n = count.sub ? `${count.text} · ${count.sub}` : count.text;
	const tip = row.estimated ? `${label.dates} · estimated from the active sprint's length` : label.dates;
	const openSheet = () => actions.openSheet({ kind: "sprint", board: row.board.key, next: true });
	return (
		<div
			className={`rel-cg spsrc${next ? " next" : ""}${row.estimated ? " estimated" : ""}`}
			data-testid="sprint-bar"
			data-sprint={next ? "next" : "active"}
			data-estimated={row.estimated || undefined}
			style={{ left: x0, top: SRC_TOP, width }}
		>
			<button
				type="button"
				className="csum"
				aria-expanded={unfolded}
				title={`${label.title} · ${tip} · ${n}`}
				onClick={() => toggle(fk)}
			>
				<span className="sl" data-sticky>
					<Chev open={unfolded} />
					<span>{label.title}</span>
					{next && <span className="passive">{label.dates}</span>}
					<b>{count.text}</b>
					{count.sub && (
						<span className="passive" data-testid="sprint-mobile-count">
							{count.sub}
						</span>
					)}
					<span className="routes">
						{label.routes.map((r) => (
							<span key={r.text} className="c" data-st={r.tone}>
								{r.text}
							</span>
						))}
					</span>
					{next && (
						<>
							<span
								role="button"
								tabIndex={0}
								className="lbn"
								onClick={(e) => {
									e.stopPropagation();
									openSheet();
								}}
								onKeyDown={(e) => {
									if (e.key !== "Enter" && e.key !== " ") return;
									e.stopPropagation();
									openSheet();
								}}
							>
								sprint ›
							</span>
							<SourceTag p={sprintSource(row)} />
						</>
					)}
				</span>
			</button>
		</div>
	);
}

function SprintBlocks({
	bar,
	board,
	tl,
	lens,
	toggle,
	actions,
}: {
	bar: Bar;
	board: Board;
	tl: Timeline;
	lens: Lens;
	toggle: (k: string) => void;
	actions: PageActions;
}) {
	const { row, fk, x0, unfolded, placed, bottom } = bar;
	const dim = row.next ? " next" : "";
	return (
		<Unfold open={unfolded} clipAt={{ top: DESTS_TOP, height: bottom - DESTS_TOP }}>
			{placed.length > 0 && <Wires board={board} placed={placed} x0={x0} tl={tl} className={dim} />}
			{placed.map((bk) => (
				<Moving
					track="blocks"
					key={bk.key}
					className={`rel-cg dest${dim}`}
					data={{
						"data-st": bk.tone,
						"data-dest": bk.key,
						"data-sprint": row.next ? "next" : "active",
						"data-testid": "sprint-dest-block",
					}}
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
	);
}

export function BoardSprintRow({
	rows,
	board,
	tl,
	lens,
	actions,
	onHide,
}: {
	rows: SprintRow[];
	board: Board;
	tl: Timeline;
	lens: Lens;
	actions: PageActions;
	onHide: (board: string) => void;
}) {
	const open = useReleasesUi((s) => s.open);
	const toggle = useReleasesUi((s) => s.toggleFold);
	const bars = layBars(rows, board, tl, lens, open);
	const { row, label, cards: shown } = bars[0];
	const h = Math.max(Math.max(...bars.map((b) => b.bottom)) + 4, SRC_TOP + SUM_H + 8, MIN_H);
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
								...sprintSource(row),
								lines: [
									{
										kind: "ao.config",
										text: `board ${row.board.id} is followed${row.board.ownsSprintBranch ? ", and owns the sprint branch" : ""}`,
									},
									...sprintSource(row).lines,
								],
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
							<span className="passive">{emptyCountText(shown, lens)}</span>
						)}
						<SourceTag p={DESTS_P} />
					</span>
				</button>
				<button
					type="button"
					className="hide"
					aria-label={`Hide ${row.board.key} sprint`}
					title={`Hide ${row.board.key} sprint${bars.length > 1 ? " with the next one" : ""}`}
					onClick={() => onHide(row.board.key)}
				>
					<EyeOff aria-hidden="true" />
				</button>
			</div>
			<div className="rel-trk" style={{ width: tl.width }}>
				{bars.map((bar) => (
					<SprintBar key={bar.fk} bar={bar} lens={lens} toggle={toggle} actions={actions} />
				))}
				{bars.map((bar) => (
					<SprintBlocks key={bar.fk} bar={bar} board={board} tl={tl} lens={lens} toggle={toggle} actions={actions} />
				))}
			</div>
		</Moving>
	);
}
