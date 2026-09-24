import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { RefLinkSettings } from "../lib/ref-links";

export type RefLinkSettingsResponse = components["schemas"]["RefLinksSettingsResponse"];

/** Shared with the Settings page, which invalidates it on save. */
export const refLinkSettingsQueryKey = ["settings", "refLinks"] as const;

/**
 * Where Jira keys and GitLab `!N` references in plain text link to. Until it
 * loads (or if it fails) the answer is `undefined`, which links nothing - text
 * reads exactly as it did before this setting existed.
 */
export function useRefLinkSettings() {
	return useQuery({
		queryKey: refLinkSettingsQueryKey,
		queryFn: async (): Promise<RefLinkSettings> => {
			const { data, error } = await apiClient.GET("/api/v1/settings/ref-links", {});
			if (error) throw new Error(apiErrorMessage(error));
			const body = data as RefLinkSettingsResponse;
			return { ...body, gitlabRepoAliases: body.gitlabRepoAliases ?? {} };
		},
		retry: 1,
	});
}
