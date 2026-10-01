import type { InfillRequest, InfillResult } from "./infill";

/**
 * How predictions reach the server: ONE on the wire, ONE waiting, the newest
 * wins the waiting place, and a request on the wire is NOT aborted when the
 * editor moves on.
 *
 * 🗝 The obvious policy - abort the in-flight request on every keystroke - is
 * the worst one, and measurably so. llama-server notices a client that went away
 * only when it next polls the connection (about once a second), so an aborted
 * request keeps the single slot busy and every keystroke's request queues behind
 * the ones it superseded: typing a word at 90-160 ms per key took the last
 * keystroke's ghost text to 0.8-2.5 s on the 1.5B model. Letting the one on the
 * wire finish costs at most one request (~150-350 ms), and its answer is not
 * wasted: the editor caches it, and the typed-through lookup turns it into the
 * ghost text for the characters typed since.
 *
 * The one exception is a cache warm-up (`nPredict: 0`): it generates nothing
 * anyone reads and can take seconds on a large ring, so it IS aborted - the
 * server keeps whatever prompt it had already processed.
 */
export type RequestLane = {
	submit(id: string, request: InfillRequest): Promise<InfillResult | null>;
	cancel(id: string): void;
	/** Drop everything: the waiting request answers null, the one on the wire is aborted. */
	clear(): void;
};

type Job = {
	id: string;
	request: InfillRequest;
	resolve: (result: InfillResult | null) => void;
	abort?: AbortController;
};

export function createRequestLane(
	send: (request: InfillRequest, signal: AbortSignal) => Promise<InfillResult>,
	options: { timeoutMs: number; onError?: (err: unknown) => void },
): RequestLane {
	let wire: Job | null = null;
	let waiting: Job | null = null;

	const pump = () => {
		if (wire || !waiting) return;
		const job = waiting;
		waiting = null;
		wire = job;
		const ac = new AbortController();
		job.abort = ac;
		const timer = setTimeout(() => ac.abort(new Error("timed out")), options.timeoutMs);
		send(job.request, ac.signal)
			.then(
				(result) => job.resolve(result),
				(err) => {
					if (!ac.signal.aborted) options.onError?.(err);
					job.resolve(null);
				},
			)
			.finally(() => {
				clearTimeout(timer);
				if (wire === job) wire = null;
				pump();
			});
	};

	return {
		submit(id, request) {
			return new Promise((resolve) => {
				// A warm-up never takes the place of a prediction someone is waiting
				// for; it is only ever worth sending into an idle lane.
				if (request.nPredict === 0 && waiting && waiting.request.nPredict !== 0) {
					resolve(null);
					return;
				}
				waiting?.resolve(null);
				waiting = { id, request, resolve };
				pump();
			});
		},
		cancel(id) {
			if (waiting?.id === id) {
				waiting.resolve(null);
				waiting = null;
				return;
			}
			if (wire?.id === id && wire.request.nPredict === 0) wire.abort?.abort();
		},
		clear() {
			waiting?.resolve(null);
			waiting = null;
			wire?.abort?.abort();
		},
	};
}
