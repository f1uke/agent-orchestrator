import { prTitleLabel } from "../lib/pr-display";
import { useSessionPRComments, type PRCommentGroup } from "../hooks/useSessionPRComments";
import { apiErrorMessage } from "../lib/api-client";
import { Badge } from "./ui/badge";
import { DiffHunk } from "./DiffHunk";
import { SendToWorkerButton } from "./SendToWorkerButton";
import { ThreadActions } from "./ThreadActions";

export type Thread = PRCommentGroup["threads"][number];
export type Comment = Thread["comments"][number];

/**
 * Comments tab: lists each PR/MR's review threads GitHub-style (author,
 * body, file:line) with the anchored diff hunk (expandable to the full
 * file), plus a reply box and resolve button per thread.
 */
export function CommentsView({ sessionId }: { sessionId: string }) {
	const query = useSessionPRComments(sessionId);
	if (query.isLoading) {
		return <p className="inspector-empty">Loading comments…</p>;
	}
	if (query.error) {
		return <p className="inspector-empty">{apiErrorMessage(query.error, "Unable to load comments")}</p>;
	}
	const groups = (query.data ?? []).filter((group) => group.threads.length > 0);
	if (groups.length === 0) {
		return <p className="inspector-empty">No review comments yet.</p>;
	}
	return (
		<div role="tabpanel">
			{groups.map((group) => (
				<section className="inspector-section" key={group.prUrl}>
					<div className="inspector-section__head">
						<span>{prTitleLabel(group.provider === "gitlab" ? "gitlab" : "github", group.number)}</span>
					</div>
					<div className="flex flex-col gap-2">
						{group.threads.map((thread) => (
							<ThreadCard key={thread.threadId} sessionId={sessionId} prUrl={group.prUrl} thread={thread} />
						))}
					</div>
				</section>
			))}
		</div>
	);
}

function ThreadCard({ sessionId, prUrl, thread }: { sessionId: string; prUrl: string; thread: Thread }) {
	return (
		<div className="rounded-[7px] border border-border bg-surface">
			<div className="flex items-center gap-2 border-b border-border px-3 py-1.5">
				<span className="truncate font-mono text-[11.5px] text-foreground">{thread.path}</span>
				<span className="shrink-0 text-[11px] text-muted-foreground">:{thread.line}</span>
				{thread.resolved && (
					<Badge className="ml-auto" variant="success">
						Resolved
					</Badge>
				)}
			</div>
			{thread.path && thread.line > 0 && (
				<DiffHunk sessionId={sessionId} prUrl={prUrl} path={thread.path} line={thread.line} />
			)}
			<div className="flex flex-col gap-2.5 px-3 py-2.5">
				{thread.comments.map((comment) => (
					<CommentRow comment={comment} key={comment.id} />
				))}
			</div>
			<div className="flex flex-col gap-2 border-t border-border px-3 py-2">
				<ThreadActions sessionId={sessionId} prUrl={prUrl} thread={thread} />
				<div className="flex justify-end">
					<SendToWorkerButton sessionId={sessionId} prUrl={prUrl} threadId={thread.threadId} />
				</div>
			</div>
		</div>
	);
}

function CommentRow({ comment }: { comment: Comment }) {
	return (
		<div className="flex flex-col gap-0.5">
			<div className="flex items-center gap-2">
				<span className="grid h-5 w-5 shrink-0 place-items-center rounded-full bg-raised text-[10px] font-medium text-muted-foreground">
					{initials(comment.author)}
				</span>
				<span className="text-[12px] font-medium text-foreground">{comment.author || "unknown"}</span>
				{comment.isBot && (
					<Badge variant="neutral" className="h-[16px] px-1.5 text-[9px]">
						Bot
					</Badge>
				)}
			</div>
			<p className="whitespace-pre-wrap pl-7 text-[12px] leading-snug text-foreground">{comment.body}</p>
		</div>
	);
}

function initials(name: string): string {
	const trimmed = (name || "?").trim();
	return trimmed ? trimmed.slice(0, 2).toUpperCase() : "?";
}
