import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type SimDevice = components["schemas"]["SimDeviceView"];
export type SimDevicesResponse = components["schemas"]["ListSimDevicesResponse"];

export const simDevicesQueryKey = ["sim", "devices"] as const;

/**
 * The machine's simulators and their lease state.
 *
 * `enabled` is the Simulator tab being looked at. Nothing is asked for while
 * nobody is watching — a list nobody can see is a request nobody needed.
 */
export function useSimDevices(enabled: boolean) {
	return useQuery({
		queryKey: simDevicesQueryKey,
		enabled,
		// Leases expire on their own, and another session can take or drop one at
		// any moment, so the list goes stale without anything changing here. Five
		// seconds is slow enough to be invisible in cost and fast enough that a
		// lease that lapsed is not shown as live for long.
		refetchInterval: 5_000,
		refetchIntervalInBackground: false,
		queryFn: async ({ signal }): Promise<SimDevicesResponse> => {
			const { data, error, response } = await apiClient.GET("/api/v1/sim/devices", { signal });
			if (error || !data) {
				// 501 is a machine that cannot list simulators at all - not a
				// failure to report as one.
				if (response?.status === 501) {
					return { devices: [], defaultUdid: null, defaultReason: "this machine has no iOS Simulator support" };
				}
				throw error ?? new Error("Could not list simulators");
			}
			return data;
		},
	});
}
