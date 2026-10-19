import type { AlertHit } from "./types";

const css = (v: string) => JSON.stringify(v);
const part = (name: string) => `[data-part=${css(name)}]`;

const RULE_PART: Readonly<Record<string, string>> = {
	A1: "jira",
	A5: "jira",
	D2: "jira",
	E1: "jira",
	B1: "mr",
	C1: "mr",
	C2: "mr",
	C3: "mr",
	C5: "mr",
	C6: "mr",
	C7: "tag",
	D1: "approvers",
};

export function sheetPart(h: AlertHit): string | null {
	if (h.subject.kind === "versionDate")
		return h.subject.field === "page" ? null : `[data-testid="stage-row"][data-stage=${css(h.subject.field)}]`;
	if (h.rule === "D3") {
		const names = Array.isArray(h.evidence.pending)
			? h.evidence.pending.filter((n): n is string => typeof n === "string")
			: [];
		return names.length ? names.map((n) => `${part("approvers")} [data-name=${css(n)}]`).join(", ") : part("approvers");
	}
	const p = RULE_PART[h.rule];
	return p ? part(p) : null;
}
