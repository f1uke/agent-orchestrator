import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ArrowUpRight, ChevronDown, ChevronRight, CircleDashed, RefreshCw, TriangleAlert } from "lucide-react";
import {
	useLinkTestinyRun,
	useRecordTestinyResult,
	useRefreshTestinyRuns,
	useSessionTestinyRuns,
	useTestinyCase,
	useTestinyRunsVersion,
	useUnlinkTestinyRun,
	type ResultWriteHold,
} from "../hooks/useSessionTestinyRuns";
import { useRefLinkSettings } from "../hooks/useRefLinkSettings";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { splitRefLinks } from "../lib/ref-links";
import { testinyCaseGlyph, type TestinyCaseTone } from "../lib/status-glyph";
import { taskKeyOf } from "../lib/task-key";
import {
	ageLabel,
	caseChips,
	caseFacts,
	evidenceLabel,
	isStepWrite,
	linkedByLabel,
	needsComment,
	orderCases,
	provenanceLabel,
	runNotice,
	scriptCoverage,
	sharedBlocker,
	standingRecord,
	stepStatus,
	summaryCounts,
	TESTINY_COMMENT_MAX,
	TESTINY_STATUSES,
	textBlocks,
	type CaseChip,
	type CaseOrder,
	type TestinyCase,
	type TestinyCaseDetail,
	type TestinyCaseStep,
	type TestinyFetchErrorKind,
	type TestinyRun,
	type TestinyStatus,
	unlinkedTaskIssue,
} from "../lib/testiny";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card } from "./ui/card";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
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
	const version = useTestinyRunsVersion(taskId);
	const sessions = (useWorkspaceQuery().data ?? []).flatMap((w) => w.sessions);
	const runs = query.data?.runs ?? [];
	// The task's issue is the dev session's, which a qa's own may not carry.
	const taskIssueId = sessions.find((s) => s.id === taskId)?.issueId ?? session.issueId;
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
						taskIssueId={taskIssueId}
						version={version}
						provenance={(c) => provenanceLabel(c, now, sessions)}
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

/** What every case row of one card needs to show and set its result. */
type RowContext = {
	taskId: string;
	taskIssueId: string | undefined;
	runId: number;
	hold: ResultWriteHold;
	provenance: (testCase: TestinyCase) => string | null;
};

function RunCard({
	run,
	notice,
	linkedBy,
	taskId,
	taskIssueId,
	version,
	provenance,
}: {
	run: TestinyRun;
	notice: string | null;
	linkedBy: string;
	taskId: string;
	taskIssueId: string | undefined;
	version: number;
	provenance: (testCase: TestinyCase) => string | null;
}) {
	const id = `TR-${run.link.runId}`;
	const unlink = useUnlinkTestinyRun(taskId);
	const counts = summaryCounts(run);
	const coverage = scriptCoverage(run.cases);
	const { open, passed, hold } = useHeldOrder(run.cases, version);
	const row: RowContext = { taskId, taskIssueId, runId: run.link.runId, hold, provenance };

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
					{open.length > 0 ? <CaseList label="Cases" cases={open} row={row} /> : null}
					{passed.length > 0 ? (
						<PassedCases cases={passed} all={run.cases.every((c) => c.status === "PASSED")} row={row} />
					) : null}
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

function PassedCases({ cases, all, row }: { cases: TestinyCase[]; all: boolean; row: RowContext }) {
	const [expanded, setExpanded] = useState(false);
	// Counted from the statuses, not the fold: a held order can keep a case here
	// for a moment after it stopped passing.
	const count = cases.filter((c) => c.status === "PASSED").length;
	return (
		<>
			<button
				type="button"
				aria-expanded={expanded}
				className="flex h-6 items-center gap-1 self-start text-[11.5px] text-muted-foreground transition-colors hover:text-foreground"
				onClick={() => setExpanded((v) => !v)}
			>
				<ChevronRight className={cn("size-3 transition-transform", expanded && "rotate-90")} aria-hidden="true" />
				{all ? `All ${count} passed` : `${count} passed`}
			</button>
			{expanded ? <CaseList label="Passed cases" cases={cases} row={row} /> : null}
		</>
	);
}

function CaseList({ label, cases, row }: { label: string; cases: TestinyCase[]; row: RowContext }) {
	return (
		<ul aria-label={label} className="flex flex-col">
			{cases.map((c) => (
				<CaseRow key={c.id} testCase={c} row={row} />
			))}
		</ul>
	);
}

/**
 * The card's case order, held still from a person's first result until the
 * runs are next read after their last write settles. Without it a case set to
 * Passed would fold away, and a failed one jump to the top, from under the
 * pointer that set it.
 */
function useHeldOrder(cases: TestinyCase[], version: number) {
	const [held, setHeld] = useState<{ order: CaseOrder; writes: number; settledAt: number } | null>(null);
	// Not `!==`: the tab can render once with the version from before the write.
	const live = held && (held.writes > 0 || version <= held.settledAt) ? held : null;
	const { open, passed } = orderCases(cases, live?.order);
	const hold: ResultWriteHold = {
		start: () =>
			setHeld({
				order: { open: open.map((c) => c.id), passed: passed.map((c) => c.id) },
				writes: (live?.writes ?? 0) + 1,
				settledAt: -1,
			}),
		end: (settledAt) => setHeld((h) => h && { ...h, writes: h.writes - 1, settledAt }),
	};
	return { open, passed, hold };
}

type CommentStatus = Extract<TestinyStatus, "FAILED" | "BLOCKED" | "SKIPPED">;

/** The comment field for each status that needs one: its name, and a hint in the plain Thai the comment is written in. */
const COMMENT_PROMPT: Record<CommentStatus, { label: string; placeholder: string }> = {
	FAILED: { label: "What went wrong", placeholder: "บอกสั้น ๆ ว่าเกิดอะไรขึ้น (1-2 ประโยค)" },
	BLOCKED: { label: "Why it is blocked", placeholder: "บอกสั้น ๆ ว่าทำไมทดสอบเคสนี้ไม่ได้ (1-2 ประโยค)" },
	SKIPPED: { label: "Why it was skipped", placeholder: "บอกสั้น ๆ ว่าทำไมข้ามเคสนี้ (1-2 ประโยค)" },
};

function CaseRow({ testCase, row }: { testCase: TestinyCase; row: RowContext }): ReactNode {
	const record = useRecordTestinyResult(row.taskId, row.runId, row.hold);
	const [draft, setDraft] = useState<{ status: CommentStatus; text: string } | null>(null);
	const trigger = useRef<HTMLButtonElement>(null);
	const field = useRef<HTMLInputElement>(null);
	const focusFieldOnClose = useRef(false);
	const [expanded, setExpanded] = useState(false);
	const detailsId = useId();
	const reason = standingRecord(testCase)?.comment;
	const provenance = row.provenance(testCase);
	// One write per case at a time, its steps' included, so Testiny cannot land
	// them out of order.
	const busy = record.isPending;
	const stepFailed = record.isError && record.variables && isStepWrite(record.variables);

	const choose = (status: TestinyStatus) => {
		record.reset();
		if (needsComment(status)) {
			focusFieldOnClose.current = true;
			setDraft({ status, text: "" });
		} else if (status !== testCase.status) {
			setDraft(null);
			record.mutate({ caseId: testCase.id, status });
		}
	};
	const saveComment = (status: CommentStatus, comment: string) => {
		setDraft(null);
		trigger.current?.focus();
		// A refused write puts the reason back in the field, so it is not lost.
		record.mutate({ caseId: testCase.id, status, comment }, { onError: () => setDraft({ status, text: comment }) });
	};
	const cancel = () => {
		setDraft(null);
		trigger.current?.focus();
	};

	return (
		<li className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-2 py-1 text-xs leading-snug">
			<ResultMenu
				ref={trigger}
				label="Result"
				status={testCase.status}
				busy={busy}
				onChoose={choose}
				onCloseAutoFocus={(event) => {
					if (!focusFieldOnClose.current) return;
					focusFieldOnClose.current = false;
					event.preventDefault();
					field.current?.focus();
				}}
			/>
			<button
				type="button"
				aria-expanded={expanded}
				aria-controls={expanded ? detailsId : undefined}
				className="min-w-0 self-start rounded-sm text-left text-foreground decoration-passive underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/60"
				onClick={() => setExpanded((v) => !v)}
			>
				{testCase.title}
			</button>
			<div className="flex shrink-0 items-center gap-1 self-start">
				{testCase.script ? (
					<span
						className="rounded border border-border px-1 font-mono text-[10px] leading-4 text-muted-foreground"
						title={testCase.script}
					>
						script
					</span>
				) : null}
				{/* The title is the accessible control; this only makes it visible that a case opens. */}
				<button
					type="button"
					tabIndex={-1}
					aria-hidden="true"
					className="grid size-5 place-items-center rounded-sm text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground"
					onClick={() => setExpanded((v) => !v)}
				>
					<ChevronDown className={cn("size-3.5 transition-transform", expanded && "rotate-180")} />
				</button>
			</div>
			{reason ? (
				<p className="col-span-2 col-start-2 mt-1 text-xs leading-relaxed [overflow-wrap:anywhere] whitespace-pre-line text-muted-foreground">
					{reason}
				</p>
			) : null}
			{provenance ? <span className="col-span-2 col-start-2 mt-0.5 text-[11px] text-passive">{provenance}</span> : null}
			{draft ? (
				<CommentField
					ref={field}
					status={draft.status}
					text={draft.text}
					onChange={(text) => setDraft({ ...draft, text })}
					onSave={(comment) => saveComment(draft.status, comment)}
					onCancel={cancel}
				/>
			) : null}
			{record.isError && !stepFailed ? (
				<p className="col-span-2 col-start-2 mt-0.5 text-[11px] leading-snug text-error" role="alert">
					{record.error.message}
				</p>
			) : null}
			{expanded ? <CaseDetails id={detailsId} row={row} testCase={testCase} record={record} /> : null}
		</li>
	);
}

const MENU_TRIGGER =
	"flex items-start gap-2 rounded px-1 py-0.5 text-left text-muted-foreground transition-colors outline-none hover:bg-raised hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/60 aria-disabled:opacity-60 data-[state=open]:bg-raised data-[state=open]:text-foreground";

/**
 * A result's glyph and word, which opens the five statuses to set it to. While
 * a write is in flight it stays shut but keeps the focus: `disabled` would drop
 * it.
 */
function ResultMenu({
	ref,
	label,
	status,
	busy,
	onChoose,
	align = "start",
	className,
	onCloseAutoFocus,
}: {
	ref?: React.Ref<HTMLButtonElement>;
	label: string;
	status: string;
	busy: boolean;
	onChoose: (status: TestinyStatus) => void;
	align?: "start" | "end";
	className?: string;
	onCloseAutoFocus?: (event: Event) => void;
}) {
	const glyph = testinyCaseGlyph(status);
	const holdShut = (event: React.SyntheticEvent) => {
		if (busy) event.preventDefault();
	};
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				asChild
				onPointerDown={holdShut}
				onKeyDown={(event) => event.key !== "Tab" && holdShut(event)}
			>
				<button
					ref={ref}
					type="button"
					aria-label={`${label}: ${glyph.label}`}
					aria-disabled={busy || undefined}
					className={cn(MENU_TRIGGER, "-mx-1 -my-0.5", className)}
				>
					<glyph.Icon
						className={cn("mt-px size-3.5 shrink-0", TONE[glyph.tone])}
						strokeWidth={2.2}
						aria-hidden="true"
					/>
					<span className="w-12 shrink-0">{glyph.label}</span>
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align={align} className="min-w-36" onCloseAutoFocus={onCloseAutoFocus}>
				<DropdownMenuRadioGroup value={status}>
					{TESTINY_STATUSES.map((option) => {
						const g = testinyCaseGlyph(option);
						return (
							<DropdownMenuRadioItem key={option} value={option} onSelect={() => onChoose(option)}>
								<g.Icon className={TONE[g.tone]} strokeWidth={2.2} aria-hidden="true" />
								{g.label}
							</DropdownMenuRadioItem>
						);
					})}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function CommentField({
	ref,
	status,
	text,
	onChange,
	onSave,
	onCancel,
}: {
	ref: React.Ref<HTMLInputElement>;
	status: CommentStatus;
	text: string;
	onChange: (text: string) => void;
	onSave: (comment: string) => void;
	onCancel: () => void;
}) {
	const prompt = COMMENT_PROMPT[status];
	const comment = text.trim();
	return (
		<form
			className="col-span-3 mt-1.5 flex gap-1.5"
			onSubmit={(event) => {
				event.preventDefault();
				if (comment) onSave(comment);
			}}
		>
			<Input
				ref={ref}
				aria-label={prompt.label}
				placeholder={prompt.placeholder}
				maxLength={TESTINY_COMMENT_MAX}
				className="h-7 px-2 text-xs"
				value={text}
				onChange={(event) => onChange(event.target.value)}
				onKeyDown={(event) => {
					if (event.key !== "Escape") return;
					event.preventDefault();
					event.stopPropagation();
					onCancel();
				}}
			/>
			<Button type="submit" variant="outline" size="sm" className="h-7 text-xs" disabled={!comment}>
				Save
			</Button>
		</form>
	);
}

type RecordResult = ReturnType<typeof useRecordTestinyResult>;

/** A case read in full from Testiny the first time its title is opened, under the title's own edge. */
function CaseDetails({
	id,
	row,
	testCase,
	record,
}: {
	id: string;
	row: RowContext;
	testCase: TestinyCase;
	record: RecordResult;
}) {
	const query = useTestinyCase(row.taskId, testCase.id);
	return (
		<section
			id={id}
			aria-label={`${testCase.title} details`}
			className="col-span-2 col-start-2 mt-2 mb-1.5 flex flex-col gap-3"
		>
			{query.isPending ? (
				<p className="text-[11.5px] text-passive">Reading the case from Testiny…</p>
			) : query.isError ? (
				<div className="flex items-start gap-2">
					<p className="min-w-0 text-[11px] leading-snug [overflow-wrap:anywhere] text-error" role="alert">
						{query.error.message}
					</p>
					<Button
						variant="ghost"
						size="sm"
						className="-my-1 -mr-2 ml-auto h-6 px-2 text-[11px]"
						disabled={query.isFetching}
						onClick={() => void query.refetch()}
					>
						Retry
					</Button>
				</div>
			) : (
				<CaseDetailBody detail={query.data} taskIssueId={row.taskIssueId} testCase={testCase} record={record} />
			)}
		</section>
	);
}

const DETAIL_TEXT = "text-[11.5px] leading-relaxed text-foreground [overflow-wrap:anywhere]";

/** Every section the case fills, in the order a tester plays it; an empty one is left out. */
function CaseDetailBody({
	detail,
	taskIssueId,
	testCase,
	record,
}: {
	detail: TestinyCaseDetail;
	taskIssueId: string | undefined;
	testCase: TestinyCase;
	record: RecordResult;
}) {
	const chips = caseChips(detail);
	return (
		<>
			{chips.length > 0 ? <CaseChips chips={chips} /> : null}
			<CaseFacts facts={caseFacts(detail)} />
			<Requirements detail={detail} taskIssueId={taskIssueId} />
			{detail.testData ? (
				<DetailSection title="Test data">
					<p className={cn(DETAIL_TEXT, "font-mono text-[11px] whitespace-pre-wrap select-text")}>{detail.testData}</p>
				</DetailSection>
			) : null}
			<RichTextSection title="Precondition" text={detail.precondition} />
			{detail.steps.length > 0 ? (
				<DetailSection title="Steps">
					<StepList steps={detail.steps} testCase={testCase} record={record} />
				</DetailSection>
			) : null}
			<RichTextSection title="Steps" text={detail.stepsText} />
			<RichTextSection title="Expected result" text={detail.expectedText} />
			{detail.bdd ? (
				<DetailSection title="Scenario">
					<p className={cn(DETAIL_TEXT, "font-mono text-[11px] whitespace-pre-wrap")}>{detail.bdd}</p>
				</DetailSection>
			) : null}
			<RichTextSection title="Description" text={detail.description} />
			<RichTextSection title="Remark" text={detail.remark} />
		</>
	);
}

function DetailSection({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-1">
			<h4 className={EYEBROW}>{title}</h4>
			{children}
		</div>
	);
}

function CaseFacts({ facts }: { facts: [string, string][] }) {
	if (facts.length === 0) return null;
	return (
		<dl className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1 text-[11.5px] text-muted-foreground">
			{facts.map(([label, value]) => (
				<div key={label} className="contents">
					<dt className={EYEBROW}>{label}</dt>
					<dd className="min-w-0 [overflow-wrap:anywhere]">{value}</dd>
				</div>
			))}
		</dl>
	);
}

function CaseChips({ chips }: { chips: CaseChip[] }) {
	return (
		<ul aria-label="About this case" className="flex flex-wrap gap-1">
			{chips.map((chip, index) => (
				<li key={index} className="max-w-full" title={`${chip.field}: ${chip.value}`}>
					<Badge className="h-auto min-h-[18px] max-w-full shrink rounded-[9px] py-px leading-[14px] [overflow-wrap:anywhere]">
						{chip.field === "Jira" ? <JiraKeys value={chip.value} /> : chip.value}
					</Badge>
				</li>
			))}
		</ul>
	);
}

/**
 * The Jira issues the case is linked to as a requirement, key beside summary
 * and status the way the facts above sit beside their labels, and a quiet line
 * while the task's own issue is not among them yet: qa links it.
 */
function Requirements({ detail, taskIssueId }: { detail: TestinyCaseDetail; taskIssueId: string | undefined }) {
	const missing = unlinkedTaskIssue(detail, taskIssueId);
	if (detail.requirements.length === 0 && !missing) return null;
	return (
		<DetailSection title="Requirements">
			{detail.requirements.length > 0 ? (
				<ul aria-label="Requirements" className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-1">
					{detail.requirements.map((r, index) => (
						<li
							key={index}
							className="col-span-2 grid grid-cols-subgrid items-baseline text-[11.5px] text-muted-foreground [overflow-wrap:anywhere]"
						>
							<span className="font-mono text-[11px]">
								<JiraKeys value={r.key} />
							</span>
							<span>
								<span className="text-foreground">{r.summary}</span>{" "}
								<span className="whitespace-nowrap">
									<span className="text-passive" aria-hidden="true">
										·
									</span>{" "}
									{r.status}
								</span>
							</span>
						</li>
					))}
				</ul>
			) : null}
			{missing ? (
				<p className="flex items-center gap-1.5 text-[11px] leading-snug text-muted-foreground">
					<TriangleAlert className="size-3 shrink-0 text-warning" aria-hidden="true" />
					Not linked to {missing} yet
				</p>
			) : null}
		</DetailSection>
	);
}

/** The case's Jira keys, each linked when Settings has the Jira address; plain text otherwise. */
function JiraKeys({ value }: { value: string }) {
	const settings = useRefLinkSettings().data;
	return (
		<span>
			{splitRefLinks(value, settings).map((part, index) =>
				part.kind === "text" ? (
					part.value
				) : (
					<a
						key={index}
						className="text-accent hover:underline"
						href={part.url}
						target="_blank"
						rel="noopener noreferrer"
					>
						{part.value}
					</a>
				),
			)}
		</span>
	);
}

/** A rich-text field as the testiny CLI renders it: "- " and "1. " runs as lists, the rest as the lines it has. */
function RichTextSection({ title, text }: { title: string; text: string }) {
	const blocks = textBlocks(text);
	if (blocks.length === 0) return null;
	return (
		<DetailSection title={title}>
			{blocks.map((block, index) =>
				block.kind === "text" ? (
					<p key={index} className={cn(DETAIL_TEXT, "whitespace-pre-wrap")}>
						{block.text}
					</p>
				) : block.ordered ? (
					<ol key={index} aria-label={title} start={block.start} className={cn(DETAIL_TEXT, LIST, "list-decimal")}>
						{block.items.map((item, at) => (
							<li key={at}>{item}</li>
						))}
					</ol>
				) : (
					<ul key={index} aria-label={title} className={cn(DETAIL_TEXT, LIST, "list-disc")}>
						{block.items.map((item, at) => (
							<li key={at}>{item}</li>
						))}
					</ul>
				),
			)}
		</DetailSection>
	);
}

const LIST = "flex flex-col gap-0.5 pl-4 marker:text-passive";

/**
 * A STEPS case's table as one row per step, the expected result under its
 * action, and the step's result in the run beside it: a menu that saves at
 * once, since a step takes no comment. Three columns left each text under
 * 100 px in the rail and wrapped most steps onto four lines.
 */
function StepList({
	steps,
	testCase,
	record,
}: {
	steps: TestinyCaseStep[];
	testCase: TestinyCase;
	record: RecordResult;
}) {
	const failed = record.isError && record.variables && isStepWrite(record.variables) ? record.variables.step.n : null;
	const choose = (step: TestinyCaseStep, status: TestinyStatus) => {
		record.reset();
		if (status !== stepStatus(testCase, step))
			record.mutate({ caseId: testCase.id, step: { n: step.n, rid: step.rid }, status });
	};
	return (
		<ol aria-label="Steps" className="flex flex-col text-[11.5px] leading-relaxed">
			{steps.map((step) => (
				<li
					key={step.n}
					className="grid grid-cols-[1rem_minmax(0,1fr)_auto] gap-x-2 gap-y-0.5 border-t border-border py-1.5 first:border-t-0 first:pt-0 last:pb-0"
				>
					<span className="font-mono text-[11px] text-passive tabular-nums">{step.n}</span>
					<p className="whitespace-pre-wrap text-foreground [overflow-wrap:anywhere]">{step.action}</p>
					<ResultMenu
						label={`Step ${step.n} result`}
						status={stepStatus(testCase, step)}
						busy={record.isPending}
						onChoose={(status) => choose(step, status)}
						align="end"
						className="-mr-1 self-start"
					/>
					{step.expected ? (
						<p className="col-span-2 col-start-2 grid grid-cols-[1rem_minmax(0,1fr)] whitespace-pre-wrap text-muted-foreground [overflow-wrap:anywhere]">
							<span className="text-passive" aria-hidden="true">
								→
							</span>
							<span>
								<span className="sr-only">Expected: </span>
								{step.expected}
							</span>
						</p>
					) : null}
					{failed === step.n ? (
						<p className="col-span-2 col-start-2 text-[11px] leading-snug text-error" role="alert">
							{record.error?.message}
						</p>
					) : null}
				</li>
			))}
		</ol>
	);
}
