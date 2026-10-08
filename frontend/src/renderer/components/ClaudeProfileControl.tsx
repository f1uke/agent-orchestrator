import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, Loader2, Route } from "lucide-react";
import { useState } from "react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import {
	profileChoices,
	profileDisplayName,
	sameProfile,
	useClaudeProfiles,
	usesClaudeProfiles,
} from "../lib/claude-profiles";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { returnFocusToTerminal } from "../lib/terminal-focus";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

export function ClaudeProfileControl({ session }: { session: WorkspaceSession }) {
	if (!usesClaudeProfiles(session.provider)) return null;
	return <ProfileSwitcher session={session} />;
}

function restartsNow(session: WorkspaceSession): boolean {
	const state = session.activity?.state;
	return state === "idle" || state === "parked";
}

function ProfileSwitcher({ session }: { session: WorkspaceSession }) {
	const queryClient = useQueryClient();
	const profilesQuery = useClaudeProfiles();
	const [target, setTarget] = useState<string | null>(null);
	const current = profileDisplayName(session.claudeProfile);
	const names = profileChoices(profilesQuery.data ?? [], current);
	const selected = names.find((n) => sameProfile(n, current)) ?? current;

	const switchProfile = useMutation({
		mutationFn: async ({ profile, restart }: { profile: string; restart: boolean }) => {
			const { error } = await apiClient.PUT("/api/v1/sessions/{sessionId}/claude-profile", {
				params: { path: { sessionId: session.id } },
				body: { profile, restart },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to switch the Claude profile"));
		},
		onSuccess: () => {
			setTarget(null);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});

	const choose = (name: string) => {
		if (sameProfile(name, current)) return;
		switchProfile.reset();
		setTarget(name);
	};
	const closeDialog = (open: boolean) => {
		if (!open && !switchProfile.isPending) setTarget(null);
	};
	const now = restartsNow(session);
	const pending = session.restartPending === true;

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<button
						aria-label={`Claude profile: ${current}${pending ? ", restart pending" : ""}`}
						className="terminal-toolbar__control terminal-toolbar__control--text"
						title={
							pending
								? `Switching to ${current}: AO restarts the agent when its current turn ends`
								: "Claude profile - the settings this session's Claude Code runs on"
						}
						type="button"
					>
						<Route className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
						<span className="max-w-[120px] truncate">{current}</span>
						{pending && <span className="terminal-toolbar__pending">· restart pending</span>}
						<ChevronDown className="h-3 w-3 shrink-0" aria-hidden="true" />
					</button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className="min-w-52">
					<DropdownMenuLabel>Claude profile</DropdownMenuLabel>
					<DropdownMenuRadioGroup value={selected} onValueChange={choose}>
						{names.map((name) => (
							<DropdownMenuRadioItem key={name} value={name}>
								{name}
							</DropdownMenuRadioItem>
						))}
					</DropdownMenuRadioGroup>
				</DropdownMenuContent>
			</DropdownMenu>

			<Dialog.Root open={target !== null} onOpenChange={closeDialog}>
				<Dialog.Portal>
					<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
					<Dialog.Content
						onCloseAutoFocus={returnFocusToTerminal}
						className="fixed left-1/2 top-1/2 z-50 w-[460px] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-5 shadow-lg"
					>
						<Dialog.Title className="text-sm font-medium text-foreground">Switch to {target}?</Dialog.Title>
						<Dialog.Description className="mt-2 text-[13px] text-muted-foreground">
							{now
								? `The agent restarts on the ${target} settings and resumes this conversation where it left off. The terminal disconnects briefly and reattaches on its own.`
								: `The agent is mid-turn, so AO restarts it when the current turn ends, then resumes this conversation on the ${target} settings.`}
						</Dialog.Description>
						<p className="mt-2 text-[12px] text-passive">
							Switch without restarting keeps the agent running as it is; the profile applies on its next restart.
						</p>
						{switchProfile.isError && (
							<div className="mt-3 text-[12px] text-destructive" role="alert">
								{switchProfile.error instanceof Error
									? switchProfile.error.message
									: "Unable to switch the Claude profile"}
							</div>
						)}
						<div className="mt-4 flex justify-end gap-2">
							<Button variant="ghost" onClick={() => closeDialog(false)} disabled={switchProfile.isPending}>
								Cancel
							</Button>
							<Button
								variant="outline"
								disabled={switchProfile.isPending}
								onClick={() => target && switchProfile.mutate({ profile: target, restart: false })}
							>
								Switch without restarting
							</Button>
							<Button
								disabled={switchProfile.isPending}
								onClick={() => target && switchProfile.mutate({ profile: target, restart: true })}
							>
								{switchProfile.isPending && <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />}
								{now ? "Restart now" : "Restart when idle"}
							</Button>
						</div>
					</Dialog.Content>
				</Dialog.Portal>
			</Dialog.Root>
		</>
	);
}
