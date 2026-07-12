import { createFileRoute } from "@tanstack/react-router";
import { BrowseJiraPage } from "../components/BrowseJiraPage";

// Un-nested from the board ($projectId_ trailing underscore) so Browse Jira renders
// as its own full-page surface in the shell outlet, not inside the board — same
// pattern as the project settings route.
export const Route = createFileRoute("/_shell/projects/$projectId_/jira")({
	component: BrowseJiraRoute,
});

function BrowseJiraRoute() {
	const { projectId } = Route.useParams();
	return <BrowseJiraPage projectId={projectId} />;
}
