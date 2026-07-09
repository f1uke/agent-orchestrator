import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { PRCommentGroup } from "./useSessionPRComments";

type ThreadComment = PRCommentGroup["threads"][number]["comments"][number];

function patchGroups(
	groups: PRCommentGroup[] | undefined,
	prUrl: string,
	threadId: string,
	fn: (thread: PRCommentGroup["threads"][number]) => PRCommentGroup["threads"][number],
): PRCommentGroup[] {
	return (groups ?? []).map((g) =>
		g.prUrl !== prUrl
			? g
			: { ...g, threads: g.threads.map((t) => (t.threadId === threadId ? fn(t) : t)) },
	);
}

export function useReplyToThread(sessionId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (vars: { prUrl: string; threadId: string; body: string }) => {
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/comment-reply", {
				params: { path: { sessionId } },
				body: vars,
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to reply"));
			return data!;
		},
		onSuccess: (data, vars) => {
			qc.setQueryData<PRCommentGroup[]>(["session-pr-comments", sessionId], (groups) =>
				patchGroups(groups, vars.prUrl, vars.threadId, (t) => ({
					...t,
					comments: [...t.comments, data.comment as ThreadComment],
				})),
			);
		},
	});
}

export function useResolveThread(sessionId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (vars: { prUrl: string; threadId: string }) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/comment-resolve", {
				params: { path: { sessionId } },
				body: vars,
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to resolve"));
		},
		onSuccess: (_data, vars) => {
			qc.setQueryData<PRCommentGroup[]>(["session-pr-comments", sessionId], (groups) =>
				patchGroups(groups, vars.prUrl, vars.threadId, (t) => ({ ...t, resolved: true })),
			);
		},
	});
}
