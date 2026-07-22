import * as Dialog from "@radix-ui/react-dialog";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "./ui/button";
import { aoBridge } from "../lib/bridge";
import { useOverlayDismissFocus } from "../lib/overlay-focus";

export const companionSettingsQueryKey = ["companion-settings"] as const;

// The desktop companion's first-run offer. It is asked ONCE, on the first launch
// after the feature lands, and the answer is recorded either way — because a
// feature that puts a character on someone's desktop must be opted into, and a
// feature that keeps asking is worse than one that never shipped.
//
// Dismissing the dialog without choosing (Escape, click outside) deliberately does
// NOT record an answer: it is not consent, so the offer comes back next launch.
export function CompanionOffer() {
	const queryClient = useQueryClient();
	const dismissFocus = useOverlayDismissFocus();
	// Closed the moment the answer is written, rather than when a refetch confirms
	// it: the user already decided, and leaving the dialog up while a disk write
	// round-trips reads as an unresponsive button.
	const [answered, setAnswered] = useState(false);
	const settings = useQuery({
		queryKey: companionSettingsQueryKey,
		queryFn: () => aoBridge.companionSettings.get(),
	});
	const answer = useMutation({
		mutationFn: (enabled: boolean) => aoBridge.companionSettings.set({ enabled, asked: true }),
		onSuccess: () => {
			setAnswered(true);
			return queryClient.invalidateQueries({ queryKey: companionSettingsQueryKey });
		},
	});

	if (answered || settings.data?.asked !== false) return null;

	return (
		<Dialog.Root open>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
				<Dialog.Content
					{...dismissFocus}
					className="fixed left-1/2 top-1/2 z-50 w-[440px] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-5 shadow-lg"
				>
					<Dialog.Title className="text-sm font-medium text-foreground">Put your sessions on the desktop?</Dialog.Title>
					<Dialog.Description className="mt-2 text-[13px] leading-[1.5] text-muted-foreground">
						The companion shows one small character per session along the bottom of your screen — working, waiting on
						you, or done. It stays out of the way: clicks pass straight through to whatever is underneath.
					</Dialog.Description>
					<p className="mt-3 text-[11px] text-muted-foreground">
						You can turn it on or off any time in Settings → System.
					</p>
					<div className="mt-4 flex items-center justify-end gap-2">
						<Button variant="ghost" type="button" disabled={answer.isPending} onClick={() => answer.mutate(false)}>
							No thanks
						</Button>
						<Button variant="primary" type="button" disabled={answer.isPending} onClick={() => answer.mutate(true)}>
							Show the companion
						</Button>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
