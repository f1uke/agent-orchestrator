import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockTestinyRuns } from "../lib/mock-data";
import type { TestinyRun, TestinyRunsResponse } from "../lib/testiny";

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/**
 * Keyed by the TASK (dev's session id): the routes are task-scoped, so dev and
 * qa read one list and a link made from either shows on both.
 */
export const sessionTestinyQueryKey = (taskId: string) => ["session-testiny", taskId] as const;

async function fetchRuns(taskId: string, refresh: boolean): Promise<TestinyRunsResponse> {
	if (usePreviewData) return mockTestinyRuns(taskId);
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/testiny/runs", {
		params: { path: { sessionId: taskId }, query: refresh ? { refresh: "1" } : undefined },
	});
	if (error) throw new Error(apiErrorMessage(error, "Couldn't load the Testiny runs"));
	return data ?? { project: "", runs: [] };
}

/**
 * The task's linked Testiny runs, read live by the daemon. Polls every 30 s
 * while the tab is mounted and on window focus, so a result qa records in
 * Testiny shows up without a click. A background refetch keeps the cards on
 * screen; the list is only empty before the first answer.
 */
export function useSessionTestinyRuns(taskId: string) {
	return useQuery({
		queryKey: sessionTestinyQueryKey(taskId),
		queryFn: () => fetchRuns(taskId, false),
		refetchInterval: usePreviewData ? false : 30_000,
	});
}

/** Reads every run from Testiny now, past the daemon's 15 s cache. */
export function useRefreshTestinyRuns(taskId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: () => fetchRuns(taskId, true),
		onSuccess: (data) => qc.setQueryData(sessionTestinyQueryKey(taskId), data),
	});
}

/** Links a run by id, TR-id or URL. The daemon refuses one Testiny cannot confirm. */
export function useLinkTestinyRun(taskId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (ref: string): Promise<TestinyRun | null> => {
			if (usePreviewData) return null;
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/testiny/runs", {
				params: { path: { sessionId: taskId } },
				body: { ref },
			});
			if (error) throw new Error(apiErrorMessage(error, "Couldn't link the run"));
			return data ?? null;
		},
		onSuccess: () => qc.invalidateQueries({ queryKey: sessionTestinyQueryKey(taskId) }),
	});
}

/** Unlinks a run. It deletes only AO's own row, so re-linking undoes it. */
export function useUnlinkTestinyRun(taskId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (runId: number) => {
			if (usePreviewData) return;
			const { error } = await apiClient.DELETE("/api/v1/sessions/{sessionId}/testiny/runs/{runId}", {
				params: { path: { sessionId: taskId, runId: String(runId) } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Couldn't unlink the run"));
		},
		onSuccess: (_data, runId) => {
			qc.setQueryData<TestinyRunsResponse>(sessionTestinyQueryKey(taskId), (prev) =>
				prev ? { ...prev, runs: prev.runs.filter((r) => r.link.runId !== runId) } : prev,
			);
			return qc.invalidateQueries({ queryKey: sessionTestinyQueryKey(taskId) });
		},
	});
}
