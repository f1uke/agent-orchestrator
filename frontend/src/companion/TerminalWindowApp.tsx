import { useCallback, useEffect, useState } from "react";
import { TerminalBubble } from "./TerminalBubble";

// PROTOTYPE (terminal bubble): what the TERMINAL WINDOW renders.
//
// The same bundle as the overlay, told by its query string to be one thing
// instead of the other — so the card, the xterm, the preload and the CSP are all
// the ones already proven, and there is no second copy of any of them to keep in
// step. What it is NOT is the overlay: no band, no Procs, no click-through, no
// pointer-region bookkeeping. It is a window with a terminal in it.

type TerminalBridge = {
	daemonUrl?(): Promise<string | null>;
	closeTerminal?(): void;
};

const bridge = (window as unknown as { aoCompanion?: TerminalBridge }).aoCompanion;

const DAEMON_WAIT_MS = 2_000;

export function TerminalWindowApp({ sessionId, handleId }: { sessionId: string; handleId: string }) {
	const [daemonUrl, setDaemonUrl] = useState<string | null>(null);

	useEffect(() => {
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | null = null;
		const ask = async () => {
			const url = await bridge?.daemonUrl?.().catch(() => null);
			if (cancelled) return;
			if (!url) {
				timer = setTimeout(ask, DAEMON_WAIT_MS);
				return;
			}
			setDaemonUrl(url);
		};
		void ask();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, []);

	// Closing is the main process's job: it owns the window, and a page cannot
	// destroy the thing it is drawn in. Everything that ends a terminal — the ✕,
	// Escape, the session ending — comes through here.
	const close = useCallback(() => bridge?.closeTerminal?.(), []);

	if (!daemonUrl) return null;
	return <TerminalBubble handleId={handleId} title={sessionId} daemonUrl={daemonUrl} onClose={close} fills />;
}
