import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { mockSessionJiraContexts } from "../lib/mock-data";

export type JiraContext = components["schemas"]["JiraContextResponse"];
export type JiraIssue = components["schemas"]["JiraIssue"];
export type JiraSubtask = components["schemas"]["JiraSubtask"];
export type AdfNode = components["schemas"]["AdfNode"];

export const sessionJiraQueryKey = (sessionId?: string) =>
	sessionId ? (["session-jira", sessionId] as const) : (["session-jira"] as const);

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

const unlinked = (sessionId: string): JiraContext => ({ sessionId, linked: false });

async function fetchSessionJira(sessionId: string): Promise<JiraContext> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/jira", {
		params: { path: { sessionId } },
	});
	if (error) throw error;
	return data ?? unlinked(sessionId);
}

/**
 * Fetches the display-only Jira context for a session. Enable only when the
 * session is Jira-bound (issueId "jira:<KEY>") so unlinked sessions never poll.
 * A Jira-side failure comes back as `linked: true` + `fetchError`, not a thrown
 * error, so the Summary tab degrades gracefully.
 */
export function useSessionJiraContext(sessionId: string | undefined, enabled: boolean) {
	return useQuery({
		queryKey: sessionJiraQueryKey(sessionId),
		enabled: Boolean(sessionId) && enabled,
		queryFn: () =>
			usePreviewData
				? Promise.resolve(mockSessionJiraContexts[sessionId!] ?? unlinked(sessionId!))
				: fetchSessionJira(sessionId!),
		retry: 1,
		staleTime: 60_000,
	});
}
