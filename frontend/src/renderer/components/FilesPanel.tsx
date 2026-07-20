import { FileText, FolderOpen, GitBranch, ListTree, RefreshCw } from "lucide-react";
import { type ChangedFile, useWorkspaceChanges } from "../hooks/useWorkspaceChanges";
import { apiErrorMessage } from "../lib/api-client";
import { cn } from "../lib/utils";
import { Skeleton } from "./ui/skeleton";
import { SimpleTooltip, TooltipProvider } from "./ui/tooltip";

/**
 * What a clicked row opens in the center pane.
 *
 * Every row opens as a DIFF against the target branch, never as a file read.
 * That is deliberate: a deleted file has no working-tree content, so routing
 * rows to the file endpoint would 404 on exactly the rows a reviewer most wants
 * to inspect. Diffing every row avoids the trap structurally instead of
 * special-casing the deleted status.
 */
export type ChangedFileTarget = { path: string };

/**
 * Changes mode: the files differing between this session's branch (working tree
 * included) and its target branch.
 *
 * The rail runs ~330px by default and never narrower than 280px (SessionView's
 * wrapper pins that min-width so the collapse animation does not reflow), so
 * this panel is a NAVIGATOR, not a viewer — a diff needs far more width than
 * that. Clicking a row opens the diff in the center pane, the same swap the
 * terminal's clickable file references already perform.
 * Browse mode ships separately; its segment is present but disabled so the
 * control does not change shape when it lands.
 */
export function FilesPanel({
	sessionId,
	onOpenFile,
	selectedPath,
}: {
	sessionId: string;
	onOpenFile?: (target: ChangedFileTarget) => void;
	selectedPath?: string;
}) {
	const query = useWorkspaceChanges(sessionId);
	const data = query.data;

	return (
		<TooltipProvider delayDuration={0}>
			<div className="files-panel" role="tabpanel">
				<div className="files-panel__modes">
					<div className="files-panel__seg" role="tablist" aria-label="Files mode">
						<button type="button" role="tab" aria-selected="true" className="files-panel__seg-btn is-active">
							<ListTree aria-hidden="true" className="h-3 w-3 shrink-0" />
							<span className="files-panel__seg-label">Changes</span>
						</button>
						<SimpleTooltip label="Browsing the whole worktree ships separately">
							{/* A disabled button emits no pointer events, so the tooltip needs a
						    wrapper to hover. */}
							<span className="files-panel__seg-slot">
								<button type="button" role="tab" aria-selected="false" disabled className="files-panel__seg-btn">
									<FolderOpen aria-hidden="true" className="h-3 w-3 shrink-0" />
									<span className="files-panel__seg-label">Browse</span>
								</button>
							</span>
						</SimpleTooltip>
					</div>
				</div>

				{query.isLoading ? <ChangesSkeleton /> : null}

				{query.error ? (
					<p className="files-panel__empty-text">{apiErrorMessage(query.error, "Unable to load changes")}</p>
				) : null}

				{data && !data.available ? <UnavailableState reason={data.reason} branch={data.targetBranch} /> : null}

				{data?.available ? (
					<>
						<SummaryLine
							branch={data.targetBranch}
							inferred={data.targetSource === "project" || data.targetSource === "git_origin_head"}
							count={data.files.length}
							additions={data.files.reduce((n, f) => n + (f.binary ? 0 : f.additions), 0)}
							deletions={data.files.reduce((n, f) => n + (f.binary ? 0 : f.deletions), 0)}
							onRefresh={() => void query.refetch()}
							refreshing={query.isFetching}
						/>
						{data.files.length === 0 ? (
							<EmptyState
								icon={<CheckIcon />}
								title={`No changes vs ${data.targetBranch || "target"}`}
								detail="This branch matches its target branch. Nothing to review yet."
							/>
						) : (
							<div className="files-panel__list">
								{data.files.map((file) => (
									<ChangedFileRow
										key={file.path}
										file={file}
										selected={file.path === selectedPath}
										onOpen={onOpenFile}
									/>
								))}
								{data.truncated ? (
									<p className="files-panel__truncated">
										Showing the first {data.files.length} files — the diff is larger.
									</p>
								) : null}
							</div>
						)}
					</>
				) : null}
			</div>
		</TooltipProvider>
	);
}

const STATUS_LETTER: Record<string, string> = {
	added: "A",
	modified: "M",
	deleted: "D",
	renamed: "R",
};

function ChangedFileRow({
	file,
	selected,
	onOpen,
}: {
	file: ChangedFile;
	selected: boolean;
	onOpen?: (target: ChangedFileTarget) => void;
}) {
	const slash = file.path.lastIndexOf("/");
	const name = slash >= 0 ? file.path.slice(slash + 1) : file.path;
	const dir = slash >= 0 ? file.path.slice(0, slash) : "";
	const oldName = file.oldPath ? file.oldPath.slice(file.oldPath.lastIndexOf("/") + 1) : "";
	const label = oldName ? `${oldName} → ${name}` : name;

	return (
		<button
			type="button"
			data-path={file.path}
			aria-current={selected ? "true" : undefined}
			className={cn("files-panel__row", selected && "is-selected")}
			onClick={() => onOpen?.({ path: file.path })}
			title={file.path}
		>
			<span className="files-panel__lead">
				{!file.committed ? (
					<span aria-label="uncommitted" className="files-panel__uncommitted" title="Uncommitted changes" />
				) : null}
				<span aria-hidden="true" className={cn("files-panel__glyph", `is-${file.status}`)}>
					{STATUS_LETTER[file.status] ?? "M"}
				</span>
			</span>
			<span className="files-panel__name">
				<bdi>{label}</bdi>
			</span>
			{/* One counts element placed by the row grid, rather than a second copy
			    on the wrapped line — duplicate text would be announced twice by
			    assistive tech whenever the stylesheet failed to load. */}
			<Counts file={file} className="files-panel__counts" />
			<span className="files-panel__dir">
				<bdi>{dir}</bdi>
			</span>
		</button>
	);
}

function Counts({ file, className }: { file: ChangedFile; className?: string }) {
	// git emits "-" counts for a binary file; rendering them arithmetically
	// produces a nonsense "+0 −0".
	if (file.binary) {
		return <span className={cn(className, "files-panel__counts--binary")}>bin</span>;
	}
	return (
		<span className={className}>
			<span className="files-panel__add">+{file.additions}</span>{" "}
			<span className="files-panel__del">−{file.deletions}</span>
		</span>
	);
}

function SummaryLine({
	branch,
	inferred,
	count,
	additions,
	deletions,
	onRefresh,
	refreshing,
}: {
	branch?: string;
	inferred: boolean;
	count: number;
	additions: number;
	deletions: number;
	onRefresh: () => void;
	refreshing: boolean;
}) {
	return (
		<div className="files-panel__summary">
			<span
				className="files-panel__vs"
				title={inferred ? `Comparing against ${branch} (inferred)` : `Comparing against ${branch}`}
			>
				vs {branch}
				{inferred ? <span className="files-panel__inferred">*</span> : null}
			</span>
			<span className="files-panel__sep">·</span>
			<span className="files-panel__count">
				{count} {count === 1 ? "file" : "files"}
			</span>
			<span className="files-panel__totals">
				<span className="files-panel__add">+{additions}</span> <span className="files-panel__del">−{deletions}</span>
			</span>
			<button
				type="button"
				aria-label="Refresh changes"
				title="Refresh"
				className="files-panel__refresh"
				onClick={onRefresh}
			>
				<RefreshCw aria-hidden="true" className={cn("h-3 w-3", refreshing && "animate-spin")} />
			</button>
		</div>
	);
}

function UnavailableState({ reason, branch }: { reason?: string; branch?: string }) {
	if (reason === "no_workspace") {
		return (
			<EmptyState
				icon={<FolderOpen aria-hidden="true" className="h-6 w-6" />}
				title="Worktree no longer on disk"
				detail="This session's worktree was cleaned up. Its diff lives on the pull request."
			/>
		);
	}
	if (reason === "not_a_repo") {
		return (
			<EmptyState
				icon={<FileText aria-hidden="true" className="h-6 w-6" />}
				title="Not a git repository"
				detail="This session's workspace is not a git repository, so there is nothing to diff."
			/>
		);
	}
	// no_target_branch — deliberately never guesses "main": a wrong target
	// renders a confidently wrong diff.
	return (
		<EmptyState
			icon={<GitBranch aria-hidden="true" className="h-6 w-6" />}
			title="No target branch to compare"
			detail={
				branch
					? `This session names ${branch} as its target, but that branch does not exist in this worktree.`
					: "This session has no PR and the project has no default branch set, so there is nothing to diff against."
			}
		/>
	);
}

function EmptyState({ icon, title, detail }: { icon: React.ReactNode; title: string; detail: string }) {
	return (
		<div className="files-panel__empty">
			<span className="files-panel__empty-icon">{icon}</span>
			<span className="files-panel__empty-title">{title}</span>
			<span className="files-panel__empty-text">{detail}</span>
		</div>
	);
}

function CheckIcon() {
	return (
		<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" className="h-6 w-6" aria-hidden="true">
			<path d="M20 6 9 17l-5-5" />
		</svg>
	);
}

function ChangesSkeleton() {
	return (
		<div className="files-panel__list" aria-hidden="true">
			{[0, 1, 2, 3].map((i) => (
				<div key={i} className="files-panel__row">
					<span className="files-panel__row-main">
						<Skeleton className="h-3 w-3 rounded-sm" />
						<Skeleton className="h-3 flex-1" />
					</span>
					<span className="files-panel__row-sub">
						<Skeleton className="h-2.5 w-2/3" />
					</span>
				</div>
			))}
		</div>
	);
}
