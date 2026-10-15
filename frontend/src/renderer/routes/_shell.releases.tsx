import { createFileRoute, redirect } from "@tanstack/react-router";
import { ReleasesPage } from "../components/releases/ReleasesPage";
import { releasesSearch, parseReleasesSearch, type ReleasesSearchRaw } from "../lib/releases/url";
import { readSavedSearch } from "../stores/releases-ui";

export const Route = createFileRoute("/_shell/releases")({
	validateSearch: (raw: Record<string, unknown>): ReleasesSearchRaw => releasesSearch(parseReleasesSearch(raw)),
	beforeLoad: ({ search, cause }) => {
		if (cause !== "enter" || Object.keys(search).length > 0) return;
		const saved = readSavedSearch();
		if (Object.keys(saved).length > 0) throw redirect({ to: "/releases", search: saved, replace: true });
	},
	component: ReleasesPage,
});
