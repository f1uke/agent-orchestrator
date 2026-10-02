import http from "node:http";

/**
 * Next-edit prediction with sweep-next-edit: given what the person just changed,
 * the model REWRITES the lines around the cursor the way it expects them to end
 * up. Whatever differs from the buffer is the suggestion - and it can be
 * anywhere in that window, not only at the cursor.
 *
 * The prompt is Sweep's training format, verbatim (model card `run_model.py`,
 * the "Our Format" section of blog.sweep.dev/posts/oss-next-edit, and Zed's
 * `sweep_prompt.rs`): each recent change in its own `<|file_sep|>{path}.diff`
 * block as `original:` / `updated:`, then the window as it was before the most
 * recent change, the window now, and an open `updated/` block the model fills.
 * The window (built in the renderer, `lib/inline-completion/next-edit.ts`) is
 * a FIXED 10 lines above and below the cursor: Sweep found the model trains
 * better on a fixed size than on syntax-aware boundaries, so any other size is
 * out of distribution.
 */

export type NextEditChange = {
	/** The file the change was made in, as the model sees paths (relative). */
	path: string;
	original: string;
	updated: string;
};

/** Built in the renderer, where the buffer and its history are. */
export type NextEditRequest = {
	/** The file being edited, relative. */
	path: string;
	/** Oldest first. Sweep's reference client sends at most three. */
	recent: NextEditChange[];
	/** The window as it was before the most recent change in this file. */
	original: string;
	/** The window now: 10 lines above the cursor's line, it, and 10 below. */
	current: string;
};

export type NextEditResult = {
	/** The rewritten window. Equal to `current` when the model expects no change. */
	window: string;
	promptTokens: number;
	promptMs: number;
	predictedTokens: number;
	predictedMs: number;
	wallMs: number;
	/** The rewrite was cut short once it had rejoined the buffer (see `rejoin`). */
	early: boolean;
};

/**
 * A 21-line window is ~250 tokens, but one of long lines can be several times
 * that; this is room for the longest plausible window plus an edit that adds
 * lines. A rewrite cut off by it is DISCARDED (see `postNextEdit`): read as a
 * window, a truncated rewrite would propose deleting everything it did not
 * reach.
 */
export const NEXT_EDIT_N_PREDICT = 1024;

/**
 * Anything that parses as one of the model's control tokens. llama-server
 * tokenizes a raw prompt WITH special tokens, so a buffer that contains
 * `<|file_sep|>` (this very file does) would forge a block boundary; such a
 * window is not asked about at all.
 */
const CONTROL_TOKEN = /<\|[a-z_]+\|>|<\/s>/;

function block(path: string, body: string): string {
	return `<|file_sep|>${path}\n${body}${body.endsWith("\n") || body === "" ? "" : "\n"}`;
}

/** The raw prompt, or null when the request cannot be expressed safely. */
export function nextEditPrompt(request: NextEditRequest): string | null {
	const fields = [request.path, request.original, request.current];
	for (const change of request.recent) fields.push(change.path, change.original, change.updated);
	if (fields.some((f) => CONTROL_TOKEN.test(f)) || request.path.includes("\n")) return null;
	let prompt = "";
	for (const change of request.recent) {
		prompt += block(`${change.path}.diff`, `original:\n${change.original}\nupdated:\n${change.updated}`);
	}
	prompt += block(`original/${request.path}`, request.original);
	prompt += block(`current/${request.path}`, request.current);
	prompt += `<|file_sep|>updated/${request.path}\n`;
	return prompt;
}

/** Lines that must match the buffer again, after a change, before the rest is taken as unchanged. */
export const REJOIN_LINES = 2;

/**
 * 🗝 The rewrite can stop as soon as it has made its change and come back to the
 * buffer: once the complete lines generated so far differ from the window and
 * their last `REJOIN_LINES` (not all blank) match consecutive lines of the
 * window again, the rest of the window is spliced in unchanged. A typical
 * suggestion is one or two changed lines near the cursor, so this skips most
 * of the ~250 tokens of copying a full rewrite costs. It also means at most
 * ONE change per suggestion - the next one comes from the next request, after
 * this one is accepted, which is how next-edit suggestions are presented anyway.
 *
 * Returns the full window, or null to keep generating.
 */
export function rejoin(generated: string, current: string): string | null {
	const gen = generated.split("\n");
	gen.pop(); // the line still being generated
	const cur = current.split("\n");
	let same = 0;
	while (same < gen.length && same < cur.length && gen[same] === cur[same]) same++;
	if (same === gen.length || gen.length - same < REJOIN_LINES) return null;
	const tail = gen.slice(-REJOIN_LINES);
	if (tail.every((l) => l.trim() === "")) return null;
	for (let j = same; j + REJOIN_LINES <= cur.length; j++) {
		if (tail.every((l, k) => l === cur[j + k])) return [...gen, ...cur.slice(j + REJOIN_LINES)].join("\n");
	}
	return null;
}

/**
 * The body for llama-server's raw `/completion`: greedy, as Sweep runs it, and
 * streamed, so the rewrite can be cut short (`rejoin`) and so an abandoned one
 * stops at the next token - unlike a non-streamed request, which the server
 * only notices is gone when it next polls the connection.
 */
export function nextEditBody(prompt: string): Record<string, unknown> {
	return {
		prompt,
		n_predict: NEXT_EDIT_N_PREDICT,
		temperature: 0,
		stop: ["<|file_sep|>", "</s>"],
		stream: true,
		cache_prompt: true,
	};
}

/** POST /completion over the server's Unix socket, streamed. Null when the request cannot be made. */
export function postNextEdit(
	socketPath: string,
	request: NextEditRequest,
	signal: AbortSignal,
	nPredict = NEXT_EDIT_N_PREDICT,
): Promise<NextEditResult | null> {
	const prompt = nextEditPrompt(request);
	if (prompt === null) return Promise.resolve(null);
	return new Promise((resolve, reject) => {
		if (signal.aborted) {
			reject(signal.reason);
			return;
		}
		const data = JSON.stringify({ ...nextEditBody(prompt), n_predict: nPredict });
		const started = performance.now();
		let text = "";
		let timings: { prompt_n?: number; prompt_ms?: number; predicted_n?: number; predicted_ms?: number } = {};
		let settled = false;
		let stopType: unknown = null;
		const finish = (window: string | null, early: boolean) => {
			if (settled) return;
			settled = true;
			signal.removeEventListener("abort", onAbort);
			if (window === null) {
				resolve(null);
				return;
			}
			resolve({
				window,
				promptTokens: timings.prompt_n ?? 0,
				promptMs: timings.prompt_ms ?? 0,
				predictedTokens: timings.predicted_n ?? 0,
				predictedMs: timings.predicted_ms ?? 0,
				wallMs: performance.now() - started,
				early,
			});
		};
		const fail = (err: unknown) => {
			if (settled) return;
			settled = true;
			signal.removeEventListener("abort", onAbort);
			reject(err);
		};
		const req = http.request(
			{
				socketPath,
				// One connection per request: llama-server closes a streamed
				// response's connection, and a pooled socket it already closed
				// fails the next request with "socket hang up".
				agent: false,
				path: "/completion",
				method: "POST",
				headers: { "content-type": "application/json", "content-length": Buffer.byteLength(data) },
			},
			(res) => {
				if (res.statusCode !== 200) {
					const chunks: Buffer[] = [];
					res.on("data", (d: Buffer) => chunks.push(d));
					res.on("end", () =>
						fail(
							new Error(
								`llama-server answered HTTP ${res.statusCode}: ${Buffer.concat(chunks).toString("utf8").slice(0, 200)}`,
							),
						),
					);
					return;
				}
				let buffered = "";
				res.setEncoding("utf8");
				res.on("data", (chunk: string) => {
					buffered += chunk;
					for (let nl = buffered.indexOf("\n"); nl >= 0; nl = buffered.indexOf("\n")) {
						const line = buffered.slice(0, nl).trim();
						buffered = buffered.slice(nl + 1);
						if (!line.startsWith("data:")) continue;
						let event: { content?: unknown; stop?: unknown; stop_type?: unknown; timings?: typeof timings };
						try {
							event = JSON.parse(line.slice(5));
						} catch {
							continue;
						}
						if (event.timings) timings = event.timings;
						if (event.stop_type !== undefined) stopType = event.stop_type;
						if (typeof event.content === "string" && event.content !== "") {
							text += event.content;
							if (event.content.includes("\n")) {
								const whole = rejoin(text, request.current);
								if (whole !== null) {
									finish(whole, true);
									req.destroy();
									return;
								}
							}
						}
					}
				});
				// Ran out of tokens before the window was rewritten: no answer at all.
				res.on("end", () => finish(stopType === "limit" ? null : text.replace(/\n$/, ""), false));
				res.on("error", fail);
			},
		);
		const onAbort = () => req.destroy(signal.reason instanceof Error ? signal.reason : new Error("aborted"));
		signal.addEventListener("abort", onAbort, { once: true });
		req.on("error", (err) => fail(signal.aborted ? signal.reason : err));
		req.end(data);
	});
}
