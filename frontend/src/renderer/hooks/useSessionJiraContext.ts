import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockSessionJiraContexts, mockSessionJiraTransitions } from "../lib/mock-data";

export type JiraContext = components["schemas"]["JiraContextResponse"];
export type JiraIssue = components["schemas"]["JiraIssue"];
export type JiraSubtask = components["schemas"]["JiraSubtask"];
export type JiraTransition = components["schemas"]["JiraTransition"];
export type JiraMoveResponse = components["schemas"]["JiraMoveResponse"];
export type AdfNode = components["schemas"]["AdfNode"];

export const sessionJiraQueryKey = (sessionId?: string) =>
	sessionId ? (["session-jira", sessionId] as const) : (["session-jira"] as const);

export const sessionJiraTransitionsQueryKey = (sessionId?: string) =>
	sessionId ? (["session-jira-transitions", sessionId] as const) : (["session-jira-transitions"] as const);

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

async function fetchJiraTransitions(sessionId: string): Promise<JiraTransition[]> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/jira/transitions", {
		params: { path: { sessionId } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Couldn't load Jira transitions"));
	return data?.transitions ?? [];
}

/**
 * Fetches the linked issue's available status transitions, LIVE (never
 * hardcoded — they differ per issue type and current status). Enable only while
 * the Move-status dialog is open so we don't poll otherwise. Unlike the display
 * context, a failure here throws so the dialog can surface it (e.g. a missing
 * JIRA_API_TOKEN).
 */
export function useJiraTransitions(sessionId: string | undefined, enabled: boolean) {
	return useQuery({
		queryKey: sessionJiraTransitionsQueryKey(sessionId),
		enabled: Boolean(sessionId) && enabled,
		queryFn: () =>
			usePreviewData ? Promise.resolve(mockSessionJiraTransitions[sessionId!] ?? []) : fetchJiraTransitions(sessionId!),
		// Transitions become stale the moment the status moves; keep them short.
		staleTime: 15_000,
	});
}

/**
 * Applies a status transition — the ONE sanctioned Jira write. On success it
 * invalidates the display context (new status) and the transitions list (the new
 * status has a different available set). In preview mode it is a no-op success so
 * the dialog flow can be demoed without a daemon.
 */
export function useMoveJiraStatus(sessionId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (transitionId: string): Promise<JiraMoveResponse> => {
			if (usePreviewData) {
				return { sessionId, key: "", status: "", statusCategory: "" };
			}
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/jira/move", {
				params: { path: { sessionId } },
				body: { transitionId },
			});
			if (error) throw new Error(apiErrorMessage(error, "Couldn't move the Jira status"));
			return data!;
		},
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: sessionJiraQueryKey(sessionId) });
			void qc.invalidateQueries({ queryKey: sessionJiraTransitionsQueryKey(sessionId) });
		},
	});
}
