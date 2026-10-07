import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type PRCommentGroup = components["schemas"]["SessionPRCommentGroup"];

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/**
 * Shared prefix for the per-session PR-comments query key. Invalidating this
 * prefix (via the CDC event transport) matches every session's comment query so
 * the Reviews tab refreshes its threads + Resolved section on external changes.
 */
export const sessionPRCommentsQueryPrefix = ["session-pr-comments"] as const;

export function useSessionPRComments(sessionId: string) {
	return useQuery({
		queryKey: [...sessionPRCommentsQueryPrefix, sessionId],
		// The preview has no daemon to ask; ReviewsView draws its mock threads.
		enabled: Boolean(sessionId) && !usePreviewData,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/pr-comments", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load comments"));
			return data?.prs ?? [];
		},
		retry: 1,
	});
}
