import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { buildSaveRequest } from "../lib/editor/save-file";
import { saveFailureFrom, type SaveFailure } from "../lib/editor/save-errors";
import { mockWorkspaceFileSave } from "../lib/mock-data";
import { workspaceChangesQueryKey } from "./useWorkspaceChanges";

export type SaveResult = components["schemas"]["WriteWorkspaceFileResponse"];

/** Thrown by the mutation so a caller can branch on the conflict without reparsing. */
export class SaveError extends Error {
	readonly failure: SaveFailure;

	constructor(failure: SaveFailure) {
		super(`${failure.title} ${failure.detail}`);
		this.name = "SaveError";
		this.failure = failure;
	}
}

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/**
 * Write a workspace file, preconditioned on the hash it was read with.
 *
 * There is deliberately no way to spell "write regardless": a mismatch is a
 * 409 the reader resolves by LOOKING at what changed, and re-preconditioning on
 * the version they were shown. See `FileDriftBanner` for the flow.
 *
 * On success the response's `changedLines` refreshes the uncommitted gutter lane
 * with no second GET — which matters here, because a second GET could race the
 * very agent whose write this save just had to be checked against.
 */
export function useSaveWorkspaceFile(sessionId: string) {
	const queryClient = useQueryClient();
	return useMutation<SaveResult, SaveError, { path: string; text: unknown; baseHash: string | undefined }>({
		mutationFn: async (input) => {
			const body = buildSaveRequest(input);
			if (usePreviewData) {
				const result = mockWorkspaceFileSave(body.path, body.content ?? "", body.baseHash);
				if (!result.ok) throw new SaveError(saveFailureFrom(result.body));
				return result.response;
			}
			const { data, error } = await apiClient.PUT("/api/v1/sessions/{sessionId}/workspace/file", {
				params: { path: { sessionId } },
				body,
			});
			if (error) throw new SaveError(saveFailureFrom(error));
			return data as SaveResult;
		},
		onSuccess: (_result, input) => {
			// The rail's counts and the branch lane both moved. The file query is
			// NOT invalidated: its content is already what we just wrote, and
			// refetching it would only widen the window in which an agent's write
			// arrives as a surprise rather than as drift the pane reports.
			void queryClient.invalidateQueries({ queryKey: workspaceChangesQueryKey(sessionId) });
			void queryClient.invalidateQueries({ queryKey: ["workspace-file-diff", sessionId, input.path] });
		},
	});
}
