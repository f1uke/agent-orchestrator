import { apiErrorMessage } from "../api-client";
import type { BoardResponse } from "./types";

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
