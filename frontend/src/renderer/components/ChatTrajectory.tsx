import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, ChevronRight, Clock, Search, X } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from "react";
import type { ContextInjection, ModelContext, ModelShape } from "../lib/chat-api";
import { sizeText } from "../lib/chat-docs";
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
	injectionText,
	isFiltering,
	type KindFilter,
	type Layout,
	LANES,
	layoutBars,
	rawEntries,
	type RowFilter,
	type RowKind,
	sourceLabel,
	type TrajectoryRow,
	visibleRows,
} from "../lib/chat-trajectory";
import { measuredOrFallbackRect } from "../lib/virtual-rect";
import { cn } from "../lib/utils";
import { CopyButton } from "./CopyButton";
import { Input } from "./ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

const ROW_PX = 28;
const RESULT_LINES = 12;
const RESULT_CHARS = 2000;
const RAW_CHARS = 20_000;
const FLASH_MS = 1400;
const AT_END_PX = 2;
const SCROLL_UP_KEYS = new Set(["ArrowUp", "PageUp", "Home"]);

const KIND_LABEL: Record<RowKind, string> = {
	system: "SYSTEM",
	context: "CONTEXT",
	user: "USER",
	assistant: "ASSISTANT",
	thinking: "THINKING",
	tool: "TOOL",
	task: "TASK",
	bash: "BASH",
	turn_end: "TURN END",
	notice: "NOTICE",
};

const AXES: Array<{ axis: Axis; label: string }> = [
	{ axis: "steps", label: "Steps" },
	{ axis: "duration", label: "Duration" },
];

const STEP_PX = 12;

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
	/** What the agent was given; absent where the harness writes no transcript AO can read. */
	loadContext?: () => Promise<ModelContext | null>;
};

const PANE_KINDS: ReadonlySet<RowKind> = new Set(["system", "context"]);

function useModelContext(load: Props["loadContext"], transcript: Transcript): ModelContext | null {
	const turnsEnded = useMemo(() => transcript.items.filter((i) => i.kind === "turn_end").length, [transcript.items]);
	const [loaded, setLoaded] = useState<{ from: Props["loadContext"]; context: ModelContext | null }>();
	useEffect(() => {
		if (!load) return;
		let live = true;
		void load().then((context) => {
			if (live) setLoaded({ from: load, context });
		});
		return () => {
			live = false;
		};
	}, [load, turnsEnded]);
	return loaded && loaded.from === load ? loaded.context : null;
}

export function ChatTrajectory({ transcript, entries, root, loadContext }: Props) {
	const context = useModelContext(loadContext, transcript);
	const trajectory = useMemo(
		() => buildTrajectory(transcript, entries, { root, context }),
		[transcript, entries, root, context],
	);
	const [paneKey, setPaneKey] = useState<string | null>(null);
	const paneRow = paneKey === null ? undefined : trajectory.rows.find((r) => r.key === paneKey);
	const [axis, setAxis] = useState<Axis>("steps");
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
	const layout = useMemo(() => layoutBars(trajectory, axis, now), [trajectory, axis, now]);

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
		(key: string, kind: RowKind) => {
			setFollowing(false);
			if (PANE_KINDS.has(kind)) {
				setPaneKey((prev) => (prev === key ? null : key));
				return;
			}
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
			if (PANE_KINDS.has(row.kind)) setPaneKey(key);
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

	const closePane = useCallback(() => setPaneKey(null), []);

	const jumpToLatest = () => {
		setFollowing(true);
		const el = scrollRef.current;
		if (el) el.scrollTop = el.scrollHeight;
	};

	return (
		<div
			className="chat-traj"
			data-axis={axis}
			data-calls={trajectory.calls}
			data-testid="chat-trajectory"
			// Handled here so the chat pane does not also read this Esc as "interrupt".
			onKeyDown={(e) => {
				if (e.key !== "Escape" || !paneRow || e.defaultPrevented || isEditable(e.target)) return;
				e.preventDefault();
				closePane();
			}}
		>
			<TrajectoryToolbar axis={axis} filter={filter} onAxis={setAxis} onFilter={setFilter} trajectory={trajectory} />
			<TimelineStrip axis={axis} layout={layout} onBar={onBar} />
			<div className="chat-traj__body">
				<div className="chat-traj__list">
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
											open={PANE_KINDS.has(row.kind) ? paneKey === row.key : openRows.has(row.key)}
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
				{paneRow ? <ContextPane onClose={closePane} row={paneRow} /> : null}
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

function barStyle(b: Bar, stepped: boolean): React.CSSProperties {
	const left = `${b.start * 100}%`;
	const width = `${(b.end - b.start) * 100}%`;
	return stepped
		? { left: `calc(${left} + 1px)`, width: `max(3px, calc(${width} - 2px))` }
		: { left, width: `max(3px, ${width})` };
}

function TimelineStrip({ axis, layout, onBar }: { axis: Axis; layout: Layout; onBar: (key: string) => void }) {
	const { bars, dividers } = layout;
	const [hover, setHover] = useState<Hover>();
	const lanesRef = useRef<HTMLDivElement | null>(null);
	const scrollRef = useRef<HTMLDivElement | null>(null);
	const scrolls = axis === "steps";

	useLayoutEffect(() => {
		const el = scrollRef.current;
		if (scrolls && el) el.scrollLeft = el.scrollWidth;
	}, [scrolls, bars.length]);

	const show = (bar: Bar, el: HTMLElement) => {
		const lanes = lanesRef.current?.getBoundingClientRect();
		const rect = el.getBoundingClientRect();
		setHover({ bar, left: lanes ? rect.left - lanes.left + rect.width / 2 : 0 });
	};
	return (
		<div className="chat-traj__strip" data-testid="chat-trajectory-strip">
			<div className="chat-traj__lanelabels" aria-hidden="true">
				{LANES.map((lane) => (
					<span key={lane}>{LANE_LABEL[lane]}</span>
				))}
			</div>
			<div className="chat-traj__lanes" onMouseLeave={() => setHover(undefined)} ref={lanesRef}>
				<div
					className="chat-traj__scroller"
					data-scrolls={scrolls ? "" : undefined}
					onScroll={() => setHover(undefined)}
					ref={scrollRef}
				>
					<div
						className="chat-traj__track"
						style={scrolls ? { minWidth: `max(100%, ${bars.length * STEP_PX}px)` } : undefined}
					>
						{dividers.map((x) => (
							<span
								aria-hidden="true"
								className="chat-traj__divider"
								data-testid="chat-trajectory-divider"
								key={x}
								style={{ left: `${x * 100}%` }}
							/>
						))}
						{LANES.map((lane) => (
							<div className="chat-traj__lane" data-lane={lane} key={lane}>
								{bars
									.filter((b) => b.lane === lane)
									.map((b) => (
										<button
											aria-label={`${LANE_LABEL[lane]}: ${b.label}`}
											className="chat-traj__bar-mark"
											data-key={b.key}
											data-lane={lane}
											data-running={b.running ? "" : undefined}
											data-tone={b.tone}
											key={`${lane}:${b.key}`}
											onBlur={() => setHover(undefined)}
											onClick={() => onBar(b.key)}
											onFocus={(e) => show(b, e.currentTarget)}
											onMouseEnter={(e) => show(b, e.currentTarget)}
											style={barStyle(b, scrolls)}
											type="button"
										/>
									))}
							</div>
						))}
					</div>
				</div>
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
	onToggle: (key: string, kind: RowKind) => void;
	onAgent: (key: string) => void;
}) {
	const inPane = PANE_KINDS.has(row.kind);
	const elapsed = row.running && !Number.isNaN(row.t0) ? formatDuration(now - row.t0) : "";
	const took = !Number.isNaN(row.t0) && !Number.isNaN(row.t1) ? formatSpan(row.t1 - row.t0) : "";
	return (
		<div
			className={cn("chat-traj__row", row.startsTurn && "chat-traj__row--turn", row.parent && "chat-traj__row--nested")}
			data-flash={flashing ? "" : undefined}
			data-key={row.key}
			data-kind={row.kind}
			data-open={open ? "" : undefined}
			data-selected={inPane && open ? "" : undefined}
			data-tone={row.tone}
		>
			<span className="chat-traj__gutter">{row.startsTurn ? `Turn ${row.turn}` : ""}</span>
			<button
				aria-expanded={inPane ? undefined : open}
				aria-pressed={inPane ? open : undefined}
				className="chat-traj__head"
				onClick={() => onToggle(row.key, row.kind)}
				type="button"
			>
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
			{open && !inPane ? <RowDetail entries={entries} row={row} /> : null}
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

function ContextPane({ row, onClose }: { row: TrajectoryRow; onClose: () => void }) {
	const item = row.item;
	if (item.kind !== "model_shape" && item.kind !== "model_injection") return null;
	return (
		<aside aria-label={row.title} className="chat-traj__pane" data-testid="chat-trajectory-pane">
			<header className="chat-traj__panehead">
				<span className="chat-traj__panechip" data-kind={row.kind}>
					{KIND_LABEL[row.kind]}
				</span>
				<span className="chat-traj__panetitle">{row.turn > 0 ? `Turn ${row.turn} · ${row.title}` : row.title}</span>
				<button aria-label="Close" className="chat-traj__paneclose" onClick={onClose} type="button">
					<X className="chat-icon" aria-hidden="true" />
				</button>
			</header>
			{item.kind === "model_shape" ? (
				<ShapeTabs key={row.key} shape={item.shape} />
			) : (
				<InjectionTabs injection={item.injection} key={row.key} />
			)}
		</aside>
	);
}

function PaneTabs({ tabs, children }: { tabs: readonly string[]; children: React.ReactNode }) {
	return (
		<Tabs className="chat-traj__tabs" defaultValue={tabs[0]}>
			<TabsList aria-label="Detail" className="chat-traj__tablist">
				{tabs.map((tab) => (
					<TabsTrigger key={tab} value={tab}>
						{tab}
					</TabsTrigger>
				))}
			</TabsList>
			{children}
		</Tabs>
	);
}

function ShapeTabs({ shape }: { shape: ModelShape }) {
	return (
		<PaneTabs tabs={["System Prompt", "Tools"]}>
			<TabsContent className="chat-traj__panebody" value="System Prompt">
				{shape.system.map((block, i) => (
					<pre className="chat-traj__pre chat-traj__pre--pane" key={i}>
						{block}
					</pre>
				))}
			</TabsContent>
			<TabsContent className="chat-traj__panebody" value="Tools">
				<ul aria-label="Tools" className="chat-traj__tools">
					{shape.tools.map((tool) => (
						<ToolEntry description={tool.description} key={tool.name} name={tool.name} schema={tool.inputSchema} />
					))}
				</ul>
			</TabsContent>
		</PaneTabs>
	);
}

const firstParagraph = (text: string) => text.split(/\n\s*\n/)[0].trim();

function ToolEntry({ name, description, schema }: { name: string; description: string; schema: unknown }) {
	const [open, setOpen] = useState(false);
	return (
		<li className="chat-traj__tool">
			<div className="chat-traj__toolname">{name}</div>
			{description !== "" ? <p className="chat-traj__tooldesc">{firstParagraph(description)}</p> : null}
			{schema !== undefined ? (
				<>
					<button aria-expanded={open} className="chat-traj__rawlink" onClick={() => setOpen((v) => !v)} type="button">
						Input schema
					</button>
					{open ? <pre className="chat-traj__pre">{JSON.stringify(schema, null, 2)}</pre> : null}
				</>
			) : null}
		</li>
	);
}

const PREVIEW_LINES = 20;

function InjectionTabs({ injection }: { injection: ContextInjection }) {
	const text = injectionText(injection.text);
	const preview = capText(text, PREVIEW_LINES, RESULT_CHARS);
	const label = sourceLabel(injection.source);
	const time = injection.ts === "" ? "unknown" : new Date(injection.ts).toLocaleString();
	const size = sizeText(new TextEncoder().encode(injection.text).length);
	return (
		<PaneTabs tabs={["Summary", "Preview", "Raw"]}>
			<TabsContent className="chat-traj__panebody" value="Summary">
				<dl className="chat-traj__facts">
					<dt>Source</dt>
					<dd>
						{label}
						{label !== injection.source ? <span className="chat-traj__factraw">{injection.source}</span> : null}
					</dd>
					<dt>Role</dt>
					<dd>{injection.role || "unknown"}</dd>
					<dt>Time</dt>
					<dd>{time}</dd>
					<dt>Size</dt>
					<dd>{injection.truncated ? `${size}, cut at the cap` : size}</dd>
				</dl>
				<pre className="chat-traj__pre chat-traj__pre--pane" data-section="preview">
					{preview.hidden > 0 ? `${preview.text}\n…` : preview.text}
				</pre>
			</TabsContent>
			<TabsContent className="chat-traj__panebody" value="Preview">
				<div className="chat-traj__panecopy">
					<CopyButton named={false} value={text} what="context" />
				</div>
				<pre className="chat-traj__pre chat-traj__pre--pane">{text}</pre>
			</TabsContent>
			<TabsContent className="chat-traj__panebody" value="Raw">
				<pre className="chat-traj__pre chat-traj__pre--pane" data-section="raw">
					{JSON.stringify(injection, null, 2)}
				</pre>
			</TabsContent>
		</PaneTabs>
	);
}
