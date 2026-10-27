import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import {
	Outlet,
	RouterProvider,
	createHashHistory,
	createRootRoute,
	createRoute,
	createRouter,
	redirect,
} from "@tanstack/react-router";
import "../renderer/styles.css";
import { queryClient } from "../renderer/lib/query-client";
import { setApiBaseUrl } from "../renderer/lib/api-client";
import { ReleasesPage } from "../renderer/components/releases/ReleasesPage";
import { ReleasesCapabilitiesProvider } from "../renderer/components/releases/ReleasesCapabilities";
import { TooltipProvider } from "../renderer/components/ui/tooltip";
import { parseReleasesSearch, releasesSearch } from "../renderer/lib/releases/url";
import { readStoredTheme } from "../renderer/stores/ui-store";
import { SignOut, signOutOnUnauthorized } from "./session";

setApiBaseUrl(window.location.origin);
signOutOnUnauthorized();

const theme = readStoredTheme() ?? (window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark");
document.documentElement.dataset.theme = theme;
document.documentElement.style.colorScheme = theme;

const rootRoute = createRootRoute({ component: Outlet });

const indexRoute = createRoute({
	getParentRoute: () => rootRoute,
	path: "/",
	beforeLoad: () => {
		throw redirect({ to: "/releases" });
	},
});

const releasesRoute = createRoute({
	getParentRoute: () => rootRoute,
	path: "/releases",
	validateSearch: (raw: Record<string, unknown>) => releasesSearch(parseReleasesSearch(raw)),
	component: () => (
		<ReleasesCapabilitiesProvider readOnly>
			<div className="flex h-screen flex-col bg-background text-foreground">
				<SignOut />
				<div className="min-h-0 flex-1">
					<ReleasesPage />
				</div>
			</div>
		</ReleasesCapabilitiesProvider>
	),
});

const router = createRouter({
	history: createHashHistory(),
	routeTree: rootRoute.addChildren([indexRoute, releasesRoute]),
	context: { queryClient },
});

createRoot(document.getElementById("root") as HTMLElement).render(
	<React.StrictMode>
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<RouterProvider router={router} />
			</TooltipProvider>
		</QueryClientProvider>
	</React.StrictMode>,
);
