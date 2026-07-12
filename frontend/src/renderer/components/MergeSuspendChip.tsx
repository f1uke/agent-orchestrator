import { useMutation, useQueryClient } from "@tanstack/react-query";
import { GitMerge } from "lucide-react";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import { cn } from "../lib/utils";
import { mergedSuspendPRNumber, type WorkspaceSession } from "../types/workspace";

/**
 * The board-card / sidebar affordance for a keep-warm worker SUSPENDED after its
 * PR merged (feature/merge-suspend-in-place). The card stays in its lane (the
 * daemon surfaces a suspended-merged worker as needs_input, not merged) instead of
 * vanishing to the hidden Done zone; this chip replaces the idle "Paused" chip.
 *
 * Just ONE explicit action — **Move to Done** (`POST /sessions/{id}/kill`,
 * terminate + reclaim worktree → Done). "Continue" needs no button: opening the
 * card resumes the session in place (SessionView POSTs /wake → Resume, recreating
 * the tmux from the kept worktree with the conversation intact), exactly like the
 * idle "Paused — open to resume" chip. The Move-to-Done click stopPropagation so
 * it never triggers the card's own open handler.
 *
 * `compact` renders a glyph-only badge for the sidebar row (the row click opens →
 * resumes); the label + button live on the full board card.
 */
export function MergeSuspendChip({ session, compact = false }: { session: WorkspaceSession; compact?: boolean }) {
	const queryClient = useQueryClient();
	const prNumber = mergedSuspendPRNumber(session);
	const label = prNumber ? `Merged #${prNumber}` : "Merged";

	const done = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.merge_suspend_done", { project_id: session.workspaceId });
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to move session to Done"));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
	});

	if (compact) {
		return (
			<GitMerge
				aria-label={`${label} — open to continue, or move to Done`}
				className="h-3 w-3 shrink-0"
				style={{ color: "var(--lane-merge-bright)" }}
				strokeWidth={2}
			/>
		);
	}

	const errorText = done.error instanceof Error ? done.error.message : null;

	return (
		<span
			aria-label={`${label} — open to continue, or move to Done`}
			className={cn(
				"inline-flex shrink-0 items-center gap-1 rounded-full border py-0.5 pl-1.5 pr-0.5 text-[10px] font-medium",
				errorText
					? "border-[color-mix(in_srgb,var(--red)_55%,transparent)]"
					: "border-[color-mix(in_srgb,var(--fg-passive)_30%,transparent)]",
			)}
			title={errorText ?? `${label} — open the card to continue, or Move to Done to archive`}
		>
			<GitMerge className="h-3 w-3" style={{ color: "var(--lane-merge-bright)" }} strokeWidth={2} />
			<span className="text-passive">{label}</span>
			<button
				type="button"
				disabled={done.isPending}
				onClick={(e) => {
					e.stopPropagation();
					done.mutate();
				}}
				className="rounded-full px-1.5 py-px text-passive transition-colors hover:bg-[color-mix(in_srgb,var(--fg-passive)_16%,transparent)] disabled:opacity-50"
			>
				{done.isPending ? "Moving…" : "Move to Done"}
			</button>
		</span>
	);
}
