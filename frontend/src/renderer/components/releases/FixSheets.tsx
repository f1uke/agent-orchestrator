import { ChevronRight } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useConfirmFix, useFixes, useFixPlan, useUndoFix } from "../../hooks/useReleaseBoard";
import {
	actorLabel,
	FIX_STATE_META,
	FixPlanChangedError,
	newestFirst,
	planView,
	requestText,
	stepNote,
	undoVerb,
} from "../../lib/releases/fix";
import { agoText } from "../../lib/releases/lens";
import type { Board, Fix, FixPlan, FixRef, FixStep } from "../../lib/releases/types";
import { appName, Sec } from "./Alerts";
import type { PageActions } from "./ReleasesPage";

type Ctx = { board: Board; actions: PageActions; now: number };
type Sheet = { title: ReactNode; body: ReactNode };

const MODE_TEXT: Readonly<Record<Exclude<FixPlan["mode"], "blocked">, string>> = {
	queue:
		"Dry run. Confirming records the exact request in AO's fix log. Nothing is sent to Jira, GitLab or Confluence.",
	choose:
		"Dry run. Pick the side that is right; confirming records that request in AO's fix log. Nothing is sent to Jira, GitLab or Confluence.",
	apply:
		"Applies now to AO's own long-lived plan. Nothing is written to GitLab, Jira or Confluence. Undo puts it back.",
	copy: "Nothing to confirm. Copy the text and paste it by hand; AO sends nothing.",
};

function Step({ s }: { s: FixStep }) {
	return (
		<div className="fixstep" data-testid="fix-step">
			<div className="lb">{s.label}</div>
			<div className="ba">
				<span className="b">{s.before}</span>
				<span className="ar">→</span>
				<span className="a">{s.after}</span>
			</div>
			<pre className="req" data-testid="fix-request">
				{requestText(s.request)}
			</pre>
			<div className="dry" data-st={s.effect.kind === "ao_plan_target" ? "accent" : "running"}>
				{stepNote(s)}
			</div>
		</div>
	);
}

function Recorded({ fix, c }: { fix: FixRef; c: Ctx }) {
	const meta = FIX_STATE_META[fix.state];
	return (
		<div className="status-line" data-st={meta.tone} data-testid="fix-recorded">
			<span>
				{meta.text}.{" "}
				<button
					type="button"
					className="lnk"
					onClick={() => c.actions.openSheet({ kind: "fixes", fix: fix.id }, "push")}
				>
					See it in Fixes ›
				</button>
			</span>
		</div>
	);
}

function CopyButton({ text }: { text: string }) {
	const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
	return (
		<div className="acts2">
			<button
				type="button"
				className="btnp"
				onClick={() =>
					navigator.clipboard.writeText(text).then(
						() => setState("copied"),
						() => setState("failed"),
					)
				}
			>
				Copy keys
			</button>
			{state === "copied" && <span className="saved">Copied</span>}
			{state === "failed" && (
				<span className="c" data-st="failed">
					The clipboard refused; select the text above.
				</span>
			)}
		</div>
	);
}

function FixBody({ hitId, choice, c }: { hitId: string; choice?: string; c: Ctx }) {
	const hit = c.board.alerts.hits.find((h) => h.id === hitId);
	const confirm = useConfirmFix();
	const recorded = confirm.data ?? null;
	const open = recorded ? { id: recorded.id, state: recorded.state } : (hit?.openFix ?? null);
	const plan = useFixPlan(hitId, choice, !recorded);
	if (plan.isPending && !recorded) return <div className="sec empty">Planning the fix…</div>;
	if (!plan.data)
		return (
			<>
				{open && (
					<div className="sec">
						<Recorded fix={open} c={c} />
					</div>
				)}
				<div className="sec empty">
					{open ? "" : `This alert has nothing to fix any more. ${plan.error?.message ?? ""}`}
				</div>
			</>
		);
	const p = plan.data;
	const view = planView(p);
	const changed = confirm.error instanceof FixPlanChangedError;
	const stepsTitle = p.mode === "apply" ? "Change" : p.mode === "copy" ? "What to paste" : "Request it would send";
	return (
		<>
			<div className="sec text-[14px]" data-testid="fix-title">
				{p.title}
			</div>
			{hit && (
				<div className="sec muted text-[12.5px]">
					{[
						hit.train ? c.board.trains.find((t) => t.id === hit.train)?.label : null,
						hit.app ? appName(c.board, hit.app) : null,
					]
						.filter(Boolean)
						.join(" ")}{" "}
					· {hit.text}
				</div>
			)}
			{p.mode !== "blocked" && (
				<div className="sec">
					<div className="status-line" data-st={p.mode === "apply" ? "accent" : "running"} data-testid="fix-mode">
						<span>{MODE_TEXT[p.mode]}</span>
					</div>
				</div>
			)}
			{p.mode === "choose" && (
				<Sec title="Which side is right">
					{(p.choices ?? []).map((ch) => (
						<button
							key={ch.id}
							type="button"
							className={`fixpick${p.choice === ch.id ? " on" : ""}`}
							aria-pressed={p.choice === ch.id}
							onClick={() => c.actions.setSearch({ sheet: { kind: "fix", hit: hitId, choice: ch.id } })}
						>
							{ch.label}
						</button>
					))}
				</Sec>
			)}
			{view.steps.length > 0 && (
				<Sec title={stepsTitle}>
					{view.steps.map((s, i) => (
						<Step key={i} s={s} />
					))}
				</Sec>
			)}
			{p.note && p.mode !== "blocked" && <div className="sec muted text-[12px]">{p.note}</div>}
			<div className="sec">
				{changed && (
					<div className="status-line mb-2" data-st="waiting" data-testid="fix-changed">
						<span>The data changed, review again. The plan above is the new one.</span>
					</div>
				)}
				{confirm.error && !changed && (
					<div className="status-line mb-2" data-st="failed">
						<span>{confirm.error.message}</span>
					</div>
				)}
				{open ? (
					<Recorded fix={open} c={c} />
				) : p.mode === "blocked" ? (
					<div className="status-line" data-st="waiting" data-testid="fix-blocked">
						<span>
							{p.note}
							{p.need && (
								<>
									{" "}
									<button
										type="button"
										className="lnk"
										onClick={() => c.actions.openSheet({ kind: "llb", id: p.need?.llb ?? "" }, "push")}
									>
										{p.need.label} ›
									</button>
								</>
							)}
						</span>
					</div>
				) : view.copy !== null ? (
					<CopyButton text={view.copy} />
				) : view.confirm ? (
					<div className="acts2">
						<button type="button" className="btnp" disabled={confirm.isPending} onClick={() => confirm.mutate(p)}>
							{view.confirm}
						</button>
					</div>
				) : null}
			</div>
		</>
	);
}

export function fixSheet(hit: string, choice: string | undefined, c: Ctx): Sheet {
	return {
		title: (
			<>
				Fix <span className="mono passive text-[12px]">{hit.slice(0, hit.indexOf("~"))}</span>
			</>
		),
		body: <FixBody key={`${hit}:${choice ?? ""}`} hitId={hit} choice={choice} c={c} />,
	};
}

function FixRow({ f, c, open: initial }: { f: Fix; c: Ctx; open: boolean }) {
	const [open, setOpen] = useState(initial);
	const undo = useUndoFix();
	const verb = undoVerb(f.state);
	const meta = FIX_STATE_META[f.state];
	const actor = f.events[0]?.actor;
	return (
		<div className="fixrow" data-st={meta.tone} data-fix={f.id} data-focus={initial || undefined}>
			<button type="button" className="fold2" aria-expanded={open} onClick={() => setOpen(!open)}>
				<ChevronRight className="size-3" style={{ rotate: open ? "90deg" : "0deg" }} aria-hidden="true" />
				<span className="rid">{f.rule}</span>
				<span className="t">{f.plan.title}</span>
			</button>
			<span className="st c" data-testid="fix-state">
				{meta.text}
			</span>
			<div className="sub">
				<span className="mono">{f.hit}</span>
				<span>
					{actor ? `by ${actorLabel(actor)} · ` : ""}
					<span title={f.createdAt}>{agoText(c.now - Date.parse(f.createdAt))}</span>
				</span>
				{verb && (
					<button type="button" className="lnk" disabled={undo.isPending} onClick={() => undo.mutate(f.id)}>
						{verb}
					</button>
				)}
			</div>
			{undo.error && (
				<div className="status-line mt-1.5" data-st="failed" data-testid="undo-refused">
					<span>
						{undo.error.message} The fix stays {meta.text}.
					</span>
				</div>
			)}
			{open && (
				<div className="evs" data-testid="fix-events">
					{f.events.map((e) => (
						<div key={e.seq} className="ev">
							<span className="mono">{e.kind}</span>
							<span>{actorLabel(e.actor)}</span>
							<span className="passive" title={e.at}>
								{agoText(c.now - Date.parse(e.at))}
							</span>
						</div>
					))}
					{f.plan.steps.map((s, i) => (
						<pre key={i} className="req">
							{requestText(s.request)}
						</pre>
					))}
				</div>
			)}
		</div>
	);
}

function FixesBody({ focus, c }: { focus?: string; c: Ctx }) {
	const q = useFixes();
	if (q.isPending) return <div className="sec empty">Loading fixes…</div>;
	if (q.isError) return <div className="sec empty">The fixes did not load: {q.error.message}</div>;
	const list = newestFirst(q.data);
	return (
		<>
			<div className="passive mt-2.5 text-[11.5px]">
				Only an L3 fix applies, to AO's own long-lived plan. Every other fix is a dry run: AO records the request it
				would send and sends nothing to Jira, GitLab or Confluence.
			</div>
			{list.length ? (
				<div className="sec" data-testid="fixes-list">
					{list.map((f) => (
						<FixRow key={f.id} f={f} c={c} open={f.id === focus} />
					))}
				</div>
			) : (
				<div className="sec empty">No fixes yet. A Fix button on an alert starts one.</div>
			)}
		</>
	);
}

export function fixesSheet(focus: string | undefined, c: Ctx): Sheet {
	return { title: "Fixes", body: <FixesBody focus={focus} c={c} /> };
}
