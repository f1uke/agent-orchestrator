import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, RotateCw } from "lucide-react";
import { useState } from "react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { returnFocusToTerminal } from "../lib/terminal-focus";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";

// Terminal-toolbar control that restarts the current session in place. The
// daemon recycles only the agent's runtime (POST /sessions/{id}/restart): the
// worktree is left untouched — uncommitted work and all — while the agent
// relaunches under the same session id with a freshly recomputed system prompt
// and resumes the native transcript via --resume. Primary use: reload a live
// session's prompt after the orchestrator/worker prompt changed, without losing
// the conversation. Restarting drops the terminal briefly, so the action arms a
// confirmation first; the terminal reattaches automatically once the session is
// back (TerminalPane re-keys on the new runtime handle).
export function RestartSessionButton({ session }: { session: WorkspaceSession }) {
	const queryClient = useQueryClient();
	const [confirmOpen, setConfirmOpen] = useState(false);

	const restart = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.session_restart_requested", { project_id: session.workspaceId });
			const { error: apiError } = await apiClient.POST("/api/v1/sessions/{sessionId}/restart", {
				params: { path: { sessionId: session.id } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Unable to restart session"));
		},
		onSuccess: () => {
			void captureRendererEvent("ao.renderer.session_restart_succeeded", { project_id: session.workspaceId });
			setConfirmOpen(false);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
		onError: () => {
			void captureRendererEvent("ao.renderer.session_restart_failed", { project_id: session.workspaceId });
		},
	});

	const openConfirm = (open: boolean) => {
		if (restart.isPending) return;
		if (open) restart.reset();
		setConfirmOpen(open);
	};

	return (
		<Dialog.Root open={confirmOpen} onOpenChange={openConfirm}>
			<Dialog.Trigger asChild>
				<button
					aria-label="Restart session"
					className="terminal-toolbar__control terminal-toolbar__control--icon"
					disabled={restart.isPending}
					title="Restart session — reloads the system prompt, keeps the conversation"
					type="button"
				>
					<RotateCw className={restart.isPending ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} aria-hidden="true" />
				</button>
			</Dialog.Trigger>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
				<Dialog.Content
					// Terminal-toolbar dialog: on close (Cancel, Esc, or an outside click)
					// return the caret to the terminal so the user can keep typing.
					onCloseAutoFocus={returnFocusToTerminal}
					className="fixed left-1/2 top-1/2 z-50 w-[420px] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-5 shadow-lg"
				>
					<Dialog.Title className="text-sm font-medium text-foreground">Restart this session?</Dialog.Title>
					<Dialog.Description className="mt-2 text-[13px] text-muted-foreground">
						The agent restarts and reloads its system prompt, then resumes this conversation where it left off. The
						terminal disconnects briefly and reattaches on its own.
					</Dialog.Description>
					{restart.isError && (
						<div className="mt-3 text-[12px] text-destructive" role="alert">
							{restart.error instanceof Error ? restart.error.message : "Unable to restart session"}
						</div>
					)}
					<div className="mt-4 flex justify-end gap-2">
						<Button variant="ghost" onClick={() => openConfirm(false)} disabled={restart.isPending}>
							Cancel
						</Button>
						<Button onClick={() => restart.mutate()} disabled={restart.isPending}>
							{restart.isPending && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
							{restart.isPending ? "Restarting…" : "Restart"}
						</Button>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
