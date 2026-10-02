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
 * Two kinds of request ARE aborted, each named by a predicate:
 * - `isWarmup`: a cache warm-up generates nothing anyone reads and can take
 *   seconds on a large ring - it never takes a prediction's place, and is
 *   aborted when cancelled (the server keeps whatever prompt it had processed).
 * - `abortsWhenSuperseded`: a STREAMED request, which the server notices is gone
 *   at its very next token, so aborting it frees the slot at once instead of
 *   late. A next-edit rewrite is one, and its answer is tied to the exact buffer
 *   it was asked about, so finishing it for a superseded state buys nothing.
 */
export type RequestLane<Req, Res> = {
	submit(id: string, request: Req): Promise<Res | null>;
	cancel(id: string): void;
	/** Drop everything: the waiting request answers null, the one on the wire is aborted. */
	clear(): void;
};

type Job<Req, Res> = {
	id: string;
	request: Req;
	resolve: (result: Res | null) => void;
	abort?: AbortController;
};

export type RequestLaneOptions<Req> = {
	timeoutMs: number;
	onError?: (err: unknown) => void;
	isWarmup?: (request: Req) => boolean;
	abortsWhenSuperseded?: (request: Req) => boolean;
};

export function createRequestLane<Req, Res>(
	send: (request: Req, signal: AbortSignal) => Promise<Res>,
	options: RequestLaneOptions<Req>,
): RequestLane<Req, Res> {
	const isWarmup = options.isWarmup ?? (() => false);
	const abortsWhenSuperseded = options.abortsWhenSuperseded ?? (() => false);
	let wire: Job<Req, Res> | null = null;
	let waiting: Job<Req, Res> | null = null;

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
				if (isWarmup(request) && waiting && !isWarmup(waiting.request)) {
					resolve(null);
					return;
				}
				waiting?.resolve(null);
				waiting = { id, request, resolve };
				if (wire && abortsWhenSuperseded(wire.request)) wire.abort?.abort();
				pump();
			});
		},
		cancel(id) {
			if (waiting?.id === id) {
				waiting.resolve(null);
				waiting = null;
				return;
			}
			if (wire?.id === id && (isWarmup(wire.request) || abortsWhenSuperseded(wire.request))) wire.abort?.abort();
		},
		clear() {
			waiting?.resolve(null);
			waiting = null;
			wire?.abort?.abort();
		},
	};
}
