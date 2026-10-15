import { Snowflake } from "lucide-react";
import { Fragment } from "react";
import { barLine, hotfixLine, type BarPart } from "../../lib/releases/barLine";
import { dayLabel, shortDay } from "../../lib/releases/day";
import { barBox, DW, markerSpots, type AppLanes, type LaneItem, type Timeline } from "../../lib/releases/layout";
import { unknownText, worstTone, type Lens } from "../../lib/releases/lens";
import type { Board, Stage } from "../../lib/releases/types";
import { SEVERITY_META, STAGE_META, STATUS_TEXT, stageTone } from "../../lib/releases/vocab";
import type { PageActions } from "./ReleasesPage";

const MARKER_TOP = 39;

export function StageMark({
	stage,
	hotfix = false,
	className = "",
	style,
	onClick,
	now,
}: {
	stage: Stage;
	hotfix?: boolean;
	className?: string;
	style?: React.CSSProperties;
	onClick?: () => void;
	now: number;
}) {
	const tone = hotfix && stage.status !== "done" ? "hotfix" : stageTone(stage.status);
	const status = stage.status === "unknown" ? unknownText(stage.missing, now) : STATUS_TEXT[stage.status];
	const title = `${STAGE_META[stage.key].label} · ${stage.date ? dayLabel(stage.date) : "no date"} · ${status}${stage.detail ? `\n${stage.detail}` : ""}`;
	return (
		<button
			type="button"
			className={`rel-mk${stage.conventional ? " conv" : ""} ${className}`}
			data-st={tone}
			data-stage={stage.key}
			style={style}
			title={title}
			aria-label={title}
			onClick={onClick}
		>
			{stage.key === "freeze" ? (
				<Snowflake className="size-[11px]" strokeWidth={2.8} aria-hidden="true" />
			) : (
				STAGE_META[stage.key].letter
			)}
		</button>
	);
}

function Line2({ parts }: { parts: BarPart[] }) {
	return (
		<span className="l2">
			{parts.map((p, i) => (
				<Fragment key={p.id}>
					{i > 0 && <span className="sep">·</span>}
					<span data-part={p.id === "title" ? undefined : p.id} data-st={p.tone} className={p.tone ? "c" : undefined}>
						{p.emphasis ? (
							<>
								{p.text.slice(0, p.text.indexOf(p.emphasis))}
								<b>{p.emphasis}</b>
								{p.text.slice(p.text.indexOf(p.emphasis) + p.emphasis.length)}
							</>
						) : (
							p.text
						)}
					</span>
				</Fragment>
			))}
		</span>
	);
}

function TrainBar({
	it,
	tl,
	top,
	lens,
	actions,
	today,
}: {
	it: Extract<LaneItem, { kind: "train" }>;
	tl: Timeline;
	top: number;
	lens: Lens;
	actions: PageActions;
	today: string;
}) {
	const t = it.train;
	const box = barBox(tl, it.freeze, it.publish);
	const focused = t.id === lens.focus?.id;
	const hits = lens.attention.filter((h) => h.train === t.id && h.app === it.version.app);
	const flag = hits[0];
	const tone = t.phase === "next" ? "todo" : worstTone(it.stages);
	const cls = ["rel-bar", t.phase, focused && "focus", !it.markers && "quiet", it.offDates && "offtrain"]
		.filter(Boolean)
		.join(" ");
	const open = () => actions.openSheet({ kind: "version", train: t.id, app: it.version.app });
	return (
		<div
			role="button"
			tabIndex={0}
			className={cls}
			data-st={tone}
			data-train={t.id}
			data-testid={`bar-${it.version.app}-${t.id}`}
			data-fit
			style={{ left: box.left, width: box.width, top }}
			onClick={open}
			onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && open()}
		>
			<span className="bl" data-sticky>
				<span className="l1">
					<span className="mono">{it.version.version}</span>
					<button
						type="button"
						className={`rel-tt${focused ? " on" : ""}`}
						data-part="tt"
						title={`Train ${t.label}: focus it and open its sheet`}
						onClick={(e) => {
							e.stopPropagation();
							actions.setSearch({ focus: t.id, sheet: { kind: "train", train: t.id } });
						}}
					>
						{t.label}
					</button>
					<span className={`dt${it.offDates ? " off" : ""}`} data-part="dates">
						{shortDay(it.freeze)} → {shortDay(it.publish)}
						{it.offDates ? ` · not ${t.label}'s dates` : ""}
					</span>
				</span>
				<Line2 parts={barLine(it.version, today)} />
			</span>
			{flag && (
				<span className="fl" data-st={SEVERITY_META[flag.severity].tone} title={SEVERITY_META[flag.severity].label}>
					!
				</span>
			)}
		</div>
	);
}

function HotfixBar({
	it,
	tl,
	top,
	actions,
}: {
	it: Extract<LaneItem, { kind: "hotfix" }>;
	tl: Timeline;
	top: number;
	actions: PageActions;
}) {
	const box = barBox(tl, it.freeze, it.publish);
	const open = () => actions.openSheet({ kind: "hotfix", app: it.hotfix.app, version: it.hotfix.version });
	return (
		<div
			role="button"
			tabIndex={0}
			className={`rel-bar hf${it.markers ? "" : " quiet"}`}
			data-st="hotfix"
			data-testid={`hotfix-${it.hotfix.app}-${it.hotfix.version}`}
			data-fit
			style={{ left: box.left, width: box.width, top }}
			onClick={open}
			onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && open()}
		>
			<span className="bl" data-sticky>
				<span className="l1">
					<span className="mono">{it.hotfix.version}</span>
					<span className="hft">hotfix</span>
					<span className="dt" data-part="dates">
						{shortDay(it.freeze)} → {shortDay(it.publish)}
					</span>
				</span>
				<Line2 parts={hotfixLine(it.hotfix)} />
			</span>
		</div>
	);
}

function Markers({
	it,
	tl,
	y,
	actions,
	now,
}: {
	it: LaneItem;
	tl: Timeline;
	y: number;
	actions: PageActions;
	now: number;
}) {
	const f = tl.x(it.freeze) + DW / 2;
	const p = tl.x(it.publish) + DW / 2;
	const lo = tl.x(it.lo) + DW / 2;
	const hi = tl.x(it.hi) + DW / 2;
	const open = () =>
		it.kind === "train"
			? actions.openSheet({ kind: "version", train: it.train.id, app: it.version.app })
			: actions.openSheet({ kind: "hotfix", app: it.hotfix.app, version: it.hotfix.version });
	return (
		<>
			<div className="rel-mtrack" style={{ left: f, width: p - f, top: y + 8 }} />
			{lo < f && <div className="rel-mtrack out" style={{ left: lo, width: f - lo, top: y + 8 }} />}
			{hi > p && <div className="rel-mtrack out" style={{ left: p, width: hi - p, top: y + 8 }} />}
			{markerSpots(tl, it.stages).map(({ stage, left }) => (
				<StageMark
					key={stage.key}
					stage={stage}
					hotfix={it.kind === "hotfix"}
					style={{ left, top: y }}
					onClick={open}
					now={now}
				/>
			))}
		</>
	);
}

export function VersionLanes({
	board,
	tl,
	lanes,
	top,
	lens,
	actions,
}: {
	board: Board;
	tl: Timeline;
	lanes: AppLanes;
	top: number;
	lens: Lens;
	actions: PageActions;
}) {
	const now = Date.now();
	return (
		<>
			{lanes.items.map((it) => {
				const y = top + lanes.laneTop[it.lane];
				return (
					<Fragment key={`${it.kind}:${it.key}`}>
						{it.kind === "train" ? (
							<TrainBar it={it} tl={tl} top={y} lens={lens} actions={actions} today={board.today} />
						) : (
							<HotfixBar it={it} tl={tl} top={y} actions={actions} />
						)}
						{it.markers && <Markers it={it} tl={tl} y={y + MARKER_TOP} actions={actions} now={now} />}
					</Fragment>
				);
			})}
		</>
	);
}
