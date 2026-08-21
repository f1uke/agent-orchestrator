import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Play } from "lucide-react";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import type { WorkspaceSession } from "../types/workspace";

/**
 * A crew member that has NEVER RUN, and the one button that starts it.
 *
 * This replaces two things that used to be here and were both false.
 *
 * The first was the BATON BAR: a wake button whose label promised to sleep the
 * other member. Both members of a crew run at the same time now - starting one
 * takes nothing from the other - so the promise stopped being true and the
 * handover it described stopped existing.
 *
 * The second was worse. A member with no terminal fell through to "Terminal
 * ended · Restore the session", which reads as DEATH for an agent that has
 * simply never been started - and offered "restore" for something there is
 * nothing to restore.
 *
 * What is actually true is what this says: the member exists, it is in dev's
 * worktree on dev's branch with its first turn already written, and nothing has
 * been spent on it. Looking at its card deliberately does NOT start it (the
 * daemon refuses that, not this component): starting an agent costs money and is
 * a decision, and a glance is not one.
 */
export function CrewMemberNotStartedPane({ session }: { session: WorkspaceSession }) {
	const queryClient = useQueryClient();
	const role = session.crew?.role ?? "qa";
	const start = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.crew_start", { project_id: session.workspaceId });
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/crew/wake", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw new Error(apiErrorMessage(error, `Unable to start ${role}`));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
	});
	const errorText = start.error instanceof Error ? start.error.message : null;

	return (
		<div className="flex h-full min-h-0 flex-col items-center justify-center bg-background p-6">
			<div className="w-full max-w-[420px] rounded-lg border border-border bg-popover p-5 text-popover-foreground shadow-sm">
				<div className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-muted-foreground">
					{role} · not started
				</div>
				<p className="mt-2 text-[13px] leading-relaxed text-muted-foreground">
					This agent is on the task and has its first turn waiting, in the same worktree and on the same branch as dev.
					Nothing is spent until you start it. Starting it does not interrupt dev: both members of a crew work at the
					same time.
				</p>
				<div className="mt-4 flex items-center gap-3">
					<button
						className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground transition hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
						disabled={start.isPending}
						onClick={() => start.mutate()}
						type="button"
					>
						<Play className="h-3 w-3" aria-hidden="true" />
						{start.isPending ? "Starting…" : `Start ${role}`}
					</button>
					{errorText && <span className="min-w-0 truncate text-[12px] text-destructive">{errorText}</span>}
				</div>
			</div>
		</div>
	);
}
