import { useState } from "react";
import { AlertTriangle, FolderGit2, Globe, Lock } from "lucide-react";
import type { components } from "../../../api/schema";
import { DiffRows } from "../DiffRows";
import { Skeleton } from "../ui/skeleton";
import { Textarea } from "../ui/textarea";
import { cn } from "../../lib/utils";
import type { Decision, Proposal, ProposalDetail as Detail, Written } from "../../hooks/useMemory";
import { DecideBar } from "./DecideBar";
import { ActionPill, Confidence, EvidenceCard, History, OutcomeStatus, RuleChip, Section, VerifierNote } from "./parts";
import { diffFiles, fileName, OUTCOME_LABEL, outcomeOf, tildePath } from "./model";

type RuleRef = components["schemas"]["ControllersLearningRuleRefDTO"];

export interface DecideProps {
	p: Proposal;
	detail?: Detail;
	loading: boolean;
	busy: boolean;
	error?: string;
	/** done runs once the decision went through (an edit saved closes the editor). */
	onDecide: (d: Decision, done?: () => void) => void;
}

function Header({ p, detail }: { p: Proposal; detail?: Detail }) {
	const global = p.scope === "global";
	const outcome = outcomeOf(p, detail?.history);
	return (
		<div className="flex flex-col gap-2.5">
			<div className="flex items-center gap-2">
				<ActionPill action={p.action} />
				<span
					className="inline-flex items-center gap-1 rounded-full border border-border px-2 py-[1px] text-[10.5px] text-muted-foreground"
					title={global ? "Every session reads it" : "Only this project's sessions read it"}
				>
					{global ? (
						<Globe className="size-3" aria-hidden="true" />
					) : (
						<FolderGit2 className="size-3" aria-hidden="true" />
					)}{" "}
					{global ? "every project" : p.projectId}
				</span>
				<span className="text-[11px] text-passive">{OUTCOME_LABEL[p.outcome] ?? p.outcome}</span>
				<span className="ml-auto flex items-center gap-1.5 text-[11px] text-passive">
					confidence <Confidence value={p.confidence} />
				</span>
			</div>
			{outcome && (
				<div className="flex min-w-0 text-[12px]" role="group" aria-label="Outcome">
					<OutcomeStatus outcome={outcome} time />
				</div>
			)}
			<h2 className="text-[18px] font-semibold leading-[1.3] tracking-[-0.01em]">{p.title}</h2>
			{p.action !== "conflict" && (
				<div className="flex flex-wrap items-center gap-1.5 font-mono text-[11.5px] text-muted-foreground">
					<span className="text-passive">{p.action === "create_memory" ? "new memory" : "changes"}</span>
					<span className="text-foreground">{fileName(p.targetPath)}</span>
					{p.indexLine && <span className="text-passive">+ a line in MEMORY.md</span>}
				</div>
			)}
			<p className="max-w-[720px] text-[13px] leading-[1.6] text-muted-foreground">{p.rationale}</p>
		</div>
	);
}

function EvidenceSection({ detail, loading }: { detail?: Detail; loading: boolean }) {
	const n = detail?.evidence.length ?? 0;
	return (
		<Section title="Your words" aside={loading ? undefined : `${n} turn${n === 1 ? "" : "s"} it rests on`}>
			{loading ? (
				<Skeleton className="h-16 w-full" />
			) : (
				detail?.evidence.map((d) => <EvidenceCard key={d.id} draft={d} />)
			)}
		</Section>
	);
}

function ruleFor(detail: Detail | undefined, id: string): RuleRef {
	return detail?.rules.find((r) => r.id === id) ?? { id, text: id, source: "" };
}

function DiffFiles({ diff, label }: { diff: string; label?: (created: boolean) => string }) {
	return (
		<div className="flex flex-col gap-2.5">
			{diffFiles(diff).map((f) => (
				<div
					key={f.path}
					className="overflow-hidden rounded-lg border border-border [&_*]:whitespace-pre-wrap [&_*]:break-words"
				>
					<div className="flex items-center gap-2 border-b border-border bg-surface px-3 py-1.5 font-mono text-[11px] text-muted-foreground">
						<span className="text-foreground">{f.path.slice(f.path.lastIndexOf("/") + 1)}</span>
						<span className="text-passive">{label ? label(f.created) : f.created ? "new file" : "changed"}</span>
					</div>
					<DiffRows lines={f.lines} size="wide" />
				</div>
			))}
		</div>
	);
}

/**
 * What changed since AO wrote it: by hand, by an agent, by another proposal.
 * Shown before anything can be undone or edited over it.
 */
function ChangedSince({ written, what }: { written: Written; what: string }) {
	return (
		<div
			className="flex flex-col gap-2.5 rounded-lg border px-3.5 py-3"
			style={{ borderColor: "color-mix(in srgb, var(--amber) 40%, transparent)" }}
		>
			<div className="flex items-start gap-2 text-[12.5px] leading-[1.5]">
				<AlertTriangle className="mt-[2px] size-3.5 shrink-0" style={{ color: "var(--amber)" }} aria-hidden="true" />
				<span>
					{!written.exists ? (
						<>The file is gone since AO wrote it - deleted by hand or by an agent.</>
					) : (
						<>
							{what} changed since AO wrote it - by hand, by an agent, or by another proposal. Undo or an edit goes over
							this change only once you confirm.
						</>
					)}
				</span>
			</div>
			{written.diff && <DiffFiles diff={written.diff} label={() => "AO wrote → now"} />}
		</div>
	);
}

/** A memory, skill or CLAUDE.md change: why, the exact change, your words, the rules it touches. */
export function ProposalDetail({ p, detail, loading, busy, error, onDecide }: DecideProps) {
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState(p.newContent);
	const applied = p.status === "applied";
	const written = detail?.written;
	return (
		<div className="flex min-h-full flex-col">
			<div className="flex flex-1 flex-col gap-6 px-8 py-6">
				<Header p={p} detail={detail} />
				<Section
					title={
						editing
							? applied
								? "Edit what was written"
								: "Edit before approving"
							: applied
								? "What was written"
								: "What would be written"
					}
					aside={
						<span className="font-mono">{tildePath(p.targetPath.slice(0, p.targetPath.lastIndexOf("/") + 1))}</span>
					}
				>
					{editing ? (
						<div className="flex flex-col gap-2">
							<Textarea
								value={draft}
								onChange={(e) => setDraft(e.target.value)}
								rows={14}
								className="font-mono text-[12px] leading-[1.6]"
							/>
							<span className="text-[11px] text-passive">
								{applied ? "You are editing the file as it is now. " : ""}The same checks run on your edit before
								anything is written: the file's format, your forbidden patterns, sensitive values.
							</span>
						</div>
					) : (
						<div className="flex flex-col gap-2.5">
							{applied && written?.changed && <ChangedSince written={written} what={fileName(p.targetPath)} />}
							{applied && written && !written.changed && (
								<span className="text-[11.5px] text-passive">Unchanged since it was written.</span>
							)}
							<DiffFiles diff={p.diff} />
						</div>
					)}
				</Section>
				<EvidenceSection detail={detail} loading={loading} />
				{p.ruleVerdicts.length > 0 && (
					<Section title="Your standing rules" aside="hover a rule for its text and where it lives">
						<div className="flex flex-wrap gap-1.5">
							{p.ruleVerdicts.map((v) => (
								<RuleChip
									key={v.ruleId + v.verdict}
									verdict={v.verdict}
									rule={ruleFor(detail, v.ruleId)}
									note={v.note}
								/>
							))}
						</div>
					</Section>
				)}
				<VerifierNote p={p} />
				{detail && detail.history.length > 0 && <History events={detail.history} />}
			</div>
			<DecideBar
				p={p}
				written={written}
				busy={busy}
				error={error}
				onDecide={(d) => onDecide(d, d.kind === "edit" ? () => setEditing(false) : undefined)}
				editing={editing}
				onEdit={() => {
					// An approved proposal is edited from the file as it is now, so
					// nobody's later change is lost by the edit.
					setDraft(applied ? (written?.content ?? p.newContent) : p.newContent);
					setEditing(true);
				}}
				onCancelEdit={() => setEditing(false)}
				draft={draft}
			/>
		</div>
	);
}

type Side = "keep_rule" | "words_win" | "both";

/** Two columns: the rule you have, your newer words. You pick which wins; learning never resolves it. */
export function ConflictDetail({ p, detail, loading, busy, error, onDecide }: DecideProps) {
	const settled = p.status !== "pending";
	const written = detail?.written;
	// A decided card shows the side that won, and nothing on it can change.
	const [side, setSide] = useState<Side | null>(settled && p.resolution ? (p.resolution as Side) : null);
	const [scope, setScope] = useState(settled && p.resolution === "both" ? p.newContent : "");
	const ruleId = p.targetPath.replace(/^rule:/, "");
	const rule = ruleFor(detail, ruleId);
	const words = detail?.evidence[0];
	const inClaude = rule.source.startsWith("~/.claude") || rule.source === "protected rule";
	const consequence: Record<Side, string> = {
		keep_rule: "The lesson is dropped and remembered as decided, so it is not proposed again.",
		words_win: rule.protected
			? "Your pinned rule's text becomes your newer words; its forbidden patterns stay - change them with ao learn rules if they no longer fit."
			: inClaude
				? `The decision is recorded and your new rule is kept here; change ${rule.source || "the rule's file"} to match.`
				: "The rule lives in a team-shared file, which AO never writes: the decision is recorded and your new rule is kept here to copy into a change for the team.",
		both: "Both stay, each narrowed to where it applies: say where each one applies below, then approve.",
	};
	return (
		<div className="flex min-h-full flex-col">
			<div className="flex flex-1 flex-col gap-6 px-8 py-6">
				<Header p={p} detail={detail} />
				{loading ? (
					<Skeleton className="h-40 w-full" />
				) : (
					<div className="grid grid-cols-2 gap-3">
						<div className="flex flex-col gap-2 rounded-lg border border-border p-4">
							<div className="flex items-center gap-1.5 text-[10.5px] font-semibold uppercase tracking-[0.09em] text-passive">
								{rule.protected && <Lock className="size-3" aria-hidden="true" />} The rule you have
								{rule.protected ? " (pinned)" : ""}
							</div>
							<p className="text-[13.5px] leading-[1.6]">{rule.text}</p>
							<div className="mt-auto font-mono text-[10.5px] text-passive">
								{rule.source}
								{rule.heading ? ` › ${rule.heading}` : ""}
							</div>
						</div>
						<div
							className="flex flex-col gap-2 rounded-lg border p-4"
							style={{ borderColor: "color-mix(in srgb, var(--amber) 40%, transparent)" }}
						>
							<div
								className="text-[10.5px] font-semibold uppercase tracking-[0.09em]"
								style={{ color: "var(--amber)" }}
							>
								Your newer words
							</div>
							<p className="whitespace-pre-wrap text-[13.5px] leading-[1.6]">{words?.quote}</p>
							{words && (
								<div className="font-mono text-[10.5px] text-passive">
									@{words.sessionId}
									{words.anchorTurnAt ? ` · ${new Date(words.anchorTurnAt).toLocaleDateString()}` : ""}
								</div>
							)}
							<div className="mt-1 border-t border-border pt-2 text-[12px] text-muted-foreground">
								As a rule: <span className="text-foreground">{p.newContent}</span>
							</div>
						</div>
					</div>
				)}
				<Section title="Which wins?">
					<div className="flex flex-col gap-1.5" role="radiogroup">
						{(
							[
								["keep_rule", "Keep the rule", "drop this lesson"],
								["words_win", "Your newer words win", "the rule changes"],
								["both", "Both, scoped", "narrow one of them"],
							] as [Side, string, string][]
						).map(([s, label, sub]) => (
							<button
								key={s}
								type="button"
								role="radio"
								aria-checked={side === s}
								disabled={settled}
								onClick={() => setSide(s)}
								className={cn(
									"flex items-start gap-3 rounded-lg border px-3.5 py-2.5 text-left disabled:cursor-default",
									side === s ? "border-accent bg-accent-weak" : "border-border",
									!settled && side !== s && "hover:bg-interactive-hover",
									settled && side !== s && "opacity-50",
								)}
							>
								<span
									className={cn(
										"mt-[3px] grid size-3.5 shrink-0 place-items-center rounded-full border",
										side === s ? "border-accent" : "border-passive",
									)}
								>
									{side === s && <span className="size-1.5 rounded-full bg-accent" />}
								</span>
								<span className="flex flex-col gap-0.5">
									<span className="text-[12.5px] font-semibold">
										{label} <span className="font-normal text-passive">- {sub}</span>
									</span>
									{side === s && (
										<span className="text-[12px] leading-[1.5] text-muted-foreground">{consequence[s]}</span>
									)}
								</span>
							</button>
						))}
					</div>
					{side === "both" && (
						<Textarea
							readOnly={settled}
							value={scope}
							onChange={(e) => setScope(e.target.value)}
							rows={2}
							placeholder="Where each applies, e.g. the rule everywhere except the MR Note section, where a little Thai is fine"
							className="text-[12.5px]"
						/>
					)}
				</Section>
				{written?.changed && (
					<Section title="Your pinned rule now">
						<ChangedSince written={written} what="Your pinned rule" />
					</Section>
				)}
				{p.ruleVerdicts.some((v) => v.ruleId !== ruleId) && (
					<Section title="Other rules it touches">
						<div className="flex flex-wrap gap-1.5">
							{p.ruleVerdicts
								.filter((v) => v.ruleId !== ruleId)
								.map((v) => (
									<RuleChip
										key={v.ruleId + v.verdict}
										verdict={v.verdict}
										rule={ruleFor(detail, v.ruleId)}
										note={v.note}
									/>
								))}
						</div>
					</Section>
				)}
				<VerifierNote p={p} />
				{detail && detail.history.length > 0 && <History events={detail.history} />}
			</div>
			{side || p.status !== "pending" ? (
				<DecideBar
					p={p}
					written={written}
					busy={busy || (side === "both" && scope.trim() === "")}
					error={error}
					editing={!settled && side === "both"}
					draft={side === "both" ? scope : undefined}
					onDecide={onDecide}
					resolution={side ?? undefined}
					approveLabel={
						side === "keep_rule" ? "Keep the rule" : side === "words_win" ? "Use my newer words" : "Scope both"
					}
				/>
			) : (
				<div className="sticky bottom-0 border-t border-border bg-background px-8 py-3 text-[12px] text-passive">
					Pick which wins to decide.
				</div>
			)}
		</div>
	);
}
