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
 */
export function useWorkspaceChanges(sessionId?: string, enabled = true) {
	return useQuery({
		queryKey: workspaceChangesQueryKey(sessionId),
		enabled: Boolean(sessionId) && enabled,
		queryFn: () =>
			usePreviewData ? Promise.resolve(mockWorkspaceChanges(sessionId!)) : fetchWorkspaceChanges(sessionId!),
		refetchOnWindowFocus: true,
		refetchOnMount: "always" as const,
		staleTime: 5_000,
		retry: 1,
	});
}
