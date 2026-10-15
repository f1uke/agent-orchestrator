import { X } from "lucide-react";
import type { ReactNode } from "react";
import { useHideHit, useUnhideHit } from "../../hooks/useReleaseBoard";
import { FIX_STATE_META, fixAction } from "../../lib/releases/fix";
import type { AlertHit, Board } from "../../lib/releases/types";
import type { SheetEntry } from "../../lib/releases/url";
import { appMarkOf } from "../../lib/releases/appMark";
import { SEVERITY_META } from "../../lib/releases/vocab";
import { AppMark } from "./AppMark";
import type { PageActions } from "./ReleasesPage";

type AlertCtx = { board: Board; actions: PageActions };

export function HitWho({ board, train, app }: { board: Board; train?: string; app?: string }) {
	const label = train ? board.trains.find((t) => t.id === train)?.label : null;
	if (!label && !app) return <>All apps</>;
	return (
		<>
			{label}
			{app && <AppMark mark={appMarkOf(board.apps, app)} size="sm" />}
		</>
	);
}

export function Sec({ title, extra, children }: { title: string; extra?: ReactNode; children: ReactNode }) {
	return (
		<div className="sec">
			<div className="sec-h">
				<span className="rel-eyebrow">{title}</span>
				{extra}
			</div>
			{children}
		</div>
	);
}

export function HitLinks({ h }: { h: AlertHit }) {
	return (
		<>
			{h.links
				.filter((l) => l.url)
				.map((l) => (
					<Ext key={l.url} url={l.url}>
						{l.label}
					</Ext>
				))}
		</>
	);
}

export function Ext({ url, children }: { url?: string; children: ReactNode }) {
	return url ? (
		<a href={url} target="_blank" rel="noreferrer">
			{children}
		</a>
	) : (
		<>{children}</>
	);
}

function sheetFor(h: AlertHit): SheetEntry | null {
	if (/^A[1-4]$/.test(h.rule)) return { kind: "mismatch", hit: h.id };
	if (h.train && h.app) return { kind: "version", train: h.train, app: h.app };
	if (h.train) return { kind: "train", train: h.train };
	return null;
}

function FixButton({ h, hidden, c }: { h: AlertHit; hidden: boolean; c: AlertCtx }) {
	const a = fixAction(h, hidden);
	if (!a) return null;
	if (a.kind === "fix")
		return (
			<button type="button" className="fixb" onClick={() => c.actions.openSheet({ kind: "fix", hit: h.id }, "push")}>
				Fix ›
			</button>
		);
	const meta = FIX_STATE_META[a.ref.state];
	return (
		<button
			type="button"
			className="fixq"
			data-st={meta.tone}
			data-testid="hit-fix-state"
			title="Open this fix in the Fixes list"
			onClick={() => c.actions.openSheet({ kind: "fixes", fix: a.ref.id }, "push")}
		>
			{meta.text}
		</button>
	);
}

export function AlertRow({ h, c, hidden = false }: { h: AlertHit; c: AlertCtx; hidden?: boolean }) {
	const hide = useHideHit();
	const unhide = useUnhideHit();
	const target = sheetFor(h);
	return (
		<div className={`al${hidden ? " dim" : ""}`} data-st={SEVERITY_META[h.severity].tone} data-hit={h.id}>
			<span className="bar3" />
			<div>
				<div className="who">
					<HitWho board={c.board} train={h.train} app={h.app} />
					<span className="rid">{h.rule}</span>
					{h.changedSinceHidden && (
						<span className="spill" data-st="waiting">
							changed since hidden
						</span>
					)}
				</div>
				<div className="tx">{h.text}</div>
				<div className="acts">
					{target && !hidden && (
						<button type="button" onClick={() => c.actions.openSheet(target, "push")}>
							Show
						</button>
					)}
					<HitLinks h={h} />
					<FixButton h={h} hidden={hidden} c={c} />
					{hidden && (
						<button type="button" onClick={() => unhide.mutate(h.id)}>
							Restore
						</button>
					)}
				</div>
			</div>
			{hidden ? (
				<span />
			) : (
				<button
					type="button"
					className="x"
					title="Hide this alert"
					aria-label={`Hide ${h.id}`}
					onClick={() => hide.mutate(h.id)}
				>
					<X className="size-3.5" />
				</button>
			)}
		</div>
	);
}

export function AlertList({ title, hits, c }: { title: string; hits: AlertHit[]; c: AlertCtx }) {
	if (!hits.length) return null;
	return (
		<Sec
			title={title}
			extra={
				<span className="badge" data-st={SEVERITY_META[hits[0].severity].tone}>
					{hits.length}
				</span>
			}
		>
			{hits.map((h) => (
				<AlertRow key={h.id} h={h} c={c} />
			))}
		</Sec>
	);
}
