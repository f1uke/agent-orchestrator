import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./companion.css";
import { CompanionStage } from "./CompanionStage";
import type { CompanionFeed } from "./feed";
import { createLiveFeed, type LiveFeed } from "./live-feed";
import { createHttpTransport } from "./live-transport";
import { createMockFeed } from "./mock-feed";

// Entry point for the overlay window. Deliberately tiny and separate from the main
// renderer: the overlay has no router, no query client, no daemon connection and no
// design-system CSS. It is a transparent page with Procs on it, and everything it
// needs to know arrives through the feed.

type CompanionBridge = {
	setInteractive(interactive: boolean): void;
	daemonUrl?(): Promise<string | null>;
};

const bridge = (window as unknown as { aoCompanion?: CompanionBridge }).aoCompanion;

/** How often to re-ask for the daemon URL while it is not up yet. */
const DAEMON_WAIT_MS = 5_000;

/**
 * Waits for the daemon, then runs on real sessions.
 *
 * Until the daemon answers there is nothing true to show, so the overlay shows the
 * MOCK cast rather than an empty band — an empty desktop would read as "the
 * companion is broken" when it only means "the daemon is still starting". The
 * moment a real roster arrives the mock is replaced wholesale.
 */
function Overlay() {
	const [live, setLive] = useState<LiveFeed | null>(null);
	const [mock] = useState<CompanionFeed>(() => createMockFeed());

	useEffect(() => {
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | null = null;

		const attach = async () => {
			const url = await bridge?.daemonUrl?.().catch(() => null);
			if (cancelled) return;
			if (!url) {
				timer = setTimeout(attach, DAEMON_WAIT_MS);
				return;
			}
			setLive(createLiveFeed({ ...createHttpTransport(url), now: () => Date.now() }));
		};

		void attach();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, []);

	return (
		<CompanionStage
			feed={live ?? mock}
			bubbleFor={live ? (id) => live.bubbleFor(id) : undefined}
			onInteractiveChange={(interactive) => bridge?.setInteractive(interactive)}
		/>
	);
}

const container = document.getElementById("companion-root");
if (container) {
	createRoot(container).render(<Overlay />);
}
