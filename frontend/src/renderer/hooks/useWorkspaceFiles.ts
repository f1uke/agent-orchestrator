import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockWorkspaceFiles } from "../lib/mock-data";

export type WorkspaceFiles = components["schemas"]["WorkspaceFilesResponse"];

export const workspaceFilesQueryKey = (sessionId?: string) =>
	sessionId ? (["workspace-files", sessionId] as const) : (["workspace-files"] as const);

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

async function fetchWorkspaceFiles(sessionId: string): Promise<WorkspaceFiles> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/files", {
		params: { path: { sessionId } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to index this workspace"));
	return data as WorkspaceFiles;
}

/**
 * The ⌘⇧O file index for a session's workspace.
 *
 * Fetched ONCE per palette session and ranked in the renderer, not queried per
 * keystroke. That is what makes the palette feel like Xcode's: `git ls-files` on
 * a large project is ~180 ms, which is a visible stall on every character, and a
 * request per keystroke is exactly the shape that lets a slow answer overwrite a
 * fast one. See `lib/open-quickly.ts`.
 *
 * Like the Changes payload, nothing on the daemon invalidates this when the agent
 * writes a file — SQLite triggers drive the CDC and the filesystem is not in it.
 * So the index refetches when the palette remounts (each open) and goes stale
 * after a minute; a file created seconds ago while the palette is already open is
 * not in it, which is the same deal every editor's fuzzy finder offers.
 */
export function useWorkspaceFiles(sessionId?: string, enabled = true) {
	return useQuery({
		queryKey: workspaceFilesQueryKey(sessionId),
		enabled: Boolean(sessionId) && enabled,
		queryFn: () => (usePreviewData ? Promise.resolve(mockWorkspaceFiles(sessionId!)) : fetchWorkspaceFiles(sessionId!)),
		refetchOnWindowFocus: false,
		refetchOnMount: "always" as const,
		staleTime: 60_000,
		retry: 1,
	});
}
