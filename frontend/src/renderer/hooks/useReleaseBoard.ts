import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage, getApiBaseUrl, hasTrustedApiBaseUrl } from "../lib/api-client";
import { boardFetcher } from "../lib/releases/boardFetch";
import { demoBoard, demoResponse, failedSourceBoard } from "../lib/releases/fixture";
import type { Board, BoardResponse, HitId } from "../lib/releases/types";
import { useApiReady } from "./useApiReady";

export const releaseBoardKey = ["release-trains", "board"] as const;
const POLL_MS = 15_000;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/** Playwright sets the variant before load; a mutation edits the board kept here. */
type PreviewSeam = { __aoReleaseBoard?: BoardResponse; __aoReleaseBoardVariant?: "failed-source" };

function previewBoard(): BoardResponse {
	const seam = globalThis as PreviewSeam;
	const today = new Date().toISOString().slice(0, 10);
	seam.__aoReleaseBoard ??= demoResponse(
		seam.__aoReleaseBoardVariant === "failed-source" ? failedSourceBoard(today) : demoBoard(today),
	);
	return structuredClone(seam.__aoReleaseBoard);
}

function editPreview(edit: (b: Board) => void): void {
	const b = (globalThis as PreviewSeam).__aoReleaseBoard?.board;
	if (b) edit(b);
}

const fetchLiveBoard = boardFetcher((ifNoneMatch) => {
	const headers: Record<string, string> = { Accept: "application/json" };
	if (ifNoneMatch) headers["If-None-Match"] = ifNoneMatch;
	return fetch(`${getApiBaseUrl()}/api/v1/release-trains/board`, { headers, cache: "no-store" });
});

/** The page polls; the sidebar passes poll: false and rides on the page's cache entry. */
export function useReleaseBoard({ poll = true }: { poll?: boolean } = {}) {
	const ready = useApiReady();
	return useQuery({
		queryKey: releaseBoardKey,
		enabled: usePreviewData || ready,
		queryFn: async () => (usePreviewData || !hasTrustedApiBaseUrl() ? previewBoard() : fetchLiveBoard()),
		refetchInterval: poll ? POLL_MS : false,
		refetchIntervalInBackground: false,
		placeholderData: keepPreviousData,
		retry: 1,
	});
}

function useBoardMutation<V>(run: (v: V) => Promise<void>, optimistic: (b: Board, v: V) => void) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async (v: V) => {
			if (usePreviewData) {
				editPreview((b) => optimistic(b, v));
				return;
			}
			await run(v);
		},
		onMutate: async (v) => {
			await qc.cancelQueries({ queryKey: releaseBoardKey });
			const before = qc.getQueryData<BoardResponse>(releaseBoardKey);
			if (before?.board) {
				const next = structuredClone(before);
				if (next.board) optimistic(next.board, v);
				qc.setQueryData(releaseBoardKey, next);
			}
			return { before };
		},
		onError: (_e, _v, ctx) => {
			if (ctx?.before) qc.setQueryData(releaseBoardKey, ctx.before);
		},
		onSettled: () => qc.invalidateQueries({ queryKey: releaseBoardKey }),
	});
}

async function must(p: Promise<{ error?: unknown }>): Promise<void> {
	const { error } = await p;
	if (error) throw new Error(apiErrorMessage(error));
}

export function useSetRule() {
	return useBoardMutation<{ id: string; on: boolean }>(
		({ id, on }) =>
			must(
				apiClient.PUT("/api/v1/release-trains/rules/{ruleId}", {
					params: { path: { ruleId: id } },
					body: { enabled: on },
				}),
			),
		(b, { id, on }) => {
			for (const r of b.catalogue) if (r.id === id) r.on = on;
			for (const r of b.alerts.rules) if (r.rule === id) r.on = on;
		},
	);
}

function moveHit(b: Board, id: HitId, bucket: "badge" | "hidden") {
	for (const h of b.alerts.hits) if (h.id === id) h.bucket = bucket;
	const badge = b.alerts.hits.filter((h) => h.bucket === "badge");
	b.alerts.badge = { ...b.alerts.badge, count: badge.length };
}

export function useHideHit() {
	return useBoardMutation<HitId>(
		(id) => must(apiClient.PUT("/api/v1/release-trains/hides/{hitId}", { params: { path: { hitId: id } }, body: {} })),
		(b, id) => moveHit(b, id, "hidden"),
	);
}

export function useUnhideHit() {
	return useBoardMutation<HitId>(
		(id) => must(apiClient.DELETE("/api/v1/release-trains/hides/{hitId}", { params: { path: { hitId: id } } })),
		(b, id) => moveHit(b, id, "badge"),
	);
}

export function useClearStaleHides() {
	return useBoardMutation<void>(
		() => must(apiClient.DELETE("/api/v1/release-trains/hides", { params: { query: { stale: true } } })),
		(b) => {
			b.alerts.staleHides = [];
		},
	);
}

export function useRefreshSources() {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: async () => {
			if (!usePreviewData) await must(apiClient.POST("/api/v1/release-trains/refresh", { body: {} }));
		},
		onSettled: () => qc.invalidateQueries({ queryKey: releaseBoardKey }),
	});
}
