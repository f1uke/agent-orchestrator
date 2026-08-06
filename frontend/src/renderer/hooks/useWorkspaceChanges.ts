import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockWorkspaceChanges } from "../lib/mock-data";

export type WorkspaceChanges = components["schemas"]["WorkspaceChangesResponse"];
export type ChangedFile = components["schemas"]["ChangedFileDTO"];

export const workspaceChangesQueryKey = (sessionId?: string) =>
	sessionId ? (["workspace-changes", sessionId] as const) : (["workspace-changes"] as const);

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

async function fetchWorkspaceChanges(sessionId: string): Promise<WorkspaceChanges> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/changes", {
		params: { path: { sessionId } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to load changes"));
	return data as WorkspaceChanges;
}

/**
 * The Changes-mode payload for a session.
 *
 * Files on disk emit no change event — the daemon's CDC comes from SQLite
 * triggers, so nothing invalidates this when the agent writes a file. Rather
 * than poll every open session, the panel refetches when it is remounted or the
 * window regains focus, and offers an explicit refresh control.
 *
 * The one exception is `targetFetch: "refreshing"`. Opening the panel starts a
 * background refresh of the target branch on the daemon, which never blocks the
 * response — so the first payload is computed from the refs already on disk and
 * the corrected diff only exists on a LATER request. Without this short poll the
 * fetch would land into an empty room and the user would keep reading the stale
 * answer until they happened to click refresh.
 */
const REFRESHING_POLL_MS = 1_000;

export function useWorkspaceChanges(sessionId?: string, enabled = true) {
	return useQuery({
		queryKey: workspaceChangesQueryKey(sessionId),
		enabled: Boolean(sessionId) && enabled,
		queryFn: () =>
			usePreviewData ? Promise.resolve(mockWorkspaceChanges(sessionId!)) : fetchWorkspaceChanges(sessionId!),
		refetchOnWindowFocus: true,
		refetchOnMount: "always" as const,
		refetchInterval: (query) => (query.state.data?.targetFetch === "refreshing" ? REFRESHING_POLL_MS : false),
		staleTime: 5_000,
		retry: 1,
	});
}
