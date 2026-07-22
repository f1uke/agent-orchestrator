import { createRoot } from "react-dom/client";
import "./companion.css";
import { CompanionStage } from "./CompanionStage";

// Entry point for the overlay window. Deliberately tiny and separate from the main
// renderer: the overlay has no router, no query client, no daemon connection and no
// design-system CSS. It is a transparent page with Procs on it, and everything it
// needs to know arrives through the feed.

type CompanionBridge = { setInteractive(interactive: boolean): void };

const bridge = (window as unknown as { aoCompanion?: CompanionBridge }).aoCompanion;

const container = document.getElementById("companion-root");
if (container) {
	createRoot(container).render(
		// The window is click-through by default; the shell takes the pointer only
		// while it is genuinely over a Proc, and hands it straight back after.
		<CompanionStage onInteractiveChange={(interactive) => bridge?.setInteractive(interactive)} />,
	);
}
