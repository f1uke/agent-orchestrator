import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";

export const projectBranchesQueryKey = (projectId?: string) =>
	projectId ? (["project-branches", projectId] as const) : (["project-branches"] as const);

export async function fetchProjectBranches(projectId: string): Promise<string[]> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}/branches", {
		params: { path: { id: projectId } },
	});
	if (error) throw error;
	return data?.branches ?? [];
}

export function useProjectBranches(projectId?: string): { branches: string[] } {
	const query = useQuery({
		queryKey: projectBranchesQueryKey(projectId),
		enabled: Boolean(projectId),
		queryFn: () => fetchProjectBranches(projectId as string),
		retry: 1,
	});
	return { branches: query.data ?? [] };
}
