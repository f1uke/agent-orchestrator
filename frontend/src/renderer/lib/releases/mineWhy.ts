import { dayLabel } from "./day";
import { their, type Who } from "./who";
import type { AlertCopy, AlertHit, MineReason, Subject } from "./types";

type Why = MineReason["why"];

const ORDER: readonly Why[] = ["release_mr_author", "release_ticket_assignee", "approver", "card", "mr_author"];

const refs = (mine: readonly MineReason[], why: Why) => [
	...new Set(mine.filter((m) => m.why === why).map((m) => m.ref ?? "")),
];

const named = (one: string, many: string, list: string[]) =>
	list.length ? `${list.length === 1 ? one : many} ${list.join(", ")}` : "";

export function mineWhy(h: AlertHit, who: Who): string {
	const mine = h.mine ?? [];
	const own = their(who);
	const hotfix = h.subject.kind === "hotfix" || h.subject.kind === "hotfixTrain";
	const phrase: Record<Why, () => string> = {
		release_mr_author: () => named(`${own} release MR`, `${own} release MRs`, refs(mine, "release_mr_author")),
		release_ticket_assignee: () => (refs(mine, "release_ticket_assignee").length ? `${own} release ticket` : ""),
		approver: () =>
			refs(mine, "approver").length ? (who.self ? "you have not approved" : `${who.name} has not approved`) : "",
		card: () => named(`${own} card`, `${own} cards`, refs(mine, "card")),
		mr_author: () =>
			hotfix
				? named(`${own} hotfix MR`, `${own} hotfix MRs`, refs(mine, "mr_author"))
				: named(`${own} MR`, `${own} MRs`, refs(mine, "mr_author")),
		assignee: () => "",
		subtask: () => "",
		landing_author: () => "",
		no_mr: () => "",
	};
	return ORDER.map((w) => phrase[w]())
		.filter(Boolean)
		.join(" · ");
}

export function onlyMeSubject(h: AlertHit, me: boolean): Subject {
	if (!me || h.subject.kind !== "pageFamily") return h.subject;
	const keys = refs(h.mine ?? [], "card");
	return keys.length ? { ...h.subject, keys } : h.subject;
}

export function alertCopy(h: AlertHit, me: boolean, trainLabel?: string): AlertCopy {
	if (!me || h.rule !== "D3") return h.copy;
	const train = trainLabel ? `train ${trainLabel.replace(/^#/, "")}` : "this train";
	return {
		what: `You have not approved ${train}`,
		do: `Approve the Jira release before publish${h.due ? ` ${dayLabel(h.due.on)}` : ""}.`,
	};
}
