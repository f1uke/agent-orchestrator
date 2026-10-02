import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockWorkspaceFileBase } from "../lib/mock-data";
import type { DiffBase } from "./useWorkspaceFileDiff";

export type WorkspaceFileBase = components["schemas"]["WorkspaceFileBaseResponse"];

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

export const workspaceFileBaseQueryKey = (sessionId: string, path: string, base: DiffBase) =>
	["workspace-file-base", sessionId, path, base] as const;

/**
 * One file's text at the base of a change level - HEAD, or the merge-base with
 * the target - which the editor measures its LIVE buffer against.
 *
 * Polled, slowly, while it is on screen. The base moves when an agent commits or
 * the target branch is fetched, and nothing about the buffer says so: without
 * the poll, a commit that lands while the reader is looking would leave every
 * just-committed line still marked uncommitted until the window lost focus.
 */
export function useWorkspaceFileBase(sessionId: string, path: string, base: DiffBase, enabled: boolean) {
	return useQuery({
		queryKey: workspaceFileBaseQueryKey(sessionId, path, base),
		enabled,
		queryFn: async (): Promise<WorkspaceFileBase> => {
			if (usePreviewData) return mockWorkspaceFileBase(path, base);
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/file-base", {
				params: { path: { sessionId }, query: { path, base } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load the file's git base"));
			return data as WorkspaceFileBase;
		},
		staleTime: 5_000,
		refetchOnWindowFocus: true,
		refetchInterval: 10_000,
		retry: 1,
	});
}

/** The base's text when it is known, `""` for a file the base does not have, null when unknown. */
export function baseTextOf(base: WorkspaceFileBase | undefined): string | null {
	if (!base?.available) return null;
	if (!base.exists) return "";
	return typeof base.text === "string" ? base.text : null;
}
