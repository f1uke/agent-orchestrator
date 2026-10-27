import { createFileRoute, redirect } from "@tanstack/react-router";
import { useState } from "react";
import { ReleasesCapabilitiesProvider } from "../components/releases/ReleasesCapabilities";
import { ReleasesPage } from "../components/releases/ReleasesPage";
import { releasesSearch, parseReleasesSearch, type ReleasesSearchRaw } from "../lib/releases/url";
import { readSavedSearch } from "../stores/releases-ui";

/** The browser preview can try the read-only host with ?readonly in the page URL; the desktop app is never read-only. */
function previewReadOnly(): boolean {
	if (import.meta.env.VITE_NO_ELECTRON !== "1") return false;
	const asked = (query: string) => new URLSearchParams(query).has("readonly");
	return asked(window.location.search) || asked(window.location.hash.split("?")[1] ?? "");
}

function ReleasesRoute() {
	const [readOnly] = useState(previewReadOnly);
	return (
		<ReleasesCapabilitiesProvider readOnly={readOnly}>
			<ReleasesPage />
		</ReleasesCapabilitiesProvider>
	);
}

export const Route = createFileRoute("/_shell/releases")({
	validateSearch: (raw: Record<string, unknown>): ReleasesSearchRaw => releasesSearch(parseReleasesSearch(raw)),
	beforeLoad: ({ search, cause }) => {
		if (cause !== "enter" || Object.keys(search).length > 0) return;
		const saved = readSavedSearch();
		if (Object.keys(saved).length > 0) throw redirect({ to: "/releases", search: saved, replace: true });
	},
	component: ReleasesRoute,
});
