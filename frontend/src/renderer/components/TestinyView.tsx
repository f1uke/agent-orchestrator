import { useState, type FormEvent, type ReactNode } from "react";
import { ArrowUpRight, ChevronRight, CircleDashed, RefreshCw, TriangleAlert } from "lucide-react";
import {
	useLinkTestinyRun,
	useRefreshTestinyRuns,
	useSessionTestinyRuns,
	useUnlinkTestinyRun,
} from "../hooks/useSessionTestinyRuns";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { testinyCaseGlyph, type TestinyCaseTone } from "../lib/status-glyph";
import { taskKeyOf } from "../lib/task-key";
import {
	ageLabel,
	evidenceLabel,
	linkedByLabel,
	orderCases,
	runNotice,
	scriptCoverage,
	sharedBlocker,
	summaryCounts,
	type TestinyCase,
	type TestinyFetchErrorKind,
	type TestinyRun,
} from "../lib/testiny";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card } from "./ui/card";
import { Input } from "./ui/input";

const TONE: Record<TestinyCaseTone, string> = {
	pass: "text-success",
	fail: "text-error",
	warn: "text-warning",
	idle: "text-passive",
};

const EYEBROW = "font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-passive";

/**
 * Inspector tab - the Testiny runs linked to this task, read live from Testiny.
 * AO stores only which runs belong to the task; titles, cases and results are
 * Testiny's, so this tab can lag Testiny by one read and never disagree with it.
 */
export function TestinyView({ session, project }: { session: WorkspaceSession; project: string }) {
	const taskId = taskKeyOf(session);
	const query = useSessionTestinyRuns(taskId);
	const refresh = useRefreshTestinyRuns(taskId);
	const sessions = (useWorkspaceQuery().data ?? []).flatMap((w) => w.sessions);
	const runs = query.data?.runs ?? [];
	const banner = sharedBlocker(runs);
	const now = Date.now();

	return (
		<div role="tabpanel" className="flex flex-col gap-3">
			<div className="flex flex-col gap-2">
				<div className="flex h-7 items-center justify-between gap-2">
					<span className={EYEBROW}>Testiny · {query.data?.project || project}</span>
					<Button
						variant="ghost"
						size="sm"
						className="-mr-2 h-7 gap-1.5 px-2 text-xs"
						disabled={refresh.isPending}
						onClick={() => refresh.mutate()}
					>
						<RefreshCw
							className={cn("size-3.5", (refresh.isPending || query.isFetching) && "animate-spin")}
							aria-hidden="true"
						/>
						Refresh
					</Button>
				</div>
				<LinkRunField taskId={taskId} />
				{refresh.isError ? (
					<p className="jira-move__error" role="alert">
						{refresh.error.message}
					</p>
				) : null}
			</div>

			{banner ? <Banner kind={banner} /> : null}

			{query.isError ? (
				<p className="inspector-empty">{query.error.message}</p>
			) : query.isPending ? (
				<p className="inspector-empty">Loading runs…</p>
			) : runs.length === 0 ? (
				<EmptyState />
			) : (
				runs.map((run) => (
					<RunCard
						key={run.link.runId}
						run={run}
						notice={runNotice(run, now, banner)}
						linkedBy={`linked by ${linkedByLabel(run.link.linkedBy, sessions)} · ${ageLabel(run.link.createdAt, now)}`}
						taskId={taskId}
					/>
				))
			)}
		</div>
	);
}

function LinkRunField({ taskId }: { taskId: string }) {
	const [ref, setRef] = useState("");
	const link = useLinkTestinyRun(taskId);
	const submit = (event: FormEvent) => {
		event.preventDefault();
		const value = ref.trim();
		if (!value || link.isPending) return;
		link.mutate(value, { onSuccess: () => setRef("") });
	};
	return (
		<form className="flex flex-col" onSubmit={submit}>
			<div className="flex gap-2">
				<Input
					aria-label="Link a Testiny run"
					className="h-7 text-xs"
					placeholder="Run id or Testiny URL"
					value={ref}
					onChange={(event) => {
						setRef(event.target.value);
						if (link.isError) link.reset();
					}}
				/>
				<Button type="submit" variant="outline" size="sm" className="text-xs" disabled={!ref.trim() || link.isPending}>
					{link.isPending ? "Linking…" : "Link run"}
				</Button>
			</div>
			{link.isError ? (
				<p className="jira-move__error" role="alert">
					{link.error.message}
				</p>
			) : null}
		</form>
	);
}

function Banner({ kind }: { kind: Extract<TestinyFetchErrorKind, "auth" | "binary_missing"> }) {
	return (
		<div className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs leading-relaxed text-foreground">
			<TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-warning" aria-hidden="true" />
			{kind === "auth" ? (
				<p>
					Testiny key missing or rejected. Run <code className="font-mono text-[11px]">testiny auth status</code> in a
					terminal.
				</p>
			) : (
				<p>The testiny CLI is not installed (looked on PATH and in ~/go/bin).</p>
			)}
		</div>
	);
}

/** The AO reference's `inspector-empty--browser` empty state. */
function EmptyState() {
	return (
		<div className="flex flex-col items-center gap-2 px-4 py-12 text-center">
			<CircleDashed className="size-[30px] text-passive" strokeWidth={1.5} aria-hidden="true" />
			<p className="text-[12.5px] text-foreground">No test runs linked</p>
			<p className="max-w-[260px] text-[11.5px] leading-normal text-muted-foreground">
				Agents link a run after you approve it. You can also paste a run id or URL above.
			</p>
		</div>
	);
}

function RunCard({
	run,
	notice,
	linkedBy,
	taskId,
}: {
	run: TestinyRun;
	notice: string | null;
	linkedBy: string;
	taskId: string;
}) {
	const id = `TR-${run.link.runId}`;
	const unlink = useUnlinkTestinyRun(taskId);
	const counts = summaryCounts(run);
	const coverage = scriptCoverage(run.cases);
	const { open, passed } = orderCases(run.cases);

	return (
		<Card role="article" aria-label={`${id} ${run.title}`.trim()} className="gap-0 py-0 shadow-none">
			<div className="flex flex-col gap-2 p-3">
				<div className="flex flex-wrap items-center gap-1.5">
					<span className="font-mono text-[11.5px] font-semibold text-foreground">{id}</span>
					{counts.length > 0 ? (
						<ul aria-label="Results" className="flex flex-wrap items-center gap-1">
							{counts.map(({ status, count }) => {
								const glyph = testinyCaseGlyph(status);
								return (
									<li key={status} aria-label={`${count} ${glyph.label}`} title={`${count} ${glyph.label}`}>
										<Badge className="gap-0.5 px-1.5">
											<glyph.Icon className={cn("size-3", TONE[glyph.tone])} strokeWidth={2.4} aria-hidden="true" />
											{count}
										</Badge>
									</li>
								);
							})}
						</ul>
					) : null}
					{coverage ? (
						<Badge
							variant="outline"
							className="text-muted-foreground"
							title={`${coverage.scripted} of ${coverage.total} cases are played by a Maestro case script`}
						>
							scripted {coverage.scripted}/{coverage.total}
						</Badge>
					) : null}
				</div>

				{run.title ? <p className="text-[12.5px] leading-snug font-semibold text-foreground">{run.title}</p> : null}

				{run.url ? (
					<a
						className="inline-flex items-center gap-0.5 self-start text-xs text-accent hover:underline"
						href={run.url}
						target="_blank"
						rel="noopener noreferrer"
					>
						Open in Testiny
						<ArrowUpRight className="size-3" aria-hidden="true" />
					</a>
				) : null}

				<RunPlace run={run} />

				{notice ? (
					<p className="text-[11.5px] text-muted-foreground" title={run.fetchError?.message}>
						{notice}
					</p>
				) : null}
			</div>

			{run.cases.length > 0 ? (
				<div className="flex flex-col gap-1 border-t border-border px-3 py-2">
					{open.length > 0 ? <CaseList label="Cases" cases={open} /> : null}
					{passed.length > 0 ? <PassedCases cases={passed} all={open.length === 0} /> : null}
				</div>
			) : null}

			<div className="flex flex-col gap-0.5 border-t border-border px-3 py-2">
				<div className="flex h-6 items-center gap-2">
					<span className={EYEBROW}>Evidence</span>
					{run.evidenceDir ? (
						<Button
							variant="ghost"
							size="sm"
							className="-mr-2 ml-auto h-6 px-2 text-[11px]"
							onClick={() => void aoBridge.shell.showItemInFolder(run.evidenceDir)}
						>
							Reveal in Finder
						</Button>
					) : (
						<span className="text-[11px] text-passive">no folder yet</span>
					)}
				</div>
				{run.evidenceDir ? <EvidencePath path={run.evidenceDir} /> : null}
			</div>

			<div className="flex items-center gap-2 border-t border-border px-3 py-2">
				<span className="text-[11px] text-passive">{linkedBy}</span>
				<button
					type="button"
					className="ml-auto text-[11px] text-muted-foreground transition-colors hover:text-error disabled:opacity-50"
					disabled={unlink.isPending}
					onClick={() => unlink.mutate(run.link.runId)}
				>
					Unlink
				</button>
			</div>
			{unlink.isError ? (
				<p className="jira-move__error mx-3 mb-3" role="alert">
					{unlink.error.message}
				</p>
			) : null}
		</Card>
	);
}

// Wide enough for "MILESTONE", so a card with only a plan row puts its value on
// the same edge as its neighbours.
const PLACE_LABEL = `${EYEBROW} min-w-[11ch]`;

/**
 * Where the run sits in Testiny, as label/value rows so the values share one
 * edge however the rail wraps. "closed" rides on the last row there is.
 */
function RunPlace({ run }: { run: TestinyRun }) {
	const rows: [string, string][] = [];
	if (run.plan) rows.push(["Plan", `TP-${run.plan.id} ${run.plan.title}`]);
	if (run.milestone) rows.push(["Milestone", run.milestone.title]);
	if (rows.length === 0 && !run.closed) return null;
	return (
		<dl className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1 text-[11.5px] text-muted-foreground">
			{rows.map(([label, value], index) => (
				<div key={label} className="contents">
					<dt className={PLACE_LABEL}>{label}</dt>
					<dd className="min-w-0">
						<span>{value}</span>
						{run.closed && index === rows.length - 1 ? <Closed /> : null}
					</dd>
				</div>
			))}
			{rows.length === 0 ? (
				<>
					<dt className={PLACE_LABEL}>Run</dt>
					<dd>
						<span>closed</span>
					</dd>
				</>
			) : null}
		</dl>
	);
}

function Closed() {
	return (
		<>
			<span aria-hidden="true"> · </span>
			<span>closed</span>
		</>
	);
}

function EvidencePath({ path }: { path: string }) {
	const { location, folder } = evidenceLabel(path);
	return (
		<span className="flex min-w-0 font-mono text-[11px] text-muted-foreground" title={path}>
			<span className="min-w-[2ch] shrink-[100] truncate text-passive">{location}</span>
			<span className="truncate">{folder}</span>
		</span>
	);
}

function PassedCases({ cases, all }: { cases: TestinyCase[]; all: boolean }) {
	const [expanded, setExpanded] = useState(false);
	return (
		<>
			<button
				type="button"
				aria-expanded={expanded}
				className="flex h-6 items-center gap-1 self-start text-[11.5px] text-muted-foreground transition-colors hover:text-foreground"
				onClick={() => setExpanded((v) => !v)}
			>
				<ChevronRight className={cn("size-3 transition-transform", expanded && "rotate-90")} aria-hidden="true" />
				{all ? `All ${cases.length} passed` : `${cases.length} passed`}
			</button>
			{expanded ? <CaseList label="Passed cases" cases={cases} /> : null}
		</>
	);
}

function CaseList({ label, cases }: { label: string; cases: TestinyCase[] }) {
	return (
		<ul aria-label={label} className="flex flex-col">
			{cases.map((c) => (
				<CaseRow key={c.id} testCase={c} />
			))}
		</ul>
	);
}

function CaseRow({ testCase }: { testCase: TestinyCase }): ReactNode {
	const glyph = testinyCaseGlyph(testCase.status);
	return (
		<li className="flex items-start gap-2 py-1 text-xs leading-snug">
			<glyph.Icon className={cn("mt-px size-3.5 shrink-0", TONE[glyph.tone])} strokeWidth={2.2} aria-hidden="true" />
			<span className="w-12 shrink-0 text-muted-foreground">{glyph.label}</span>
			<span className="min-w-0 flex-1 text-foreground">{testCase.title}</span>
			{testCase.script ? (
				<span
					className="shrink-0 rounded border border-border px-1 font-mono text-[10px] leading-4 text-muted-foreground"
					title={testCase.script}
				>
					script
				</span>
			) : null}
		</li>
	);
}
