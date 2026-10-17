import { ChevronRight, X } from "lucide-react";
import type { ReactNode } from "react";
import { useHideHit, useUnhideHit } from "../../hooks/useReleaseBoard";
import { FIX_STATE_META, fixAction } from "../../lib/releases/fix";
import type { AlertHit, Board } from "../../lib/releases/types";
import type { SheetEntry } from "../../lib/releases/url";
import { appMarkOf } from "../../lib/releases/appMark";
import { sheetPart } from "../../lib/releases/sheetPart";
import { subjectLabel, subjectSheet } from "../../lib/releases/subject";
import { SEVERITY_META } from "../../lib/releases/vocab";
import { AppMark } from "./AppMark";
import type { PageActions } from "./ReleasesPage";
import { ruleTagOf, type Provenance } from "../../lib/releases/sources";
import { SourceTag, useSources } from "./Sources";

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

export function Sec({
	title,
	extra,
	source,
	part,
	children,
}: {
	title: string;
	extra?: ReactNode;
	source?: Provenance | null;
	part?: string;
	children: ReactNode;
}) {
	return (
		<div className="sec" data-part={part}>
			<div className="sec-h">
				<span className="rel-eyebrow">{title}</span>
				{source && <SourceTag p={source} wide />}
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

function RuleId({ board, rule }: { board: Board; rule: string }) {
	const p = ruleTagOf(board.catalogue, rule);
	return useSources() && p ? <SourceTag p={p} wide /> : <span className="rid">{rule}</span>;
}

function sheetFor(h: AlertHit): SheetEntry | null {
	if (/^A[1-4]$/.test(h.rule)) return { kind: "mismatch", hit: h.id };
	return subjectSheet(h.subject, h.id);
}

function SubjectLine({ h, c, open }: { h: AlertHit; c: AlertCtx; open: SheetEntry | null }) {
	const l = subjectLabel(h.subject, c.board);
	const body = (
		<>
			{l.app && <AppMark mark={appMarkOf(c.board.apps, l.app)} size="sm" />}
			<span>{l.text}</span>
		</>
	);
	return open ? (
		<button
			type="button"
			className="subj"
			data-testid="hit-subject"
			title="Open its details"
			onClick={() => c.actions.openPart(open, sheetPart(h))}
		>
			{body}
			<ChevronRight className="size-3" aria-hidden="true" />
		</button>
	) : (
		<div className="subj" data-testid="hit-subject">
			{body}
		</div>
	);
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
	const where = c.actions.whereIs(h.subject);
	return (
		<div className={`al${hidden ? " dim" : ""}`} data-st={SEVERITY_META[h.severity].tone} data-hit={h.id}>
			<div>
				<div className="who">
					<span className="sev">{SEVERITY_META[h.severity].label}</span>
					<RuleId board={c.board} rule={h.rule} />
					{h.changedSinceHidden && (
						<span className="spill" data-st="waiting">
							changed since hidden
						</span>
					)}
				</div>
				<SubjectLine h={h} c={c} open={hidden ? null : target} />
				<div className="tx">{h.text}</div>
				<div className="acts">
					<button
						type="button"
						data-testid="hit-show"
						aria-describedby={where ? `${h.id}-where` : undefined}
						onClick={() => c.actions.reveal(h.subject)}
					>
						Show
					</button>
					<HitLinks h={h} />
					<FixButton h={h} hidden={hidden} c={c} />
					{hidden && (
						<button type="button" onClick={() => unhide.mutate(h.id)}>
							Restore
						</button>
					)}
				</div>
				{where && (
					<div className="where" id={`${h.id}-where`} data-testid="hit-where">
						Show lands {where}
					</div>
				)}
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
