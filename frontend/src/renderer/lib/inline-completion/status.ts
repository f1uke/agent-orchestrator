import { useSyncExternalStore } from "react";
import type { InlineCompletionStatus } from "../../../main/inline-completion/service";
import { inlineCompletionBridge } from "./bridge";

/**
 * One copy of the main process's inline-completion status per window, shared
 * by the editor's provider (which must answer "is it ready" synchronously, on
 * every keystroke) and by every control that draws it.
 */
let current: InlineCompletionStatus | null = null;
const listeners = new Set<() => void>();
let started = false;
let stopBridge: (() => void) | null = null;

function publish(next: InlineCompletionStatus): void {
	current = next;
	for (const listener of listeners) listener();
}

function start(): void {
	if (started) return;
	started = true;
	stopBridge = inlineCompletionBridge().onStatus(publish);
	void inlineCompletionBridge()
		.getStatus()
		.then((status) => {
			// A broadcast that raced the first read is newer; keep it.
			if (!current) publish(status);
		})
		.catch(() => {});
}

export function subscribeInlineCompletionStatus(listener: () => void): () => void {
	start();
	listeners.add(listener);
	return () => listeners.delete(listener);
}

export function inlineCompletionStatus(): InlineCompletionStatus | null {
	start();
	return current;
}

/** Whether a keystroke may ask the model at all. False until main says otherwise. */
export function isInlineCompletionReady(): boolean {
	return inlineCompletionStatus()?.server === "ready";
}

export function useInlineCompletionStatus(): InlineCompletionStatus | null {
	return useSyncExternalStore(subscribeInlineCompletionStatus, inlineCompletionStatus, inlineCompletionStatus);
}

/** For tests: forget everything, as a fresh window would. */
export function resetInlineCompletionStatusForTests(): void {
	stopBridge?.();
	stopBridge = null;
	current = null;
	started = false;
	listeners.clear();
}
