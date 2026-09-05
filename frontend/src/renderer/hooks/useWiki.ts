/**
 * The Wiki's data layer: the vault's configuration, its file index, one note,
 * and the lifecycle of the single agent running inside it.
 *
 * 🗝 The agent pane is NOT a session, so none of this goes through the
 * workspace/session queries. It has its own handle, its own status, and it
 * never appears on the board.
 */

import { useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { SaveFailure } from "../lib/editor/save-errors";
import { useApiReady } from "./useApiReady";

export type WikiStatus = components["schemas"]["WikiStatusResponse"];
export type WikiFiles = components["schemas"]["WikiFilesResponse"];
export type WikiNote = components["schemas"]["WikiNoteResponse"];
export type WikiNoteSaved = components["schemas"]["WriteWikiNoteResponse"];

export const wikiStatusQueryKey = ["wiki", "status"] as const;
export const wikiFilesQueryKey = ["wiki", "files"] as const;
export const wikiNoteQueryKey = (path: string) => ["wiki", "note", path] as const;

/**
 * How often the pane's liveness is re-read. The agent can exit on its own (the
 * user typed `exit`, the CLI crashed) with nothing to tell us, so the pill
 * would otherwise sit on green over a dead pane.
 */
const STATUS_POLL_MS = 5_000;

/**
 * The vault's configuration and its agent's liveness.
 *
 * `poll` is per-OBSERVER, not per-query: the Wiki page polls because the pane
 * can exit on its own with nothing to tell us, while the sidebar (which is
 * mounted for the whole app's life and only wants to know whether the row
 * exists) does not. They share one cache entry, so with the page open the
 * sidebar's dot rides along on the page's poll for free, and with the page
 * closed nothing polls at all.
 */
export function useWikiStatus({ poll = true }: { poll?: boolean } = {}) {
	// Gated on the daemon's port being known, NOT left to fail against it. The
	// sidebar reads this without polling, so a request spent inside the boot
	// window is a request it never gets back: it would burn its retries on the
	// synthesized "daemon is not ready" 503 and leave the Wiki row hidden for the
	// whole life of the app, with a vault configured and the route answering
	// perfectly. `useApiReady` also flips on a daemon restart, which is what
	// re-runs this query afterwards in place of a poll.
	const ready = useApiReady();
	return useQuery({
		queryKey: wikiStatusQueryKey,
		enabled: ready,
		queryFn: async (): Promise<WikiStatus> => {
			const { data, error } = await apiClient.GET("/api/v1/wiki", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiStatus;
		},
		refetchInterval: poll ? STATUS_POLL_MS : false,
		retry: 1,
	});
}

export function useWikiFiles(enabled: boolean) {
	return useQuery({
		queryKey: wikiFilesQueryKey,
		enabled,
		queryFn: async (): Promise<WikiFiles> => {
			const { data, error } = await apiClient.GET("/api/v1/wiki/files", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiFiles;
		},
		// The agent edits the vault under us, so a stale index is the normal case
		// rather than the exception.
		staleTime: 10_000,
		refetchInterval: 30_000,
	});
}

export function useWikiNote(path: string | null) {
	return useQuery({
		queryKey: wikiNoteQueryKey(path ?? ""),
		enabled: path !== null,
		queryFn: async (): Promise<WikiNote> => {
			const { data, error } = await apiClient.GET("/api/v1/wiki/file", {
				params: { query: { path: path ?? "" } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiNote;
		},
		retry: false,
	});
}

/** Start, switch, restart and stop — the four things the agent pill can do. */
export function useWikiAgentControls() {
	const queryClient = useQueryClient();
	const settle = (status: WikiStatus) => {
		queryClient.setQueryData(wikiStatusQueryKey, status);
		void queryClient.invalidateQueries({ queryKey: wikiFilesQueryKey });
	};

	const start = useMutation({
		mutationFn: async (harness?: string): Promise<WikiStatus> => {
			const { data, error } = await apiClient.POST("/api/v1/wiki/agent", {
				body: { harness: harness ?? "" },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiStatus;
		},
		onSuccess: settle,
	});

	const restart = useMutation({
		mutationFn: async (): Promise<WikiStatus> => {
			const { data, error } = await apiClient.POST("/api/v1/wiki/agent/restart", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiStatus;
		},
		onSuccess: settle,
	});

	const stop = useMutation({
		mutationFn: async (): Promise<WikiStatus> => {
			const { data, error } = await apiClient.DELETE("/api/v1/wiki/agent", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiStatus;
		},
		onSuccess: settle,
	});

	return { start, restart, stop };
}

/** Thrown by the save so a caller can branch on the conflict without reparsing. */
export class WikiSaveError extends Error {
	readonly failure: SaveFailure;

	constructor(failure: SaveFailure) {
		super(`${failure.title} ${failure.detail}`);
		this.name = "WikiSaveError";
		this.failure = failure;
	}
}

type ErrorBody = { code?: unknown; message?: unknown; details?: Record<string, unknown> };

function str(value: unknown): string | undefined {
	return typeof value === "string" && value !== "" ? value : undefined;
}

/**
 * What went wrong with a save, in words the reader can act on.
 *
 * `conflict` is its own kind because it is the only one the reader RESOLVES
 * rather than reads: the vault's own agent writes these files, so a note moving
 * under you is the normal case rather than an edge case. The wording mirrors
 * `lib/editor/save-errors`, which answers the same question for a workspace
 * file; the CODES differ, so the mapping does too.
 */
export function wikiSaveFailure(error: unknown): SaveFailure {
	const body: ErrorBody = typeof error === "object" && error !== null ? (error as ErrorBody) : {};
	const details = body.details ?? {};
	switch (str(body.code)) {
		case "WIKI_NOTE_CONFLICT":
			return {
				kind: "conflict",
				title: "This note changed on disk while you were editing it.",
				detail: "Nothing was written.",
				current: {
					hash: str(details.currentHash),
					size: typeof details.currentSize === "number" ? details.currentSize : undefined,
					modifiedAt: str(details.currentModifiedAt),
				},
			};
		case "WIKI_NOTE_NOT_FOUND":
			return {
				kind: "refused",
				title: "This note is no longer there.",
				detail: "It was moved or deleted since you opened it. The route never creates a note it did not find.",
			};
		case "WIKI_NOTE_TOO_LARGE":
			return { kind: "refused", title: "This note can’t be saved.", detail: "It is larger than the editor can load." };
		default:
			// Never swallow an error we did not anticipate: show what the server
			// actually said rather than a generic sentence that hides it.
			return {
				kind: "refused",
				title: "This note couldn’t be saved.",
				detail: apiErrorMessage(error, "The daemon refused the write."),
			};
	}
}

/**
 * Write a note, preconditioned on the hash it was read with.
 *
 * There is deliberately no way to spell "write regardless": a mismatch is a 409
 * the reader resolves by looking at what the agent wrote. On success the cache
 * is UPDATED rather than invalidated — the content is already what we just
 * wrote, and a refetch would only widen the window in which the vault agent's
 * next write arrives as a surprise instead of as drift the page reports.
 */
export function useSaveWikiNote() {
	const queryClient = useQueryClient();
	return useMutation<WikiNoteSaved, WikiSaveError, { path: string; content: string; baseHash: string | undefined }>({
		mutationFn: async (input) => {
			if (!input.baseHash) {
				// A save with no precondition is not a request the route can serve -
				// it is a client bug, and it must fail here rather than as a puzzling
				// 400 the reader cannot act on.
				throw new WikiSaveError({
					kind: "refused",
					title: "This note can’t be saved.",
					detail: "It was read without a content hash to write against. Close the note and open it again.",
				});
			}
			const { data, error } = await apiClient.PUT("/api/v1/wiki/file", {
				body: { path: input.path, content: input.content, baseHash: input.baseHash },
			});
			if (error) throw new WikiSaveError(wikiSaveFailure(error));
			return data as WikiNoteSaved;
		},
		onSuccess: (result, input) => {
			queryClient.setQueryData(wikiNoteQueryKey(input.path), (current: WikiNote | undefined) =>
				current
					? {
							...current,
							content: input.content,
							contentHash: result.contentHash,
							size: result.size,
							modifiedAt: result.modifiedAt,
						}
					: current,
			);
			// The rail shows each note's age, and this note just aged.
			void queryClient.invalidateQueries({ queryKey: wikiFilesQueryKey });
		},
	});
}

/* -------------------------------------------------------------------------
 * Tasks
 *
 * The unchecked `- [ ]` rows in one configured corner of the vault, and the
 * one write that ticks a row off.
 *
 * 🗝 A tick is preconditioned on the row's EXACT text. The daemon writes only
 * to a line whose full text still equals the `raw` sent back, so a row that
 * changed underneath the reader is refused rather than guessed at — and this
 * layer never invents a `raw` of its own.
 * ---------------------------------------------------------------------- */

export type WikiTasks = components["schemas"]["WikiTasksResponse"];
export type WikiTaskRow = components["schemas"]["WikiTaskRow"];
export type WikiTaskCompleted = components["schemas"]["CompleteWikiTaskResponse"];
export type WikiTasksSettings = components["schemas"]["WikiTasksSettingsResponse"];

export const wikiTasksQueryKey = ["wiki", "tasks"] as const;
export const wikiTasksSettingsQueryKey = ["wiki", "tasks", "settings"] as const;

/**
 * How often the task list is re-read. The vault's own agent edits these notes,
 * so a stale list is the normal case rather than the exception — but the
 * interval is longer than the file rail's because a task list is read
 * deliberately, not glanced at.
 *
 * `enabled` gates the poll on the tab being open: nothing scans the vault
 * while the reader is looking at their notes.
 */
const TASKS_POLL_MS = 60_000;

export function useWikiTasks(enabled: boolean) {
	return useQuery({
		queryKey: wikiTasksQueryKey,
		enabled,
		queryFn: async (): Promise<WikiTasks> => {
			const { data, error } = await apiClient.GET("/api/v1/wiki/tasks", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiTasks;
		},
		staleTime: 15_000,
		refetchInterval: TASKS_POLL_MS,
		retry: 1,
	});
}

export function useWikiTasksSettings(enabled: boolean) {
	return useQuery({
		queryKey: wikiTasksSettingsQueryKey,
		enabled,
		queryFn: async (): Promise<WikiTasksSettings> => {
			const { data, error } = await apiClient.GET("/api/v1/settings/wiki/tasks", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiTasksSettings;
		},
		retry: 1,
	});
}

/**
 * Write the Tasks tab's configuration.
 *
 * 🗝 The settings object goes out WHOLE. Every field on the wire is optional and
 * a missing key reads as its zero value, so a hand-listed body does not merely
 * fail to save a field it forgot - it writes the zero over whatever was stored.
 * That is how `requireCreated` came to untick itself on every save. Listing the
 * fields here again would re-open that hole for the next field added, so the
 * request is the validated object the form handed us, unpicked by nobody.
 */
export function useSaveWikiTasksSettings() {
	const queryClient = useQueryClient();
	return useMutation<WikiTasksSettings, Error, WikiTasksSettings>({
		mutationFn: async (input) => {
			const { data, error } = await apiClient.PUT("/api/v1/settings/wiki/tasks", {
				body: input,
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiTasksSettings;
		},
		onSuccess: (saved) => {
			queryClient.setQueryData(wikiTasksSettingsQueryKey, saved);
			// The config decides what is scanned, so the list is now wrong.
			void queryClient.invalidateQueries({ queryKey: wikiTasksQueryKey });
		},
	});
}

/** Why a tick was refused, in words the reader can act on. */
export type TaskTickFailure = {
	/** `stale` is the reader's to resolve; `refused` is everything else. */
	kind: "stale" | "refused";
	title: string;
	detail: string;
};

/** Thrown by the tick so the tab can branch without reparsing the body. */
export class WikiTaskTickError extends Error {
	readonly failure: TaskTickFailure;

	constructor(failure: TaskTickFailure) {
		super(`${failure.title} ${failure.detail}`);
		this.name = "WikiTaskTickError";
		this.failure = failure;
	}
}

/**
 * What went wrong with a tick.
 *
 * Every one of these means NOTHING WAS WRITTEN, and each says which of the
 * "we did not touch your note" cases happened — the whole point of matching on
 * the row's exact text is that a mismatch can be explained rather than guessed
 * at, so the wording here has to carry that through instead of flattening the
 * three cases into one shrug.
 */
export function taskTickFailure(error: unknown): TaskTickFailure {
	const body: ErrorBody = typeof error === "object" && error !== null ? (error as ErrorBody) : {};
	switch (str(body.code)) {
		case "WIKI_TASK_NOT_FOUND":
			return {
				kind: "stale",
				title: "This row has changed in the note.",
				detail: "Nothing was written. Re-read the vault to see what it says now.",
			};
		case "WIKI_TASK_AMBIGUOUS":
			return {
				kind: "stale",
				title: "This note has more than one row with exactly this text.",
				detail: "Nothing was written, because there is no way to tell which one you meant. Tick it in the note itself.",
			};
		case "WIKI_TASK_ALREADY_DONE":
			return { kind: "stale", title: "This was already ticked off.", detail: "Nothing was written." };
		case "WIKI_NOTE_CONFLICT":
			return {
				kind: "stale",
				title: "The note changed while this was being written.",
				detail: "Nothing was written. Re-read the vault and try again.",
			};
		case "WIKI_NOTE_NOT_FOUND":
			return { kind: "refused", title: "This note is no longer there.", detail: "It was moved or deleted." };
		default:
			// Never swallow an error we did not anticipate: show what the daemon
			// actually said rather than a sentence that hides it.
			return {
				kind: "refused",
				title: "This couldn’t be ticked off.",
				detail: apiErrorMessage(error, "The daemon refused the write."),
			};
	}
}

/**
 * Tick one row off.
 *
 * 🗝 Ticks are SERIALIZED through one promise chain. Two ticks in the same note
 * otherwise race on the note's content hash and the second is refused with a
 * conflict the reader did nothing to cause. They are rare enough — one click
 * each — that a queue costs nothing and removes the whole class of failure.
 *
 * The caller owns the pending/optimistic state, because it is what must
 * survive the list being refetched underneath it.
 */
export function useCompleteWikiTask() {
	const queryClient = useQueryClient();
	// One chain for the whole app. A ref rather than state: it is a lock, and
	// re-rendering because the lock moved would be noise.
	const chain = useRef<Promise<unknown>>(Promise.resolve());

	return useMutation<WikiTaskCompleted, WikiTaskTickError, { path: string; line: number; raw: string }>({
		mutationFn: async (input) => {
			const send = async (): Promise<WikiTaskCompleted> => {
				const { data, error } = await apiClient.POST("/api/v1/wiki/tasks/complete", {
					// `raw` goes out exactly as it came in. Trimming or
					// re-rendering it here would break the one guarantee this
					// whole path rests on.
					body: { path: input.path, line: input.line, raw: input.raw },
				});
				if (error) throw new WikiTaskTickError(taskTickFailure(error));
				return data as WikiTaskCompleted;
			};
			// `then(send, send)` rather than `then(send)`: a tick that failed
			// must not poison the queue for every tick behind it.
			const run = chain.current.then(send, send);
			// The chain itself never rejects, or the next `.then` would skip.
			chain.current = run.catch(() => undefined);
			return run;
		},
		onSuccess: (_result, input) => {
			// The note aged, and the rail shows each note's age.
			void queryClient.invalidateQueries({ queryKey: wikiFilesQueryKey });
			void queryClient.invalidateQueries({ queryKey: wikiNoteQueryKey(input.path) });
		},
	});
}
