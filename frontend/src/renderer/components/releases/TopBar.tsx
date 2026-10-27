import { GitFork, RefreshCw } from "lucide-react";
import { useRefreshSources } from "../../hooks/useReleaseBoard";
import { badgeLabel, type Lens, type SourceAge } from "../../lib/releases/lens";
import { systemMeta, type SourceIndex } from "../../lib/releases/sources";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "../ui/hover-card";
import { appMark } from "../../lib/releases/appMark";
import { isOpenLlb } from "../../lib/releases/llb";
import type { Board } from "../../lib/releases/types";
import type { ReleasesSearch } from "../../lib/releases/url";
import { SEVERITY_META, SEVERITY_ORDER } from "../../lib/releases/vocab";
import { SimpleTooltip } from "../ui/tooltip";
import { AppMark } from "./AppMark";
import type { PageActions } from "./ReleasesPage";
import { useReleasesReadOnly } from "./ReleasesCapabilities";
import { ViewAs } from "./ViewAs";
import { SystemMark } from "./Sources";

const MARK_ORDER = ["jira", "gitlab", "confluence"];

function markOrder(system: string): number {
	const i = MARK_ORDER.indexOf(system);
	return i < 0 ? MARK_ORDER.length : i;
}

function SourceMark({ age: s, idx }: { age: SourceAge; idx: SourceIndex }) {
	const name = systemMeta(s.system).name;
	const state = s.unknown ? "unknown" : s.age === "now" ? "read just now" : `read ${s.age} ago`;
	return (
		<HoverCard openDelay={80} closeDelay={120}>
			<HoverCardTrigger asChild>
				<button
					type="button"
					className="rel-smark"
					data-source={s.system}
					data-unknown={s.unknown || undefined}
					aria-label={`${name}: ${state}`}
				>
					<SystemMark system={s.system} />
					{s.unknown && <b>unknown</b>}
				</button>
			</HoverCardTrigger>
			<HoverCardContent className="srcpop" onEscapeKeyDown={(e) => e.stopPropagation()}>
				<div className="srccard" data-testid="source-card">
					<div className="sc-h">
						{name} · {s.unknown ? `unknown, last read ${s.age === "never read" ? "never" : `${s.age} ago`}` : state}
					</div>
					<div className="sc-k">Feeds</div>
					{idx.kindsOf(s.system).map((k) => (
						<div key={k} className="sc-l">
							<SystemMark system={s.system} />
							<span data-unknown={idx.kindStale(k) || undefined}>
								{idx.labelOf(k)}
								<small>{k}</small>
							</span>
						</div>
					))}
					<div className="sc-how">
						{s.unknown
							? `Not fresh: ${s.why}. Values read from it show as unknown, and their source tags get an amber dot.`
							: `Every source tag that names ${name} reads from here.`}
					</div>
					<div className="sc-fd">
						<code>Board.kinds, Board.sources</code>
					</div>
				</div>
			</HoverCardContent>
		</HoverCard>
	);
}

export function TopBar({
	board,
	url,
	lens,
	actions,
	idx,
}: {
	board: Board;
	url: ReleasesSearch;
	lens: Lens;
	actions: PageActions;
	idx: SourceIndex;
}) {
	const refresh = useRefreshSources();
	const readOnly = useReleasesReadOnly();
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
			<div className="rel-src" data-testid="release-sources" role="group" aria-label="Sources">
				{[...idx.systems]
					.sort((a, b) => markOrder(a.system) - markOrder(b.system))
					.map((s) => (
						<SourceMark key={s.system} age={s} idx={idx} />
					))}
			</div>
			<ViewAs
				board={board}
				person={url.person}
				filtering={lens.me}
				onPick={(person) => actions.setSearch({ person })}
			/>
			{!readOnly && (
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
			)}
			<SimpleTooltip label="Every long-lived branch and its plan, dropped ones included">
				<button
					type="button"
					className="rel-chip"
					data-testid="llbs-button"
					aria-label={`Long-lived ${board.llbs.filter(isOpenLlb).length}`}
					onClick={() => actions.openSheet({ kind: "llbs" })}
				>
					<GitFork className="size-3 text-[var(--purple)]" aria-hidden="true" />
					<span className="wide-only">Long-lived</span>
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
