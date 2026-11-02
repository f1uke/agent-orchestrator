/** A failed turn in plain words: a short label, an optional one-line detail, and the raw text when it is too noisy to show. */
export type TurnError = { label: string; detail?: string; raw?: string };

const SUBTYPE_LABEL: Record<string, string> = {
	error_during_execution: "Interrupted",
	error_max_turns: "Turn limit reached",
	error_max_budget_usd: "Budget limit reached",
};

const NESTED_TYPE_LABEL: Record<string, string> = {
	rate_limit_error: "Rate limited",
	overloaded_error: "API overloaded",
	authentication_error: "Not signed in",
	permission_error: "Permission denied",
};

const API_ERROR_PREFIX = /^API Error:\s*/;
const MAX_DETAIL = 200;

/** Reads the text claude puts in a failed result (or its synthetic error message) into a label a person can scan. */
export function describeTurnError(subtype: string, text: string): TurnError {
	const trimmed = text.trim();
	const nested = nestedError(trimmed);
	const label = withReset(classify(subtype, trimmed, nested?.type), trimmed);
	const message = nested?.message || trimmed.replace(API_ERROR_PREFIX, "");
	if (message === "") return { label };
	const firstLine = message.split("\n", 1)[0].trim();
	const detail = firstLine.length > MAX_DETAIL ? `${firstLine.slice(0, MAX_DETAIL - 1)}…` : firstLine;
	return nested || detail !== message ? { label, detail, raw: text } : { label, detail };
}

function classify(subtype: string, text: string, nestedType: string | undefined): string {
	const bySubtype = SUBTYPE_LABEL[subtype];
	if (bySubtype) return bySubtype;
	if (/spend limit/i.test(text)) return "Spend limit reached";
	const byType = nestedType && NESTED_TYPE_LABEL[nestedType];
	if (byType) return byType;
	if (/rate limit|usage limit/i.test(text)) return "Rate limited";
	if (API_ERROR_PREFIX.test(text)) {
		const status = apiStatus(text);
		return status ? `API error ${status}` : "API error";
	}
	return "Turn failed";
}

// A gateway reports its own status first and the upstream one in brackets: "API Error: 503 [model] [429]: {...}".
function apiStatus(text: string): string | undefined {
	const head = text.replace(API_ERROR_PREFIX, "").split(/[:{]/, 1)[0];
	const codes = head.match(/\b\d{3}\b/g);
	return codes?.at(-1);
}

function withReset(label: string, text: string): string {
	const after = /reset after ([^)]+)\)/i.exec(text)?.[1];
	if (after) return `${label} - resets in ${after.trim()}`;
	const at = /resets (?:at )?(\d{1,2}(?::\d{2})?\s*(?:am|pm)?)/i.exec(text)?.[1];
	return at ? `${label} - resets ${at.trim()}` : label;
}

function nestedError(text: string): { type: string; message: string } | undefined {
	const start = text.indexOf("{");
	if (start < 0) return undefined;
	const body = balancedObject(text, start);
	if (!body) return undefined;
	try {
		const parsed: unknown = JSON.parse(body);
		const error = isRecord(parsed) && isRecord(parsed.error) ? parsed.error : undefined;
		if (!error) return undefined;
		return {
			type: typeof error.type === "string" ? error.type : "",
			message: typeof error.message === "string" ? error.message : "",
		};
	} catch {
		return undefined;
	}
}

// The JSON sits mid-sentence with prose after it, so cut it at its matching brace before parsing.
function balancedObject(text: string, start: number): string | undefined {
	let depth = 0;
	let inString = false;
	for (let i = start; i < text.length; i++) {
		const ch = text[i];
		if (inString) {
			if (ch === "\\") i++;
			else if (ch === '"') inString = false;
		} else if (ch === '"') inString = true;
		else if (ch === "{") depth++;
		else if (ch === "}" && --depth === 0) return text.slice(start, i + 1);
	}
	return undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}
