import http from "node:http";

/**
 * What the editor sends for one ghost-text prediction. Built in the renderer,
 * where the buffer is; main only adds the sampling settings and speaks HTTP.
 */
export type InfillRequest = {
	/** Lines above the cursor's line, newline-terminated. */
	inputPrefix: string;
	/** The cursor's line up to the cursor. Sent as `prompt`, after the FIM middle token. */
	prompt: string;
	/** The rest of the cursor's line, then the lines below it. */
	inputSuffix: string;
	/** Chunks from recently opened files, so project symbols can be predicted. */
	inputExtra: { filename: string; text: string }[];
	/** Indentation of the cursor's line: generation stops at a line indented less. */
	nIndent: number;
	/**
	 * Zero for a cache warm-up: the server processes the prompt (and keeps it in
	 * its KV cache) without generating, so the next real request starts warm.
	 */
	nPredict?: number;
};

export type InfillResult = {
	content: string;
	promptTokens: number;
	promptMs: number;
	predictedTokens: number;
	predictedMs: number;
	wallMs: number;
};

/**
 * The sampling settings are llama.vim's, which is what llama-server's `/infill`
 * was built against: `top_k`/`top_p` then the `infill` sampler, which folds
 * end-of-generation into the probability mass so a confident "nothing to add"
 * comes back as an empty completion rather than filler.
 *
 * `t_max_predict_ms` cuts generation once a newline has been produced and the
 * budget is spent - so a multi-line guess is bounded in time, while the first
 * line (the part a person reads first) is always finished.
 */
export const N_PREDICT = 64;
export const T_MAX_PREDICT_MS = 250;

export function infillBody(request: InfillRequest): Record<string, unknown> {
	return {
		input_prefix: request.inputPrefix,
		input_suffix: request.inputSuffix,
		input_extra: request.inputExtra,
		prompt: request.prompt,
		n_predict: request.nPredict ?? N_PREDICT,
		n_indent: request.nIndent,
		top_k: 40,
		top_p: 0.99,
		samplers: ["top_k", "top_p", "infill"],
		stream: false,
		cache_prompt: true,
		t_max_predict_ms: T_MAX_PREDICT_MS,
	};
}

/**
 * POST /infill over the server's Unix socket. Aborting destroys the request;
 * note that llama-server only notices a departed client when it next polls the
 * connection, so an abort frees its slot late - which is why request-lane.ts
 * rarely aborts at all.
 */
export function postInfill(socketPath: string, request: InfillRequest, signal: AbortSignal): Promise<InfillResult> {
	return new Promise((resolve, reject) => {
		if (signal.aborted) {
			reject(signal.reason);
			return;
		}
		const data = JSON.stringify(infillBody(request));
		const started = performance.now();
		const req = http.request(
			{
				socketPath,
				path: "/infill",
				method: "POST",
				headers: { "content-type": "application/json", "content-length": Buffer.byteLength(data) },
			},
			(res) => {
				const chunks: Buffer[] = [];
				res.on("data", (d: Buffer) => chunks.push(d));
				res.on("end", () => {
					signal.removeEventListener("abort", onAbort);
					const text = Buffer.concat(chunks).toString("utf8");
					if (res.statusCode !== 200) {
						reject(new Error(`llama-server answered HTTP ${res.statusCode}: ${text.slice(0, 200)}`));
						return;
					}
					try {
						const body = JSON.parse(text) as {
							content?: unknown;
							timings?: { prompt_n?: number; prompt_ms?: number; predicted_n?: number; predicted_ms?: number };
						};
						resolve({
							content: typeof body.content === "string" ? body.content : "",
							promptTokens: body.timings?.prompt_n ?? 0,
							promptMs: body.timings?.prompt_ms ?? 0,
							predictedTokens: body.timings?.predicted_n ?? 0,
							predictedMs: body.timings?.predicted_ms ?? 0,
							wallMs: performance.now() - started,
						});
					} catch {
						reject(new Error(`llama-server answered something that is not JSON: ${text.slice(0, 200)}`));
					}
				});
				res.on("error", reject);
			},
		);
		const onAbort = () => req.destroy(signal.reason instanceof Error ? signal.reason : new Error("aborted"));
		signal.addEventListener("abort", onAbort, { once: true });
		req.on("error", (err) => {
			signal.removeEventListener("abort", onAbort);
			reject(signal.aborted ? signal.reason : err);
		});
		req.end(data);
	});
}
