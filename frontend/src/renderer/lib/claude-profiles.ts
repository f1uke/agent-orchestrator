import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";
import { mockClaudeProfiles } from "./mock-data";

export type ClaudeProfile = components["schemas"]["ClaudeProfile"];
export type ClaudeProfileInput = components["schemas"]["ClaudeProfileInput"];

export const SUBSCRIPTION_PROFILE = "Subscription";
export const CLAUDE_CODE_AGENT = "claude-code";

export const claudeProfilesQueryKey = ["settings", "claudeProfiles"] as const;

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

export async function fetchClaudeProfiles(): Promise<ClaudeProfile[]> {
	if (usePreviewData) return mockClaudeProfiles();
	const { data, error } = await apiClient.GET("/api/v1/settings/claude-profiles", {});
	if (error) throw new Error(apiErrorMessage(error));
	return (data as components["schemas"]["ClaudeProfilesResponse"]).profiles;
}

export function useClaudeProfiles(enabled = true) {
	return useQuery({ queryKey: claudeProfilesQueryKey, queryFn: fetchClaudeProfiles, enabled });
}

export function usesClaudeProfiles(agent: string | undefined): boolean {
	return agent === CLAUDE_CODE_AGENT;
}

export function profileDisplayName(name: string | null | undefined): string {
	return name?.trim() || SUBSCRIPTION_PROFILE;
}

export function isRoutedProfile(name: string | null | undefined): boolean {
	return profileDisplayName(name).toLowerCase() !== SUBSCRIPTION_PROFILE.toLowerCase();
}

export function sameProfile(a: string | null | undefined, b: string | null | undefined): boolean {
	return profileDisplayName(a).toLowerCase() === profileDisplayName(b).toLowerCase();
}

export function userProfiles(profiles: ClaudeProfile[]): ClaudeProfileInput[] {
	return profiles.filter((p) => !p.builtin).map(({ name, settingsFile }) => ({ name, settingsFile }));
}
