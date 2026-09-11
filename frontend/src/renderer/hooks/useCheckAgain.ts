import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import type { WorkspaceSession } from "../types/workspace";
import { workspaceQueryKey } from "./useWorkspaceQuery";

/**
 * `↻ check again` — ask the qa this task ALREADY HAS to look once more.
 *
 * It is the second half of `+ qa`, and the two are one control in two states:
 * a task with no qa is offered one, a task whose qa has finished is offered
 * another round. Nothing here creates a member — one task still gets one qa, and
 * this is the same session coming back into the same worktree, keeping its seat.
 *
 * ## Why the crew route rather than the plain restore
 *
 * `POST /sessions/{id}/restore` would revive it too, and that was the whole
 * problem: restore is filed as session RECOVERY, and a person looking for
 * "check it again" never reads it as the answer. The crew route is the one that
 * knows this is a member of a task — it refuses to wake a member whose crewmates
 * have all finished (that task's worktree came down with them), and it reports
 * whether it restored or merely started.
 *
 * ## A second round needs no new commit
 *
 * The reasons it is asked for are not code changes: test cases written after the
 * first round closed, a simulator erased so the first round's evidence proves
 * nothing about this build, a case that turned out to expect the wrong thing.
 * Nothing here is keyed to a commit, deliberately.
 */
export function useCheckAgain(member: WorkspaceSession | undefined) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async () => {
			if (!member) throw new Error("Unable to ask this task's qa for another round");
			void captureRendererEvent("ao.renderer.crew_check_again", { project_id: member.workspaceId });
			const { error: apiError } = await apiClient.POST("/api/v1/sessions/{sessionId}/crew/wake", {
				params: { path: { sessionId: member.id } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Unable to ask this task's qa for another round"));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
	});
}
