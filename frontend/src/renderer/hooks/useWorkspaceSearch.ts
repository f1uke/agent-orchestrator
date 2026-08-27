import { useEffect, useRef, useState } from "react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { WorkspaceSearchResponse } from "../lib/editor/search-results";
import { mockWorkspaceSearch } from "../lib/mock-data";

/** The three toggles and the two glob boxes, as the panel holds them. */
export type SearchOptions = {
	matchCase: boolean;
	wholeWord: boolean;
	regex: boolean;
	include: string;
	exclude: string;
};

export type WorkspaceSearchState = {
	/** The last result that arrived, kept while the next one is in flight. */
	data?: WorkspaceSearchResponse;
	error?: string;
	/** A request is out. The previous results stay on screen underneath it. */
	searching: boolean;
	/** The query `data` answers — not necessarily the one in the box. */
	answered: string;
};

/**
 * How long the box rests before a search is sent.
 *
 * Not tuned for the network — the daemon is on loopback — but for the CPU. A
 * full search of a real 6,940-file project costs 0.79 CPU-seconds across the
 * scanning pool, so firing one per keystroke would spend several CPU-seconds
 * answering prefixes nobody typed on purpose. 150ms is under the gap between
 * characters of ordinary typing and short enough that a deliberate pause reads
 * as instant.
 */
const DEBOUNCE_MS = 150;

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/**
 * ⌘⇧F: every line in the session's workspace whose CONTENT matches.
 *
 * 🗝 THIS HOOK CANCELS, and that is a deliberate departure from the rule
 * recorded twice in this project's store. #258 and #259 both concluded "never
 * send `$/cancelRequest`" — about LANGUAGE SERVERS, where cancelling discards an
 * in-progress type-check the next request must redo. A content search holds no
 * such state: killing a superseded one frees its CPU outright. Measured against
 * the real 6,940-file tree, a full search costs 792 ms of CPU across the pool
 * and one aborted at 10 ms costs 1 ms. So every superseded query is aborted, and
 * the abort travels the whole way: `AbortController` → `fetch` → net/http
 * cancels the handler's context → the Go scan stops reading files.
 *
 * Hand-rolled rather than a `useQuery`, for one reason worth the lines: this
 * hook's contract is that the previous request is ABORTED the moment the query
 * changes. React Query's cancellation is tied to a query becoming unused rather
 * than to a key changing, so relying on it would make the measured behaviour
 * above an accident of the library's internals rather than something this file
 * guarantees. Results are also not cached on purpose — a worktree with agents
 * writing in it makes a cached search a stale one.
 */
export function useWorkspaceSearch(
	sessionId: string | undefined,
	query: string,
	options: SearchOptions,
	enabled: boolean,
): WorkspaceSearchState {
	const [state, setState] = useState<WorkspaceSearchState>({ searching: false, answered: "" });
	// The options object is rebuilt every render by its owner; the request only
	// cares about the values, so the effect keys on a string of them.
	const { matchCase, wholeWord, regex, include, exclude } = options;
	const trimmed = query.trim();
	// Held across renders so a cleanup can abort the request the last effect
	// started, even when React runs cleanups out of order under StrictMode.
	const inFlight = useRef<AbortController | null>(null);

	useEffect(() => {
		if (!enabled || !sessionId) {
			inFlight.current?.abort();
			inFlight.current = null;
			setState({ searching: false, answered: "" });
			return undefined;
		}
		// The previous results stay visible underneath: blanking the list between
		// two keystrokes is a flicker, not information.
		setState((prev) => ({ ...prev, searching: true, error: undefined }));

		// An EMPTY box is asked immediately and costs nothing: the service answers
		// it before it indexes anything. It is asked at all so that a worktree
		// that is gone from disk says so on arrival rather than only once someone
		// has typed into a search that could never have worked.
		const timer = window.setTimeout(
			() => {
				const controller = new AbortController();
				inFlight.current = controller;
				void runSearch(sessionId, trimmed, { matchCase, wholeWord, regex, include, exclude }, controller.signal)
					.then((data) => {
						if (controller.signal.aborted) return;
						setState({ data, searching: false, answered: trimmed });
					})
					.catch((err: unknown) => {
						// An abort is this hook doing its job, not a failure to report.
						if (controller.signal.aborted) return;
						setState((prev) => ({
							...prev,
							searching: false,
							error: apiErrorMessage(err, "Unable to search this workspace"),
						}));
					});
			},
			trimmed === "" ? 0 : DEBOUNCE_MS,
		);

		return () => {
			window.clearTimeout(timer);
			inFlight.current?.abort();
			inFlight.current = null;
		};
	}, [sessionId, trimmed, matchCase, wholeWord, regex, include, exclude, enabled]);

	return state;
}

async function runSearch(
	sessionId: string,
	q: string,
	options: SearchOptions,
	signal: AbortSignal,
): Promise<WorkspaceSearchResponse> {
	if (usePreviewData) return mockWorkspaceSearch(sessionId, q, options);
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/search", {
		params: {
			path: { sessionId },
			query: {
				q,
				matchCase: options.matchCase,
				wholeWord: options.wholeWord,
				regex: options.regex,
				include: options.include,
				exclude: options.exclude,
			},
		},
		signal,
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to search this workspace"));
	return data as WorkspaceSearchResponse;
}
