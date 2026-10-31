import type { components } from "../../api/schema";
import { isObject } from "./chat-event";

/** One Claude subscription window: utilization is 0..1, resetsAt an ISO time. */
export type UsageWindow = components["schemas"]["ControllersChatUsageWindow"];

/** Claude subscription usage, as GET /api/v1/chat/usage serves it and a session's log folds it. */
export type SubscriptionUsage = components["schemas"]["ControllersChatUsageResponse"];

const finite = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value);

function parseWindow(raw: unknown): UsageWindow | undefined {
	if (!isObject(raw) || !finite(raw.utilization) || !finite(raw.resetsAt)) return undefined;
	const status = typeof raw.status === "string" ? raw.status : "";
	return { utilization: raw.utilization, resetsAt: new Date(raw.resetsAt * 1000).toISOString(), status };
}

export function parseRateLimit(rateLimitInfo: unknown, loggedAt: string): SubscriptionUsage | undefined {
	const windows = isObject(rateLimitInfo) && isObject(rateLimitInfo.unifiedWindows) ? rateLimitInfo.unifiedWindows : {};
	const fiveHour = parseWindow(windows.five_hour);
	const sevenDay = parseWindow(windows.seven_day);
	if (!fiveHour && !sevenDay) return undefined;
	return { ...(fiveHour && { fiveHour }), ...(sevenDay && { sevenDay }), updatedAt: loggedAt };
}

export function newestUsage(
	own: SubscriptionUsage | undefined,
	shared: SubscriptionUsage | undefined,
): SubscriptionUsage | undefined {
	if (!own || !shared) return own ?? shared;
	return Date.parse(own.updatedAt) > Date.parse(shared.updatedAt) ? own : shared;
}
