import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Loader2, Trash2 } from "lucide-react";
import { useState } from "react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { returnFocusToTerminal } from "../lib/terminal-focus";
import { findProjectOrchestrator, type WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";

// Icon-only terminal-toolbar control that stops a running worker and tears down
// its runtime/workspace (POST /sessions/{id}/kill). Kill is irreversible, so the
// action arms a confirmation dialog before firing — matching the adjacent
// RestartSessionButton so the two destructive/lifecycle toolbar controls read as
// a pair. Lives here (not the topbar) so the worker header keeps only the
// inspector toggle. On success it returns to the project's orchestrator (or the
// board when none is live), the same landing the old topbar Kill used.
export function KillSessionButton({ session }: { session: WorkspaceSession }) {
	const queryClient = useQueryClient();
	const navigate = useNavigate();
	const workspaces = useWorkspaceQuery().data ?? [];
	const [confirmOpen, setConfirmOpen] = useState(false);

	const kill = useMutation({
		mutationFn: async () => {
			void captureRendererEvent("ao.renderer.session_kill_requested", { project_id: session.workspaceId });
			const { error: apiError } = await apiClient.POST("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: session.id } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Unable to kill session"));
		},
		onSuccess: () => {
			void captureRendererEvent("ao.renderer.session_kill_succeeded", { project_id: session.workspaceId });
			setConfirmOpen(false);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			const orchestrator = findProjectOrchestrator(workspaces, session.workspaceId);
			if (orchestrator) {
				void navigate({
					to: "/projects/$projectId/sessions/$sessionId",
					params: { projectId: session.workspaceId, sessionId: orchestrator.id },
				});
				return;
			}
			void navigate({ to: "/projects/$projectId", params: { projectId: session.workspaceId } });
		},
		onError: () => {
			void captureRendererEvent("ao.renderer.session_kill_failed", { project_id: session.workspaceId });
		},
	});

	const openConfirm = (open: boolean) => {
		if (kill.isPending) return;
		if (open) kill.reset();
		setConfirmOpen(open);
	};

	return (
		<Dialog.Root open={confirmOpen} onOpenChange={openConfirm}>
			<Dialog.Trigger asChild>
				<button
					aria-label="Kill session"
					className="terminal-toolbar__control terminal-toolbar__control--icon terminal-toolbar__control--danger"
					disabled={kill.isPending}
					title="Kill session — stops the agent and ends this session"
					type="button"
				>
					<Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
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
					<Dialog.Title className="text-sm font-medium text-foreground">Kill this session?</Dialog.Title>
					<Dialog.Description className="mt-2 text-[13px] text-muted-foreground">
						The agent stops and this session ends, tearing down its runtime. This can't be undone from here.
					</Dialog.Description>
					{kill.isError && (
						<div className="mt-3 text-[12px] text-destructive" role="alert">
							{kill.error instanceof Error ? kill.error.message : "Unable to kill session"}
						</div>
					)}
					<div className="mt-4 flex justify-end gap-2">
						<Button variant="ghost" onClick={() => openConfirm(false)} disabled={kill.isPending}>
							Cancel
						</Button>
						<Button
							className="border-destructive bg-destructive text-destructive-foreground hover:opacity-90"
							onClick={() => kill.mutate()}
							disabled={kill.isPending}
						>
							{kill.isPending && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
							{kill.isPending ? "Killing…" : "Kill"}
						</Button>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
