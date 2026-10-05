import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockWorkspaceChanges } from "../lib/mock-data";

export type WorkspaceChanges = components["schemas"]["WorkspaceChangesResponse"];
export type ChangedFile = components["schemas"]["ChangedFileDTO"];

export const workspaceChangesQueryKey = (sessionId?: string) =>
	sessionId ? (["workspace-changes", sessionId] as const) : (["workspace-changes"] as const);

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

async function fetchWorkspaceChanges(sessionId: string, refresh = false): Promise<WorkspaceChanges> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/changes", {
		params: { path: { sessionId }, query: refresh ? { refresh: true } : {} },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to load changes"));
	return data as WorkspaceChanges;
}

function loadWorkspaceChanges(sessionId: string, refresh = false): Promise<WorkspaceChanges> {
	return usePreviewData ? Promise.resolve(mockWorkspaceChanges(sessionId)) : fetchWorkspaceChanges(sessionId, refresh);
}

/**
 * The Changes-mode payload for a session.
 *
 * Files on disk emit no change event — the daemon's CDC comes from SQLite
 * triggers, so nothing invalidates this when the agent writes a file. The panel
 * refetches when it is remounted or the window regains focus, offers an explicit
 * refresh control, and - while `autoRefresh` is on, i.e. the Changes list is on
 * screen - rereads every AUTO_REFRESH_MS. Each read lets the daemon refresh the
 * target branch from its remote; the daemon throttles and de-duplicates those
 * fetches per repository, so the human rarely has to press refresh to see the
 * target as it is on the forge.
 *
 * The one fast lane is a target fetch in flight (`isFetchingTarget`). A read never waits on the
 * network, so the first payload is computed from the refs already on disk and
 * the corrected diff only exists on a LATER request. Without this short poll the
 * fetch would land into an empty room and the user would keep reading the stale
 * answer until the next tick.
 */
const REFRESHING_POLL_MS = 1_000;

/**
 * Whether the daemon is fetching the target right now. `targetFetchInFlight` is
 * the direct signal: a retry after a failure keeps `targetFetch` at "failed"
 * until it lands, and only polling through it shows the recovery promptly.
 */
export function isFetchingTarget(data?: WorkspaceChanges): boolean {
	return data?.targetFetch === "refreshing" || Boolean(data?.targetFetchInFlight);
}
const AUTO_REFRESH_MS = 60_000;

export function useWorkspaceChanges(sessionId?: string, { autoRefresh = true }: { autoRefresh?: boolean } = {}) {
	return useQuery({
		queryKey: workspaceChangesQueryKey(sessionId),
		enabled: Boolean(sessionId),
		queryFn: () => loadWorkspaceChanges(sessionId!),
		refetchOnWindowFocus: true,
		refetchOnMount: "always" as const,
		refetchInterval: (query) =>
			isFetchingTarget(query.state.data) ? REFRESHING_POLL_MS : autoRefresh ? AUTO_REFRESH_MS : false,
		staleTime: 5_000,
		retry: 1,
	});
}

/**
 * The refresh button: ask the daemon to fetch the target branch NOW rather than
 * after its throttle, then keep showing the current list while that fetch runs
 * (the payload comes back "refreshing", which starts the fast poll above).
 */
export function useRefreshWorkspaceChanges(sessionId?: string) {
	const queryClient = useQueryClient();
	return useCallback(async () => {
		if (!sessionId) return;
		const queryKey = workspaceChangesQueryKey(sessionId);
		// A poll already in flight would otherwise absorb this request, and the
		// fetch the human asked for would never be sent.
		await queryClient.cancelQueries({ queryKey });
		try {
			await queryClient.fetchQuery({ queryKey, queryFn: () => loadWorkspaceChanges(sessionId, true), staleTime: 0 });
		} catch {
			// The query's own error state carries the failure to the panel.
		}
	}, [queryClient, sessionId]);
}
