import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type DaemonLoop = components["schemas"]["ControllersDaemonLoop"];
type ListDaemonLoopsResponse = components["schemas"]["ControllersListDaemonLoopsResponse"];

export const daemonLoopsQueryKey = ["daemon", "loops"] as const;

async function fetchDaemonLoops(): Promise<DaemonLoop[]> {
	const { data, error } = await apiClient.GET("/api/v1/daemon/loops");
	if (error) throw new Error(apiErrorMessage(error));
	return (data as ListDaemonLoopsResponse).loops ?? [];
}

/**
 * Fetches the daemon's background-loop timing. Gated by `enabled` so it only
 * polls while the popover is open (and the daemon is reachable). The endpoint is
 * refetched slowly to correct client-side ring drift; the smooth per-second
 * countdown is driven separately in the popover, not by this query.
 */
export function useDaemonLoops(enabled: boolean) {
	return useQuery({
		queryKey: daemonLoopsQueryKey,
		queryFn: fetchDaemonLoops,
		enabled,
		retry: 1,
		staleTime: 5_000,
		refetchInterval: enabled ? 15_000 : false,
	});
}
