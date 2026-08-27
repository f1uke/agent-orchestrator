import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { contrastRatio } from "../../companion/palette";
import {
	AO_DARK_THEME,
	AO_LIGHT_THEME,
	EDITOR_THEME_TOKENS,
	grammarRole,
	SEMANTIC_SCOPES,
	SYNTAX_ROLES,
} from "./monaco-theme";

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
			const byColour = new Map<string, string>();
			for (const role of SYNTAX_ROLES) {
				const colour = t[role].toLowerCase();
				expect(byColour.get(colour), `${role} and ${byColour.get(colour)} share ${colour}`).toBeUndefined();
				byColour.set(colour, role);
			}
			expect(byColour.size).toBe(SYNTAX_ROLES.length);
		});

		/**
		 * `--code-plain` is the editor's default foreground AND the colour of the
		 * `keyword.operator` rule (Xcode paints operators as plain text), so every
		 * unstyled token reverse-maps to whatever scope that first rule names. That
		 * scope decides the token's `StandardTokenType`: name it something matching
		 * `/\b(comment|string|regex|regexp)\b/` and Monaco would read plain code as
		 * a comment, banding the minimap off arbitrary lines.
		 */
		it(`${theme.name}: the plain colour reverse-maps to a scope Monaco reads as Other`, () => {
			const plain = EDITOR_THEME_TOKENS[theme.type]["--code-plain"].toLowerCase();
			const first = rules.find((r) => r.foreground?.toLowerCase() === plain);
			expect(first?.scope, "no rule carries the plain colour").toBeTruthy();
			expect(first?.scope).not.toMatch(/\b(comment|string|regex|regexp)\b/);
		});

		it(`${theme.name}: every rule colour is one of the syntax roles`, () => {
			const known = new Set(SYNTAX_ROLES.map((role) => EDITOR_THEME_TOKENS[theme.type][role].toLowerCase()));
			for (const rule of rules) {
				if (!rule.foreground) continue;
				expect(known, `${rule.scope} paints ${rule.foreground}, which is no --code-* role`).toContain(
					rule.foreground.toLowerCase(),
				);
			}
		});
	}
});

/**
 * The semantic layer shares this one rule table with shiki - Monaco standalone
 * resolves an LSP token by joining its type and modifiers with dots and matching
 * THAT against these same rules. So the rules that make semantic tokens work are
 * also rules the #248 reverse map walks, and both facts have to stay true.
 */
describe("semantic token rules", () => {
	for (const theme of [AO_DARK_THEME, AO_LIGHT_THEME]) {
		const rules = theme.settings.flatMap((entry) =>
			entry.scope.map((scope) => ({
				scope,
				foreground: "foreground" in entry.settings ? entry.settings.foreground : undefined,
			})),
		);
		const semantic = new Set<string>(SEMANTIC_SCOPES.map((s) => s.scope));

		it(`${theme.name}: every semantic scope has a rule carrying its role's colour`, () => {
			const t = EDITOR_THEME_TOKENS[theme.type];
			for (const { scope, role } of SEMANTIC_SCOPES) {
				const rule = rules.find((r) => r.scope === scope);
				expect(rule, `no rule for ${scope}`).toBeTruthy();
				expect(rule?.foreground?.toLowerCase()).toBe(t[role].toLowerCase());
			}
		});

		/**
		 * 🗝 Order, again. `@shikijs/monaco` keeps the FIRST rule holding a colour
		 * as that colour's scope, and Monaco reads `StandardTokenType` off it. Every
		 * semantic rule reuses a colour a grammar rule already carries, so as long
		 * as they all come last, the reverse map is exactly what #255 left.
		 */
		it(`${theme.name}: the semantic rules come after every grammar rule`, () => {
			const firstSemantic = rules.findIndex((r) => semantic.has(r.scope));
			expect(firstSemantic, "no semantic rules at all").toBeGreaterThan(0);
			expect(rules.slice(firstSemantic).every((r) => semantic.has(r.scope))).toBe(true);
		});

		/**
		 * The `ao.` namespace is what keeps the two vocabularies apart: an LSP type
		 * called `function` or `type` would otherwise match a TextMate rule of the
		 * same name and take the wrong role entirely.
		 */
		it(`${theme.name}: no semantic scope collides with a grammar scope`, () => {
			const grammar = rules.filter((r) => !semantic.has(r.scope)).map((r) => r.scope);
			for (const scope of semantic) {
				expect(scope.startsWith("ao."), `${scope} is not namespaced`).toBe(true);
				expect(grammar, `${scope} is also a grammar scope`).not.toContain(scope);
				// A TextMate token that reverse-mapped onto one of these would have its
				// StandardTokenType read from the name - see the round-trip block above.
				expect(scope).not.toMatch(/\b(comment|string|regex|regexp)\b/);
			}
		});

		it(`${theme.name}: the SDK-value colour is reachable only from the semantic layer`, () => {
			const colour = EDITOR_THEME_TOKENS[theme.type]["--code-fn-system"].toLowerCase();
			const carriers = rules.filter((r) => r.foreground?.toLowerCase() === colour);
			expect(carriers.length, "no rule carries --code-fn-system").toBeGreaterThan(0);
			expect(carriers.every((r) => semantic.has(r.scope))).toBe(true);
		});
	}

	/**
	 * The table `monaco.editor.tokenize`'s answer is read through. It is built
	 * from the theme's own rules, so this is really asserting that the derivation
	 * survives - a scope with no rule, and the `keyword.operator` spelling of
	 * unstyled text, both have to come out plain.
	 */
	it("reads a grammar scope back as the role that painted it", () => {
		expect(grammarRole("comment")).toBe("--code-comment");
		expect(grammarRole("entity.name.type")).toBe("--code-type");
		expect(grammarRole("entity.name.function")).toBe("--code-declaration");
		expect(grammarRole("support.type")).toBe("--code-type-system");
		expect(grammarRole("keyword.operator")).toBe("--code-plain");
		expect(grammarRole("")).toBe("--code-plain");
		expect(grammarRole("no.such.scope")).toBe("--code-plain");
	});
});

/**
 * The whole-line diagnostic band (`lib/editor/diagnostic-lines.ts`) is CSS, not
 * a Monaco theme colour, so nothing above would notice it drifting. It is
 * nonetheless the one decoration this editor paints UNDER the code a reader is
 * trying to read, and its alpha is therefore a contrast budget rather than a
 * taste.
 *
 * 🗝 A band in a dark theme can only LIGHTEN a near-black surface, so every one
 * of the thirteen `--code-*` roles loses contrast by the same proportion the
 * band adds luminance. Twelve of them have to survive it; the thirteenth,
 * `--code-comment`, sits at 4.55:1 on the bare surface in Xcode's dark palette -
 * a margin of 0.05 - so ANY visible band takes it under AA, and pretending
 * otherwise would mean shipping no band in dark at all. It is held to its own,
 * measured floor rather than waved through.
 */
describe("the whole-line diagnostic band", () => {
	/** The alpha out of `color-mix(in oklab, var(--token) N%, transparent)`. */
	function bandAlpha(selector: string): number {
		const block = blockOf(selector);
		const found = block.match(/background:\s*color-mix\(in oklab,\s*var\(--[\w-]+\)\s*([\d.]+)%,\s*transparent\)/);
		expect(found?.[1], `${selector} declares no color-mix band background`).toBeTruthy();
		return Number(found?.[1]) / 100;
	}

	/** `over` in sRGB, which is what the compositor does with a translucent fill. */
	function composite(fill: string, alpha: number, under: string): string {
		const channels = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
		const [f, u] = [channels(fill), channels(under)];
		return `#${f
			.map((c, i) => Math.round(c * alpha + u[i] * (1 - alpha)))
			.map((c) => c.toString(16).padStart(2, "0"))
			.join("")}`;
	}

	const BANDS = [
		{
			theme: "dark",
			kind: "error",
			selector: ".monaco-editor .ao-diagnostic-line--error",
			token: "--diagnostic-error",
		},
		{ theme: "dark", kind: "warning", selector: ".monaco-editor .ao-diagnostic-line", token: "--diagnostic-warning" },
		{
			theme: "light",
			kind: "error",
			selector: ':root[data-theme="light"] .monaco-editor .ao-diagnostic-line--error',
			token: "--diagnostic-error",
		},
		{
			theme: "light",
			kind: "warning",
			selector: ':root[data-theme="light"] .monaco-editor .ao-diagnostic-line',
			token: "--diagnostic-warning",
		},
	] as const;

	for (const band of BANDS) {
		const tokens = EDITOR_THEME_TOKENS[band.theme];
		const roles = SYNTAX_ROLES.filter((role) => role !== "--code-comment");

		it(`${band.theme}/${band.kind}: every syntax role but the comment clears 4.5:1 on the band`, () => {
			const under = composite(resolve(band.theme, band.token), bandAlpha(band.selector), tokens["--viewer-bg"]);
			for (const role of roles) {
				expect(contrastRatio(tokens[role], under), `${role} on the ${band.kind} band`).toBeGreaterThanOrEqual(4.5);
			}
		});

		/**
		 * The comment role's own floor. Dark cannot reach 4.5 (see above); light
		 * can and must, so the two are asserted at what each actually achieves
		 * rather than at one number that would be a lie in one of them.
		 */
		it(`${band.theme}/${band.kind}: the comment role holds its measured floor`, () => {
			const under = composite(resolve(band.theme, band.token), bandAlpha(band.selector), tokens["--viewer-bg"]);
			const floor = band.theme === "light" ? 4.5 : 4.0;
			expect(contrastRatio(tokens["--code-comment"], under)).toBeGreaterThanOrEqual(floor);
		});

		/**
		 * A band is a hint, not a highlight. Beyond roughly a fifth it stops
		 * reading as a tint under the code and starts reading as a selection - and
		 * with dozens of warnings in a file, as a wall.
		 */
		it(`${band.theme}/${band.kind}: the band stays a tint`, () => {
			expect(bandAlpha(band.selector)).toBeLessThanOrEqual(0.2);
		});
	}

	/**
	 * The weighting the human was shown and asked to overrule. Errors are rare
	 * and must be unmissable; warnings are common - their own Xcode screenshot
	 * showed 3 284 of them - and must stay readable underneath the code.
	 */
	it("dark: an error is louder than a warning", () => {
		expect(bandAlpha(".monaco-editor .ao-diagnostic-line--error")).toBeGreaterThan(
			bandAlpha(".monaco-editor .ao-diagnostic-line"),
		);
	});

	/**
	 * The edge is what makes ONE error findable in a file full of warnings
	 * without making every warning shout, so it belongs to the error alone.
	 */
	it("only the error carries the leading edge", () => {
		expect(blockOf(".monaco-editor .ao-diagnostic-line--error")).toContain("box-shadow: inset");
		expect(blockOf(".monaco-editor .ao-diagnostic-line")).not.toContain("box-shadow");
	});

	/** The colours the human asked to be the same as the minimap's, named once. */
	it("takes its colours from the same two tokens the minimap marks use", () => {
		for (const band of BANDS) expect(blockOf(band.selector)).toContain(`var(${band.token})`);
	});
});
