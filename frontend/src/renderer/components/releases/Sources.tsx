import { createContext, useContext, useRef, useState, type ReactElement, type ReactNode } from "react";
import {
	systemMeta,
	tagStale,
	tagSystems,
	tagText,
	type Provenance,
	type SourceIndex,
} from "../../lib/releases/sources";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "../ui/hover-card";

export type SourcesCtx = { on: boolean; idx: SourceIndex };

const Ctx = createContext<SourcesCtx | null>(null);

export const SourcesProvider = Ctx.Provider;

export function useSources(): SourcesCtx | null {
	const s = useContext(Ctx);
	return s?.on ? s : null;
}

const TAG_DELAY = 80;
const VALUE_DELAY = 400;

const HEAD: Readonly<Record<Provenance["how"], string>> = {
	read: "Read from",
	derived: "Worked out by AO from",
	merged: "In because of",
	rule: "Rule reads",
};

export function SystemMark({ system, off = false }: { system: string; off?: boolean }) {
	return (
		<span className="smark" data-off={off || undefined} aria-hidden="true">
			{systemMeta(system).mark}
		</span>
	);
}

export function SourceCard({ p, idx }: { p: Provenance; idx: SourceIndex }) {
	const merged = p.how === "merged";
	const systems = [...new Set(p.lines.map((l) => idx.systemOf(l.kind)))].filter((s) => idx.read(s));
	return (
		<div className="srccard" data-testid="source-card">
			<div className="sc-h">{p.title}</div>
			{p.sub && <div className="sc-sub">{p.sub}</div>}
			<div className="sc-k">{HEAD[p.how]}</div>
			{p.lines.map((l, i) => {
				const sys = idx.systemOf(l.kind);
				return (
					<div key={`${l.kind}:${i}`} className="sc-l" data-on={merged ? String(!!l.on) : undefined}>
						<SystemMark system={sys} off={merged && !l.on} />
						<span>
							<b>{systemMeta(sys).name}</b> {l.text ?? idx.labelOf(l.kind)}
							<small>{l.kind}</small>
						</span>
						{merged && <i>{l.on ? "✓" : "·"}</i>}
					</div>
				);
			})}
			{p.note && <div className="sc-how">{p.note}</div>}
			{systems.length > 0 && (
				<div className="sc-f">
					{systems.map((s) => {
						const r = idx.read(s);
						if (!r) return null;
						return (
							<span key={s} data-unknown={r.unknown || undefined}>
								{systemMeta(s).name} {r.unknown ? "unknown, last read" : "read"}{" "}
								{r.age === "never read" ? "never" : r.age === "now" ? "just now" : `${r.age} ago`}
							</span>
						);
					})}
				</div>
			)}
			{p.field && (
				<div className="sc-fd">
					<code>{p.field}</code>
				</div>
			)}
		</div>
	);
}

function stopKeys(e: React.KeyboardEvent | React.MouseEvent | React.PointerEvent) {
	e.stopPropagation();
}

export function SourceTag({ p, wide = false }: { p: Provenance | null; wide?: boolean }) {
	const s = useSources();
	const [open, setOpen] = useState(false);
	// Radix's Escape dismiss leaves the hover-open timer of the pointer's entry
	// running, so Esc within TAG_DELAY of a click let that timer reopen the card.
	// After Esc only a fresh pointer entry, a click or a key may open it again.
	const escaped = useRef(false);
	if (!s || !p) return null;
	const text = tagText(p, wide, s.idx);
	return (
		<HoverCard
			open={open}
			onOpenChange={(next) => {
				if (next && escaped.current) return;
				setOpen(next);
			}}
			openDelay={TAG_DELAY}
			closeDelay={120}
		>
			<HoverCardTrigger asChild>
				<span
					role="button"
					tabIndex={0}
					className="stag"
					data-how={p.how}
					data-testid="source-tag"
					data-systems={tagSystems(p, s.idx).join(",")}
					data-unknown={tagStale(p, s.idx) || undefined}
					aria-label={`Source: ${tagText(p, true, s.idx)}`}
					onClick={(e) => {
						e.preventDefault();
						e.stopPropagation();
						setOpen(true);
					}}
					onPointerDown={stopKeys}
					onPointerEnter={() => {
						escaped.current = false;
					}}
					onKeyDown={(e) => {
						if (e.key !== "Enter" && e.key !== " ") return;
						e.preventDefault();
						e.stopPropagation();
						setOpen(true);
					}}
				>
					{text}
				</span>
			</HoverCardTrigger>
			<HoverCardContent
				onEscapeKeyDown={(e) => {
					e.stopPropagation();
					escaped.current = true;
				}}
				onClick={(e) => e.stopPropagation()}
				className="srcpop"
			>
				<SourceCard p={p} idx={s.idx} />
			</HoverCardContent>
		</HoverCard>
	);
}

export function SourceValue({ p, children }: { p: Provenance | null; children: ReactElement }) {
	const s = useSources();
	if (!s || !p) return children;
	return (
		<HoverCard openDelay={VALUE_DELAY} closeDelay={80}>
			<HoverCardTrigger asChild data-srcv="">
				{children}
			</HoverCardTrigger>
			<HoverCardContent onEscapeKeyDown={(e) => e.stopPropagation()} className="srcpop">
				<SourceCard p={p} idx={s.idx} />
			</HoverCardContent>
		</HoverCard>
	);
}

export function SourceCell({ children }: { children: ReactNode }) {
	return useSources() ? <span className="srccell">{children}</span> : null;
}
