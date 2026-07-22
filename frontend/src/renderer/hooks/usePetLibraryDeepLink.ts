import { useNavigate } from "@tanstack/react-router";
import { useEffect } from "react";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";

// Right-click a Proc on the desktop, land in its Pet library.
//
// The overlay cannot open the picker itself - it is a transparent always-on-top
// band whose click-through is decided per pointer move, and a popover on it would
// have to pin the window interactive for as long as it stayed open. So the gesture
// crosses to the main process, which brings THIS window forward and sends the
// session here.
//
// Mounted once, in the app shell, because the request can arrive whatever route is
// open. The Settings pane is what honours it and what clears it.
export function usePetLibraryDeepLink(): void {
	const navigate = useNavigate();
	const requestPetLibrary = useUiStore((state) => state.requestPetLibrary);

	useEffect(() => {
		return aoBridge.companion.onOpenPetLibrary((sessionRef) => {
			if (!sessionRef) return;
			requestPetLibrary(sessionRef);
			void navigate({ to: "/settings" });
		});
	}, [navigate, requestPetLibrary]);
}
