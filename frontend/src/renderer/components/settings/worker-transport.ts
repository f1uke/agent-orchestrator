import type { components } from "../../../api/schema";

export type WorkerTransport = components["schemas"]["WorkerTransportSettingsResponse"]["workerTransport"];

export const WORKER_TRANSPORT_OPTIONS: { value: WorkerTransport; label: string }[] = [
	{ value: "chat", label: "Chat" },
	{ value: "terminal", label: "Terminal" },
];

export function workerTransportLabel(value: WorkerTransport): string {
	return WORKER_TRANSPORT_OPTIONS.find((o) => o.value === value)?.label ?? value;
}

export const BUILT_IN_WORKER_TRANSPORT: WorkerTransport = "chat";
