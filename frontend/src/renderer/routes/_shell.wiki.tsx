import { createFileRoute } from "@tanstack/react-router";
import { WikiPage } from "../components/WikiPage";

export const Route = createFileRoute("/_shell/wiki")({
	component: WikiPage,
});
