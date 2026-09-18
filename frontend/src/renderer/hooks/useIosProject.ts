import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockIosProject } from "../lib/mock-data";

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

export type IosProject = components["schemas"]["IosrunProject"];
export type IosRun = components["schemas"]["IosrunRun"];
export type IosProjectResponse = components["schemas"]["ControllersIOSProjectResponse"];

export const iosProjectQueryKey = (sessionId: string) => ["ios-project", sessionId] as const;

/**
 * What this session can build for iOS, and the run it already has.
 *
 * 🗝 An empty `project.name` is the ORDINARY answer, not a failure: most
 * worktrees are not iOS projects, and that is exactly the test the run bar uses
 * to decide whether to render at all. So a 501 (a machine with no Xcode) and a
 * Go worktree produce the same shape, and neither is surfaced as an error.
 *
 * Polling is slow by default and fast only while a run is live, for the reason
 * `useSimDevices` polls the same way: nothing about a project changes on its
 * own, but a build finishing is something the bar has to notice. What a poll
 * cannot cover is the human who runs `xcodegen` in the terminal and reaches
 * straight for a picker - that is what `useRefreshIosProject` is for, and it is
 * driven by the dropdown opening rather than by a faster timer.
 */
export function useIosProject(sessionId: string | undefined) {
	return useQuery<IosProjectResponse, Error, IosProjectResponse, ReturnType<typeof iosProjectQueryKey>>({
		queryKey: iosProjectQueryKey(sessionId ?? ""),
		enabled: Boolean(sessionId),
		// Fast only while a build is actually going; a finished run's verdict does
		// not change, so there is nothing to poll for afterwards.
		refetchInterval: (query) => (query.state.data?.run?.state === "running" ? 2_000 : 30_000),
		refetchIntervalInBackground: false,
		queryFn: async ({ signal }): Promise<IosProjectResponse> => {
			if (usePreviewData) return mockIosProject();
			const { data, error, response } = await apiClient.GET("/api/v1/sessions/{sessionId}/ios-project", {
				params: { path: { sessionId: sessionId ?? "" } },
				signal,
			});
			if (error || !data) {
				// 501 is a daemon that cannot build for iOS at all. That is not a
				// failure to report - it is "there is no run bar here", which is
				// the same answer a Go worktree gives.
				if (response?.status === 501) return noIosProject();
				throw error ?? new Error("Could not read this session's Xcode project");
			}
			return data;
		},
	});
}

/** A daemon that cannot build for iOS at all, in the shape a Go worktree has. */
function noIosProject(): IosProjectResponse {
	return { project: { name: "", path: "", kind: "", schemes: [], configurations: [] } };
}

/**
 * Re-read the project from disk, past the daemon's one-minute listing cache.
 *
 * 🗝 Opening a picker is the exact moment the answer matters, and the workflow
 * this serves is `xcodegen` in the terminal followed immediately by a click -
 * a 30-second poll notices that far too late. It costs nothing while the window
 * sits idle, because nothing calls it until a dropdown opens.
 *
 * A refresh that fails leaves the last good listing in place rather than
 * blanking the picker: the poll underneath is still running, and a dropdown
 * that empties itself because one request failed is worse than a stale one.
 */
export function useRefreshIosProject(sessionId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (): Promise<IosProjectResponse> => {
			if (usePreviewData) return mockIosProject();
			const { data, error, response } = await apiClient.GET("/api/v1/sessions/{sessionId}/ios-project", {
				params: { path: { sessionId }, query: { refresh: true } },
			});
			if (error || !data) {
				if (response?.status === 501) return noIosProject();
				throw error ?? new Error("Could not re-read this session's Xcode project");
			}
			return data;
		},
		onSuccess: (data) => queryClient.setQueryData(iosProjectQueryKey(sessionId), data),
	});
}

export type StartIosRunRequest = { scheme: string; configuration: string; udid?: string };

/**
 * Pressing Run. The daemon starts `ao sim run` in a pane of its own and hands
 * back the handle the terminal attaches to; everything that makes a run safe -
 * the lease, the boot cap, the build that names no device - happens inside that
 * command, not here.
 */
export function useStartIosRun(sessionId: string, onProblem: (message: string) => void) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ scheme, configuration, udid }: StartIosRunRequest): Promise<IosRun> => {
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/ios-runs", {
				params: { path: { sessionId } },
				body: { scheme, configuration, udid },
			});
			if (error || !data) throw error ?? new Error("Could not start the run");
			return data.run;
		},
		onMutate: () => onProblem(""),
		onError: (error) => onProblem(apiErrorMessage(error, "Could not start the run")),
		onSettled: () => void queryClient.invalidateQueries({ queryKey: iosProjectQueryKey(sessionId) }),
	});
}
