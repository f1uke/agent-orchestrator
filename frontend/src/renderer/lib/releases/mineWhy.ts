import type { AlertHit, MinePolicy, MineReason, RuleInfo, Subject } from "./types";

type Why = MineReason["why"];

const ORDER: readonly Why[] = ["release_mr_author", "release_ticket_assignee", "approver", "card", "mr_author"];

const refs = (mine: readonly MineReason[], why: Why) => [
	...new Set(mine.filter((m) => m.why === why).map((m) => m.ref ?? "")),
];

const named = (one: string, many: string, list: string[]) =>
	list.length ? `${list.length === 1 ? one : many} ${list.join(", ")}` : "";

export function mineWhy(h: AlertHit): string {
	const mine = h.mine ?? [];
	const hotfix = h.subject.kind === "hotfix" || h.subject.kind === "hotfixTrain";
	const phrase: Record<Why, () => string> = {
		release_mr_author: () => named("your release MR", "your release MRs", refs(mine, "release_mr_author")),
		release_ticket_assignee: () => (refs(mine, "release_ticket_assignee").length ? "your release ticket" : ""),
		approver: () => (refs(mine, "approver").length ? "you have not approved" : ""),
		card: () => named("your card", "your cards", refs(mine, "card")),
		mr_author: () =>
			hotfix
				? named("your hotfix MR", "your hotfix MRs", refs(mine, "mr_author"))
				: named("your MR", "your MRs", refs(mine, "mr_author")),
		assignee: () => "",
		subtask: () => "",
		landing_author: () => "",
		no_mr: () => "",
	};
	return ORDER.map((w) => phrase[w]())
		.filter(Boolean)
		.join(" · ");
}

export function minePolicyOf(catalogue: readonly RuleInfo[], rule: string): MinePolicy | null {
	return catalogue.find((r) => r.id === rule)?.minePolicy ?? null;
}

export function onlyMeSubject(h: AlertHit, me: boolean): Subject {
	if (!me || h.subject.kind !== "pageFamily") return h.subject;
	const keys = refs(h.mine ?? [], "card");
	return keys.length ? { ...h.subject, keys } : h.subject;
}

export function alertText(h: AlertHit, me: boolean): string {
	if (!me || h.rule !== "D3") return h.text;
	const { pending, total } = h.evidence;
	return Array.isArray(pending) && typeof total === "number"
		? `${pending.length} of ${total} not approved`
		: "Not every approver approved";
}
