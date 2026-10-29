import { X } from "lucide-react";
import type { ReactNode } from "react";
import { useHideHit, useUnhideHit } from "../../hooks/useReleaseBoard";
import { dueChip } from "../../lib/releases/dueChip";
import { FIX_STATE_META, primaryAction, type PrimaryAction } from "../../lib/releases/fix";
import { cardLine } from "../../lib/releases/hitScope";
import type { Lens } from "../../lib/releases/lens";
import { alertCopy, mineWhy, onlyMeSubject } from "../../lib/releases/mineWhy";
import type { AlertHit, Board } from "../../lib/releases/types";
import type { SheetEntry } from "../../lib/releases/url";
import { appMarkOf } from "../../lib/releases/appMark";
import { trainLabel } from "../../lib/releases/llb";
import { sheetPart } from "../../lib/releases/sheetPart";
import { subjectSheet } from "../../lib/releases/subject";
import { SEVERITY_META, SEVERITY_ORDER } from "../../lib/releases/vocab";
import { AppMark } from "./AppMark";
import { NoMrButton } from "./NoMr";
import type { PageActions } from "./ReleasesPage";
import { useReleasesReadOnly } from "./ReleasesCapabilities";
import { ruleTagOf, type Provenance } from "../../lib/releases/sources";
import { SourceTag, useSources } from "./Sources";

export type AlertCtx = { board: Board; lens: Lens; actions: PageActions };

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

export function hitLinks(h: AlertHit): { label: string; url: string }[] {
	const all = [...(h.primary?.url ? [{ label: h.primary.label, url: h.primary.url }] : []), ...h.links];
	return all.flatMap((l) => (l.url ? [{ label: l.label, url: l.url }] : []));
}

export function HitLinks({ h }: { h: AlertHit }) {
	return (
		<>
			{hitLinks(h).map((l) => (
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

function RuleSource({ board, rule }: { board: Board; rule: string }) {
	const p = ruleTagOf(board.catalogue, rule);
	return useSources() && p ? <SourceTag p={p} wide /> : null;
}

function sheetFor(h: AlertHit): SheetEntry | null {
	if (/^A[1-4]$/.test(h.rule)) return { kind: "mismatch", hit: h.id };
	return subjectSheet(h.subject, h.id);
}

function tooltipOf(h: AlertHit, c: AlertCtx, canOpen: boolean): string {
	const title = c.board.catalogue.find((r) => r.id === h.rule)?.title;
	const scope = c.lens.scope;
	const why = scope ? mineWhy(h, scope) : "";
	return [
		h.rule,
		title,
		canOpen ? "click for details" : null,
		scope && why ? `Shown for ${scope.self ? "you" : scope.name}: ${why}` : null,
	]
		.filter(Boolean)
		.join(" · ");
}

function CardsLine({ h, c, what }: { h: AlertHit; c: AlertCtx; what: string }) {
	const line = cardLine(h, c.board, c.lens.me, what);
	if (!line) return null;
	if ("summary" in line)
		return (
			<div className="cards sum" data-testid="hit-cards">
				{line.summary}
			</div>
		);
	return (
		<div className="cards" data-testid="hit-cards">
			{line.keys.map((k) => (
				<button
					key={k}
					type="button"
					className="key"
					data-testid="hit-key"
					title="Open this card"
					onClick={() => c.actions.openSheet({ kind: "card", key: k }, "push")}
				>
					{k}
				</button>
			))}
			{line.more > 0 && <span className="more">+{line.more} more</span>}
		</div>
	);
}

function PrimaryButton({ a, h, c }: { a: PrimaryAction; h: AlertHit; c: AlertCtx }) {
	if (a.kind === "link")
		return (
			<a className="prim" data-testid="hit-primary" href={a.url} target="_blank" rel="noreferrer">
				{a.label}
			</a>
		);
	if (a.kind === "fix")
		return (
			<button
				type="button"
				className="prim"
				data-testid="hit-primary"
				onClick={() => c.actions.openSheet({ kind: "fix", hit: h.id }, "push")}
			>
				{a.label}
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
	const readOnly = useReleasesReadOnly();
	const open = hidden ? null : sheetFor(h);
	const copy = alertCopy(h, c.lens.me, h.train ? trainLabel(c.board, h.train) : undefined);
	const subject = onlyMeSubject(h, c.lens.me);
	const where = c.actions.whereIs(subject);
	const primary = primaryAction(h, hidden, readOnly);
	const chip = h.due ? dueChip(h.due) : null;
	const links = h.links.filter((l) => l.url && !(primary?.kind === "link" && primary.url === l.url));
	const tip = tooltipOf(h, c, open !== null);
	return (
		<div className={`al${hidden ? " dim" : ""}`} data-hit={h.id}>
			<div className="main">
				<div className="l1">
					{h.app && <AppMark mark={appMarkOf(c.board.apps, h.app)} size="sm" />}
					{open ? (
						<button
							type="button"
							className="subj"
							data-testid="hit-subject"
							title={tip}
							onClick={() => c.actions.openPart(open, sheetPart(h))}
						>
							{copy.what}
						</button>
					) : (
						<span className="subj" data-testid="hit-subject" title={tip}>
							{copy.what}
						</span>
					)}
					<RuleSource board={c.board} rule={h.rule} />
					{h.changedSinceHidden && (
						<span className="spill" data-st="waiting">
							changed since hidden
						</span>
					)}
				</div>
				<div className="do" data-testid="hit-do">
					{copy.do}
				</div>
				<CardsLine h={h} c={c} what={copy.what} />
				<div className="acts">
					{primary && <PrimaryButton a={primary} h={h} c={c} />}
					{links.map((l) => (
						<Ext key={l.url} url={l.url}>
							{l.label}
						</Ext>
					))}
					{h.rule === "G2" && h.subject.kind === "card" && !hidden && (
						<NoMrButton board={c.board} k={h.subject.key} variant="link" />
					)}
					<button
						type="button"
						data-testid="hit-show"
						aria-describedby={where ? `${h.id}-where` : undefined}
						onClick={() => c.actions.reveal(subject)}
					>
						Show
					</button>
					{hidden && !readOnly && (
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
			{chip && (
				<span className="due" data-st={chip.tone} data-testid="hit-due">
					{chip.text}
				</span>
			)}
			{!hidden && !readOnly && (
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

export function SeverityGroups({ hits, c }: { hits: AlertHit[]; c: AlertCtx }) {
	return (
		<>
			{SEVERITY_ORDER.map((s) => {
				const list = hits.filter((h) => h.severity === s);
				return list.length ? (
					<div key={s} data-testid="sev-group">
						<div className="grp" data-st={SEVERITY_META[s].tone} data-testid="sev-header">
							{SEVERITY_META[s].header}
							<span className="n">{list.length}</span>
						</div>
						{list.map((h) => (
							<AlertRow key={h.id} h={h} c={c} />
						))}
					</div>
				) : null;
			})}
		</>
	);
}

export function AlertList({ title, hits, c }: { title: string; hits: AlertHit[]; c: AlertCtx }) {
	const worst = SEVERITY_ORDER.find((s) => hits.some((h) => h.severity === s));
	if (!worst) return null;
	return (
		<Sec
			title={title}
			extra={
				<span className="badge" data-st={SEVERITY_META[worst].tone}>
					{hits.length}
				</span>
			}
		>
			<SeverityGroups hits={hits} c={c} />
		</Sec>
	);
}
