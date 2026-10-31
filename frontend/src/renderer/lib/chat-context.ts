import { isObject } from "./chat-event";

export type ContextCategoryKind = "used" | "deferred" | "buffer" | "free";

export type ContextCategory = { id: string; name: string; tokens: number; kind: ContextCategoryKind };

export type ContextBreakdown = {
	categories: ContextCategory[];
	totalTokens: number;
	maxTokens: number;
	percentage: number;
	autoCompactThreshold?: number;
};

export const CONTEXT_REQUEST_PREFIX = "ctx-";

const KINDS = new Set<string>(["used", "deferred", "buffer", "free"]);

const finite = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value);

function parseCategory(raw: unknown): ContextCategory | undefined {
	if (!isObject(raw) || typeof raw.id !== "string" || typeof raw.name !== "string") return undefined;
	if (!finite(raw.tokens) || typeof raw.kind !== "string" || !KINDS.has(raw.kind)) return undefined;
	return { id: raw.id, name: raw.name, tokens: raw.tokens, kind: raw.kind as ContextCategoryKind };
}

/** Reads a get_context_usage answer. The request is undocumented, so any shape it cannot fully read gives undefined. */
export function parseContextUsage(raw: unknown): ContextBreakdown | undefined {
	if (!isObject(raw) || !Array.isArray(raw.categories)) return undefined;
	const { totalTokens, maxTokens, percentage, autoCompactThreshold } = raw;
	if (!finite(totalTokens) || !finite(maxTokens) || maxTokens <= 0 || !finite(percentage)) return undefined;
	const categories = raw.categories.map(parseCategory);
	if (categories.some((c) => c === undefined)) return undefined;
	const out: ContextBreakdown = { categories: categories as ContextCategory[], totalTokens, maxTokens, percentage };
	if (finite(autoCompactThreshold)) out.autoCompactThreshold = autoCompactThreshold;
	return out;
}
