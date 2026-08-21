import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { crewRunState } from "../lib/crew-run";
import { mockCrewRuns } from "../lib/mock-data";

export type ListCrewRunsResponse = components["schemas"]["ListCrewRunsResponse"];

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/** Shared query key so anything reading a session's bracketed runs reads (and
 * invalidates) one cache. */
export const sessionCrewRunsQueryKey = (sessionId: string) => ["session-crew-runs", sessionId] as const;

/**
 * Loads a session's bracketed build/test runs, newest first.
 *
 * Polls while a run is OPEN, because that is the only window in which the answer
 * changes on its own - the member is mid-build and the strip has to stop saying
 * "running" when it stops. A settled history does not poll.
 */
export function useSessionCrewRuns(sessionId: string) {
	return useQuery({
		queryKey: sessionCrewRunsQueryKey(sessionId),
		refetchInterval: (q) => {
			if (usePreviewData) return false;
			const data = q.state.data as ListCrewRunsResponse | undefined;
			return (data?.runs ?? []).some((run) => crewRunState(run) === "running") ? 4000 : false;
		},
		queryFn: async () => {
			if (usePreviewData) return mockCrewRuns(sessionId);
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/crew/runs", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load machine runs"));
			return data ?? ({ runs: [] } satisfies ListCrewRunsResponse);
		},
	});
}
