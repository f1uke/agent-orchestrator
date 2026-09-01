import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
// The WCAG maths already lives in the companion palette; a second copy here would
// be a second thing to keep right.
import { contrastRatio } from "../companion/palette";

// Comments are stripped first: a `/* … */` block can hold braces and commas that
// would otherwise read as a selector.
const STYLES = readFileSync(path.resolve(__dirname, "./styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

/**
 * A rule written outside every `@layer` lands in the implicit unlayered layer,
 * which outranks EVERY layered declaration - including all of Tailwind's
 * utilities, whatever their specificity. So a blanket `button { font: … }` here
 * silently wins over `<button class="text-[10px] font-medium">`, and the button
 * renders at the inherited size and weight instead of its own.
 *
 * That is not hypothetical: `button, input, textarea, select { font: inherit }`
 * lived in this file from the first scaffold, and every small-text button in the
 * renderer rendered at 14px/400 until it was removed. Form controls DO need that
 * reset - they otherwise fall back to the browser's own small system font - but
 * Tailwind's preflight already ships it inside `@layer base`, where a utility can
 * still win. Do not re-add it here.
 */
describe("renderer stylesheet cascade", () => {
	const FORM_TAGS = ["button", "input", "textarea", "select"];

	/** Selectors of unlayered rules, paired with the declarations they carry. */
	function unlayeredRules(css: string): { selector: string; body: string }[] {
		const out: { selector: string; body: string }[] = [];
		// Depth of the nearest enclosing at-rule that opens its own cascade layer.
		let layerDepth: number | null = null;
		let depth = 0;
		let i = 0;
		let chunkStart = 0;
		while (i < css.length) {
			const c = css[i];
			if (c === "{") {
				const prelude = css.slice(chunkStart, i).trim();
				if (depth === 0 && layerDepth === null && /^@layer\b/.test(prelude)) layerDepth = 0;
				if (depth === 0 && layerDepth === null && !prelude.startsWith("@")) {
					// A plain rule at the top level: capture it whole.
					const close = matchingBrace(css, i);
					out.push({ selector: prelude, body: css.slice(i + 1, close) });
					i = close + 1;
					chunkStart = i;
					continue;
				}
				depth++;
				i++;
				chunkStart = i;
				continue;
			}
			if (c === "}") {
				depth--;
				if (layerDepth !== null && depth === layerDepth) layerDepth = null;
				i++;
				chunkStart = i;
				continue;
			}
			if (c === ";") {
				i++;
				chunkStart = i;
				continue;
			}
			i++;
		}
		return out;
	}

	function matchingBrace(css: string, open: number): number {
		let depth = 0;
		for (let i = open; i < css.length; i++) {
			if (css[i] === "{") depth++;
			else if (css[i] === "}" && --depth === 0) return i;
		}
		return css.length;
	}

	it("sets no font property on a bare form-control selector outside a layer", () => {
		// Only a BLANKET rule is the hazard: one of the comma-separated selectors is
		// the bare tag and nothing else, so it matches every such control in the app.
		// `.jira-browse__jql input` is scoped to one component and stays allowed.
		const isBlanket = (selector: string) => selector.split(",").some((part) => FORM_TAGS.includes(part.trim()));
		const offenders = unlayeredRules(STYLES)
			.filter((rule) => isBlanket(rule.selector))
			.filter((rule) => /(^|[\s;])font(-family|-size|-weight|-style|-stretch|-variant)?\s*:/.test(rule.body))
			.map((rule) => rule.selector.replace(/\s+/g, " "));
		expect(offenders).toEqual([]);
	});
});

/**
 * `Button`'s primary variant paints `bg-primary` / `text-primary-foreground`,
 * which resolve through `@theme inline` to `--accent` / `--accent-fg`. So the
 * legibility of EVERY primary action in the app - the TODO "Start work" button
 * among them - is a property of those two tokens in each theme and of nothing
 * else, and a component test asserting the button carries `bg-primary` proves
 * only that it stopped hardcoding a colour, never that what it switched TO is
 * readable.
 *
 * What broke originally was structural rather than a low number: the fill came
 * from `--lane-todo-bright`, which deliberately inverts lightness between themes
 * (#a6a6be dark, #6b6b85 light), while the ink beside it was a hardcoded
 * `#12121a` that could not follow. Measured, that pair ran 7.82:1 in dark and
 * 3.61:1 in light - so the light theme was not merely dim, it was a dark slab
 * wearing near-black text, and no single hardcoded ink could have served both.
 * The guard that matters is therefore that BOTH halves of the pair are declared
 * in BOTH theme blocks, which is what resolving each token per theme asserts;
 * the ratio floor below is the second line of defence.
 */
describe("primary action legibility", () => {
	const THEMES = [
		{ name: "dark", selector: ":root" },
		{ name: "light", selector: ':root[data-theme="light"]' },
	];

	/** The value of a custom property declared directly in `selector`'s own block. */
	function token(selector: string, prop: string): string {
		const at = STYLES.search(new RegExp(`(^|\\})\\s*${escapeForRegex(selector)}\\s*\\{`, "m"));
		expect(at, `no ${selector} block in styles.css`).toBeGreaterThanOrEqual(0);
		const open = STYLES.indexOf("{", at);
		const block = STYLES.slice(open + 1, matchingBrace(STYLES, open));
		const found = block.match(new RegExp(`(^|[\\s;])${escapeForRegex(prop)}\\s*:\\s*([^;]+);`));
		// A token the theme does not restate is a token frozen at the other theme's
		// value - exactly the half-inverted pair this whole test exists to prevent.
		expect(found?.[2], `${selector} declares no ${prop}`).toBeTruthy();
		return (found?.[2] ?? "").trim();
	}

	function escapeForRegex(literal: string): string {
		return literal.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	}

	function matchingBrace(css: string, open: number): number {
		let depth = 0;
		for (let i = open; i < css.length; i++) {
			if (css[i] === "{") depth++;
			else if (css[i] === "}" && --depth === 0) return i;
		}
		return css.length;
	}

	for (const theme of THEMES) {
		it(`resolves --accent and --accent-fg together in the ${theme.name} theme`, () => {
			const fill = token(theme.selector, "--accent");
			const ink = token(theme.selector, "--accent-fg");

			// ⚠ This assertion is what makes the ratio below mean anything. `contrastRatio`
			// reads `#rrggbb` and nothing else, and it does NOT throw on a value it cannot
			// parse - `parseInt("kl", 16)` is NaN, and a NaN luminance quietly collapses the
			// result into a number that was never measured. Tailwind v4 ships `oklch()`
			// colours, so the day someone restates an accent in that space this test would
			// keep passing while checking nothing. Refuse the input instead: red here means
			// go re-measure in a checker that speaks oklch, not that the colour got worse.
			for (const [what, value] of [
				["--accent", fill],
				["--accent-fg", ink],
			]) {
				expect(value, `${what} is "${value}" - contrastRatio() reads #rrggbb only`).toMatch(/^#[0-9a-f]{6}$/i);
			}

			// 3:1 is the WCAG 1.4.11 floor, and deliberately NOT 4.5:1, because today's
			// accent does not clear 4.5 in dark: white on #4d8dff measures 3.20:1, against
			// 5.17:1 for white on #2563eb in light. That shortfall is app-wide - every
			// primary button in the renderer sits on this same pair - so it is an accent
			// decision for a human, not something to pin a red bar on here. Recorded rather
			// than hidden: a green bar on this test does not certify AA for body text.
			expect(contrastRatio(fill, ink), `${fill} on ${ink}`).toBeGreaterThanOrEqual(3);
		});
	}
});

/**
 * Native controls have to follow OUR theme, not the machine's.
 *
 * The app switches theme by setting `data-theme` on <html> and redefining custom
 * properties. Custom properties do not reach a native control's own painting:
 * without `color-scheme`, the UA renders checkboxes, form fields and scrollbars
 * from the OS appearance. On a dark-mode Mac that showed as an unchecked
 * checkbox in project settings painted as a solid black square on our light
 * surface - every checkbox on the page, in the theme where it is most visible.
 *
 * One declaration per theme root is the whole fix, and asserting it here is what
 * keeps a later token refactor from dropping it silently.
 */
describe("native controls follow the app theme", () => {
	for (const theme of [
		{ name: "dark", selector: ":root", scheme: "dark" },
		{ name: "light", selector: ':root[data-theme="light"]', scheme: "light" },
	]) {
		it(`declares color-scheme: ${theme.scheme} in the ${theme.name} theme`, () => {
			const at = STYLES.search(
				new RegExp(`(^|\\})\\s*${theme.selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m"),
			);
			expect(at, `no ${theme.selector} block in styles.css`).toBeGreaterThanOrEqual(0);
			const open = STYLES.indexOf("{", at);
			let depth = 0;
			let close = STYLES.length;
			for (let i = open; i < STYLES.length; i++) {
				if (STYLES[i] === "{") depth++;
				else if (STYLES[i] === "}" && --depth === 0) {
					close = i;
					break;
				}
			}
			const block = STYLES.slice(open + 1, close);
			expect(block).toMatch(new RegExp(`(^|[\\s;])color-scheme\\s*:\\s*${theme.scheme}\\s*;`));
		});
	}
});
