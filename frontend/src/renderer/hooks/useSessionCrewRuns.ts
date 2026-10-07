import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { crewRunState } from "../lib/crew-run";
import { mockCrewRuns } from "../lib/mock-data";

export type ListCrewRunsResponse = components["schemas"]["ListCrewRunsResponse"];

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/** Shared query key, by TASK, so a crew's dev and qa read (and invalidate) one
 * cache. */
export const sessionCrewRunsQueryKey = (taskId: string) => ["session-crew-runs", taskId] as const;

/**
 * Loads the bracketed build/test runs of every member of a task, newest first,
 * each naming its member when the task has a crew. `taskId` is the task's key
 * (dev's session id; a solo session's own).
 *
 * Polls while a run is OPEN, because that is the only window in which the answer
 * changes on its own - the member is mid-build and the strip has to stop saying
 * "running" when it stops. A settled history does not poll.
 */
export function useSessionCrewRuns(taskId: string) {
	return useQuery({
		queryKey: sessionCrewRunsQueryKey(taskId),
		refetchInterval: (q) => {
			if (usePreviewData) return false;
			const data = q.state.data as ListCrewRunsResponse | undefined;
			return (data?.runs ?? []).some((run) => crewRunState(run) === "running") ? 4000 : false;
		},
		queryFn: async () => {
			if (usePreviewData) return mockCrewRuns(taskId);
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/crew/runs", {
				params: { path: { sessionId: taskId }, query: { scope: "task" } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load machine runs"));
			return data ?? ({ runs: [] } satisfies ListCrewRunsResponse);
		},
	});
}
