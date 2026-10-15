import { GitFork } from "lucide-react";
import { llbWire, shortBranch, targetTrain, urgency, type LLBLane } from "../../lib/releases/llb";
import type { Board, LLBGroup, Train, TrainKey } from "../../lib/releases/types";
import { useReleasesUi } from "../../stores/releases-ui";
import { Chev } from "./CardGroups";
import type { PageActions } from "./ReleasesPage";

export type BarSpot = { left: number; right: number; top: number };

export function LongLivedSummary({
	app,
	groups,
	lanes,
	board,
	focus,
	width,
	height,
	barOf,
	mine,
	actions,
}: {
	app: string;
	groups: LLBGroup[];
	lanes: LLBLane[];
	board: Board;
	focus: Train | null;
	width: number;
	height: number;
	barOf: (train: TrainKey) => BarSpot | null;
	mine: boolean;
	actions: PageActions;
}) {
	const key = `${app}:llbs`;
	const open = useReleasesUi((s) => s.open.has(key));
	const toggle = useReleasesUi((s) => s.toggleFold);
	const due = groups.filter((g) => focus && targetTrain(g) === focus.id);
	const none = groups.filter((g) => g.target.kind === "none").length;
	return (
		<>
			<div className="rel-llbsum" style={{ top: 6, width }} data-testid="llb-summary" data-app={app}>
				<button type="button" className="ls" aria-expanded={open} onClick={() => toggle(key)}>
					<Chev open={open} />
					<GitFork className="ic size-3" aria-hidden="true" />
					<b>
						{groups.length} long-lived{mine ? " of mine" : ""}
					</b>
					{due.length > 0 && focus && (
						<>
							<span className="sep">·</span>
							<span className="c" data-st={urgency(due[0], board)}>
								{due.map((g) => g.plan?.name ?? g.name).join(", ")} → {focus.label}
							</span>
						</>
					)}
					{none > 0 && (
						<>
							<span className="sep">·</span>
							<span className="passive">{none} no target</span>
						</>
					)}
				</button>
			</div>
			{open && (
				<>
					<svg className="rel-wires" width={width} height={height} data-testid="llb-wires" aria-hidden="true">
						{lanes.map((l) => {
							const bar = l.train ? barOf(l.train) : null;
							if (!bar) return null;
							const w = llbWire(l, bar);
							const tone = w.planned ? urgency(l.group, board) : l.tone === "failed" ? "failed" : "done";
							return (
								<g key={`${l.branch.app}:${l.branch.branch}`} data-st={tone} data-wire={l.branch.branch}>
									<path className={w.planned ? "plan" : ""} d={w.d} />
									<circle cx={w.dot.x} cy={w.dot.y} r={2.5} />
								</g>
							);
						})}
					</svg>
					{lanes.map((l) => (
						<LaneRow
							key={`${l.branch.app}:${l.branch.branch}`}
							lane={l}
							wired={!!(l.train && barOf(l.train))}
							board={board}
							actions={actions}
						/>
					))}
				</>
			)}
		</>
	);
}

function LaneRow({
	lane: l,
	wired,
	board,
	actions,
}: {
	lane: LLBLane;
	wired: boolean;
	board: Board;
	actions: PageActions;
}) {
	const width = l.xr - l.x0;
	const aim = !wired && l.xr > l.x1;
	return (
		<button
			type="button"
			className={`rel-llb${l.open ? " open" : ""}${width < 260 ? " short" : ""}`}
			data-st={l.tone}
			data-testid="llb-lane"
			data-branch={l.branch.branch}
			style={{ left: l.x0, width, top: l.top }}
			onClick={() => actions.openSheet({ kind: "llb", id: l.group.id })}
		>
			<span className="lb-ln" style={{ right: l.xr - l.x1 }} />
			{l.ticks.map((t, i) => (
				<i key={`${t.key}-${i}`} className="tick" data-st={t.tone} style={{ left: t.x }} title={t.title} />
			))}
			{aim && (
				<span className="aim" data-st={urgency(l.group, board)} style={{ left: l.x1 - l.x0, width: l.xr - l.x1 }} />
			)}
			{!l.open && <span className="lb-end" style={{ right: l.xr - l.x1 - 6 }} />}
			<span className="lb-lbl" data-sticky>
				<b>{shortBranch(l.branch.branch)}</b>
				{l.target && (
					<span className={`tgt${l.target.tone ? "" : " none"}`} data-st={l.target.tone ?? undefined}>
						{l.target.text}
					</span>
				)}
				<span>
					{l.keys} card{l.keys === 1 ? "" : "s"}
				</span>
				{(l.accepted || l.state.tone === "failed" || l.state.tone === "waiting") && (
					<span className="c" data-st={l.state.tone}>
						{l.state.label}
					</span>
				)}
			</span>
		</button>
	);
}
