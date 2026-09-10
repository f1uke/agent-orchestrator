/**
 * How long a session's sidebar label may be, in RUNES (not UTF-16 code units,
 * and not bytes) - the same unit the daemon counts in.
 *
 * The number is MEASURED, not chosen by taste. It is how many runes the
 * sidebar's session-name column can render in full, without `truncate` eating
 * the tail, at the rail's DEFAULT width (240px - `useResizable` in Sidebar.tsx):
 *
 *   240px rail
 *   - 28px  sidebar content padding (10 left + 18 right)
 *   - 28px  session-list indent (18px margin + 10px padding)
 *   - 10px  row padding-left
 *   - 16px  session glyph, - 9px glyph gap
 *   -  6px  row padding-right at rest
 *   = 143px of name
 *
 * Measured in Chrome at 12.5px/500 in the renderer's UI stack, 143px holds 23
 * runes of lowercase Latin, 23 of kebab-case, 24-25 of Thai, 26 of Thai mixed
 * with Latin, and 21 of ordinary Title Case. 22 is the largest number all of
 * those clear, and the tightest of them ("longer session names o") lands at
 * 140.5px of 143 - so 23 would not fit.
 *
 * Two things this deliberately does NOT promise, because no rune count can bound
 * rendered width: a name of ALL-CAPS or unusually wide glyphs can still
 * ellipsize, and so can any name once the rail is dragged below its 240px
 * default - the rail goes down to 200px, where the column is 103px and holds
 * ~16. Widening only ever helps: at 420px the column is 323px and holds ~45.
 *
 * Kept in sync by hand with `maxDisplayNameLen` in
 * `backend/internal/{cli/spawn.go,httpd/controllers/sessions.go}` and with the
 * `<label, max N chars>` line in `backend/internal/prompts/prompts.go`.
 */
export const MAX_DISPLAY_NAME_LEN = 22;

/**
 * Cut a candidate label to the cap the API enforces, counting RUNES so a Thai
 * name is charged per character rather than per UTF-16 surrogate.
 */
export function clampDisplayName(name: string): string {
	const runes = [...name];
	return runes.length <= MAX_DISPLAY_NAME_LEN ? name : runes.slice(0, MAX_DISPLAY_NAME_LEN).join("");
}
