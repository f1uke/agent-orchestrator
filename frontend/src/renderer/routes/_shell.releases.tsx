import { createFileRoute } from "@tanstack/react-router";
import { ReleasesPage } from "../components/releases/ReleasesPage";
import { releasesSearch, parseReleasesSearch, type ReleasesSearchRaw } from "../lib/releases/url";

export const Route = createFileRoute("/_shell/releases")({
	validateSearch: (raw: Record<string, unknown>): ReleasesSearchRaw => releasesSearch(parseReleasesSearch(raw)),
	component: ReleasesPage,
});
