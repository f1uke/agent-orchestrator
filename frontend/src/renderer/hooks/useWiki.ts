/**
 * The Wiki's data layer: the vault's configuration, its file index, one note,
 * and the lifecycle of the single agent running inside it.
 *
 * 🗝 The agent pane is NOT a session, so none of this goes through the
 * workspace/session queries. It has its own handle, its own status, and it
 * never appears on the board.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { SaveFailure } from "../lib/editor/save-errors";

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
	return useQuery({
		queryKey: wikiStatusQueryKey,
		queryFn: async (): Promise<WikiStatus> => {
			const { data, error } = await apiClient.GET("/api/v1/wiki", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as WikiStatus;
		},
		refetchInterval: poll ? STATUS_POLL_MS : false,
		// A daemon that is not up yet answers this route with an error; retrying
		// forever would spam it, and the poll above recovers on its own.
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
