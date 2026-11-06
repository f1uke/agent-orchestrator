import type { components } from "../../../api/schema";

// Workers and orchestrators share one wire enum. The intersection keeps this type
// honest if either schema ever grows a value the other lacks.
export type Transport = components["schemas"]["WorkerTransportSettingsResponse"]["workerTransport"] &
	components["schemas"]["OrchestratorTransportSettingsResponse"]["orchestratorTransport"];

export const TRANSPORT_OPTIONS: { value: Transport; label: string }[] = [
	{ value: "chat", label: "Chat" },
	{ value: "terminal", label: "Terminal" },
];

export function transportLabel(value: Transport): string {
	return TRANSPORT_OPTIONS.find((o) => o.value === value)?.label ?? value;
}

export const BUILT_IN_WORKER_TRANSPORT: Transport = "chat";
export const BUILT_IN_ORCHESTRATOR_TRANSPORT: Transport = "chat";
