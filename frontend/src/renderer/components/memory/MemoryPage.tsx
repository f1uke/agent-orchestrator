import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, Check, GraduationCap, Inbox, Play, PowerOff, RefreshCw, Scale } from "lucide-react";
import { Button } from "../ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Skeleton } from "../ui/skeleton";
import { cn } from "../../lib/utils";
import {
	isWaiting,
	useDecideNow,
	useDecideProposal,
	useLearningStatus,
	useProposal,
	useProposals,
	type Decision,
	type Proposal,
} from "../../hooks/useMemory";
import { ConflictDetail, ProposalDetail } from "./Detail";
import { ActionPill, Confidence } from "./parts";
import { fileName, queueOrder } from "./model";

type Tab = "pending" | "snoozed" | "decided";

function tabOf(p: Proposal, now: number): Tab | null {
	if (p.status === "pending") return isWaiting(p, now) ? "pending" : "snoozed";
	if (p.status === "applied" || p.status === "rejected") return "decided";
	return null; // dropped, stale and superseded proposals are not decisions to show
}

/**
 * The Memory inbox: what learning proposes from your sessions, one decision at a
 * time. The queue (conflicts first, then by confidence) on the left; on the
 * right everything needed to decide one proposal without leaving the page: why,
 * exactly what would be written, your own words, and how it sits with the rules
 * you already have. Nothing is written until you approve.
 */
export function MemoryPage() {
	const proposals = useProposals();
	const status = useLearningStatus();
	const decide = useDecideProposal();
	const decideNow = useDecideNow();
	const [tab, setTab] = useState<Tab>("pending");
	const [project, setProject] = useState("all");
	const [selected, setSelected] = useState<number | null>(null);
	const [toast, setToast] = useState<string | null>(null);
	const now = Date.now();

	const all = proposals.data?.proposals ?? [];
	const visible = useMemo(
		() => queueOrder(all.filter((p) => tabOf(p, now) === tab && (project === "all" || p.projectId === project))),
		[all, tab, project],
	);
	const count = (t: Tab) => all.filter((p) => tabOf(p, now) === t).length;
	const current = all.find((p) => p.id === selected) ?? null;
	const detail = useProposal(current?.id ?? null);
	const learning = (status.data?.projects ?? []).some((p) => p.enabled);
	const runningDecide = proposals.data?.run?.running === true;

	// Keep a proposal open: the first in the queue, or the next after a decision.
	useEffect(() => {
		if (!current || tabOf(current, now) !== tab) setSelected(visible[0]?.id ?? null);
	}, [visible, tab]);

	useEffect(() => {
		if (!toast) return;
		const t = window.setTimeout(() => setToast(null), 4500);
		return () => window.clearTimeout(t);
	}, [toast]);

	const onDecide = (p: Proposal, d: Decision) => {
		decide.mutate(
			{ id: p.id, decision: d },
			{
				onSuccess: (after) => {
					setToast(toastFor(p, after, d));
					const next = visible.find((x) => x.id !== p.id);
					setSelected(next?.id ?? null);
				},
			},
		);
	};

	const spend = status.data?.collect;
	return (
		<div className="flex h-full min-h-0 flex-col">
			<div className="flex h-[47px] shrink-0 items-center gap-[11px] border-b border-border px-[14px]">
				<GraduationCap className="size-[15px] text-muted-foreground" aria-hidden="true" />
				<span className="text-[13px] font-semibold">Memory</span>
				<span className="truncate text-[12px] text-passive">
					Lessons from your sessions, written to your agents' memory once you say yes
				</span>
				<div className="flex-1" />
				{spend && (
					<span
						className="shrink-0 font-mono text-[10.5px] text-passive"
						title="Collect, rules and decide share one daily budget"
					>
						today ${spend.todaySpendUsd.toFixed(2)} of ${spend.dailyBudgetUsd.toFixed(2)}
					</span>
				)}
				{learning && all.length > 0 && (
					<Select value={project} onValueChange={setProject}>
						<SelectTrigger className="h-7 w-[150px] text-[11.5px]" aria-label="Project">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="all">All projects</SelectItem>
							{[...new Set(all.map((p) => p.projectId))].sort().map((id) => (
								<SelectItem key={id} value={id}>
									{id}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				)}
				<Button
					variant="ghost"
					size="sm"
					disabled={!learning || decideNow.isPending || runningDecide}
					title="Decide every finished task now"
					onClick={() =>
						decideNow.mutate(undefined, {
							onSuccess: () => setToast("Deciding finished tasks now; new proposals appear here."),
						})
					}
				>
					<Play className="size-3.5" /> Decide now
				</Button>
			</div>

			{proposals.isPending || status.isPending ? (
				<LoadingState />
			) : proposals.isError ? (
				<Message
					icon={<AlertTriangle className="size-5" style={{ color: "var(--red)" }} aria-hidden="true" />}
					title="Could not reach the daemon"
					body={`The proposals are safe in AO's database; nothing has been applied. ${proposals.error.message}`}
					action={
						<Button variant="outline" size="sm" onClick={() => void proposals.refetch()}>
							<RefreshCw className="size-3.5" /> Retry now
						</Button>
					}
				/>
			) : !learning && all.length === 0 ? (
				<Message
					icon={<PowerOff className="size-5 text-passive" aria-hidden="true" />}
					title="No project learns from sessions"
					body="Turn on Learn from sessions in a project's settings. AO then reads only what you type to that project's agents, keeps it redacted, and proposes lessons here. Nothing is written without your yes."
				/>
			) : (
				<div className="flex min-h-0 flex-1">
					<div className="flex w-[340px] shrink-0 flex-col border-r border-border">
						<div className="flex shrink-0 items-center gap-1 border-b border-border px-2.5 py-2" role="tablist">
							{(["pending", "snoozed", "decided"] as Tab[]).map((t) => (
								<button
									key={t}
									type="button"
									role="tab"
									aria-selected={tab === t}
									onClick={() => setTab(t)}
									className={cn(
										"whitespace-nowrap rounded-md px-2 py-1 text-[12px] font-medium",
										tab === t ? "bg-raised text-foreground" : "text-muted-foreground hover:text-foreground",
									)}
								>
									{t === "pending" ? "To decide" : t === "snoozed" ? "Snoozed" : "Decided"}
									<span className="ml-1.5 font-mono text-[10.5px] text-passive">{count(t)}</span>
								</button>
							))}
						</div>
						<div className="min-h-0 flex-1 overflow-y-auto p-1.5" role="list" aria-label="Proposals">
							{visible.length === 0 ? (
								<div className="px-3 py-10 text-center text-[12px] text-passive">
									{tab === "pending" ? (
										<EmptyQueue waiting={openDrafts(status.data)} />
									) : tab === "snoozed" ? (
										"Nothing snoozed."
									) : (
										"Nothing decided yet."
									)}
								</div>
							) : (
								visible.map((p) => (
									<Row key={p.id} p={p} active={p.id === selected} onClick={() => setSelected(p.id)} />
								))
							)}
						</div>
					</div>
					<div className="min-w-0 flex-1 overflow-y-auto">
						{current ? (
							current.action === "conflict" ? (
								<ConflictDetail
									key={current.id}
									p={current}
									detail={detail.data}
									loading={detail.isPending}
									busy={decide.isPending}
									error={decide.isError && decide.variables?.id === current.id ? decide.error.message : undefined}
									onDecide={(d) => onDecide(current, d)}
								/>
							) : (
								<ProposalDetail
									key={current.id}
									p={current}
									detail={detail.data}
									loading={detail.isPending}
									busy={decide.isPending}
									error={decide.isError && decide.variables?.id === current.id ? decide.error.message : undefined}
									onDecide={(d) => onDecide(current, d)}
								/>
							)
						) : (
							<div className="grid h-full place-items-center px-8 text-[12.5px] text-passive">
								{tab === "pending" && count("pending") === 0 ? (
									<EmptyQueue big waiting={openDrafts(status.data)} />
								) : (
									"Pick a proposal on the left."
								)}
							</div>
						)}
					</div>
				</div>
			)}
			{toast && (
				<div className="pointer-events-none fixed bottom-5 left-1/2 z-50 -translate-x-1/2" role="status">
					<div className="flex items-center gap-2 rounded-lg border border-border bg-raised px-3.5 py-2 text-[12.5px] shadow-lg">
						<Check className="size-3.5" style={{ color: "var(--green)" }} aria-hidden="true" /> {toast}
					</div>
				</div>
			)}
		</div>
	);
}

function toastFor(p: Proposal, after: Proposal, d: Decision): string {
	if (d.kind === "reject") return "Rejected. Learning will not propose this again without new evidence.";
	if (d.kind === "snooze")
		return `Snoozed until ${new Date(d.until).toLocaleDateString()}; it comes back sooner if taught again.`;
	if (p.action === "conflict") {
		return after.resolution === "keep_rule"
			? "Kept the rule. The lesson will not be proposed again."
			: "Decided. The decision and your new rule are kept here.";
	}
	return `Wrote ${fileName(p.targetPath)}${p.indexLine ? " and its MEMORY.md line" : ""} - new sessions of the project read it.`;
}

function openDrafts(status: ReturnType<typeof useLearningStatus>["data"]): number {
	return (status?.projects ?? []).reduce((n, p) => n + (p.drafts?.open ?? 0), 0);
}

function Row({ p, active, onClick }: { p: Proposal; active: boolean; onClick: () => void }) {
	const n = p.evidenceIds.length;
	const sub =
		p.status === "rejected"
			? `Rejected - ${p.rejectReason}`
			: p.status === "applied"
				? `${p.action === "conflict" ? "Decided" : "Written"} ${p.decidedAt ? new Date(p.decidedAt).toLocaleDateString() : ""}`
				: p.snoozedUntil && Date.parse(p.snoozedUntil) > Date.now()
					? `Snoozed until ${new Date(p.snoozedUntil).toLocaleDateString()}`
					: `${n} ${n === 1 ? "quote" : "quotes"} of yours · ${new Date(p.createdAt).toLocaleDateString()}`;
	return (
		<button
			type="button"
			onClick={onClick}
			aria-current={active}
			className={cn(
				"mb-0.5 flex w-full flex-col gap-1.5 rounded-lg px-3 py-2.5 text-left",
				active
					? "bg-accent-weak shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--accent)_28%,transparent)]"
					: "hover:bg-interactive-hover",
			)}
		>
			<div className="flex items-center gap-2">
				<ActionPill action={p.action} />
				<span className="truncate font-mono text-[10.5px] text-passive">{p.projectId}</span>
				<span className="ml-auto">
					<Confidence value={p.confidence} />
				</span>
			</div>
			<div className="line-clamp-2 text-[12.5px] font-medium leading-[1.4] text-foreground">{p.title}</div>
			<div className="flex items-center gap-1.5 text-[11px] text-passive">
				{p.action === "conflict" && <Scale className="size-3" aria-hidden="true" />}
				<span className="truncate">{sub}</span>
			</div>
		</button>
	);
}

function EmptyQueue({ big = false, waiting }: { big?: boolean; waiting: number }) {
	return (
		<div className={cn("flex flex-col items-center gap-2", big && "max-w-[340px] text-center")}>
			<Inbox className={cn("text-passive", big ? "size-7" : "size-5")} aria-hidden="true" />
			<div className="text-[13px] font-semibold text-foreground">Nothing to decide</div>
			<div className="text-[12px] leading-[1.5] text-passive">
				{waiting > 0 ? `${waiting} candidate lessons are waiting for their tasks to finish. ` : ""}A lesson is proposed
				once its task is done and it is strong enough on its own or taught twice.
			</div>
		</div>
	);
}

function LoadingState() {
	return (
		<div className="flex min-h-0 flex-1" aria-busy="true">
			<div className="flex w-[340px] shrink-0 flex-col gap-2 border-r border-border p-3">
				{[0, 1, 2, 3].map((i) => (
					<Skeleton key={i} className="h-[68px] rounded-lg" />
				))}
			</div>
			<div className="flex flex-1 flex-col gap-3 p-6">
				<Skeleton className="h-6 w-2/5" />
				<Skeleton className="h-4 w-3/5" />
				<Skeleton className="mt-4 h-24 w-full" />
				<Skeleton className="h-48 w-full" />
			</div>
		</div>
	);
}

function Message({
	icon,
	title,
	body,
	action,
}: {
	icon: React.ReactNode;
	title: string;
	body: string;
	action?: React.ReactNode;
}) {
	return (
		<div className="grid flex-1 place-items-center p-8">
			<div className="flex max-w-[420px] flex-col items-center gap-3 text-center">
				{icon}
				<div className="text-[14px] font-semibold">{title}</div>
				<p className="text-[12.5px] leading-[1.55] text-muted-foreground">{body}</p>
				{action}
			</div>
		</div>
	);
}
