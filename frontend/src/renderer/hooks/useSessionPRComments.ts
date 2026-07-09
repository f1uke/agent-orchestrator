import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type PRCommentGroup = components["schemas"]["SessionPRCommentGroup"];

export function useSessionPRComments(sessionId: string) {
	return useQuery({
		queryKey: ["session-pr-comments", sessionId],
		enabled: Boolean(sessionId),
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
