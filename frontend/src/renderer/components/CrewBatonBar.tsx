import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Moon } from "lucide-react";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { crewChipState, holdsTheTurn, tasksFrom } from "../lib/crew";
import { captureRendererEvent } from "../lib/telemetry";
import type { WorkspaceSession } from "../types/workspace";

/**
 * The baton, and the one button that moves it.
 *
 * Two agents share this task's worktree and exactly one of them may be running
 * at a time - AO enforces that, it is not a convention. So a crew member's
 * terminal needs to answer a question a solo session never has to: is this the
 * agent that has the turn, and if not, how do I give it to them?
 *
 * "Wake" is a HANDOVER, not a second agent: the daemon stands the current holder
 * down (suspended, terminal reaped, worktree and transcript kept) before it
 * brings this one up. The copy says that out loud, because a person who thinks
 * they are adding an agent will be surprised when the other one goes quiet.
 *
 * AO deliberately makes no automatic decision about WHEN the baton should move.
 * That policy is meant to be chosen after watching real tasks; this is the
 * affordance, and there is no scheduler behind it.
 *
 * Renders nothing at all for a solo session - which is every session on an
 * ordinary board - so nothing about opening one changes.
 */
export function CrewBatonBar({
	session,
	sessions,
	onOpenMember,
}: {
	session: WorkspaceSession;
	sessions: WorkspaceSession[];
	onOpenMember?: (member: WorkspaceSession) => void;
}) {
	const queryClient = useQueryClient();
	const wake = useMutation({
		mutationFn: async (id: string) => {
			void captureRendererEvent("ao.renderer.crew_wake", { project_id: session.workspaceId });
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/crew/wake", {
				params: { path: { sessionId: id } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to hand over the turn"));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
	});

	if (!session.crew) return null;
	const task = tasksFrom(sessions).find((t) => t.members.some((m) => m.id === session.id));
	if (!task?.isCrew) return null;

	const role = session.crew.role;
	const other = task.members.find((m) => m.id !== session.id);
	const state = crewChipState(session);
	// Waking this member stands the other one down only if the other one is
	// actually RUNNING. Once it has finished (its PR merged), or is itself asleep,
	// there is no turn to take off anybody - the daemon resolves the holder and
	// takes the ordinary resume path - so neither the sentence nor the button may
	// keep saying otherwise. The human hit exactly this: a crew whose dev had
	// terminated still offered "Wake qa (sleeps dev)".
	const displaced = other && holdsTheTurn(other) ? other : undefined;
	const errorText = wake.error instanceof Error ? wake.error.message : null;

	// Terminal members are not part of the baton any more: a finished task should
	// not offer to wake anything.
	if (state === "done") return null;

	return (
		<div
			className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-surface px-3 py-1.5 text-[11.5px]"
			data-crew-baton={state === "working" ? "holder" : "asleep"}
		>
			{state === "asleep" && <Moon aria-hidden="true" className="h-3 w-3 shrink-0 text-passive" />}
			<span className="min-w-0 text-muted-foreground">
				{state === "working" ? (
					<>
						<span className="font-medium text-foreground">{role}</span> has the turn on this task.
					</>
				) : displaced ? (
					<>
						<span className="font-medium text-foreground">{role}</span> is asleep —{" "}
						{displaced.crew?.role ?? "the other agent"} has the turn. Only one of them may run in this worktree at a
						time.
					</>
				) : (
					<>
						<span className="font-medium text-foreground">{role}</span> is asleep — nobody has the turn on this task.
					</>
				)}
			</span>
			{errorText && <span className="text-error">{errorText}</span>}
			<span className="ml-auto flex shrink-0 items-center gap-2">
				{other && onOpenMember && (
					<button
						className="rounded-[4px] px-1.5 py-0.5 text-passive transition-colors hover:bg-interactive-hover hover:text-foreground"
						onClick={() => onOpenMember(other)}
						type="button"
					>
						Open {other.crew?.role}
					</button>
				)}
				{state === "asleep" && (
					<button
						className="rounded-[4px] border border-border-strong px-2 py-0.5 font-medium text-foreground transition-colors hover:bg-interactive-hover disabled:opacity-60"
						disabled={wake.isPending}
						onClick={() => wake.mutate(session.id)}
						type="button"
					>
						{wake.isPending
							? "Handing over…"
							: displaced
								? `Wake ${role} (sleeps ${displaced.crew?.role})`
								: `Wake ${role}`}
					</button>
				)}
			</span>
		</div>
	);
}
