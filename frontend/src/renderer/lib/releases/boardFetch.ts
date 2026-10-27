import { apiErrorMessage } from "../api-client";
import type { BoardResponse } from "./types";

/** The Board's path for a person's Mine; no person is the monitor's own. */
export function boardPath(person: string | undefined): string {
	return `/api/v1/release-trains/board${person ? `?person=${encodeURIComponent(person)}` : ""}`;
}

const KEPT_FETCHERS = 4;

/** One ETag memory per person, since each person's Board has its own ETag; the least recently asked are dropped. */
export function personFetchers(
	makeGet: (person: string) => BoardGet,
): (person: string | undefined) => Promise<BoardResponse> {
	const fetchers = new Map<string, () => Promise<BoardResponse>>();
	return (person) => {
		const key = person ?? "";
		let fetcher = fetchers.get(key);
		if (fetcher) fetchers.delete(key);
		else fetcher = boardFetcher(makeGet(key));
		fetchers.set(key, fetcher);
		if (fetchers.size > KEPT_FETCHERS) fetchers.delete(fetchers.keys().next().value as string);
		return fetcher();
	};
}

export type BoardGet = (ifNoneMatch: string | null) => Promise<Response>;

/**
 * A Board fetch that remembers the last body and its ETag, so a 304 costs
 * nothing to parse. The daemon answers 304 while no row, setting or budget moved.
 */
export function boardFetcher(get: BoardGet): () => Promise<BoardResponse> {
	let last: { etag: string; body: BoardResponse } | null = null;
	const full = async (res: Response): Promise<BoardResponse> => {
		if (!res.ok) {
			let err: unknown = await res.text();
			try {
				err = JSON.parse(err as string);
			} catch {}
			throw new Error(apiErrorMessage(err, `release board ${res.status}`));
		}
		const body = (await res.json()) as BoardResponse;
		const etag = res.headers.get("ETag");
		last = etag ? { etag, body } : null;
		return body;
	};
	return async () => {
		const res = await get(last?.etag ?? null);
		if (res.status !== 304) return full(res);
		if (last) return last.body;
		return full(await get(null));
	};
}
