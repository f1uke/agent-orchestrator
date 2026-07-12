import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "./useWorkspaceQuery";
import {
	mockJiraIssue,
	mockJiraProjects,
	mockJiraSearch,
	mockSessionJiraContexts,
	mockSessionJiraTransitions,
} from "../lib/mock-data";

export type JiraContext = components["schemas"]["JiraContextResponse"];
export type JiraIssue = components["schemas"]["JiraIssue"];
export type JiraSubtask = components["schemas"]["JiraSubtask"];
export type JiraTransition = components["schemas"]["JiraTransition"];
export type JiraMoveResponse = components["schemas"]["JiraMoveResponse"];
export type JiraIssueSummary = components["schemas"]["JiraIssueSummary"];
export type JiraProject = components["schemas"]["JiraProject"];
export type JiraLinkResponse = components["schemas"]["JiraLinkResponse"];
export type AdfNode = components["schemas"]["AdfNode"];

export const sessionJiraQueryKey = (sessionId?: string) =>
	sessionId ? (["session-jira", sessionId] as const) : (["session-jira"] as const);

// Transitions are cached per (session, targetKey): the bound issue and each
// subtask have their own transition set. An undefined issueKey = the bound issue.
// Invalidating with just (sessionId) prefix-matches the bound issue AND every
// subtask, so one move refreshes them all.
export const sessionJiraTransitionsQueryKey = (sessionId?: string, issueKey?: string) => {
	if (!sessionId) return ["session-jira-transitions"] as const;
	return issueKey
		? (["session-jira-transitions", sessionId, issueKey] as const)
		: (["session-jira-transitions", sessionId] as const);
};

/** Filters pushed into the server-side Jira search JQL (Browse Jira). All optional;
 *  `jql`, when set, is raw advanced JQL that replaces the structured filters. */
export type JiraSearchFilters = {
	assignee?: string;
	types?: string[];
	hideDone?: boolean;
	activeSprint?: boolean;
	jql?: string;
};

export const jiraSearchQueryKey = (project: string, query: string, filters: JiraSearchFilters = {}) =>
	[
		"jira-search",
		project,
		query,
		filters.assignee ?? "",
		(filters.types ?? []).join(","),
		filters.hideDone ? "1" : "",
		filters.activeSprint ? "1" : "",
		filters.jql ?? "",
	] as const;

export const jiraProjectsQueryKey = (query: string) => ["jira-projects", query] as const;

export const jiraIssueQueryKey = (key: string) => ["jira-issue", key] as const;

export const jiraIssueTransitionsQueryKey = (key: string) => ["jira-issue-transitions", key] as const;

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

async function fetchJiraTransitions(sessionId: string, issueKey?: string): Promise<JiraTransition[]> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/jira/transitions", {
		params: { path: { sessionId }, query: issueKey ? { key: issueKey } : undefined },
	});
	if (error) throw new Error(apiErrorMessage(error, "Couldn't load Jira transitions"));
	return data?.transitions ?? [];
}

/**
 * Fetches the available status transitions, LIVE (never hardcoded — they differ
 * per issue type and current status), for the session's bound issue or — with an
 * issueKey (a subtask of it) — for that subtask. Enable only while the Move-status
 * dialog is open so we don't poll otherwise. Unlike the display context, a failure
 * here throws so the dialog can surface it (e.g. a missing JIRA_API_TOKEN).
 */
export function useJiraTransitions(sessionId: string | undefined, enabled: boolean, issueKey?: string) {
	return useQuery({
		queryKey: sessionJiraTransitionsQueryKey(sessionId, issueKey),
		enabled: Boolean(sessionId) && enabled,
		queryFn: () =>
			usePreviewData
				? Promise.resolve(mockSessionJiraTransitions[sessionId!] ?? [])
				: fetchJiraTransitions(sessionId!, issueKey),
		// Transitions become stale the moment the status moves; keep them short.
		staleTime: 15_000,
	});
}

/**
 * Applies a status transition — the ONE sanctioned Jira write — to the session's
 * bound issue, or to a subtask of it when issueKey is set. On success it
 * invalidates the display context (new status, incl. the subtasks list) and the
 * transitions cache for the session (bound issue + every subtask). In preview mode
 * it is a no-op success so the dialog flow can be demoed without a daemon.
 */
export function useMoveJiraStatus(sessionId: string, issueKey?: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (transitionId: string): Promise<JiraMoveResponse> => {
			if (usePreviewData) {
				return { sessionId, key: issueKey ?? "", status: "", statusCategory: "" };
			}
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/jira/move", {
				params: { path: { sessionId } },
				body: { transitionId, issueKey: issueKey || undefined },
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

async function fetchJiraSearch(
	project: string,
	query: string,
	filters: JiraSearchFilters,
): Promise<JiraIssueSummary[]> {
	const types = filters.types ?? [];
	const { data, error } = await apiClient.GET("/api/v1/jira/search", {
		params: {
			query: {
				q: query,
				project: project || undefined,
				assignee: filters.assignee || undefined,
				type: types.length > 0 ? types.join(",") : undefined,
				hideDone: filters.hideDone || undefined,
				activeSprint: filters.activeSprint || undefined,
				jql: filters.jql || undefined,
			},
		},
	});
	if (error) throw new Error(apiErrorMessage(error, "Couldn't search Jira"));
	return data?.issues ?? [];
}

/**
 * Cross-project issue search for the New-task + link-existing pickers AND the
 * project-scoped Browse Jira list, read LIVE via REST (jira-cli list is unusable
 * here). Fires once there is a 2+ char query OR a project is scoped — so Browse
 * lists a project's recent issues with no text typed, while the pickers (which
 * pass project="") still wait for two characters. A failure throws so the caller
 * can surface it (e.g. a missing JIRA_API_TOKEN).
 *
 * The `filters` (assignee accountId / "unassigned" token, issue types, hide-done,
 * active-sprint) are pushed into the server-side JQL, so Browse Jira can filter
 * without paring down a capped page; omitting them yields the same query key as the
 * unfiltered fetch, so React Query shares that request. `filters.jql`, when set, is
 * raw advanced JQL that drives the search verbatim (fires even with no project/text).
 */
export function useJiraSearch(query: string, project: string, enabled: boolean, filters: JiraSearchFilters = {}) {
	const q = query.trim();
	const scoped = project.trim().length > 0;
	const advanced = Boolean(filters.jql && filters.jql.trim().length > 0);
	return useQuery({
		queryKey: jiraSearchQueryKey(project, q, filters),
		enabled: enabled && (q.length >= 2 || scoped || advanced),
		queryFn: () =>
			usePreviewData ? Promise.resolve(mockJiraSearch(project, q, filters)) : fetchJiraSearch(project, q, filters),
		staleTime: 15_000,
	});
}

async function fetchJiraProjects(query: string): Promise<JiraProject[]> {
	const { data, error } = await apiClient.GET("/api/v1/jira/projects", {
		params: { query: { q: query || undefined } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Couldn't load Jira projects"));
	return data?.projects ?? [];
}

/**
 * Lists the user's Jira projects for the Browse-Jira project picker, read LIVE via
 * REST (`/rest/api/3/project/search`). Optional `query` filters server-side. A
 * failure throws so the picker can surface it (e.g. a missing JIRA_API_TOKEN).
 */
export function useJiraProjects(query: string, enabled: boolean) {
	const q = query.trim();
	return useQuery({
		queryKey: jiraProjectsQueryKey(q),
		enabled,
		queryFn: () => (usePreviewData ? Promise.resolve(mockJiraProjects(q)) : fetchJiraProjects(q)),
		staleTime: 60_000,
	});
}

async function fetchJiraIssue(key: string): Promise<JiraIssue | null> {
	const { data, error } = await apiClient.GET("/api/v1/jira/issue", { params: { query: { key } } });
	if (error) throw new Error(apiErrorMessage(error, "Couldn't load the Jira issue"));
	return data?.issue ?? null;
}

/**
 * Reads one issue's full display projection by KEY (pre-session), for the Browse
 * Jira detail view — the same projection the Summary tab renders, but not scoped to
 * a session. Enable only while the detail panel is open. A failure throws so the
 * panel can surface it (e.g. a missing token).
 */
export function useJiraIssue(key: string | undefined, enabled: boolean) {
	return useQuery({
		queryKey: jiraIssueQueryKey(key ?? ""),
		enabled: Boolean(key) && enabled,
		queryFn: () => (usePreviewData ? Promise.resolve(mockJiraIssue(key!)) : fetchJiraIssue(key!)),
		staleTime: 30_000,
	});
}

async function fetchJiraIssueTransitions(key: string): Promise<JiraTransition[]> {
	const { data, error } = await apiClient.GET("/api/v1/jira/issue/transitions", { params: { query: { key } } });
	if (error) throw new Error(apiErrorMessage(error, "Couldn't load Jira transitions"));
	return data?.transitions ?? [];
}

/**
 * Lists an issue's live status transitions by KEY — the detail view's Move-status
 * entry (pre-session). Enable only while the move dialog is open. A failure throws.
 */
export function useJiraIssueTransitions(key: string | undefined, enabled: boolean) {
	return useQuery({
		queryKey: jiraIssueTransitionsQueryKey(key ?? ""),
		enabled: Boolean(key) && enabled,
		queryFn: () => (usePreviewData ? Promise.resolve([]) : fetchJiraIssueTransitions(key!)),
		staleTime: 15_000,
	});
}

/**
 * Applies a status transition to an issue by KEY — the ONE sanctioned Jira write,
 * from the pre-session Browse Jira detail view. On success it invalidates the issue
 * and its transitions so the pill reflects the new status. Preview = no-op success.
 */
export function useMoveJiraIssue(key: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (transitionId: string): Promise<JiraMoveResponse> => {
			if (usePreviewData) return { sessionId: "", key, status: "", statusCategory: "" };
			const { data, error } = await apiClient.POST("/api/v1/jira/issue/move", {
				body: { key, transitionId },
			});
			if (error) throw new Error(apiErrorMessage(error, "Couldn't move the Jira status"));
			return data!;
		},
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: jiraIssueQueryKey(key) });
			void qc.invalidateQueries({ queryKey: jiraIssueTransitionsQueryKey(key) });
		},
	});
}

/**
 * Binds an EXISTING session to a Jira issue after the fact (PUT). The key is
 * validated server-side before it binds. On success it refreshes the session's
 * Jira context AND the workspace (board/sidebar badge). Preview mode is a no-op
 * success so the flow can be demoed without a daemon.
 */
export function useSetJiraBinding(sessionId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (issueKey: string): Promise<JiraLinkResponse> => {
			if (usePreviewData) return { sessionId, linked: true };
			const { data, error } = await apiClient.PUT("/api/v1/sessions/{sessionId}/jira", {
				params: { path: { sessionId } },
				body: { issueKey },
			});
			if (error) throw new Error(apiErrorMessage(error, "Couldn't link the Jira issue"));
			return data!;
		},
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: sessionJiraQueryKey(sessionId) });
			void qc.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});
}

/** Removes a session's Jira binding (DELETE). Refreshes context + workspace. */
export function useUnlinkJira(sessionId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (): Promise<JiraLinkResponse> => {
			if (usePreviewData) return { sessionId, linked: false };
			const { data, error } = await apiClient.DELETE("/api/v1/sessions/{sessionId}/jira", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Couldn't unlink the Jira issue"));
			return data!;
		},
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: sessionJiraQueryKey(sessionId) });
			void qc.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});
}
