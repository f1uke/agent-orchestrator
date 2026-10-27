import type { Board, BoardPerson } from "./types";

/**
 * Whose Mine a line of copy describes. `self` means the reader is that person, so the copy says "your";
 * otherwise it names them. Nobody is self in a read-only host: every teammate reads through the host's tokens.
 */
export type Who = { self: boolean; name: string };

export function viewerLabel(person: BoardPerson, readOnly: boolean): Who {
	return { self: person.self === true && !readOnly, name: person.name };
}

/** The person the Board's Mine reasons are about, whether or not the page is filtered to them. */
export function boardWho(b: Board, readOnly: boolean): Who | null {
	return b.viewer.kind === "person" ? viewerLabel(b.viewer.person, readOnly) : null;
}

/** "your" for the reader, else "Ben Sample's": the one possessive every Mine line uses. */
export const their = (who: Who): string => (who.self ? "your" : `${who.name}'s`);

/** "of mine" for the reader, else "of Ben Sample's". */
export const ofText = (who: Who): string => (who.self ? "of mine" : `of ${who.name}'s`);

/** "of my" for the reader, else "of Ben Sample's", before a noun. */
export const ofTheir = (who: Who): string => (who.self ? "of my" : `of ${who.name}'s`);
