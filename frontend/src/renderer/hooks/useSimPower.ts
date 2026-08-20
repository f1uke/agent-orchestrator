import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { simDevicesQueryKey } from "./useSimDevices";

/**
 * Booting a simulator, and shutting one down, from the Device tab.
 *
 * 🗝 Why this is a route and not a command. `ao sim` deliberately has no boot
 * or shutdown subcommand: powering a device on and off is a HUMAN capability,
 * because an agent that could boot devices would quietly accumulate 4 GB
 * virtual machines while nobody was watching. The daemon carries the route only
 * because the renderer talks to the daemon rather than to simctl.
 *
 * The request is answered the moment the work starts, not when it finishes - a
 * cold boot takes tens of seconds, past the daemon's own per-request ceiling.
 * Progress arrives on the device listing instead, as `device.power`, which is
 * why every call invalidates it.
 */
export type SimPowerState = "booted" | "shutdown";

export type SimPowerRequest = {
	udid: string;
	state: SimPowerState;
	/**
	 * The session that currently leases the device, when that session is
	 * somebody else. The daemon refuses a shutdown that does not name them, so
	 * this is a confirmation the protocol enforces rather than one the UI
	 * merely performs: a picker acting on a list that went stale cannot shut a
	 * device down on the strength of a lease it read before somebody else took
	 * it.
	 */
	confirmHolder?: string;
};

export function useSimPower(sessionId: string, onProblem: (message: string) => void) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ udid, state, confirmHolder }: SimPowerRequest) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/sim-devices/{udid}/power", {
				params: { path: { sessionId, udid } },
				body: { state, confirmHolder },
			});
			if (error) throw error;
		},
		onMutate: () => onProblem(""),
		onError: (error, { state }) =>
			onProblem(
				apiErrorMessage(error, state === "booted" ? "Could not boot the simulator" : "Could not shut the simulator down"),
			),
		// Both ends: the listing carries the operation while it runs, so it has
		// to be re-read as soon as one starts, not only when it ends.
		onSettled: () => void queryClient.invalidateQueries({ queryKey: simDevicesQueryKey }),
	});
}
