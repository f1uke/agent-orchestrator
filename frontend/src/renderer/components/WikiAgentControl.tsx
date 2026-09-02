import { useEffect } from "react";
import { BookText, ChevronDown, RefreshCw, Square } from "lucide-react";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuShortcut,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { isMacPlatform } from "../lib/platform";

/**
 * The Wiki's agent control: ONE pill in the page topbar that is both the status
 * and every action. There are deliberately no separate Restart/Stop buttons —
 * the topbar of a reading page should carry one affordance, not three.
 *
 * It stays mounted whatever the centre is showing (terminal, picker, or an open
 * note), so the agent is always reachable while reading.
 */

export type WikiAgentState = "running" | "thinking" | "stopped";

export function WikiAgentControl({
	state,
	harness,
	startedAt,
	onRestart,
	onSwitch,
	onStop,
	onStart,
}: {
	state: WikiAgentState;
	/** The running agent, or the last one chosen. Empty when there has never been one. */
	harness: string;
	/** When the pane started, for the menu's "running for" line. */
	startedAt?: string;
	onRestart: () => void;
	onSwitch: () => void;
	onStop: () => void;
	/** Clicking the stopped pill goes straight to picking an agent. */
	onStart: () => void;
}) {
	const stopped = state === "stopped";

	// ⌘R restarts the agent, matching the menu's shortcut. Bound only while an
	// agent is actually running, so the browser/Electron reload it shadows is
	// left alone on a page with nothing to restart.
	useEffect(() => {
		if (stopped) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key.toLowerCase() !== "r") return;
			if (!(event.metaKey || event.ctrlKey) || event.shiftKey || event.altKey) return;
			event.preventDefault();
			onRestart();
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, [stopped, onRestart]);

	if (stopped) {
		return (
			<button type="button" className="wiki-pill wiki-pill--stopped" onClick={onStart}>
				<span className="wiki-pill__dot" />
				No agent
				<ChevronDown aria-hidden="true" className="wiki-pill__chevron" />
			</button>
		);
	}

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<button type="button" className={`wiki-pill wiki-pill--${state}`}>
					{/* The breathing dot is the app's own working signal (status-pulse),
					    so "the agent is busy" reads the same here as on the board. */}
					<span className={`wiki-pill__dot${state === "thinking" ? " wiki-pill__dot--pulse" : ""}`} />
					{harness || "agent"}
					<ChevronDown aria-hidden="true" className="wiki-pill__chevron" />
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-[248px]">
				<div className="px-2.5 pb-1 pt-1.5">
					<div className="font-mono text-[10px] uppercase tracking-[0.08em] text-passive">{harness || "agent"}</div>
					<div className="mt-[3px] font-mono text-[11px] text-passive">{runningFor(startedAt)}</div>
				</div>
				<DropdownMenuSeparator />
				<DropdownMenuItem onSelect={onRestart}>
					<RefreshCw aria-hidden="true" />
					Restart
					<DropdownMenuShortcut>{isMacPlatform() ? "⌘R" : "Ctrl R"}</DropdownMenuShortcut>
				</DropdownMenuItem>
				<DropdownMenuItem onSelect={onSwitch}>
					<BookText aria-hidden="true" />
					Switch agent…
				</DropdownMenuItem>
				<DropdownMenuSeparator />
				<DropdownMenuItem className="text-error focus:text-error [&_svg]:text-error" onSelect={onStop}>
					<Square aria-hidden="true" />
					Stop agent
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

/** "Running 2h 14m" — how long this pane has been up. */
export function runningFor(startedAt: string | undefined, now = Date.now()): string {
	if (!startedAt) return "Running";
	const started = Date.parse(startedAt);
	if (Number.isNaN(started)) return "Running";
	const minutes = Math.max(0, Math.floor((now - started) / 60_000));
	if (minutes < 1) return "Just started";
	if (minutes < 60) return `Running ${minutes}m`;
	const hours = Math.floor(minutes / 60);
	return `Running ${hours}h ${minutes % 60}m`;
}
