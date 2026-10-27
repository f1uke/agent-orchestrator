import type { Board, BoardPerson } from "./types";

export type Who = { self: boolean; name: string };

export function viewerLabel(person: BoardPerson, readOnly: boolean): Who {
	return { self: person.self === true && !readOnly, name: person.name };
}

export function boardWho(b: Board, readOnly: boolean): Who | null {
	return b.viewer.kind === "person" ? viewerLabel(b.viewer.person, readOnly) : null;
}

export const their = (who: Who): string => (who.self ? "your" : `${who.name}'s`);

export const ofText = (who: Who): string => (who.self ? "of mine" : `of ${who.name}'s`);

export const ofTheir = (who: Who): string => (who.self ? "of my" : `of ${who.name}'s`);
