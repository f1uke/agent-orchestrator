import { Brain, FilePen, Lock, MessageSquareQuote, ScrollText, ShieldCheck, Swords } from "lucide-react";
import type { components } from "../../../api/schema";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import type { Proposal, ProposalEvent } from "../../hooks/useMemory";
import { ACTION_LABEL, type Action } from "./model";

type Draft = components["schemas"]["ControllersLearningDraftDTO"];
type RuleRef = components["schemas"]["ControllersLearningRuleRefDTO"];

const ACTION_COLOR: Record<Action, string> = {
	create_memory: "var(--accent)",
	update_memory: "var(--accent)",
	update_skill: "var(--accent)",
	edit_rule_file: "var(--purple)",
	conflict: "var(--amber)",
};

export function ActionPill({ action }: { action: Action }) {
	const color = ACTION_COLOR[action];
	const Icon =
		action === "create_memory" || action === "update_memory"
			? Brain
			: action === "update_skill"
				? FilePen
				: action === "conflict"
					? Swords
					: ScrollText;
	return (
		<span
			className="inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-[1px] text-[10.5px] font-semibold"
			style={{
				color,
				background: `color-mix(in srgb, ${color} 13%, transparent)`,
				border: `1px solid color-mix(in srgb, ${color} 30%, transparent)`,
			}}
		>
			<Icon className="size-3" aria-hidden="true" /> {ACTION_LABEL[action]}
		</span>
	);
}

/** Five pips: how likely the person approves it as written, after the task's outcome weight. */
export function Confidence({ value }: { value: number }) {
	const on = Math.round(value * 5);
	return (
		<span
			className="inline-flex items-center gap-[3px]"
			title={`Confidence ${value.toFixed(2)}`}
			aria-label={`Confidence ${value.toFixed(2)}`}
		>
			{[0, 1, 2, 3, 4].map((i) => (
				<span
					key={i}
					className="h-[7px] w-[3px] rounded-full"
					style={{ background: i < on ? "var(--fg-muted)" : "var(--border)" }}
				/>
			))}
		</span>
	);
}

const VERDICT: Record<string, { color: string; label: string }> = {
	consistent: { color: "var(--green)", label: "fits" },
	refines: { color: "var(--accent)", label: "refines" },
	contradicts: { color: "var(--red)", label: "contradicts" },
};

/** A standing rule the proposal touches; its text and source are a hover away. */
export function RuleChip({ verdict, rule, note }: { verdict: string; rule: RuleRef; note?: string }) {
	const v = VERDICT[verdict] ?? VERDICT.consistent;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex max-w-full cursor-default items-center gap-1.5 rounded-md border border-border px-2 py-1 text-[11.5px]">
					<span className="size-1.5 shrink-0 rounded-full" style={{ background: v.color }} />
					<span className="shrink-0 font-semibold" style={{ color: v.color }}>
						{v.label}
					</span>
					{rule.protected && <Lock className="size-3 shrink-0 text-passive" aria-label="pinned" />}
					<span className="truncate text-muted-foreground">{rule.text}</span>
				</span>
			</TooltipTrigger>
			<TooltipContent className="max-w-[420px] text-[11.5px] leading-[1.5]">
				<div className="font-semibold">{rule.text}</div>
				<div className="mt-1 font-mono text-[10.5px] opacity-70">
					{rule.source}
					{rule.heading ? ` › ${rule.heading}` : ""}
				</div>
				{note && <div className="mt-1.5 opacity-80">{note}</div>}
			</TooltipContent>
		</Tooltip>
	);
}

const HOW_SENT: Record<string, string> = {
	typed: "typed",
	queued: "queued",
	suggestion_accepted: "accepted suggestion",
};

/** The person's own words: the only thing a proposal may rest on. */
export function EvidenceCard({ draft }: { draft: Draft }) {
	return (
		<div className="rounded-lg border border-border bg-surface px-3.5 py-3">
			<div className="flex items-start gap-2.5">
				<MessageSquareQuote className="mt-[3px] size-3.5 shrink-0 text-passive" aria-hidden="true" />
				<p className="whitespace-pre-wrap text-[13.5px] leading-[1.6] text-foreground">{draft.quote}</p>
			</div>
			<div className="mt-2 flex flex-wrap items-center gap-x-2.5 gap-y-1 pl-6 font-mono text-[10.5px] text-passive">
				<span className="text-accent">@{draft.sessionId}</span>
				{draft.anchorTurnAt && <span>{new Date(draft.anchorTurnAt).toLocaleString()}</span>}
				{draft.anchorSourceClass && <span>{HOW_SENT[draft.anchorSourceClass] ?? draft.anchorSourceClass}</span>}
			</div>
			{draft.agentBefore && (
				<details className="mt-2 pl-6 text-[11.5px] text-passive">
					<summary className="cursor-pointer select-none">What the agent had just done</summary>
					<p className="mt-1.5 whitespace-pre-wrap leading-[1.5] text-muted-foreground">{draft.agentBefore}</p>
				</details>
			)}
		</div>
	);
}

export function VerifierNote({ p }: { p: Proposal }) {
	const ok = p.verifier.grounded && !p.verifier.sensitiveData;
	return (
		<div className="flex gap-2.5 rounded-lg border border-border px-3.5 py-3 text-[12px] leading-[1.55]">
			<ShieldCheck
				className="mt-[2px] size-3.5 shrink-0"
				style={{ color: ok ? "var(--green)" : "var(--amber)" }}
				aria-hidden="true"
			/>
			<div>
				<div className="font-semibold text-foreground">
					Second opinion: {ok ? "grounded in your words" : "needs care"}
					{p.verifier.contradictsRule ? " · contradicts a rule" : ""}
				</div>
				{p.verifier.notes && <p className="mt-0.5 text-muted-foreground">{p.verifier.notes}</p>}
			</div>
		</div>
	);
}

export function Section({
	title,
	aside,
	children,
}: {
	title: string;
	aside?: React.ReactNode;
	children: React.ReactNode;
}) {
	return (
		<section className="flex flex-col gap-2">
			<div className="flex items-baseline gap-2">
				<h3 className="shrink-0 whitespace-nowrap text-[10.5px] font-semibold uppercase tracking-[0.09em] text-passive">
					{title}
				</h3>
				{aside && <span className="min-w-0 truncate text-[11px] text-passive">{aside}</span>}
			</div>
			{children}
		</section>
	);
}

const EVENT_LABEL: Record<ProposalEvent["kind"], string> = {
	approved: "Approved",
	edited: "Edited what was written",
	rejected: "Rejected",
	snoozed: "Snoozed",
	unsnoozed: "Unsnoozed",
	reopened: "Reopened",
	undone: "Undone",
	stale: "Went stale",
};

const SIDE_LABEL: Record<string, string> = {
	keep_rule: "kept the rule",
	words_win: "your newer words win",
	both: "both, scoped",
};

function eventText(e: ProposalEvent): string {
	if (e.kind === "snoozed" && e.snoozedUntil) return `Snoozed until ${new Date(e.snoozedUntil).toLocaleDateString()}`;
	const side = SIDE_LABEL[e.note ?? ""];
	if (side) return `${EVENT_LABEL[e.kind]}: ${side}`;
	if (e.kind === "rejected" && e.note) return `Rejected: “${e.note}”`;
	if (e.note) return `${EVENT_LABEL[e.kind]}: ${e.note}`;
	return EVENT_LABEL[e.kind];
}

function eventBy(e: ProposalEvent): string {
	if (e.via === "app") return "in the app";
	if (e.via === "cli") return e.session ? `from the CLI by @${e.session}` : "from the CLI";
	if (e.via === "api") return "through the API";
	return "";
}

/** Every decision on a proposal, oldest first: what, when, and from where. */
export function History({ events }: { events: ProposalEvent[] }) {
	return (
		<Section title="History">
			<ol className="flex flex-col gap-1.5" aria-label="History">
				{events.map((e, i) => (
					<li key={`${e.at}-${i}`} className="flex items-baseline gap-2.5 text-[12px] leading-[1.5]">
						<span className="mt-[5px] size-1.5 shrink-0 self-start rounded-full bg-passive" aria-hidden="true" />
						<span className="min-w-0 flex-1 text-foreground">{eventText(e)}</span>
						<span className="shrink-0 font-mono text-[10.5px] text-passive">
							{new Date(e.at).toLocaleString()}
							{eventBy(e) ? ` · ${eventBy(e)}` : ""}
						</span>
					</li>
				))}
			</ol>
		</Section>
	);
}
