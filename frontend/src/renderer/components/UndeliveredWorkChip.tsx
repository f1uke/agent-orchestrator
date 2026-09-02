import { useMutation, useQueryClient } from "@tanstack/react-query";
import { FileWarning } from "lucide-react";
import { useState } from "react";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { killSession, UndeliveredWorkError, type UncommittedFile } from "../lib/kill-session";
import { captureRendererEvent } from "../lib/telemetry";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { UndeliveredWorkDialog } from "./UndeliveredWorkDialog";

/**
 * The board-card / sidebar affordance for a worker PARKED holding work nobody
 * has seen: it ended its own turn and no pull request was ever opened from it.
 *
 * It exists because the alternative was silence. The card sits in "Needs you"
 * looking like every other card, "Move to Done" answered 200 and did nothing,
 * and the human reasonably concluded the board was broken. This chip says the
 * word - *Undelivered* - and its button is the same one-click Move to Done the
 * merge-suspend chip offers, except that here the click can be REFUSED, and the
 * refusal opens the dialog that explains it with the file list.
 *
 * Opening the card needs no button, as with every other suspended session:
 * opening resumes the agent into the tree its work is still sitting in.
 */
export function UndeliveredWorkChip({
	session,
	compact = false,
	onOpenSession,
}: {
	session: WorkspaceSession;
	compact?: boolean;
	/** Opening the card resumes the agent in its worktree - the "finish it" half. */
	onOpenSession?: () => void;
}) {
	const queryClient = useQueryClient();
	const [refused, setRefused] = useState<UncommittedFile[] | null>(null);
	const label = "Undelivered";
	const title = "Ended holding work no pull request carries — open to finish it, or Move to Done";

	const done = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.undelivered_done_requested", { project_id: session.workspaceId });
			return killSession(session.id);
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
		onError: (error) => {
			// The refusal is not a failure to report — it is the explanation the
			// board owed the human, and it arrives with the files.
			if (error instanceof UndeliveredWorkError) setRefused(error.files);
		},
	});

	if (compact) {
		return (
			<span className="inline-flex shrink-0" title={title}>
				<FileWarning aria-label={label} className="h-3 w-3 text-warning" strokeWidth={2} />
			</span>
		);
	}

	const otherError = done.error && !(done.error instanceof UndeliveredWorkError) ? done.error.message : null;

	return (
		<>
			<span
				aria-label={`${label} — open to finish the work, or move to Done`}
				className={cn(
					"inline-flex shrink-0 items-center gap-1 rounded-full border py-0.5 pl-1.5 pr-0.5 text-[10px] font-medium",
					otherError
						? "border-[color-mix(in_srgb,var(--red)_55%,transparent)]"
						: "border-[color-mix(in_srgb,var(--amber)_45%,transparent)]",
				)}
				title={otherError ?? title}
			>
				<FileWarning className="h-3 w-3 text-warning" strokeWidth={2} />
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
			{refused && (
				<UndeliveredWorkDialog
					open
					onOpenChange={(next) => !next && setRefused(null)}
					sessionId={session.id}
					sessionTitle={session.title}
					files={refused}
					onOpenSession={onOpenSession}
				/>
			)}
		</>
	);
}
