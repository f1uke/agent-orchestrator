import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, hasTrustedApiBaseUrl } from "../lib/api-client";
import { mockWorkspaces } from "../lib/mock-data";
import { displaySessionName } from "../lib/session-title";
import {
	type PRState,
	type PullRequestFacts,
	toAgentProvider,
	toSessionActivity,
	toSessionStatus,
	toStatusReason,
	type WorkspaceSummary,
} from "../types/workspace";

function toPullRequestFacts(pr: components["schemas"]["SessionPRFacts"]): PullRequestFacts {
	return {
		url: pr.url,
		number: pr.number,
		state: pr.state as PRState,
		ci: pr.ci,
		review: pr.review,
		mergeability: pr.mergeability,
		reviewComments: pr.reviewComments,
		updatedAt: pr.updatedAt,
	};
}

export const workspaceQueryKey = ["workspaces"] as const;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

async function fetchWorkspaces(): Promise<WorkspaceSummary[]> {
	if (usePreviewData) {
		return mockWorkspaces;
	}
	if (!hasTrustedApiBaseUrl()) {
		return [];
	}

	const [{ data: projectsData, error: projectsError }, { data: sessionsData, error: sessionsError }] =
		await Promise.all([apiClient.GET("/api/v1/projects"), apiClient.GET("/api/v1/sessions")]);

	if (projectsError || sessionsError) throw projectsError ?? sessionsError;

	return (projectsData?.projects ?? []).map((project) => ({
		id: project.id,
		name: project.name,
		kind: project.kind === "workspace" ? "workspace" : "single_repo",
		path: project.path,
		orchestratorAgent: project.orchestratorAgent ? toAgentProvider(project.orchestratorAgent) : undefined,
		sessions: (sessionsData?.sessions ?? [])
			.filter((session) => session.projectId === project.id)
			.map((session) => ({
				id: session.id,
				terminalHandleId: session.terminalHandleId,
				workspaceId: project.id,
				workspaceName: project.name,
				// Display NAME is the human summary, never the Jira card number — the
				// key rides the branch + its own badge (enhancement #5).
				title: displaySessionName(session),
				issueId: session.issueId,
				provider: toAgentProvider(session.harness),
				kind: session.kind === "orchestrator" ? "orchestrator" : session.kind === "worker" ? "worker" : undefined,
				branch: session.branch ?? `session/${session.id}`,
				workspacePath: session.workspacePath,
				status: toSessionStatus(session.status, session.isTerminated),
				statusReason: toStatusReason(session.statusReason),
				nextTransitionAt: session.nextTransitionAt ?? undefined,
				nextTransitionTo: session.nextTransitionTo ? toSessionStatus(session.nextTransitionTo) : undefined,
				createdAt: session.createdAt,
				updatedAt: session.updatedAt,
				activity: toSessionActivity(session.activity),
				previewUrl: session.previewUrl,
				previewRevision: session.previewRevision,
				prs: (session.prs ?? []).map(toPullRequestFacts),
				isTodo: session.isTodo,
				isTerminated: session.isTerminated,
				isSuspended: session.isSuspended,
				keepWarmOnMerge: session.keepWarmOnMerge,
				idleCloseAt: session.idleCloseAt ?? undefined,
				baseBranch: session.baseBranch,
				prTarget: session.prTarget,
				targetBranch: session.targetBranch,
				targetSource: session.targetSource,
				autoNameBranch: session.autoNameBranch,
				createdBy: session.createdBy,
				prompt: session.prompt,
				tokenUsage: session.tokenUsage,
			})),
	}));
}

// Shared so route loaders can prefetch via queryClient.ensureQueryData (paired
// with the router's defaultPreload: "intent") and the hook reads the same cache.
export const workspaceQueryOptions = {
	queryKey: workspaceQueryKey,
	queryFn: fetchWorkspaces,
	retry: 1,
	refetchInterval: 15_000,
};

export function useWorkspaceQuery() {
	return useQuery(workspaceQueryOptions);
}
