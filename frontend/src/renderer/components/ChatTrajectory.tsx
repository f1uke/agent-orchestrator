import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, ChevronRight, Clock, Search, X } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from "react";
import type { ChatEntry } from "../lib/chat-event";
import type { Transcript } from "../lib/chat-fold";
import { formatDuration } from "../lib/chat-time";
import {
	type Axis,
	type Bar,
	buildTrajectory,
	capText,
	filterRows,
	formatSpan,
	fullText,
	isFiltering,
	type KindFilter,
	LANES,
	layoutBars,
	rawEntries,
	type RowFilter,
	type RowKind,
	type TrajectoryRow,
	visibleRows,
} from "../lib/chat-trajectory";
import { measuredOrFallbackRect } from "../lib/virtual-rect";
import { cn } from "../lib/utils";
import { CopyButton } from "./CopyButton";
import { Input } from "./ui/input";

const ROW_PX = 28;
const RESULT_LINES = 12;
const RESULT_CHARS = 2000;
const RAW_CHARS = 20_000;
const FLASH_MS = 1400;
const AT_END_PX = 2;
const SCROLL_UP_KEYS = new Set(["ArrowUp", "PageUp", "Home"]);

const KIND_LABEL: Record<RowKind, string> = {
	user: "USER",
	assistant: "ASSISTANT",
	thinking: "THINKING",
	tool: "TOOL",
	task: "TASK",
	turn_end: "TURN END",
	notice: "NOTICE",
};

const AXES: Array<{ axis: Axis; label: string }> = [
	{ axis: "duration", label: "Duration" },
	{ axis: "turns", label: "Turns" },
	{ axis: "calls", label: "Calls" },
];

const LANE_LABEL = { input: "Input", model: "Model", tools: "Tools" } as const;

const distanceFromEnd = (el: HTMLElement) => el.scrollHeight - el.scrollTop - el.clientHeight;

function isEditable(target: EventTarget): boolean {
	return (
		target instanceof HTMLElement && (target.isContentEditable || target.closest("input, textarea, select") !== null)
	);
}

type Props = {
	transcript: Transcript;
	entries: readonly ChatEntry[];
	root: string;
};

export function ChatTrajectory({ transcript, entries, root }: Props) {
	const trajectory = useMemo(() => buildTrajectory(transcript, entries, { root }), [transcript, entries, root]);
	const [axis, setAxis] = useState<Axis>("duration");
	const [filter, setFilter] = useState<RowFilter>({ query: "", kind: "all" });
	const [openRows, setOpenRows] = useState<ReadonlySet<string>>(new Set());
	const [openAgents, setOpenAgents] = useState<ReadonlySet<string>>(new Set());
	const [flash, setFlash] = useState<string | null>(null);
	const [jump, setJump] = useState<string | null>(null);

	const hasRunning = trajectory.rows.some((r) => r.running);
	const [now, setNow] = useState(Date.now);
	useEffect(() => {
		if (!hasRunning) return;
		setNow(Date.now());
		const timer = setInterval(() => setNow(Date.now()), 1000);
		return () => clearInterval(timer);
	}, [hasRunning]);

	const shown = useMemo(
		() => (isFiltering(filter) ? filterRows(trajectory.rows, filter) : visibleRows(trajectory.rows, openAgents)),
		[trajectory.rows, filter, openAgents],
	);
	const bars = useMemo(() => layoutBars(trajectory, axis, now), [trajectory, axis, now]);

	const scrollRef = useRef<HTMLDivElement | null>(null);
	const followingRef = useRef(true);
	const draggingRef = useRef(false);
	const [following, setFollowingState] = useState(true);
	const setFollowing = useCallback((value: boolean) => {
		if (followingRef.current === value) return;
		followingRef.current = value;
		setFollowingState(value);
	}, []);

	const pinToEnd = useCallback(() => {
		const el = scrollRef.current;
		if (followingRef.current && el && distanceFromEnd(el) > AT_END_PX) el.scrollTop = el.scrollHeight;
	}, []);

	const virtualizer = useVirtualizer({
		count: shown.length,
		getScrollElement: () => scrollRef.current,
		estimateSize: () => ROW_PX,
		getItemKey: (index) => shown[index]?.key ?? index,
		overscan: 20,
		observeElementRect: measuredOrFallbackRect,
		useFlushSync: false,
		onChange: pinToEnd,
	});

	// The virtualizer learns of a scroll from the scroll event, a frame after pinToEnd writes it.
	const [, repaint] = useReducer((n: number) => n + 1, 0);
	useLayoutEffect(() => {
		pinToEnd();
		const el = scrollRef.current;
		if (el && virtualizer.scrollOffset !== el.scrollTop) {
			virtualizer.scrollOffset = el.scrollTop;
			repaint();
		}
	});

	useEffect(() => {
		const el = scrollRef.current;
		if (!el) return;
		const onWheel = (e: WheelEvent) => {
			if (e.deltaY < 0 && el.scrollTop > 0) setFollowing(false);
		};
		el.addEventListener("wheel", onWheel, { capture: true, passive: true });
		return () => el.removeEventListener("wheel", onWheel, { capture: true });
	}, [setFollowing]);

	const onScroll = (e: React.UIEvent<HTMLDivElement>) => {
		if (distanceFromEnd(e.currentTarget) <= AT_END_PX) setFollowing(true);
		else if (draggingRef.current) setFollowing(false);
	};
	const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
		if (e.target !== e.currentTarget) return;
		draggingRef.current = true;
		window.addEventListener("pointerup", () => (draggingRef.current = false), { once: true });
	};

	const toggleRow = useCallback(
		(key: string) => {
			setFollowing(false);
			setOpenRows((prev) => {
				const next = new Set(prev);
				if (!next.delete(key)) next.add(key);
				return next;
			});
		},
		[setFollowing],
	);
	const toggleAgent = useCallback((key: string) => {
		setOpenAgents((prev) => {
			const next = new Set(prev);
			if (!next.delete(key)) next.add(key);
			return next;
		});
	}, []);

	const onBar = useCallback(
		(key: string) => {
			const row = trajectory.rows.find((r) => r.key === key);
			if (!row) return;
			if (row.parent) setOpenAgents((prev) => new Set(prev).add(row.parent!));
			if (isFiltering(filter) && !filterRows(trajectory.rows, filter).includes(row)) {
				setFilter({ query: "", kind: "all" });
			}
			setFollowing(false);
			setJump(key);
		},
		[trajectory.rows, filter, setFollowing],
	);

	useEffect(() => {
		if (jump === null) return;
		const index = shown.findIndex((r) => r.key === jump);
		if (index < 0) return;
		virtualizer.scrollToIndex(index, { align: "start" });
		setFlash(jump);
		setJump(null);
	}, [jump, shown, virtualizer]);

	useEffect(() => {
		if (flash === null) return;
		const timer = setTimeout(() => setFlash(null), FLASH_MS);
		return () => clearTimeout(timer);
	}, [flash]);

	const jumpToLatest = () => {
		setFollowing(true);
		const el = scrollRef.current;
		if (el) el.scrollTop = el.scrollHeight;
	};

	return (
		<div className="chat-traj" data-axis={axis} data-calls={trajectory.calls} data-testid="chat-trajectory">
			<TrajectoryToolbar axis={axis} filter={filter} onAxis={setAxis} onFilter={setFilter} trajectory={trajectory} />
			<TimelineStrip bars={bars} onBar={onBar} />
			<div className="chat-traj__body">
				<div
					className="chat-traj__scroll"
					data-testid="chat-trajectory-rows"
					onKeyDown={(e) => {
						if (SCROLL_UP_KEYS.has(e.key) && !isEditable(e.target)) setFollowing(false);
					}}
					onPointerDown={onPointerDown}
					onScroll={onScroll}
					ref={scrollRef}
				>
					{shown.length === 0 ? (
						<div className="chat-empty">
							{trajectory.rows.length === 0 ? "Nothing in this session's log yet." : "No steps match."}
						</div>
					) : null}
					<div className="relative" style={{ height: virtualizer.getTotalSize() }}>
						{virtualizer.getVirtualItems().map((v) => {
							const row = shown[v.index];
							return (
								<div
									className="chat-traj__item"
									data-index={v.index}
									key={v.key}
									ref={virtualizer.measureElement}
									style={{ transform: `translateY(${v.start}px)` }}
								>
									<TrajectoryRowView
										agentOpen={openAgents.has(row.key)}
										entries={entries}
										flashing={flash === row.key}
										now={now}
										onAgent={toggleAgent}
										onToggle={toggleRow}
										open={openRows.has(row.key)}
										row={row}
									/>
								</div>
							);
						})}
					</div>
				</div>
				{following ? null : (
					<button
						aria-label="Jump to latest"
						className="chat-jump"
						onClick={jumpToLatest}
						title="Jump to latest"
						type="button"
					>
						<ArrowDown className="chat-icon" aria-hidden="true" />
					</button>
				)}
			</div>
		</div>
	);
}

const KIND_CHIPS: Array<{ kind: KindFilter; label: string }> = [
	{ kind: "all", label: "All" },
	{ kind: "tools", label: "Tools" },
	{ kind: "errors", label: "Errors" },
	{ kind: "turns", label: "Turns" },
];

function TrajectoryToolbar({
	axis,
	filter,
	trajectory,
	onAxis,
	onFilter,
}: {
	axis: Axis;
	filter: RowFilter;
	trajectory: { rows: readonly TrajectoryRow[]; calls: number; errors: number; turns: number };
	onAxis: (axis: Axis) => void;
	onFilter: (filter: RowFilter) => void;
}) {
	const count: Record<KindFilter, number | undefined> = {
		all: undefined,
		tools: trajectory.calls,
		errors: trajectory.errors,
		turns: trajectory.turns,
	};
	return (
		<div className="chat-traj__bar">
			<div aria-label="Timeline axis" className="chat-traj__seg" role="group">
				{AXES.map((a) => (
					<button
						aria-pressed={axis === a.axis}
						className="chat-traj__segbtn"
						key={a.axis}
						onClick={() => onAxis(a.axis)}
						type="button"
					>
						{a.axis === "duration" ? <Clock className="chat-icon" aria-hidden="true" /> : null}
						{a.label}
					</button>
				))}
			</div>
			<div aria-label="Show steps" className="chat-traj__chips" role="group">
				{KIND_CHIPS.map((chip) => (
					<button
						aria-pressed={filter.kind === chip.kind}
						className="chat-traj__chip"
						key={chip.kind}
						onClick={() => onFilter({ ...filter, kind: chip.kind })}
						type="button"
					>
						{chip.label}
						{count[chip.kind] !== undefined ? (
							<>
								{" "}
								<span className="chat-traj__count">{count[chip.kind]}</span>
							</>
						) : null}
					</button>
				))}
			</div>
			<div className="chat-traj__search">
				<Search aria-hidden="true" className="chat-icon chat-traj__searchicon" />
				<Input
					aria-label="Search steps"
					className="h-7 pl-7 pr-7 text-xs"
					onChange={(e) => onFilter({ ...filter, query: e.target.value })}
					onKeyDown={(e) => {
						if (e.key !== "Escape") return;
						e.preventDefault();
						onFilter({ ...filter, query: "" });
					}}
					placeholder="Search"
					type="search"
					value={filter.query}
				/>
				{filter.query !== "" ? (
					<button
						aria-label="Clear search"
						className="chat-traj__clear"
						onClick={() => onFilter({ ...filter, query: "" })}
						type="button"
					>
						<X className="chat-icon" aria-hidden="true" />
					</button>
				) : null}
			</div>
		</div>
	);
}

type Hover = { bar: Bar; left: number };

function TimelineStrip({ bars, onBar }: { bars: readonly Bar[]; onBar: (key: string) => void }) {
	const [hover, setHover] = useState<Hover>();
	const stripRef = useRef<HTMLDivElement | null>(null);
	const show = (bar: Bar, el: HTMLElement) => {
		const strip = stripRef.current?.getBoundingClientRect();
		const rect = el.getBoundingClientRect();
		setHover({ bar, left: strip ? rect.left - strip.left + rect.width / 2 : 0 });
	};
	return (
		<div className="chat-traj__strip" data-testid="chat-trajectory-strip">
			<div className="chat-traj__lanelabels" aria-hidden="true">
				{LANES.map((lane) => (
					<span key={lane}>{LANE_LABEL[lane]}</span>
				))}
			</div>
			<div className="chat-traj__lanes" onMouseLeave={() => setHover(undefined)} ref={stripRef}>
				{LANES.map((lane) => (
					<div className="chat-traj__lane" data-lane={lane} key={lane}>
						{bars
							.filter((b) => b.lane === lane)
							.map((b) => (
								<button
									aria-label={`${LANE_LABEL[lane]}: ${b.label}`}
									className={cn("chat-traj__bar-mark", b.error && "chat-traj__bar-mark--error")}
									data-key={b.key}
									data-lane={lane}
									data-running={b.running ? "" : undefined}
									key={`${lane}:${b.key}`}
									onBlur={() => setHover(undefined)}
									onClick={() => onBar(b.key)}
									onFocus={(e) => show(b, e.currentTarget)}
									onMouseEnter={(e) => show(b, e.currentTarget)}
									style={{ left: `${b.start * 100}%`, width: `max(3px, ${(b.end - b.start) * 100}%)` }}
									type="button"
								/>
							))}
					</div>
				))}
				{hover ? (
					<div className="chat-traj__tip" role="tooltip" style={{ left: hover.left }}>
						<b>{LANE_LABEL[hover.bar.lane]}</b> · Turn {hover.bar.turn}
						<div>{hover.bar.label}</div>
						{hover.bar.lane === "input" ? null : <div className="chat-traj__tipms">{formatSpan(hover.bar.ms)}</div>}
					</div>
				) : null}
			</div>
		</div>
	);
}

function TrajectoryRowView({
	row,
	open,
	agentOpen,
	flashing,
	now,
	entries,
	onToggle,
	onAgent,
}: {
	row: TrajectoryRow;
	open: boolean;
	agentOpen: boolean;
	flashing: boolean;
	now: number;
	entries: readonly ChatEntry[];
	onToggle: (key: string) => void;
	onAgent: (key: string) => void;
}) {
	const elapsed = row.running && !Number.isNaN(row.t0) ? formatDuration(now - row.t0) : "";
	const took = !Number.isNaN(row.t0) && !Number.isNaN(row.t1) ? formatSpan(row.t1 - row.t0) : "";
	return (
		<div
			className={cn("chat-traj__row", row.startsTurn && "chat-traj__row--turn", row.parent && "chat-traj__row--nested")}
			data-flash={flashing ? "" : undefined}
			data-key={row.key}
			data-kind={row.kind}
			data-open={open ? "" : undefined}
			data-tone={row.tone}
		>
			<span className="chat-traj__gutter">{row.startsTurn ? `Turn ${row.turn}` : ""}</span>
			<button aria-expanded={open} className="chat-traj__head" onClick={() => onToggle(row.key)} type="button">
				<span className="chat-traj__badge" data-kind={row.kind}>
					{KIND_LABEL[row.kind]}
				</span>
				<span className="chat-traj__text">
					{row.title !== "" ? <span className="chat-traj__title">{row.title}</span> : null}
					{row.detail !== "" ? <span className="chat-traj__detail">{row.detail}</span> : null}
					{row.outcome !== "" ? (
						<>
							<span aria-hidden="true" className="chat-traj__arrow">
								→
							</span>
							<span className="chat-traj__outcome">{row.outcome}</span>
						</>
					) : null}
					{row.running ? <span className="chat-traj__outcome chat-traj__outcome--live">running</span> : null}
				</span>
				<span className="chat-traj__meta">{row.running ? elapsed : took}</span>
			</button>
			{row.children > 0 ? (
				<button
					aria-expanded={agentOpen}
					aria-label={`${agentOpen ? "Hide" : "Show"} ${row.children} sub-agent ${row.children === 1 ? "call" : "calls"}`}
					className="chat-traj__agent"
					onClick={() => onAgent(row.key)}
					type="button"
				>
					<ChevronRight className={cn("chat-chev", agentOpen && "chat-chev--open")} aria-hidden="true" />
					{row.children} {row.children === 1 ? "call" : "calls"}
				</button>
			) : (
				<span />
			)}
			{open ? <RowDetail entries={entries} row={row} /> : null}
		</div>
	);
}

function Capped({ label, text, error, capped }: { label: string; text: string; error?: boolean; capped: boolean }) {
	const [all, setAll] = useState(false);
	const cut = useMemo(() => (capped ? capText(text, RESULT_LINES, RESULT_CHARS) : { text, hidden: 0 }), [capped, text]);
	const shownText = all ? text : cut.text;
	return (
		<section className="chat-traj__section">
			<header>
				<span>{label}</span>
				<CopyButton named={false} value={text} what={`${label.toLowerCase()}`} />
			</header>
			<pre className={cn("chat-traj__pre", error && "chat-traj__pre--error")} data-section={label.toLowerCase()}>
				{shownText}
			</pre>
			{cut.hidden > 0 && !all ? (
				<button className="chat-traj__more" onClick={() => setAll(true)} type="button">
					Show all ({cut.hidden} more {cut.hidden === 1 ? "line" : "lines"})
				</button>
			) : null}
		</section>
	);
}

function RowDetail({ row, entries }: { row: TrajectoryRow; entries: readonly ChatEntry[] }) {
	const text = useMemo(() => fullText(row), [row]);
	const [raw, setRaw] = useState(false);
	const source = useMemo(() => (raw ? rawEntries(row, entries) : []), [raw, row, entries]);
	return (
		<div className="chat-traj__detail-panel" data-testid="chat-trajectory-detail">
			{text.input !== "" ? <Capped capped={false} label="Input" text={text.input} /> : null}
			{text.result !== "" ? <Capped capped error={text.resultIsError} label="Result" text={text.result} /> : null}
			<button aria-expanded={raw} className="chat-traj__rawlink" onClick={() => setRaw((v) => !v)} type="button">
				Raw events
			</button>
			{raw ? (
				<div className="chat-traj__raw" data-testid="chat-trajectory-raw">
					{source.map((e) => (
						<div className="chat-traj__rawentry" data-seq={e.seq} key={e.seq}>
							<div className="chat-traj__rawhead">
								#{e.seq} · {e.dir} · {e.ts}
							</div>
							<pre className="chat-traj__pre">{JSON.stringify(e.payload, null, 2).slice(0, RAW_CHARS)}</pre>
						</div>
					))}
				</div>
			) : null}
		</div>
	);
}
