import { isPerson } from "./lens";
import type { Board, BoardPerson } from "./types";

/** The picker offers a search box once the list is longer than this. */
export const SEARCH_FROM = 8;

export type PickerPeople = { self: BoardPerson | null; others: BoardPerson[] };

/**
 * The monitor's own person leads as "Me", then everyone else by name, the way the API sorts them.
 * A read-only host has no "Me": every teammate reads through the host's tokens, so its own person is
 * one more name in the list.
 */
export function pickerPeople(people: readonly BoardPerson[], readOnly = false): PickerPeople {
	if (readOnly) return { self: null, others: [...people].sort((a, b) => a.name.localeCompare(b.name)) };
	const self = people.find((p) => p.self) ?? null;
	return { self, others: people.filter((p) => p !== self) };
}

export function searchPeople(people: readonly BoardPerson[], query: string): BoardPerson[] {
	const q = query.trim().toLowerCase();
	if (!q) return [...people];
	return people.filter((p) =>
		[p.name, p.id, p.gitlabUsername, p.jiraAccountId].some((x) => x?.toLowerCase().includes(q)),
	);
}

export const MATCH_TAG: Readonly<Partial<Record<BoardPerson["match"], string>>> = {
	jira_only: "Jira only",
	gitlab_only: "GitLab only",
};

export const MATCH_HINT: Readonly<Record<BoardPerson["match"], string>> = {
	linked: "Jira and GitLab accounts joined by the people file",
	self: "The monitor's own Jira and GitLab accounts",
	name: "Jira and GitLab accounts joined by the same display name",
	jira_only: "Only the Jira account is known, so MRs by this person are not counted",
	gitlab_only: "Only the GitLab user is known, so cards assigned to this person are not counted",
};

export function matchHint(person: BoardPerson, readOnly: boolean): string {
	return readOnly && person.match === "self"
		? "The Jira and GitLab accounts this page reads with"
		: MATCH_HINT[person.match];
}

export type ViewAsState =
	{ kind: "everyone" } | { kind: "person"; person: BoardPerson } | { kind: "unknown"; id: string; reason: string };

/** What the picker shows for ?person=: the person once the Board knows them, else why not. */
export function viewAsState(b: Board, person: string | undefined): ViewAsState {
	if (!person) return { kind: "everyone" };
	const known = b.people.find((p) => isPerson(p, person));
	if (known) return { kind: "person", person: known };
	const reason =
		b.viewer.kind === "unknown" && b.viewer.id === person
			? b.viewer.reason
			: `${person} is on no card of this Board, so nothing is filtered`;
	return { kind: "unknown", id: person, reason };
}
