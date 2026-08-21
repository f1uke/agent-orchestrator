import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

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
