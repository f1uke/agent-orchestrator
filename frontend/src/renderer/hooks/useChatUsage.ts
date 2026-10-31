import { useQuery } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { SubscriptionUsage } from "../lib/chat-usage";

export const chatUsageQueryKey = ["chat", "usage"] as const;

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

const PREVIEW_USAGE: SubscriptionUsage = {
	fiveHour: {
		utilization: 0.01,
		resetsAt: new Date(Date.now() + 4 * 3_600_000 + 12 * 60_000).toISOString(),
		status: "allowed",
	},
	sevenDay: { utilization: 0.36, resetsAt: new Date(Date.now() + 3 * 86_400_000).toISOString(), status: "allowed" },
	updatedAt: new Date(Date.now() - 3 * 60_000).toISOString(),
};

async function fetchChatUsage(): Promise<SubscriptionUsage | null> {
	const { data, error, response } = await apiClient.GET("/api/v1/chat/usage");
	if (error) throw new Error(apiErrorMessage(error));
	return response.status === 204 ? null : (data ?? null);
}

export function useChatUsage(daemonReady: boolean): SubscriptionUsage | undefined {
	const enabled = daemonReady || usePreviewData;
	const { data } = useQuery({
		queryKey: chatUsageQueryKey,
		queryFn: () => (usePreviewData ? Promise.resolve(PREVIEW_USAGE) : fetchChatUsage()),
		enabled,
		retry: 1,
		refetchOnMount: "always",
		refetchInterval: enabled ? 60_000 : false,
	});
	return data ?? undefined;
}
