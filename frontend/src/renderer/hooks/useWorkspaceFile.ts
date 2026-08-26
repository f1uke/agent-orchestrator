import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockWorkspaceFile } from "../lib/mock-data";

export type WorkspaceFile = components["schemas"]["WorkspaceFileResponse"];

export const workspaceFileQueryKey = (sessionId: string, path: string) => ["workspace-file", sessionId, path] as const;

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

async function fetchWorkspaceFile(sessionId: string, path: string): Promise<WorkspaceFile> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/file", {
		params: { path: { sessionId }, query: { path } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to load file"));
	return data as WorkspaceFile;
}

/**
 * One workspace file's content, per-line change map and content hash.
 *
 * 🗝 The preview branch is not a nicety. Without it this surface renders
 * NOTHING under `ao preview` — not the editor, not "Loading file…", not an
 * error. The request goes to a daemon that is not there, and between react-query
 * retries the query has no data, no error and `isLoading` false, so every render
 * branch in the viewer is simultaneously falsy. That silence cost slice 3 its
 * visual demo, and — because AO attaches a qa the first time a task drives the
 * app — it quietly cost that task its tester too.
 *
 * `watch` polls while there is something to lose. An AO worktree has agents
 * writing in it, so a file open in the editor drifts under the reader as a
 * matter of course; seeing that before they press save is worth far more than
 * handling the 409 well afterwards. A CLEAN buffer costs nothing to rebase, so
 * only a dirty one is worth polling for, and an idle pane must not poll the
 * daemon forever.
 */
export function useWorkspaceFile(sessionId: string, path: string, options?: { watch?: boolean }) {
	return useQuery({
		queryKey: workspaceFileQueryKey(sessionId, path),
		queryFn: () => (usePreviewData ? Promise.resolve(mockWorkspaceFile(path)) : fetchWorkspaceFile(sessionId, path)),
		refetchOnWindowFocus: true,
		refetchInterval: options?.watch ? 5_000 : false,
		staleTime: 5_000,
		retry: 1,
	});
}
