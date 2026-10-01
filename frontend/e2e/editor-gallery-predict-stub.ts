import type { InfillRequest } from "../src/main/inline-completion/infill";
import type { InlineCompletionStatus } from "../src/main/inline-completion/service";

/**
 * A stand-in for the local model, for the ghost-text specs: everything from the
 * bridge inwards (status store, provider, cache, Monaco's own inline-suggest
 * contribution) is the app's real code; only the model's answer is canned.
 *
 * Answers are keyed on what the line ends with, the way a model would answer
 * them; anything else gets an empty completion (the model's "nothing to add").
 */
const ANSWERS: [suffix: string, completion: string][] = [
	["let promo", "tionTitle = offersTitle"],
	["self.offersCount", " += 1"],
	["self.", "offersCount += 1"],
];

export function installFakeInlineCompletion(options: { ready: boolean; delayMs?: number }): void {
	const asked: { prompt: string; nPredict?: number }[] = [];
	const cancelled: string[] = [];
	(globalThis as { __aoPredictAsked?: typeof asked }).__aoPredictAsked = asked;
	(globalThis as { __aoPredictCancelled?: typeof cancelled }).__aoPredictCancelled = cancelled;
	const status: InlineCompletionStatus = {
		unsupported: null,
		enabled: options.ready,
		modelId: "qwen2.5-coder-1.5b",
		server: options.ready ? "ready" : "off",
		serverDetail: options.ready ? "Qwen2.5-Coder 1.5B" : null,
		pid: options.ready ? 4242 : null,
		confirm: null,
		download: null,
		downloadError: null,
		models: [
			{
				id: "qwen2.5-coder-1.5b",
				label: "Qwen2.5-Coder 1.5B",
				blurb: "Fastest.",
				sizeBytes: 1_646_573_056,
				installed: options.ready,
			},
		],
	};
	const g = globalThis as { ao?: Record<string, unknown> };
	g.ao = {
		...(g.ao ?? {}),
		inlineCompletion: {
			getStatus: async () => status,
			enable: async () => undefined,
			disable: async () => undefined,
			selectModel: async () => undefined,
			confirmDownload: async () => undefined,
			cancelDownload: async () => undefined,
			removeModel: async () => undefined,
			complete: async (_id: string, request: InfillRequest) => {
				asked.push({ prompt: request.prompt, nPredict: request.nPredict });
				if (options.delayMs) await new Promise((r) => setTimeout(r, options.delayMs));
				if (request.nPredict === 0) return null;
				const hit = ANSWERS.find(([suffix]) => request.prompt.endsWith(suffix));
				return {
					content: hit ? hit[1] : "",
					promptTokens: 0,
					promptMs: 0,
					predictedTokens: 0,
					predictedMs: 0,
					wallMs: 0,
				};
			},
			cancel: (id: string) => cancelled.push(id),
			onStatus: () => () => undefined,
		},
	};
}
