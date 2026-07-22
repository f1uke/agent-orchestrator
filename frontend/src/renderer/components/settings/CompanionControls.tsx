import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { aoBridge } from "../../lib/bridge";
import { companionSettingsQueryKey } from "../CompanionOffer";
import { Switch } from "../ui/switch";

// The desktop companion switch. An INSTANT action, deliberately out of the global
// save bar: it opens or closes a window on the user's desktop, and a control with a
// visible consequence that waits for a Save press reads as broken.
//
// Using the switch also records `asked`, because flipping it IS an answer to the
// first-run offer — otherwise a user who found the setting first would still be
// asked the question they had already settled.
export function CompanionControls() {
	const queryClient = useQueryClient();
	const settings = useQuery({
		queryKey: companionSettingsQueryKey,
		queryFn: () => aoBridge.companionSettings.get(),
	});
	const save = useMutation({
		mutationFn: (enabled: boolean) => aoBridge.companionSettings.set({ enabled, asked: true }),
		onSuccess: () => queryClient.invalidateQueries({ queryKey: companionSettingsQueryKey }),
	});

	const enabled = settings.data?.enabled === true;

	return (
		<div className="flex items-center gap-3">
			<Switch
				id="companionEnabled"
				checked={enabled}
				disabled={settings.isPending || save.isPending}
				onCheckedChange={(checked) => save.mutate(checked)}
			/>
			<label htmlFor="companionEnabled" className="text-[12px] text-muted-foreground">
				Show the companion on my desktop
			</label>
		</div>
	);
}
