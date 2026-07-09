import { ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { prTitleLabel } from "../lib/pr-display";
import { useSessionPRComments, type PRCommentGroup } from "../hooks/useSessionPRComments";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { cn } from "../lib/utils";
import { Badge } from "./ui/badge";
import { Switch } from "./ui/switch";
import { DiffHunk } from "./DiffHunk";
import { FileHeader } from "./FileHeader";
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
	const queryClient = useQueryClient();

	const settingsQuery = useQuery({
		queryKey: ["settings", "autoNudge"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/auto-nudge", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
	});
	const sessionQuery = useQuery({
		queryKey: ["session", sessionId, "autoNudge"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data.session;
		},
	});
	const override = sessionQuery.data?.autoNudgeComments ?? null;
	const globalDefault = settingsQuery.data?.enabled ?? false;
	const effective = override !== null ? override : globalDefault;

	const setOverride = useMutation({
		mutationFn: async (next: boolean | null) => {
			const { error } = await apiClient.PUT("/api/v1/sessions/{sessionId}/auto-nudge", {
				params: { path: { sessionId } },
				body: { override: next },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: ["session", sessionId, "autoNudge"] });
		},
	});

	let body: React.ReactNode;
	if (query.isLoading) {
		body = <p className="inspector-empty">Loading comments…</p>;
	} else if (query.error) {
		body = <p className="inspector-empty">{apiErrorMessage(query.error, "Unable to load comments")}</p>;
	} else {
		const groups = (query.data ?? []).filter((group) => group.threads.length > 0);
		body =
			groups.length === 0 ? (
				<p className="inspector-empty">No review comments yet.</p>
			) : (
				<>
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
				</>
			);
	}

	return (
		<div role="tabpanel">
			<AutoNudgeToggle
				effective={effective}
				override={override}
				busy={settingsQuery.isLoading || sessionQuery.isLoading || setOverride.isPending}
				onToggle={(next) => setOverride.mutate(next)}
				onReset={() => setOverride.mutate(null)}
				error={setOverride.isError ? apiErrorMessage(setOverride.error) : null}
			/>
			{body}
		</div>
	);
}

function AutoNudgeToggle({
	effective,
	override,
	busy,
	onToggle,
	onReset,
	error,
}: {
	effective: boolean;
	override: boolean | null;
	busy: boolean;
	onToggle: (next: boolean) => void;
	onReset: () => void;
	error: string | null;
}) {
	return (
		<div className="flex flex-col gap-1 border-b border-border px-3 py-2">
			<div className="flex items-center justify-between gap-3">
				<div className="flex flex-col">
					<span className="text-[12px] font-medium text-foreground">Auto-send unresolved comments to the worker</span>
					<span className="text-[11px] text-muted-foreground">
						{override === null ? "Following the global default" : "Overridden for this session"}
					</span>
				</div>
				<div className="flex items-center gap-2">
					{override !== null && (
						<button
							type="button"
							className="text-[11px] text-accent hover:underline disabled:opacity-50"
							disabled={busy}
							onClick={onReset}
						>
							Reset to default
						</button>
					)}
					<Switch
						aria-label="Auto-send unresolved comments to the worker"
						checked={effective}
						disabled={busy}
						onCheckedChange={onToggle}
					/>
				</div>
			</div>
			{error && (
				<span className="text-[11px] text-error" role="alert">
					{error}
				</span>
			)}
		</div>
	);
}

function ThreadCard({ sessionId, prUrl, thread }: { sessionId: string; prUrl: string; thread: Thread }) {
	// Resolved threads start collapsed (they're settled, no action needed);
	// unresolved threads start expanded so they draw the eye.
	const [open, setOpen] = useState(!thread.resolved);
	return (
		<div className="overflow-hidden rounded-[7px] border border-border bg-surface">
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((o) => !o)}
				className={cn(
					"flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-interactive-hover",
					open && "border-b border-border",
				)}
			>
				{open ? (
					<ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
				) : (
					<ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
				)}
				<FileHeader path={thread.path} line={thread.line} />
				{thread.resolved && (
					<>
						<Badge className="ml-auto" variant="success">
							Resolved
						</Badge>
						<span className="shrink-0 text-[11px] text-muted-foreground">
							· {thread.comments.length} {thread.comments.length === 1 ? "comment" : "comments"}
						</span>
					</>
				)}
			</button>
			{open && (
				<>
					{thread.path && thread.line > 0 && (
						<DiffHunk sessionId={sessionId} prUrl={prUrl} path={thread.path} line={thread.line} />
					)}
					<div className="flex flex-col gap-1.5 px-3 py-2">
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
				</>
			)}
		</div>
	);
}

function CommentRow({ comment }: { comment: Comment }) {
	return (
		<div className="flex flex-col gap-0.5">
			<div className="flex items-center gap-2">
				<span className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-raised text-[11px] font-medium text-muted-foreground">
					{initials(comment.author)}
				</span>
				<span className="text-[13px] font-medium text-foreground">{comment.author || "unknown"}</span>
				{comment.isBot && (
					<Badge variant="neutral" className="h-[16px] px-1.5 text-[9px]">
						Bot
					</Badge>
				)}
			</div>
			<p className="whitespace-pre-wrap pl-9 text-[12px] leading-snug text-foreground">{comment.body}</p>
		</div>
	);
}

function initials(name: string): string {
	const trimmed = (name || "?").trim();
	return trimmed ? trimmed.slice(0, 2).toUpperCase() : "?";
}
