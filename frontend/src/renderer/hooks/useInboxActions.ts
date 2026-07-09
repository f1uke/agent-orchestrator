import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { baseName } from "../lib/comment-inbox";
import { useReplyToThread, useResolveThread } from "./useThreadActions";

const TOAST_MS = 2800;

/**
 * The four review-comment actions (resolve / reply / dispatch-to-worker / send)
 * plus a transient toast, shared by the Comments inbox and the full-file diff
 * viewer's anchored comment so both surfaces behave identically. Resolve and
 * reply reuse the cache-patching thread hooks; dispatch and send post directly.
 */
export function useInboxActions(sessionId: string) {
	const reply = useReplyToThread(sessionId);
	const resolve = useResolveThread(sessionId);
	const dispatch = useMutation({
		mutationFn: async (vars: { prUrl: string; threadId: string; extraPrompt?: string }) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/comment-dispatch", {
				params: { path: { sessionId } },
				body: vars,
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to send"));
		},
	});
	const send = useMutation({
		mutationFn: async (message: string) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/send", {
				params: { path: { sessionId } },
				body: { message },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to send"));
		},
	});
	const busy = resolve.isPending || dispatch.isPending || send.isPending;

	const [toast, setToast] = useState<string | null>(null);
	const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const showToast = (msg: string) => {
		setToast(msg);
		if (toastTimer.current) clearTimeout(toastTimer.current);
		toastTimer.current = setTimeout(() => setToast(null), TOAST_MS);
	};
	useEffect(() => () => void (toastTimer.current && clearTimeout(toastTimer.current)), []);

	const doResolve = (prUrl: string, threadId: string) => {
		resolve.mutate({ prUrl, threadId });
		showToast("Comment resolved");
	};
	const doReply = (prUrl: string, threadId: string, body: string, author: string) => {
		reply.mutate({ prUrl, threadId, body });
		showToast(`Replied to ${author || "author"}`);
	};
	const doSendQuick = (prUrl: string, threadId: string, path: string) => {
		dispatch.mutate({ prUrl, threadId });
		showToast(`Sent to worker · ${baseName(path)}`);
	};
	const doSendPrompt = (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => {
		send.mutate(message);
		if (resolveAfter) resolve.mutate({ prUrl, threadId });
		showToast(resolveAfter ? "Prompt sent to worker · resolving" : "Prompt sent to worker");
	};

	return { reply, resolve, dispatch, send, busy, toast, showToast, doResolve, doReply, doSendQuick, doSendPrompt };
}
