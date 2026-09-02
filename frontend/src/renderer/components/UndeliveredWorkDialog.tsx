import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { FileWarning, Loader2 } from "lucide-react";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { killSession, type UncommittedFile } from "../lib/kill-session";
import { useOverlayDismissFocus } from "../lib/overlay-focus";
import { captureRendererEvent } from "../lib/telemetry";
import { Button } from "./ui/button";

/**
 * Why this card will not move to Done, and the two things to do about it.
 *
 * It opens on the daemon's REFUSAL, never on a guess: the plain kill destroys
 * nothing and answers with the files that stopped it, so what this dialog lists
 * is what discarding would actually lose - shown before it happens, which is the
 * whole point of the two-step.
 *
 * The two options are deliberately not symmetrical. "Open the session" is the
 * quiet default (finish the work and deliver it); discarding is the destructive
 * one, and it says what survives - the branch, its commits, and a capture of the
 * files at refs/ao/preserved/<id> - so the choice is informed rather than brave.
 */
export function UndeliveredWorkDialog({
	open,
	onOpenChange,
	sessionId,
	sessionTitle,
	files,
	onOpenSession,
	onDiscarded,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	sessionId: string;
	sessionTitle?: string;
	files: UncommittedFile[];
	/** Undefined on surfaces already inside the session (its own toolbar). */
	onOpenSession?: () => void;
	onDiscarded?: () => void;
}) {
	const queryClient = useQueryClient();
	const dismissFocus = useOverlayDismissFocus();

	const discard = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.session_discard_requested", { files: files.length });
			return killSession(sessionId, { discardUncommitted: true });
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			onOpenChange(false);
			onDiscarded?.();
		},
	});

	const noun = files.length === 1 ? "file" : "files";

	return (
		<Dialog.Root open={open} onOpenChange={(next) => !discard.isPending && onOpenChange(next)}>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
				<Dialog.Content
					{...dismissFocus}
					// The content is PORTALED, but React events still bubble along the
					// React tree - and this dialog is mounted inside a board card whose
					// wrapper opens the session on click. Without this, cancelling the
					// dialog (or discarding from it) also navigates into the session.
					onClick={(event) => event.stopPropagation()}
					className="fixed left-1/2 top-1/2 z-50 w-[460px] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-5 shadow-lg"
				>
					<Dialog.Title className="flex items-center gap-2 text-sm font-medium text-foreground">
						<FileWarning className="h-4 w-4 text-warning" aria-hidden="true" />
						This session still holds undelivered work
					</Dialog.Title>
					<Dialog.Description className="mt-2 text-[13px] leading-relaxed text-muted-foreground">
						{sessionTitle ? `“${sessionTitle}” ` : ""}has {files.length} uncommitted {noun} that no pull request
						carries, so it was not moved to Done and nothing was torn down.
					</Dialog.Description>

					<ul className="mt-3 max-h-52 overflow-y-auto rounded-md border border-border bg-background p-2">
						{files.length === 0 ? (
							<li className="px-1 py-0.5 text-[12px] text-muted-foreground">
								The daemon named no files. Open the session to look before discarding.
							</li>
						) : (
							files.map((file) => (
								<li key={file.path} className="flex items-baseline gap-2 px-1 py-0.5 font-mono text-[11.5px]">
									<span className="w-[4.75rem] shrink-0 text-passive">{file.status}</span>
									{/* The path WRAPS rather than truncating. A truncated path is the
									    one thing this list may not do: the decision is about which
									    files these are, and `src/…/Vie…` answers nothing. */}
									<span className="min-w-0 flex-1 break-all text-foreground">{file.path}</span>
								</li>
							))
						)}
					</ul>

					{/* Both ways out, named. The card cannot move to Done until one of
					    them happens, and a dialog that described only the destructive
					    one would be as unhelpful as the silence it replaces. */}
					<p className="mt-3 text-[12px] leading-relaxed text-muted-foreground">
						<span className="text-foreground">Finish it:</span> open the session — it resumes the agent in this
						worktree, with the work still there.
					</p>
					<p className="mt-1.5 text-[12px] leading-relaxed text-muted-foreground">
						<span className="text-foreground">Or discard it:</span> the worktree is removed. The branch and every
						commit on it stay, and these files are captured to{" "}
						<span className="font-mono text-[11px] text-passive">refs/ao/preserved/{sessionId}</span> first, so this is
						recoverable.
					</p>

					{discard.isError && (
						<div className="mt-3 text-[12px] text-error" role="alert">
							{discard.error instanceof Error ? discard.error.message : "Unable to discard this work"}
						</div>
					)}

					<div className="mt-4 flex justify-end gap-2">
						<Button variant="ghost" onClick={() => onOpenChange(false)} disabled={discard.isPending}>
							Cancel
						</Button>
						{onOpenSession && (
							<Button
								variant="ghost"
								disabled={discard.isPending}
								onClick={() => {
									onOpenChange(false);
									onOpenSession();
								}}
							>
								Open and finish it
							</Button>
						)}
						<Button
							className="border-destructive bg-destructive text-destructive-foreground hover:opacity-90"
							disabled={discard.isPending}
							onClick={() => discard.mutate()}
						>
							{discard.isPending && <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />}
							{discard.isPending ? "Discarding…" : "Discard and move to Done"}
						</Button>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
