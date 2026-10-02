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

/** What the running model is asked: ghost text at the cursor (`fim`) or a rewrite (`next-edit`). */
export function inlineCompletionKind(): InlineCompletionStatus["activeKind"] {
	const status = inlineCompletionStatus();
	return status?.server === "ready" ? status.activeKind : null;
}

/** Whether the running model also answers fill-in-the-middle requests (every model so far does). */
export function inlineCompletionInfill(): boolean {
	const status = inlineCompletionStatus();
	return status?.server === "ready" && status.activeInfill;
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
