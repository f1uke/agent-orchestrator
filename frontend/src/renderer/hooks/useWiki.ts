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

export type WikiStatus = components["schemas"]["WikiStatusResponse"];
export type WikiFiles = components["schemas"]["WikiFilesResponse"];
export type WikiNote = components["schemas"]["WikiNoteResponse"];

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
