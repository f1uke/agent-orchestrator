import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import type { WorkspaceSession } from "../types/workspace";
import { workspaceQueryKey } from "./useWorkspaceQuery";

/**
 * `+ qa` — attach a second agent to a task that has one.
 *
 * It lives here rather than on the board because the affordance now has TWO
 * homes: the card's crew strip, and the session topbar's member switcher. They
 * must start the same agent the same way, so they call the same mutation rather
 * than two copies of it that can drift.
 *
 * The member starts working straight away, in the same worktree, beside the
 * agent that is already there — nothing running is interrupted.
 */
export function useAddCrewRole(session: WorkspaceSession | undefined) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async () => {
			if (!session) throw new Error("Unable to add a qa to this task");
			void captureRendererEvent("ao.renderer.crew_add", { project_id: session.workspaceId });
			const { error: apiError } = await apiClient.POST("/api/v1/sessions/{sessionId}/crew/members", {
				params: { path: { sessionId: session.id } },
				body: { role: "qa" },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Unable to add a qa to this task"));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
	});
}
