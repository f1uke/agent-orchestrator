import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { AO_DARK_THEME, AO_LIGHT_THEME, EDITOR_THEME_TOKENS } from "./monaco-theme";

/**
 * Monaco cannot read a CSS custom property: it tokenizes into a packed colour
 * map, so the editor themes carry RESOLVED values copied out of `styles.css`.
 * That copy is the whole risk — a token retuned in the stylesheet leaves the
 * editor painting last month's palette, and nothing anywhere fails.
 *
 * So this re-parses `styles.css` and compares. It is the same trick
 * `styles.test.ts` uses to keep the two theme blocks honest.
 */
const STYLES = readFileSync(path.resolve(__dirname, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

const THEME_SELECTOR = { dark: ":root", light: ':root[data-theme="light"]' } as const;

function matchingBrace(css: string, open: number): number {
	let depth = 0;
	for (let i = open; i < css.length; i++) {
		if (css[i] === "{") depth++;
		else if (css[i] === "}" && --depth === 0) return i;
	}
	return css.length;
}

function blockOf(selector: string): string {
	const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	const at = STYLES.search(new RegExp(`(^|\\})\\s*${escaped}\\s*\\{`, "m"));
	expect(at, `no ${selector} block in styles.css`).toBeGreaterThanOrEqual(0);
	const open = STYLES.indexOf("{", at);
	return STYLES.slice(open + 1, matchingBrace(STYLES, open));
}

/** `rgb(r g b / a)` → `#rrggbbaa`, so an alpha token can be compared as hex. */
function toHex(value: string): string {
	const rgb = value.match(/^rgba?\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)\s*(?:[/,]\s*([\d.]+)\s*)?\)$/);
	if (!rgb) return value.toLowerCase();
	const [, r, g, b, a] = rgb;
	const hex = [r, g, b].map((n) => Number(n).toString(16).padStart(2, "0")).join("");
	if (a === undefined) return `#${hex}`;
	return `#${hex}${Math.round(Number(a) * 255)
		.toString(16)
		.padStart(2, "0")}`;
}

/** A token's value in one theme block, following `var(--other)` indirection. */
function resolve(theme: "dark" | "light", name: string, seen = new Set<string>()): string {
	expect(seen.has(name), `${name} resolves in a cycle`).toBe(false);
	seen.add(name);
	const block = blockOf(THEME_SELECTOR[theme]);
	const found = block.match(new RegExp(`(^|[\\s;])${name.replace(/-/g, "\\-")}\\s*:\\s*([^;]+);`));
	expect(found?.[2], `${THEME_SELECTOR[theme]} declares no ${name}`).toBeTruthy();
	const value = (found?.[2] ?? "").trim();
	const indirect = value.match(/^var\(\s*(--[\w-]+)\s*\)$/);
	if (indirect) return resolve(theme, indirect[1], seen);
	return toHex(value);
}

describe("editor theme tokens", () => {
	for (const theme of ["dark", "light"] as const) {
		for (const [name, value] of Object.entries(EDITOR_THEME_TOKENS[theme])) {
			it(`${theme}: ${name} matches styles.css`, () => {
				expect(value.toLowerCase()).toBe(resolve(theme, name));
			});
		}
	}
});

/**
 * 🗝 The invariant `@shikijs/monaco` imposes, and the reason the minimap's
 * `// MARK:` bands exist at all.
 *
 * shiki hands Monaco a scope NAME per token, and finds it by looking the token's
 * resolved colour up in the theme's rules and taking the first match. Monaco then
 * derives `StandardTokenType` from that name — `/\b(comment|string|regex|regexp)\b/`
 * — and the section-header detector drops any header whose line does not start in
 * a Comment token. So if `comment` is not the first rule with the comment colour,
 * or if two roles share a colour, comments stop being comments as far as Monaco
 * is concerned: no bands, no error, nothing in the console.
 */
describe("shiki → Monaco scope round-trip", () => {
	for (const theme of [AO_DARK_THEME, AO_LIGHT_THEME]) {
		const rules = theme.settings.flatMap((entry) =>
			entry.scope.map((scope) => ({
				scope,
				foreground: "foreground" in entry.settings ? entry.settings.foreground : undefined,
			})),
		);

		it(`${theme.name}: the first rule carrying the comment colour is a comment scope`, () => {
			const commentColour = rules.find((r) => r.scope === "comment")?.foreground;
			expect(commentColour).toBeTruthy();
			expect(rules.find((r) => r.foreground === commentColour)?.scope).toBe("comment");
		});

		it(`${theme.name}: the first rule carrying the string colour is a string scope`, () => {
			const stringColour = rules.find((r) => r.scope === "string")?.foreground;
			expect(stringColour).toBeTruthy();
			expect(rules.find((r) => r.foreground === stringColour)?.scope).toBe("string");
		});

		it(`${theme.name}: every syntax role has its own colour`, () => {
			const t = EDITOR_THEME_TOKENS[theme.type];
			const roles = [
				t["--code-keyword"],
				t["--code-string"],
				t["--code-comment"],
				t["--code-number"],
				t["--code-type"],
				t["--code-fn"],
				t["--code-plain"],
			];
			expect(new Set(roles).size).toBe(roles.length);
		});
	}
});
