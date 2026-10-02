import type { InfillRequest } from "../src/main/inline-completion/infill";
import type { NextEditRequest } from "../src/main/inline-completion/next-edit";
import type { InlineCompletionStatus } from "../src/main/inline-completion/service";

/**
 * A stand-in for the local model, for the ghost-text specs: everything from the
 * bridge inwards (status store, provider, cache, Monaco's own inline-suggest
 * contribution) is the app's real code; only the model's answer is canned.
 *
 * Answers are keyed on what the line ends with, the way a model would answer
 * them; anything else gets an empty completion (the model's "nothing to add").
 *
 * With `kind: "next-edit"` it plays sweep-next-edit instead: it rewrites the
 * window the way that model does for the two things it is best at - carrying a
 * rename made in the latest change through the rest of the window, and
 * finishing the line being typed (from the same table).
 */
const ANSWERS: [suffix: string, completion: string][] = [
	["let promo", "tionTitle = offersTitle"],
	["self.offersCount", " += 1"],
	["self.", "offersCount += 1"],
];

/** The one identifier the latest change renamed, if that is what it did. */
function renameIn(request: NextEditRequest): { from: string; to: string } | null {
	const latest = request.recent[request.recent.length - 1];
	if (!latest) return null;
	const before = latest.original.match(/[A-Za-z_][A-Za-z0-9_]*/g) ?? [];
	const after = latest.updated.match(/[A-Za-z_][A-Za-z0-9_]*/g) ?? [];
	if (before.length !== after.length) return null;
	const pairs = before.map((w, i) => [w, after[i]] as const).filter(([a, b]) => a !== b);
	if (pairs.length === 0 || pairs.some(([a, b]) => a !== pairs[0][0] || b !== pairs[0][1])) return null;
	return { from: pairs[0][0], to: pairs[0][1] };
}

/** sweep-next-edit, canned: the window as the model would rewrite it. */
export function fakeRewrite(request: NextEditRequest): string {
	const rename = renameIn(request);
	if (rename) return request.current.replace(new RegExp(`\\b${rename.from}\\b`, "g"), rename.to);
	return request.current
		.split("\n")
		.map((line) => {
			const hit = ANSWERS.find(([suffix]) => line.endsWith(suffix));
			return hit ? `${line}${hit[1]}` : line;
		})
		.join("\n");
}

export function installFakeInlineCompletion(options: {
	ready: boolean;
	delayMs?: number;
	kind?: "fim" | "next-edit";
}): void {
	const kind = options.kind ?? "fim";
	const edits: NextEditRequest[] = [];
	(globalThis as { __aoPredictEdits?: typeof edits }).__aoPredictEdits = edits;
	const asked: { prompt: string; nPredict?: number }[] = [];
	const cancelled: string[] = [];
	(globalThis as { __aoPredictAsked?: typeof asked }).__aoPredictAsked = asked;
	(globalThis as { __aoPredictCancelled?: typeof cancelled }).__aoPredictCancelled = cancelled;
	const status: InlineCompletionStatus = {
		unsupported: null,
		enabled: options.ready,
		modelId: "qwen2.5-coder-1.5b",
		server: options.ready ? "ready" : "off",
		activeKind: options.ready ? kind : null,
		serverDetail: options.ready ? "Qwen2.5-Coder 1.5B" : null,
		pid: options.ready ? 4242 : null,
		confirm: null,
		download: null,
		downloadError: null,
		models: [
			{
				id: "qwen2.5-coder-1.5b",
				kind: "fim",
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
			predictEdit: async (_id: string, request: NextEditRequest) => {
				edits.push(request);
				if (options.delayMs) await new Promise((r) => setTimeout(r, options.delayMs));
				return {
					window: fakeRewrite(request),
					promptTokens: 0,
					promptMs: 0,
					predictedTokens: 0,
					predictedMs: 0,
					wallMs: 0,
					early: false,
				};
			},
			cancel: (id: string) => cancelled.push(id),
			onStatus: () => () => undefined,
		},
	};
}
