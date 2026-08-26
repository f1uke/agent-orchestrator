import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockWorkspaceFileDiff } from "../lib/mock-data";

export type WorkspaceFileDiff = components["schemas"]["DiffContextResponse"];

/**
 * Which of the two change levels a diff answers. They are never merged into one
 * list, because they answer different questions:
 *
 * - `target` — merge-base(target, HEAD) .. working tree: everything this branch
 *   did, committed or not. The Changes rail, and the editor's branch lane.
 * - `head` — HEAD .. working tree: what Discard Change can undo. The editor's
 *   uncommitted lane, and the hunks its discard popover restores.
 */
export type DiffBase = "target" | "head";

export type FileDiffOptions = {
	base?: DiffBase;
	/**
	 * Ask for every unchanged line rather than git's default three, so the whole
	 * ORIGINAL side can be replayed for a diff editor. Off by default: the
	 * windowed payload is what carries the skip markers that tell a reader lines
	 * were left out, and the stacked diff view needs them.
	 */
	fullContext?: boolean;
};

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

export const workspaceFileDiffQueryKey = (sessionId: string, path: string, options?: FileDiffOptions) =>
	["workspace-file-diff", sessionId, path, options?.base ?? "target", options?.fullContext === true] as const;

/**
 * One changed file's diff against the session's target branch, or against HEAD.
 *
 * `enabled` is what makes the stacked Changes view affordable: a collapsed file
 * section never requests its diff, so a large pull request costs one header row
 * per file until the reader opens one. Once fetched, react-query keeps the diff
 * cached, so collapsing and re-expanding a file is free.
 */
export function useWorkspaceFileDiff(sessionId: string, path: string, enabled: boolean, options?: FileDiffOptions) {
	const base = options?.base ?? "target";
	const fullContext = options?.fullContext === true;
	return useQuery({
		queryKey: workspaceFileDiffQueryKey(sessionId, path, options),
		enabled,
		queryFn: async () => {
			if (usePreviewData) return mockWorkspaceFileDiff(path, { base, fullContext });
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/file-diff", {
				params: { path: { sessionId }, query: { path, base, fullContext } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load diff"));
			return data as WorkspaceFileDiff;
		},
		staleTime: 5_000,
	});
}
