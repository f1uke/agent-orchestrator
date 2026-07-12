import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockSmokeChecks } from "../lib/mock-data";

export type SmokeChecksResponse = components["schemas"]["ListSmokeChecksResponse"];

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/** Shared query key so the Tests tab and the Summary readiness strip read (and
 * invalidate) the same smoke-checks cache — one request, one source of truth. */
export const sessionSmokeQueryKey = (sessionId: string) => ["session-smoke", sessionId] as const;

/**
 * Loads a session's smoke checklist. Polls every 6s while any case is still
 * pending (so a live verdict lands quickly), then settles. `worker` only labels
 * the empty-state fallback.
 */
export function useSessionSmokeChecks(sessionId: string, worker?: string) {
	return useQuery({
		queryKey: sessionSmokeQueryKey(sessionId),
		refetchInterval: (q) => {
			if (usePreviewData) return false;
			const data = q.state.data as SmokeChecksResponse | undefined;
			return (data?.checks ?? []).some((c) => c.verdict === "pending") ? 6000 : false;
		},
		queryFn: async () => {
			if (usePreviewData) return mockSmokeChecks(sessionId, worker);
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/smoke-checks", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load smoke checks"));
			return data ?? ({ worker: worker ?? "", checks: [] } satisfies SmokeChecksResponse);
		},
	});
}
