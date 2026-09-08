import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "../../styles.css";
import { SettingsPreviewApp } from "./SettingsPreviewApp";

// Standalone entry for the Settings redesign preview. Deliberately NOT listed in
// vite.renderer.config.ts's build inputs: it is a design artefact that must run in
// `vite dev` and never ship inside the packaged app. No router, no query client,
// no daemon - every value it shows is mock data from ./model.ts and nothing saves.
const el = document.getElementById("settings-preview-root");
if (el) {
	createRoot(el).render(
		<StrictMode>
			<SettingsPreviewApp />
		</StrictMode>,
	);
}
