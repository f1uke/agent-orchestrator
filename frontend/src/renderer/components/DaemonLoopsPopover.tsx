import { useEffect, useState } from "react";
import { RadialCountdown } from "./RadialCountdown";
import { SimpleTooltip } from "./ui/tooltip";
import { useDaemonLoops, type DaemonLoop } from "../hooks/useDaemonLoops";
import { computeFraction, effectiveNextRunAt, formatInterval, formatNextIn } from "../lib/loop-format";

interface DaemonLoopsPopoverProps {
	/** Whether the popover is open; gates the query + the ticker. */
	open: boolean;
	/** False when the daemon isn't ready; short-circuits to the offline state. */
	daemonReachable: boolean;
}

/**
 * Popover body listing every fixed-interval daemon background loop with a live
 * radial countdown to its next run. A single 1s ticker re-renders all rows; each
 * ring interpolates from the loop's nextRunAt and rolls over locally between
 * endpoint refetches.
 */
export function DaemonLoopsPopover({ open, daemonReachable }: DaemonLoopsPopoverProps) {
	const { data: loops, isLoading, isError } = useDaemonLoops(open && daemonReachable);
	const [nowMs, setNowMs] = useState(() => Date.now());

	useEffect(() => {
		if (!open) return;
		const id = window.setInterval(() => setNowMs(Date.now()), 1000);
		return () => window.clearInterval(id);
	}, [open]);

	return (
		<div className="flex flex-col">
			<div className="px-2 pt-1.5 pb-1 text-xs font-medium text-passive">Background loops</div>
			{!daemonReachable || isError ? (
				<EmptyRow label="Daemon offline" />
			) : isLoading ? (
				<EmptyRow label="Loading loops…" />
			) : !loops || loops.length === 0 ? (
				<EmptyRow label="No loops running" />
			) : (
				<ul className="flex flex-col">
					{loops.map((loop) => (
						<LoopRow key={loop.name} loop={loop} nowMs={nowMs} />
					))}
				</ul>
			)}
		</div>
	);
}

function EmptyRow({ label }: { label: string }) {
	return <div className="px-2 py-3 text-center text-xs text-passive">{label}</div>;
}

function LoopRow({ loop, nowMs }: { loop: DaemonLoop; nowMs: number }) {
	const nextMs = loop.nextRunAt ? Date.parse(loop.nextRunAt) : null;
	const neverRun = nextMs == null || Number.isNaN(nextMs);
	const paused = !loop.running;
	const indeterminate = paused || neverRun;

	let fraction = 0;
	let secondary: string;
	if (paused) {
		secondary = "paused";
	} else if (neverRun) {
		secondary = `waiting for first run · ${formatInterval(loop.intervalMs)}`;
	} else {
		const effectiveNext = effectiveNextRunAt(nextMs, loop.intervalMs, nowMs);
		fraction = computeFraction(nextMs, loop.intervalMs, nowMs);
		const remaining = effectiveNext - nowMs;
		const absTime = new Date(effectiveNext).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
		secondary = `next in ${formatNextIn(remaining)} · ${formatInterval(loop.intervalMs)} · ${absTime}`;
	}

	return (
		<li>
			<SimpleTooltip label={loop.description} side="left">
				<div className="flex items-center gap-3 rounded-md px-2 py-1.5 hover:bg-interactive-hover">
					<RadialCountdown fraction={fraction} indeterminate={indeterminate} />
					<div className="min-w-0 flex-1">
						<div className="truncate text-sm text-foreground">{loop.displayName}</div>
						<div className="truncate text-xs text-passive tabular-nums">{secondary}</div>
					</div>
				</div>
			</SimpleTooltip>
		</li>
	);
}
