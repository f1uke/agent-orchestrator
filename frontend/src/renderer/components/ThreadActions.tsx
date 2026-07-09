import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useReplyToThread, useResolveThread } from "../hooks/useThreadActions";
import { apiErrorMessage } from "../lib/api-client";
import type { Thread } from "./CommentsView";
import { Button } from "./ui/button";
import { Textarea } from "./ui/textarea";

/**
 * Reply box + resolve button for a review-thread card. Sits alongside
 * `SendToWorkerButton` in the `ThreadCard` footer.
 */
export function ThreadActions({
	sessionId,
	prUrl,
	thread,
}: {
	sessionId: string;
	prUrl: string;
	thread: Thread;
}) {
	const [body, setBody] = useState("");
	const [composing, setComposing] = useState(false);
	const reply = useReplyToThread(sessionId);
	const resolve = useResolveThread(sessionId);
	const busy = reply.isPending || resolve.isPending;

	useEffect(() => {
		if (reply.isSuccess) {
			setBody("");
			setComposing(false);
		}
	}, [reply.isSuccess]);

	function submitReply() {
		if (reply.isPending || body.trim().length === 0) return;
		reply.mutate({ prUrl, threadId: thread.threadId, body });
	}

	function cancelReply() {
		setComposing(false);
		setBody("");
	}

	return (
		<div className="flex w-full flex-col gap-1.5">
			{composing ? (
				<div className="flex items-start gap-2">
					<Textarea
						autoFocus
						aria-label={`Reply to thread at ${thread.path}:${thread.line}`}
						className="min-h-8 flex-1 text-[12px]"
						placeholder="Reply…"
						value={body}
						onChange={(event) => setBody(event.target.value)}
						onKeyDown={(event) => {
							if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
								event.preventDefault();
								submitReply();
							}
						}}
					/>
					<div className="flex shrink-0 gap-1.5">
						<Button type="button" variant="ghost" size="sm" disabled={busy} onClick={cancelReply}>
							Cancel
						</Button>
						<Button
							type="button"
							size="sm"
							disabled={busy || body.trim().length === 0}
							onClick={submitReply}
						>
							{reply.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />}
							Reply
						</Button>
					</div>
				</div>
			) : (
				<div className="flex items-center justify-end gap-1.5">
					{!thread.resolved && (
						<Button
							type="button"
							variant="outline"
							size="sm"
							disabled={busy}
							onClick={() => resolve.mutate({ prUrl, threadId: thread.threadId })}
						>
							{resolve.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />}
							Resolve
						</Button>
					)}
					<Button
						type="button"
						variant="outline"
						size="sm"
						disabled={busy}
						onClick={() => setComposing(true)}
					>
						Reply
					</Button>
				</div>
			)}
			{(reply.isError || resolve.isError) && (
				<div className="text-[11px] text-destructive" role="alert">
					{apiErrorMessage(reply.isError ? reply.error : resolve.error, "Unable to complete action")}
				</div>
			)}
		</div>
	);
}
