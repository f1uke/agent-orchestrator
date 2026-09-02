import { useMemo, useRef, useState } from "react";
import { Check, CornerDownLeft, Search } from "lucide-react";
import { useAgentsQuery } from "../hooks/useAgentsQuery";

/**
 * Which agent opens the vault.
 *
 * A command palette rather than a row of buttons, because the daemon ships
 * twenty-three harnesses: a row would either hide most of them or wrap into a
 * wall. The last one used is pre-selected, so the common case is Enter.
 *
 * This is also what the centre returns to when the agent is stopped — stopping
 * does not close the page, it just puts the choice back.
 */
export function WikiAgentPicker({
	vaultPath,
	vaultPathTitle,
	lastHarness,
	busy,
	error,
	onLaunch,
}: {
	/** Already shortened for display; the exact path goes on the title. */
	vaultPath: string;
	vaultPathTitle?: string;
	lastHarness: string;
	busy: boolean;
	error?: string;
	onLaunch: (harness: string) => void;
}) {
	const agentsQuery = useAgentsQuery();
	const [query, setQuery] = useState("");
	const listRef = useRef<HTMLDivElement | null>(null);

	// Installed agents first, then the rest of what the daemon supports: an
	// agent that is not on this machine can still be picked (and says so),
	// rather than being invisible with no explanation.
	const agents = useMemo(() => {
		const supported = agentsQuery.data?.supported ?? [];
		const installed = new Set((agentsQuery.data?.installed ?? []).map((a) => a.id));
		const ranked = [...supported].sort((a, b) => {
			if (a.id === lastHarness) return -1;
			if (b.id === lastHarness) return 1;
			const byInstalled = Number(installed.has(b.id)) - Number(installed.has(a.id));
			return byInstalled !== 0 ? byInstalled : a.id.localeCompare(b.id);
		});
		const needle = query.trim().toLowerCase();
		const matching = needle
			? ranked.filter((a) => a.id.toLowerCase().includes(needle) || a.label.toLowerCase().includes(needle))
			: ranked;
		return matching.map((a) => ({ ...a, installed: installed.has(a.id) }));
	}, [agentsQuery.data, lastHarness, query]);

	const [selected, setSelected] = useState<string | null>(null);
	const active = agents.find((a) => a.id === selected) ?? agents[0];

	const move = (delta: number) => {
		if (agents.length === 0) return;
		const current = agents.findIndex((a) => a.id === active?.id);
		const next = agents[Math.min(agents.length - 1, Math.max(0, current + delta))];
		if (!next) return;
		setSelected(next.id);
		listRef.current?.querySelector(`[data-agent="${CSS.escape(next.id)}"]`)?.scrollIntoView({ block: "nearest" });
	};

	return (
		<div className="wiki-picker">
			<div className="wiki-picker__intro">
				<div className="wiki-picker__title">Which agent should open your notes?</div>
				<p className="wiki-picker__subtitle">
					It runs in{" "}
					<span className="wiki-picker__path" title={vaultPathTitle || vaultPath}>
						{vaultPath}
					</span>{" "}
					and can edit and create notes freely.
				</p>
			</div>

			<div className="wiki-picker__panel">
				<div className="wiki-picker__search">
					<Search aria-hidden="true" className="wiki-picker__search-icon" />
					<input
						// The picker is the centre of the page when it is shown, so it takes
						// the caret without the reader having to click into it.
						autoFocus
						className="wiki-picker__input"
						placeholder="Search agents"
						spellCheck={false}
						value={query}
						onChange={(event) => {
							setQuery(event.target.value);
							setSelected(null);
						}}
						onKeyDown={(event) => {
							if (event.key === "ArrowDown") {
								event.preventDefault();
								move(1);
							} else if (event.key === "ArrowUp") {
								event.preventDefault();
								move(-1);
							} else if (event.key === "Enter" && active && !busy) {
								event.preventDefault();
								onLaunch(active.id);
							}
						}}
					/>
					<span className="wiki-picker__count">{agents.length}</span>
				</div>

				<div className="wiki-picker__list" ref={listRef}>
					{agentsQuery.isPending && <div className="wiki-picker__empty">Reading the agent catalog…</div>}
					{!agentsQuery.isPending && agents.length === 0 && (
						<div className="wiki-picker__empty">No agent matches “{query.trim()}”.</div>
					)}
					{agents.map((agent) => {
						const isActive = agent.id === active?.id;
						return (
							<button
								type="button"
								key={agent.id}
								data-agent={agent.id}
								className={`wiki-picker__row${isActive ? " wiki-picker__row--active" : ""}`}
								onMouseEnter={() => setSelected(agent.id)}
								onClick={() => !busy && onLaunch(agent.id)}
							>
								<span className={`wiki-picker__mark${isActive ? " wiki-picker__mark--active" : ""}`}>
									{agent.id.charAt(0).toUpperCase()}
								</span>
								<span className="wiki-picker__name">{agent.id}</span>
								{agent.id === lastHarness && <span className="wiki-picker__badge">last used</span>}
								{!agent.installed && <span className="wiki-picker__not-installed">not installed</span>}
								{isActive && <Check aria-hidden="true" className="wiki-picker__tick" />}
							</button>
						);
					})}
				</div>

				<div className="wiki-picker__footer">
					<span className="wiki-picker__hint">
						{error ? <span className="wiki-picker__error">{error}</span> : "This choice is remembered for next time"}
					</span>
					<button
						type="button"
						className="wiki-picker__launch"
						disabled={!active || busy}
						onClick={() => active && onLaunch(active.id)}
					>
						{busy ? "Opening…" : "Open"}
						<CornerDownLeft aria-hidden="true" className="wiki-picker__launch-key" />
					</button>
				</div>
			</div>
		</div>
	);
}
