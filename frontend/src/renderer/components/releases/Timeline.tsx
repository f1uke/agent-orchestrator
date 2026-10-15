import { animate } from "motion/react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from "react";
import { addDays, dayOfMonth, monthYear, weekday, weekdayShort } from "../../lib/releases/day";
import { cardGroups, groupRowCount, jiraUrl } from "../../lib/releases/cards";
import {
	appLanes,
	barBox,
	cardGroupTop,
	DW,
	LABEL_W,
	llbBlockH,
	rowHeight,
	timeline,
	type AppLanes,
	type Timeline as TL,
} from "../../lib/releases/layout";
import type { Lens } from "../../lib/releases/lens";
import { llbGroupsFor, llbLanes } from "../../lib/releases/llb";
import { momentumDistance, moveKeys, type ReleaseMotion } from "../../lib/releases/motion";
import { BAR_DROP_ORDER, fitParts, labelHidden, type BarPartId } from "../../lib/releases/stickyFit";
import type { Board, BoardApp, TrainKey } from "../../lib/releases/types";
import { LAYER_ORDER, type Layer, type Layers, type ReleasesSearch } from "../../lib/releases/url";
import { useReleasesUi } from "../../stores/releases-ui";
import { BoardSprintRow } from "./BoardSprintRows";
import { CardGroupBox } from "./CardGroups";
import { LongLivedSummary, type BarSpot } from "./LongLivedSummary";
import { MoveRoot, Moving, useReleaseMotion } from "./ReleaseMotion";
import type { PageActions } from "./ReleasesPage";
import { StickyAppLabel } from "./StickyAppLabel";
import { VersionLanes } from "./VersionLanes";

const LAYER_NAMES: Record<Layer, string> = {
	sprint: "Sprint cards",
	llb: "Long-lived",
	cards: "Cards",
	past: "History",
};

export function Timeline({
	board,
	url,
	lens,
	layers,
	actions,
}: {
	board: Board;
	url: ReleasesSearch;
	lens: Lens;
	layers: Layers;
	actions: PageActions;
}) {
	const tl = useMemo(() => timeline(board), [board]);
	const sc = useRef<HTMLDivElement>(null);
	const canvas = useRef<HTMLDivElement>(null);
	const mo = useReleaseMotion();
	const folds = useReleasesUi((s) => s.open);
	const focusFreeze = lens.focus?.freeze;
	const focusId = lens.focus?.id ?? null;

	useLayoutEffect(() => {
		const el = sc.current;
		const saved = useReleasesUi.getState().scroll;
		if (el && saved && saved.focus === focusId) {
			el.scrollLeft = saved.left;
			el.scrollTop = saved.top;
			return;
		}
		if (!el || !focusFreeze) return;
		const to = Math.max(0, tl.x(addDays(focusFreeze, -8)));
		if (mo.reduced || el.scrollLeft === 0) {
			el.scrollLeft = to;
			return;
		}
		const a = animate(el.scrollLeft, to, { ...mo.axis, onUpdate: (v) => (el.scrollLeft = v) });
		return () => a.stop();
		// Scroll only when the focus moves, never on a refetch.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [focusFreeze]);

	const fit = useCallback(() => {
		const root = canvas.current;
		const box = sc.current?.getBoundingClientRect();
		if (!root || !box) return;
		const edge = box.left + LABEL_W;
		for (const bar of root.querySelectorAll<HTMLElement>("[data-fit]")) fitBar(bar);
		for (const el of root.querySelectorAll<HTMLElement>("[data-sticky]")) {
			const r = el.parentElement?.getBoundingClientRect();
			if (r)
				el.classList.toggle(
					"tight",
					labelHidden({ boxLeft: r.left, boxRight: r.right, labelWidth: el.offsetWidth, edge }),
				);
		}
	}, []);
	useEffect(() => {
		const el = sc.current;
		if (!el) return;
		let queued = 0;
		const queue = () => {
			if (queued) return;
			queued = requestAnimationFrame(() => {
				queued = 0;
				fit();
				useReleasesUi.getState().setScroll({ left: el.scrollLeft, top: el.scrollTop, focus: focusId });
			});
		};
		queue();
		el.addEventListener("scroll", queue, { passive: true });
		window.addEventListener("resize", queue);
		return () => {
			el.removeEventListener("scroll", queue);
			window.removeEventListener("resize", queue);
			cancelAnimationFrame(queued);
		};
	});

	usePan(sc, mo);

	return (
		<div className="rel-sc" ref={sc} data-testid="release-timeline">
			<MoveRoot keys={moveKeys(url, layers, folds)}>
				<div className="rel-canvas" ref={canvas} style={{ width: LABEL_W + tl.width }}>
					<Background tl={tl} />
					<Axis tl={tl} layers={layers} />
					<div className="rel-sched">
						<div className="rel-row">
							<div className="rel-lab">
								<span className="rel-eyebrow">Sprint</span>
							</div>
							<div className="rel-trk" style={{ width: tl.width, height: 30 }}>
								{board.sprints
									.filter((s) => s.board.ownsSprintBranch && s.sprint.start && s.sprint.end)
									.map((s) => (
										<div
											key={s.sprint.id}
											className="rel-spr active"
											style={{
												left: tl.x(s.sprint.start ?? ""),
												width: tl.x(s.sprint.end ?? "") - tl.x(s.sprint.start ?? ""),
											}}
										>
											<span className="sl" data-sticky>
												<span>Sprint {s.number} · active</span>
												<span className="passive">ships in #{s.number + 1}</span>
											</span>
										</div>
									))}
							</div>
						</div>
						{layers.sprint &&
							board.sprints.map((row) => (
								<BoardSprintRow key={row.board.key} row={row} board={board} tl={tl} lens={lens} actions={actions} />
							))}
					</div>
					{lens.apps.map((a) => (
						<AppRow key={a.id} app={a} board={board} tl={tl} url={url} lens={lens} layers={layers} actions={actions} />
					))}
				</div>
			</MoveRoot>
		</div>
	);
}

const SUM_H = 26;
const CARD_ROW_H = 24;

function barSpot(L: AppLanes, tl: TL, llbH: number, train: TrainKey): BarSpot | null {
	const it = L.items.find((x) => x.kind === "train" && x.key === train);
	if (!it) return null;
	const box = barBox(tl, it.freeze, it.publish);
	return { left: box.left, right: box.left + box.width, top: llbH + L.laneTop[it.lane] };
}

function AppRow({
	app: a,
	board,
	tl,
	url,
	lens,
	layers,
	actions,
}: {
	app: BoardApp;
	board: Board;
	tl: TL;
	url: ReleasesSearch;
	lens: Lens;
	layers: Layers;
	actions: PageActions;
}) {
	const open = useReleasesUi((s) => s.open);
	const scoped = lens.me ? { ...board, llbs: lens.llbs, hotfixes: board.hotfixes.filter(lens.keepHotfix) } : board;
	const L = appLanes(scoped, tl, a.id, { focus: lens.focus?.id ?? null, past: layers.past, hotfixes: url.hotfixes });
	const groups = layers.llb ? llbGroupsFor(scoped, a.id) : [];
	const lanes = groups.length ? llbLanes(scoped, a.id, tl) : [];
	const llbH = llbBlockH(lanes.length, open.has(`${a.id}:llbs`));
	const isOpen = (k: string) => open.has(k);
	const jira = (key: string) => jiraUrl(board, key);
	const boxes = (layers.cards ? cardGroups(board, a.id) : []).map((g) => {
		const it = L.items.find((x) => x.kind === "train" && x.key === g.train.id);
		const top = it ? cardGroupTop(L, it, tl, llbH) : llbH + 8 + L.lanesH + 2;
		const rows = open.has(g.key) ? groupRowCount(g, isOpen) : 0;
		return {
			g,
			left: tl.x(it?.freeze ?? g.train.freeze ?? tl.start) + 2,
			top,
			bottom: top + SUM_H + rows * CARD_ROW_H,
		};
	});
	const cardsBottom = Math.max(0, ...boxes.map((b) => b.bottom));
	return (
		<Moving
			track="rows"
			appear
			className="rel-row rel-app"
			data={{ "data-app": a.id }}
			height={rowHeight({ llbH, lanesH: L.lanesH, cardsBottom })}
		>
			<StickyAppLabel app={a} lens={lens} actions={actions} />
			<div className="rel-trk" style={{ width: tl.width }}>
				{groups.length > 0 && (
					<LongLivedSummary
						app={a.id}
						groups={groups}
						lanes={lanes}
						board={board}
						focus={lens.focus}
						width={tl.width}
						height={llbH + L.lanesH + 8}
						barOf={(t) => barSpot(L, tl, llbH, t)}
						mine={lens.me}
						actions={actions}
					/>
				)}
				<Moving track="blocks" className="rel-below" style={{ top: llbH, width: tl.width }}>
					<VersionLanes board={scoped} tl={tl} lanes={L} top={0} lens={lens} actions={actions} />
					{boxes.map(({ g, left, top }) => (
						<CardGroupBox
							key={g.key}
							g={g}
							board={board}
							left={left}
							top={top - llbH}
							focused={g.train.id === lens.focus?.id}
							face={lens.face}
							actions={actions}
							jira={jira}
						/>
					))}
				</Moving>
			</div>
		</Moving>
	);
}

function fitBar(bar: HTMLElement): void {
	const label = bar.querySelector<HTMLElement>(".bl");
	if (!label) return;
	const parts = new Map<BarPartId, HTMLElement>();
	for (const el of bar.querySelectorAll<HTMLElement>("[data-part]")) parts.set(el.dataset.part as BarPartId, el);
	const apply = (hidden: ReadonlySet<BarPartId>) => {
		for (const [id, el] of parts) el.hidden = hidden.has(id);
		const line2 = [...bar.querySelectorAll<HTMLElement>(".l2 > [data-part]")];
		for (const [i, el] of line2.entries()) {
			const sep = el.previousElementSibling as HTMLElement | null;
			if (sep?.classList.contains("sep")) sep.hidden = el.hidden || line2.slice(0, i).every((x) => x.hidden);
		}
	};
	const order = BAR_DROP_ORDER.filter((p) => parts.has(p));
	const hidden = fitParts(order, bar.clientWidth, (h) => {
		apply(h);
		return label.scrollWidth;
	});
	apply(hidden);
	bar.title = hidden.size ? `${label.innerText.replace(/\s+/g, " ")} (click for the rest)` : "";
}

function Background({ tl }: { tl: TL }) {
	const first = (6 - weekday(tl.start) + 7) % 7;
	const off = first * DW;
	return (
		<div
			className="rel-bg"
			style={{
				width: tl.width,
				background: `repeating-linear-gradient(90deg, transparent 0 ${off}px, color-mix(in srgb, var(--fg) 2.2%, transparent) ${off}px ${off + 2 * DW}px, transparent ${off + 2 * DW}px ${7 * DW}px)`,
			}}
		>
			<div className="todaycol" style={{ left: tl.x(tl.today), width: DW }} />
			<div className="today" style={{ left: tl.x(tl.today) + DW / 2 }} />
		</div>
	);
}

function Axis({ tl, layers }: { tl: TL; layers: Layers }) {
	const setLayer = useReleasesUi((s) => s.setLayer);
	const days = Array.from({ length: tl.days }, (_, i) => addDays(tl.start, i));
	const months = days.filter((d, i) => i === 0 || dayOfMonth(d) === 1);
	return (
		<div className="rel-row rel-axis">
			<div className="rel-lab">
				<div className="rel-layers" role="group" aria-label="Layers">
					<span className="rel-eyebrow lt">Show</span>
					{LAYER_ORDER.map((k) => (
						<button
							key={k}
							type="button"
							className="rel-lyr"
							aria-pressed={layers[k]}
							onClick={() => setLayer(k, !layers[k])}
						>
							{LAYER_NAMES[k]}
						</button>
					))}
				</div>
			</div>
			<div className="rel-trk" style={{ width: tl.width }}>
				{months.map((d, i) => {
					const next = months[i + 1];
					const w = (next ? tl.x(next) : tl.width) - tl.x(d);
					return (
						<div key={d} className="rel-mon" style={{ left: tl.x(d), width: w }}>
							<span>{monthYear(d)}</span>
						</div>
					);
				})}
				{days.map((d) => {
					const we = weekday(d) === 0 || weekday(d) === 6;
					return (
						<div
							key={d}
							className={`rel-day${we ? " we" : ""}${d === tl.today ? " td" : ""}`}
							style={{ left: tl.x(d), width: DW }}
						>
							<small>{weekdayShort(d)}</small>
							<span>{dayOfMonth(d)}</span>
						</div>
					);
				})}
			</div>
		</div>
	);
}

function usePan(sc: React.RefObject<HTMLDivElement | null>, mo: ReleaseMotion) {
	useEffect(() => {
		const el = sc.current;
		if (!el) return;
		type Drag = { x: number; y: number; sl: number; st: number; t: number; vx: number; vy: number; moved: boolean };
		let drag: Drag | null = null;
		let inertia: { stop: () => void }[] = [];
		const stopInertia = () => {
			for (const a of inertia) a.stop();
			inertia = [];
		};
		const down = (e: PointerEvent) => {
			if (e.button !== 0 || (e.target as HTMLElement).closest("button, a, input")) return;
			stopInertia();
			drag = {
				x: e.clientX,
				y: e.clientY,
				sl: el.scrollLeft,
				st: el.scrollTop,
				t: performance.now(),
				vx: 0,
				vy: 0,
				moved: false,
			};
		};
		const move = (e: PointerEvent) => {
			if (!drag) return;
			const dx = e.clientX - drag.x;
			const dy = e.clientY - drag.y;
			if (!drag.moved && Math.hypot(dx, dy) < 4) return;
			if (!drag.moved) el.classList.add("panning");
			const now = performance.now();
			const dt = Math.max(1, now - drag.t);
			const left = drag.sl - dx;
			const top = drag.st - dy;
			drag.vx = (left - el.scrollLeft) / dt;
			drag.vy = (top - el.scrollTop) / dt;
			drag.t = now;
			drag.moved = true;
			el.scrollLeft = left;
			el.scrollTop = top;
		};
		const up = () => {
			if (!drag) return;
			el.classList.remove("panning");
			if (drag.moved && !mo.reduced) {
				const glide = (from: number, by: number, set: (v: number) => void) =>
					by ? [animate(from, from + by, { ...mo.momentum, onUpdate: set })] : [];
				inertia = [
					...glide(el.scrollLeft, momentumDistance(drag.vx), (v) => (el.scrollLeft = v)),
					...glide(el.scrollTop, momentumDistance(drag.vy), (v) => (el.scrollTop = v)),
				];
			}
			drag = null;
		};
		el.addEventListener("pointerdown", down);
		window.addEventListener("pointermove", move);
		window.addEventListener("pointerup", up);
		return () => {
			stopInertia();
			el.removeEventListener("pointerdown", down);
			window.removeEventListener("pointermove", move);
			window.removeEventListener("pointerup", up);
		};
	}, [sc, mo]);
}
