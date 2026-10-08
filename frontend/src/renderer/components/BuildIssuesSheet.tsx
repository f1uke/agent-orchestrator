import { AlertTriangle, Loader2, SquareTerminal, XCircle } from "lucide-react";
import { useEffect, useRef } from "react";
import type { components } from "../../api/schema";
import { useIosBuildLog, type IosRun } from "../hooks/useIosProject";
import { builtName } from "../lib/ios-run-progress";
import type { WorkspaceFileOpen } from "../lib/open-workspace-file";
import { cn } from "../lib/utils";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "./ui/sheet";

type Issue = components["schemas"]["XcresultstreamIssue"];

export function issueCounts(run: Pick<IosRun, "errors" | "warnings">): string {
	const parts: string[] = [];
	if (run.errors) parts.push(`${run.errors} ${run.errors === 1 ? "error" : "errors"}`);
	if (run.warnings) parts.push(`${run.warnings} ${run.warnings === 1 ? "warning" : "warnings"}`);
	return parts.join(" · ");
}

function location(issue: Issue): string {
	if (!issue.file) return "";
	return [issue.file, issue.line, issue.column].filter(Boolean).join(":");
}

function IssueRow({ issue, onOpen }: { issue: Issue; onOpen?: (file: WorkspaceFileOpen) => void }) {
	const where = location(issue);
	const Icon = issue.severity === "error" ? XCircle : AlertTriangle;
	const body = (
		<>
			<Icon
				aria-hidden
				className={cn("mt-0.5 size-3.5 shrink-0", issue.severity === "error" ? "text-error" : "text-warning")}
			/>
			<span className="min-w-0">
				{where ? <code className="block truncate font-mono text-[12px] text-foreground">{where}</code> : null}
				<span className="block text-[12px] leading-snug text-muted-foreground">{issue.message}</span>
			</span>
		</>
	);
	if (!issue.file || !onOpen) {
		return <li className="flex items-start gap-2 rounded-md px-2 py-1.5">{body}</li>;
	}
	return (
		<li>
			<button
				className="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-overlay"
				onClick={() => onOpen({ path: issue.file ?? "", line: issue.line, column: issue.column })}
				title={`Open ${where}`}
				type="button"
			>
				{body}
			</button>
		</li>
	);
}

function LogExcerpt({ open, sessionId, startedAt }: { open: boolean; sessionId: string; startedAt: string }) {
	const log = useIosBuildLog(sessionId, startedAt, open);
	const errorRef = useRef<HTMLDivElement | null>(null);
	useEffect(() => {
		errorRef.current?.scrollIntoView({ block: "center" });
	}, [log.data]);
	if (log.isLoading) {
		return (
			<p className="flex items-center gap-2 text-[12px] text-muted-foreground">
				<Loader2 aria-hidden className="size-3.5 animate-spin motion-reduce:animate-none" />
				Reading the build log…
			</p>
		);
	}
	if (!log.data) return <p className="text-[12px] text-muted-foreground">This run kept no build log.</p>;
	const { lines, firstLine, errorLine, totalLines } = log.data;
	return (
		<div>
			<p className="mb-1.5 text-[11px] text-muted-foreground">
				{errorLine
					? `Lines ${firstLine}-${firstLine + lines.length - 1} of ${totalLines}, at the first error`
					: `The last ${lines.length} of ${totalLines} lines`}
			</p>
			<div
				className="max-h-80 overflow-auto rounded-md bg-muted/60 py-1.5 font-mono text-[11px] leading-[1.45]"
				data-testid="build-log-excerpt"
			>
				{lines.map((line, i) => {
					const number = firstLine + i;
					const isError = number === errorLine;
					return (
						<div
							className={cn("flex gap-3 px-2", isError && "bg-error/15 text-foreground")}
							key={number}
							ref={isError ? errorRef : undefined}
						>
							<span className="w-10 shrink-0 select-none text-right tabular-nums text-passive">{number}</span>
							<span className="whitespace-pre-wrap break-all text-muted-foreground">{line}</span>
						</div>
					);
				})}
			</div>
		</div>
	);
}

/** A failed build's errors with their file and line, and its log at the first one. */
export function BuildIssuesSheet({
	onOpenChange,
	onOpenWorkspaceFile,
	onShowRun,
	open,
	run,
	sessionId,
}: {
	onOpenChange: (open: boolean) => void;
	onOpenWorkspaceFile?: (file: WorkspaceFileOpen) => void;
	onShowRun: (handleId: string) => void;
	open: boolean;
	run: IosRun;
	sessionId: string;
}) {
	const issues = run.issues ?? [];
	const errors = issues.filter((issue) => issue.severity === "error");
	const warnings = issues.filter((issue) => issue.severity !== "error");
	const openFile = onOpenWorkspaceFile
		? (file: WorkspaceFileOpen) => {
				onOpenWorkspaceFile(file);
				onOpenChange(false);
			}
		: undefined;
	return (
		<Sheet onOpenChange={onOpenChange} open={open}>
			<SheetContent className="w-full gap-0 sm:max-w-xl" side="right">
				<SheetHeader className="border-b border-border">
					<SheetTitle>{builtName(run)} failed</SheetTitle>
					<SheetDescription>{[run.summary, issueCounts(run)].filter(Boolean).join(" · ")}</SheetDescription>
				</SheetHeader>
				<div className="flex-1 space-y-4 overflow-y-auto p-4">
					{errors.length ? (
						<section>
							<h3 className="mb-1 text-[12px] font-medium text-foreground">
								{run.errors && run.errors > errors.length ? `First ${errors.length} of ${run.errors} errors` : "Errors"}
							</h3>
							<ul className="-mx-2">
								{errors.map((issue, i) => (
									<IssueRow issue={issue} key={`${location(issue)}-${i}`} onOpen={openFile} />
								))}
							</ul>
						</section>
					) : null}
					<section>
						<h3 className="mb-1 text-[12px] font-medium text-foreground">Build log</h3>
						<LogExcerpt open={open} sessionId={sessionId} startedAt={run.startedAt} />
					</section>
					{warnings.length ? (
						<details>
							<summary className="cursor-pointer text-[12px] font-medium text-foreground">
								{run.warnings && run.warnings > warnings.length
									? `First ${warnings.length} of ${run.warnings} warnings`
									: `${warnings.length} ${warnings.length === 1 ? "warning" : "warnings"}`}
							</summary>
							<ul className="-mx-2 mt-1">
								{warnings.map((issue, i) => (
									<IssueRow issue={issue} key={`${location(issue)}-${i}`} onOpen={openFile} />
								))}
							</ul>
						</details>
					) : null}
				</div>
				<div className="border-t border-border p-3">
					<button
						className="flex h-7 items-center gap-1.5 rounded-md px-2.5 text-[12px] font-medium text-foreground transition-colors hover:bg-overlay"
						onClick={() => {
							onShowRun(run.handleId);
							onOpenChange(false);
						}}
						type="button"
					>
						<SquareTerminal aria-hidden className="size-3.5" />
						Show the whole output in the terminal
					</button>
				</div>
			</SheetContent>
		</Sheet>
	);
}
