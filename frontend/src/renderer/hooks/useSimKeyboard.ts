import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type SimKeyboard = components["schemas"]["SimKeyboardView"];

export const simKeyboardQueryKey = (udid: string) => ["sim", "keyboard", udid] as const;

/**
 * Which input mode a guest will read key presses through - asked BEFORE anybody
 * types, because asking costs about a second.
 *
 * 🗝 This is the whole point of the query. The daemon has to know the guest's
 * input mode to plan a `type` request, and finding it out spawns a process
 * inside the simulator, measured at 909-960 ms. It used to be asked in front of
 * every keystroke, which is what made a character take 1164-1181 ms to appear
 * when the device itself answers in 6 ms. Asking here - as soon as the human
 * can drive, long before they reach the keyboard - means the daemon already has
 * the answer when the first character arrives, and the cost is paid where
 * nobody is waiting on it.
 *
 * What comes back is used only to PACE typing (may a character go out on its
 * own, or must characters be batched). The daemon still plans every request
 * itself, so a stale answer here costs speed and never correctness.
 */
export function useSimKeyboard(udid: string | null, enabled: boolean) {
	return useQuery({
		queryKey: simKeyboardQueryKey(udid ?? ""),
		enabled: enabled && Boolean(udid),
		// The guest's own input mode can be changed - on the device, or by
		// Simulator.app when it is the thing driving - so this does go stale. It
		// does NOT follow the Mac: assuming that is #277. The answer is
		// re-established by the daemon on a window of its own, and asking harder
		// from here would spawn a process in the guest for every refetch. Half a
		// minute is often enough to follow a change, and rare enough to cost
		// nothing.
		staleTime: 30_000,
		refetchOnWindowFocus: true,
		queryFn: async ({ signal }): Promise<SimKeyboard> => {
			const { data, error, response } = await apiClient.GET("/api/v1/sim/devices/{udid}/keyboard", {
				params: { path: { udid: udid ?? "" } },
				signal,
			});
			if (error || !data) throw new Error(`keyboard: ${response.status}`);
			return data;
		},
	});
}
