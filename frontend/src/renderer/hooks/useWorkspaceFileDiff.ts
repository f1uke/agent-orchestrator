import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockWorkspaceFileDiff } from "../lib/mock-data";

export type WorkspaceFileDiff = components["schemas"]["DiffContextResponse"];

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/**
 * One changed file's diff against the session's target branch.
 *
 * `enabled` is what makes the stacked Changes view affordable: a collapsed file
 * section never requests its diff, so a large pull request costs one header row
 * per file until the reader opens one. Once fetched, react-query keeps the diff
 * cached, so collapsing and re-expanding a file is free.
 */
export function useWorkspaceFileDiff(sessionId: string, path: string, enabled: boolean) {
	return useQuery({
		queryKey: ["workspace-file-diff", sessionId, path],
		enabled,
		queryFn: async () => {
			if (usePreviewData) return mockWorkspaceFileDiff(path);
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/file-diff", {
				params: { path: { sessionId }, query: { path } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load diff"));
			return data as WorkspaceFileDiff;
		},
		staleTime: 5_000,
	});
}
