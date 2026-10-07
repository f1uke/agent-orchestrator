import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockRecordTestinyResult, mockTestinyCase, mockTestinyRuns } from "../lib/mock-data";
import {
	withCase,
	withResult,
	type TestinyCaseDetail,
	type TestinyResultWrite,
	type TestinyRun,
	type TestinyRunsResponse,
} from "../lib/testiny";

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

const testinyCaseQueryKey = (taskId: string, caseId: number) => ["session-testiny-case", taskId, caseId] as const;

/**
 * One case of the task's runs in full (test data, precondition, steps), read
 * live by the daemon when its panel first opens. Kept for a minute like the
 * daemon's own cache, so closing and reopening a panel reads nothing. Not
 * retried: a refusal shows at once with its reason, and the panel has a Retry.
 */
export function useTestinyCase(taskId: string, caseId: number) {
	return useQuery({
		queryKey: testinyCaseQueryKey(taskId, caseId),
		queryFn: async (): Promise<TestinyCaseDetail> => {
			if (usePreviewData) return mockTestinyCase(taskId, caseId);
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/testiny/cases/{caseId}", {
				params: { path: { sessionId: taskId, caseId: String(caseId) } },
			});
			if (error || !data) throw new Error(apiErrorMessage(error, "Couldn't read the case"));
			return data;
		},
		staleTime: 60_000,
		retry: false,
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

function replaceRun(
	data: TestinyRunsResponse | undefined,
	runId: number,
	next: (run: TestinyRun) => TestinyRun,
): TestinyRunsResponse | undefined {
	return data && { ...data, runs: data.runs.map((r) => (r.link.runId === runId ? next(r) : r)) };
}

/**
 * How many times the task's runs have been written to the cache, by a read or by
 * a result. Read at render: the runs query re-renders the tab on every change.
 */
export function useTestinyRunsVersion(taskId: string): number {
	return useQueryClient().getQueryState(sessionTestinyQueryKey(taskId))?.dataUpdateCount ?? 0;
}

/**
 * Told when a write starts and when it settles, with the runs version then, so
 * the card can keep its rows still until the runs are written again.
 */
export type ResultWriteHold = { start: () => void; end: (version: number) => void };

/**
 * Sets one case's result in a linked run, as the person (no `from`). The row
 * shows the new status at once; a refused write puts the case back as it was,
 * and a written one swaps the run for the fresh view the daemon read back.
 */
export function useRecordTestinyResult(taskId: string, runId: number, hold?: ResultWriteHold) {
	const qc = useQueryClient();
	const key = sessionTestinyQueryKey(taskId);
	return useMutation({
		mutationFn: async (write: TestinyResultWrite): Promise<TestinyRun> => {
			if (usePreviewData) return mockRecordTestinyResult(taskId, runId, write);
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/testiny/runs/{runId}/results", {
				params: { path: { sessionId: taskId, runId: String(runId) } },
				body: { results: [write] },
			});
			if (error || !data) throw new Error(apiErrorMessage(error, "Couldn't set the result"));
			return data;
		},
		onMutate: async (write) => {
			hold?.start();
			// A poll in flight was read before this write; landing after it, it
			// would put the old status back under the person's eyes.
			await qc.cancelQueries({ queryKey: key });
			const before = qc
				.getQueryData<TestinyRunsResponse>(key)
				?.runs.find((r) => r.link.runId === runId)
				?.cases.find((c) => c.id === write.caseId);
			const at = new Date().toISOString();
			qc.setQueryData<TestinyRunsResponse>(key, (data) => replaceRun(data, runId, (r) => withResult(r, write, at)));
			return { before };
		},
		onError: (_error, _write, context) => {
			const before = context?.before;
			if (before)
				qc.setQueryData<TestinyRunsResponse>(key, (data) => replaceRun(data, runId, (r) => withCase(r, before)));
		},
		onSuccess: (run) => qc.setQueryData<TestinyRunsResponse>(key, (data) => replaceRun(data, runId, () => run)),
		onSettled: () => hold?.end(qc.getQueryState(key)?.dataUpdateCount ?? 0),
	});
}
