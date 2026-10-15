import { GitFork, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { useRefreshSources } from "../../hooks/useReleaseBoard";
import { badgeLabel, sourceAges, type Lens } from "../../lib/releases/lens";
import { appMark } from "../../lib/releases/appMark";
import { isOpenLlb } from "../../lib/releases/llb";
import type { Board } from "../../lib/releases/types";
import type { ReleasesSearch } from "../../lib/releases/url";
import { SEVERITY_META, SEVERITY_ORDER } from "../../lib/releases/vocab";
import { SimpleTooltip } from "../ui/tooltip";
import { AppMark } from "./AppMark";
import { Avatar } from "./Avatar";
import type { PageActions } from "./ReleasesPage";

function OnlyMe({ board, on, onToggle }: { board: Board; on: boolean; onToggle: () => void }) {
	const id = board.identity;
	if (id.kind === "unknown") {
		return (
			<SimpleTooltip label={`Only me needs your Jira and GitLab accounts: ${id.reason}`}>
				<span>
					<button type="button" className="rel-me" aria-pressed="false" disabled data-testid="only-me">
						<span className="sw" />
						Only me
					</button>
				</span>
			</SimpleTooltip>
		);
	}
	return (
		<SimpleTooltip label={`Show only what is ${id.displayName}'s`}>
			<button type="button" className="rel-me" aria-pressed={on} onClick={onToggle} data-testid="only-me">
				<span className="sw" />
				<Avatar accountId={id.jiraAccountId} name={id.displayName} size={18} />
				Only me
			</button>
		</SimpleTooltip>
	);
}

function useNow(ms: number): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const id = window.setInterval(() => setNow(Date.now()), ms);
		return () => window.clearInterval(id);
	}, [ms]);
	return now;
}

export function TopBar({
	board,
	url,
	lens,
	actions,
	fetchError,
	updatedAt,
}: {
	board: Board;
	url: ReleasesSearch;
	lens: Lens;
	actions: PageActions;
	fetchError: string | null;
	updatedAt: number;
}) {
	const refresh = useRefreshSources();
	const now = useNow(30_000);
	const ages = sourceAges(
		board.sources,
		now,
		fetchError ? `${fetchError}; board last read ${Math.max(0, Math.round((now - updatedAt) / 60_000))}m ago` : null,
	);
	const toggleApp = (id: string) => {
		const on = new Set(url.apps ?? board.apps.map((a) => a.id));
		if (on.has(id)) on.delete(id);
		else on.add(id);
		actions.setSearch({ apps: on.size === board.apps.length ? null : on });
	};
	const bySev = SEVERITY_ORDER.map((s) => [s, lens.attention.filter((h) => h.severity === s).length] as const).filter(
		([, n]) => n > 0,
	);
	return (
		<div className="rel-top">
			<h1>Releases</h1>
			<SimpleTooltip label="How to read this page (coming soon)">
				<span>
					<button type="button" className="rel-round" aria-label="Guide" disabled>
						?
					</button>
				</span>
			</SimpleTooltip>
			<SimpleTooltip label="Tour (coming soon)">
				<span>
					<button type="button" className="rel-round" aria-label="Tour" disabled>
						▶
					</button>
				</span>
			</SimpleTooltip>
			<div className="flex flex-none gap-1.5" role="group" aria-label="Apps">
				{board.apps.map((a) => (
					<SimpleTooltip key={a.id} label={a.name}>
						<button
							type="button"
							className="rel-chip"
							aria-pressed={!url.apps || url.apps.has(a.id)}
							onClick={() => toggleApp(a.id)}
						>
							<AppMark mark={appMark(a)} />
						</button>
					</SimpleTooltip>
				))}
				<button
					type="button"
					className="rel-chip hf"
					aria-pressed={url.hotfixes}
					onClick={() => actions.setSearch({ hotfixes: !url.hotfixes })}
				>
					<span className="d" />
					Hotfixes
				</button>
			</div>
			<div className="flex-1" />
			<div className="rel-src" data-testid="release-sources">
				{ages.map((s) => (
					<SimpleTooltip
						key={s.system}
						label={s.unknown ? `${s.system}: ${s.why}` : `${s.system}: every resource fresh`}
					>
						<span className={s.unknown ? "u" : ""} data-source={s.system}>
							{s.system} {s.unknown ? `unknown · ${s.age}` : s.age}
						</span>
					</SimpleTooltip>
				))}
			</div>
			<OnlyMe board={board} on={lens.me} onToggle={() => actions.setSearch({ me: !url.me })} />
			<SimpleTooltip label="Read every source again now">
				<button
					type="button"
					className="rel-round"
					aria-label="Refresh sources"
					onClick={() => refresh.mutate()}
					disabled={refresh.isPending}
				>
					<RefreshCw className="size-3" aria-hidden="true" />
				</button>
			</SimpleTooltip>
			<SimpleTooltip label="Every long-lived branch and its plan, dropped ones included">
				<button
					type="button"
					className="rel-chip"
					data-testid="llbs-button"
					onClick={() => actions.openSheet({ kind: "llbs" })}
				>
					<GitFork className="size-3 text-[var(--purple)]" aria-hidden="true" />
					Long-lived
					<b className="n">{board.llbs.filter(isOpenLlb).length}</b>
				</button>
			</SimpleTooltip>
			<button type="button" className="rel-chip" onClick={() => actions.openSheet({ kind: "key" })}>
				Key
			</button>
			<button
				type="button"
				className="rel-attn"
				onClick={() => actions.openSheet({ kind: "attention" })}
				title={bySev.map(([s, n]) => `${n} ${SEVERITY_META[s].label}`).join(" · ")}
			>
				<span className="rel-sevbar" aria-hidden="true">
					{bySev.map(([s]) => (
						<i key={s} data-st={SEVERITY_META[s].tone} />
					))}
				</span>
				Needs attention <b data-testid="attention-count">{badgeLabel(lens.badge)}</b>
			</button>
		</div>
	);
}
