import { ChevronDown, Square } from "lucide-react";
import { createContext, type ReactNode, useContext, useLayoutEffect, useRef, useState } from "react";
import { BASH_VISIBLE_LINES, bashBadge, bashOutputLines } from "../lib/chat-bash";
import type { BashItem } from "../lib/chat-fold";
import { cn } from "../lib/utils";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";

const StopBashContext = createContext<((id: string) => void) | null>(null);

/** Lets a bash row stop its run without the stream threading the handler through every row. */
export function StopBashProvider({ stop, children }: { stop: (id: string) => void; children: ReactNode }) {
	return <StopBashContext.Provider value={stop}>{children}</StopBashContext.Provider>;
}

const BADGE_VARIANT = { running: "accent", ok: "success", error: "error", stopped: "warning" } as const;

export function ChatBashRow({ item }: { item: BashItem }) {
	const stop = useContext(StopBashContext);
	const [expanded, setExpanded] = useState(false);
	const [stopping, setStopping] = useState(false);
	const out = useRef<HTMLPreElement>(null);
	const lines = bashOutputLines(item);
	const capped = lines > BASH_VISIBLE_LINES;
	const running = item.state === "running";
	const badge = bashBadge(item);

	useLayoutEffect(() => {
		if (running && !expanded && out.current) out.current.scrollTop = out.current.scrollHeight;
	}, [item.chunks, running, expanded]);

	return (
		<div className="chat-bash" data-bash={item.id} data-state={item.state}>
			<div className="chat-bash__head">
				<span aria-hidden="true" className="chat-bash__bang">
					!
				</span>
				<code className="chat-bash__command" title={item.command}>
					{item.command}
				</code>
				<Badge
					className={cn(running && "chat-bash__live")}
					data-testid="chat-bash-badge"
					variant={BADGE_VARIANT[badge.tone]}
				>
					{badge.label}
				</Badge>
				{running && stop ? (
					<Button
						aria-label={`Stop ${item.command}`}
						disabled={stopping}
						onClick={() => {
							setStopping(true);
							stop(item.id);
						}}
						size="sm"
						variant="outline"
					>
						<Square aria-hidden="true" className="chat-bash__stop-icon" />
						Stop
					</Button>
				) : null}
			</div>
			{item.chunks.length > 0 ? (
				<pre
					aria-label="Command output"
					className="chat-bash__out"
					data-capped={capped && !expanded ? "" : undefined}
					ref={out}
					tabIndex={0}
				>
					{item.chunks.map((chunk, i) => (
						<span
							className={chunk.stream === "stderr" ? "chat-bash__err" : undefined}
							data-stream={chunk.stream}
							key={i}
						>
							{chunk.text}
						</span>
					))}
				</pre>
			) : !running ? (
				<div className="chat-bash__empty">No output</div>
			) : null}
			{capped ? (
				<button
					aria-expanded={expanded}
					className="chat-bash__more"
					onClick={() => setExpanded((open) => !open)}
					type="button"
				>
					<ChevronDown aria-hidden="true" className="chat-icon chat-bash__chev" data-open={expanded || undefined} />
					{expanded ? "Show less" : `Show all ${lines} lines`}
				</button>
			) : null}
			{item.truncated ? <div className="chat-bash__note">Output was cut off at 1 MiB.</div> : null}
		</div>
	);
}
