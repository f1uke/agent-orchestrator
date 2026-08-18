import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type SimRecording = components["schemas"]["SimRecording"];
export type SimFlow = components["schemas"]["ControllersSimFlowView"];

/**
 * What is being recorded on one device, and how much of it there is.
 *
 * 🗝 The step count is the reason this polls at all. The worst failure this
 * whole surface exists to expose is a recorder that is not wired to the daemon:
 * `record start` succeeds, `status` sits at 0 forever, and `stop` produces a
 * header-only file - discovered later, from an empty flow, by somebody who has
 * already replayed the path by hand. A number on screen that visibly does not
 * move makes that obvious in the second it happens.
 *
 * It asks with `steps=none`, so the answer is the count and not every selector
 * captured so far. A recording of two hundred steps polled once a second would
 * otherwise drag its whole history back over the wire every second to call
 * length on it.
 *
 * A recording is per DEVICE, not per surface: one started from `ao sim record
 * start` in a terminal is the same recording this reports, and one started here
 * is the one `ao sim record status` sees. That is why this reads the device's
 * recording rather than anything the panel remembers locally.
 */
export const simRecordingQueryKey = (udid: string | null) => ["sim", "recording", udid] as const;

export type SimRecordingState = {
	/** A recording exists on this device, open or already stopped. */
	found: boolean;
	/** It is open right now. */
	open: boolean;
	/** Which session owns it - not necessarily this one. */
	holder: string;
	name: string;
	stepCount: number;
};

const NOTHING: SimRecordingState = { found: false, open: false, holder: "", name: "", stepCount: 0 };

/**
 * `enabled` is the Device tab being watched, and `udid` the device chosen.
 * While a recording is open this asks once a second, because the counter is
 * meant to move under the human's own finger; while there is none it falls back
 * to the list's own five, which is only there to notice a recording somebody
 * started from a terminal.
 */
export function useSimRecording(sessionId: string, udid: string | null, enabled: boolean) {
	return useQuery<SimRecordingState>({
		queryKey: simRecordingQueryKey(udid),
		enabled: enabled && Boolean(udid),
		refetchIntervalInBackground: false,
		refetchInterval: (query) => (query.state.data?.open ? 1_000 : 5_000),
		queryFn: async ({ signal }): Promise<SimRecordingState> => {
			if (!udid) return NOTHING;
			const { data, error, response } = await apiClient.GET("/api/v1/sessions/{sessionId}/sim-recordings/{udid}", {
				params: { path: { sessionId, udid }, query: { steps: "none" } },
				signal,
			});
			if (error || !data) {
				// Nothing has ever been recorded on this device. That is an
				// answer, not a failure - the same one `ao sim record status`
				// gives, and it must not read as an error in the panel.
				if (response?.status === 404) return NOTHING;
				if (response?.status === 501) return NOTHING;
				throw error ?? new Error("Could not read the recording");
			}
			return {
				found: true,
				open: !data.recording.stoppedAt,
				holder: data.recording.sessionId,
				name: data.recording.name ?? "",
				stepCount: data.stepCount,
			};
		},
	});
}

/** The flows this session has recorded, as the list shows them. */
export const simFlowsQueryKey = (sessionId: string) => ["sim", "flows", sessionId] as const;

export function useSimFlows(sessionId: string, enabled: boolean) {
	return useQuery({
		queryKey: simFlowsQueryKey(sessionId),
		enabled,
		// The count on the trigger is on screen at all times, so a stale one is
		// a wrong answer to "how many attempts do I have at this path". Flows
		// appear from outside this panel - `ao sim record stop` in a terminal
		// writes into the same directory - so noticing them cannot depend on
		// this panel having been the one to create them. A directory read every
		// ten seconds while somebody is looking at the tab is the cheap half of
		// what the device list already costs.
		refetchInterval: 10_000,
		refetchIntervalInBackground: false,
		queryFn: async ({ signal }): Promise<SimFlow[]> => {
			const { data, error, response } = await apiClient.GET("/api/v1/sessions/{sessionId}/sim-flows", {
				params: { path: { sessionId } },
				signal,
			});
			if (error || !data) {
				// A daemon with no data directory has nowhere to look. An empty
				// list is the honest thing to show, and it says so through the
				// panel's own empty state rather than as a failure.
				if (response?.status === 501) return [];
				throw error ?? new Error("Could not list recordings");
			}
			// Never undefined. React Query treats an undefined result as a
			// programming error and throws, which takes the whole panel down -
			// so a response without the field reads as "nothing recorded",
			// which is also what it means.
			return data.flows ?? [];
		},
	});
}

/** Renaming and deleting, sharing one invalidation so the list is never stale. */
export function useSimFlowActions(sessionId: string, onProblem: (message: string) => void) {
	const queryClient = useQueryClient();
	const refresh = () => {
		void queryClient.invalidateQueries({ queryKey: simFlowsQueryKey(sessionId) });
	};

	const rename = useMutation({
		mutationFn: async ({ fileName, name }: { fileName: string; name: string }) => {
			const { error } = await apiClient.PATCH("/api/v1/sessions/{sessionId}/sim-flows/{fileName}", {
				params: { path: { sessionId, fileName } },
				body: { name },
			});
			if (error) throw error;
		},
		onSuccess: refresh,
		onError: (error) => onProblem(problemText(error, "Could not rename that recording")),
	});

	const remove = useMutation({
		mutationFn: async (fileName: string) => {
			const { error } = await apiClient.DELETE("/api/v1/sessions/{sessionId}/sim-flows/{fileName}", {
				params: { path: { sessionId, fileName } },
			});
			if (error) throw error;
		},
		onSuccess: refresh,
		onError: (error) => onProblem(problemText(error, "Could not delete that recording")),
	});

	return { rename, remove, refresh };
}

function problemText(error: unknown, fallback: string): string {
	const message = (error as { message?: string } | null)?.message;
	return typeof message === "string" && message !== "" ? message : fallback;
}
