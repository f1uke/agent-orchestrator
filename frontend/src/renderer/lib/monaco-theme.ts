/**
 * Monaco's own themes do not match this app, so the editor gets two themes built
 * from the app's OWN tokens: the thirteen `--code-*` syntax roles plus the
 * surface/chrome tokens around them. `monaco-theme.test.ts` re-parses
 * `styles.css` and fails if any value here drifts from the token it claims.
 *
 * 🗝 The palette is XCODE's, role for role — the one place the renderer
 * deliberately does not clone the reference app (explicit decision, 2026-08-26).
 * Dark is the human's own `Default (Dark).xccolortheme`, light is Xcode's
 * bundled `Default (Light)`. The scope of that exception is the syntax colours
 * only: the surface, gutter, minimap and widget colours below still come from
 * the app's tokens, and so does everything outside the editor.
 *
 * What this CANNOT match, and why no palette can: Xcode colours from compiler
 * knowledge — it knows `titleLabel` is a declared property and not a local, and
 * that `UILabel` is a system type while `SettingsViewModel` is yours. shiki
 * tokenises with TextMate grammars, which are regex over word shapes. So every
 * role below is anchored to a scope the GRAMMAR actually emits; where the
 * grammar emits nothing (a property name, a type in a parameter clause) the
 * token stays plain rather than being guessed at. The language server fills
 * those in on top - see `SEMANTIC_SCOPES` and `lib/lsp/semantic-tokens.ts`.
 *
 * Why literals rather than `var(--code-keyword)`: Monaco tokenizes into a packed
 * colour map, not into CSS classes, so a theme colour must be a resolved value.
 * The runtime theme switch is `monaco.editor.setTheme(...)`, not a CSS cascade.
 *
 * 🗝 Rule ORDER is load-bearing. `@shikijs/monaco` maps a token back to a scope
 * by looking up its resolved COLOUR in the theme's rules and taking the first
 * match, and Monaco derives `StandardTokenType` from that scope string
 * (`/\b(comment|string|regex|regexp)\b/`). Section headers in the minimap are
 * dropped unless the line's first token is a Comment, so `comment` must be the
 * first rule carrying the comment colour — and every role colour must be
 * distinct. Both are asserted in the test.
 */

/** The `styles.css` tokens the two editor themes are built from. */
export const EDITOR_THEME_TOKENS = {
	dark: {
		"--code-plain": "#dadada",
		"--code-comment": "#6c7986",
		"--code-string": "#fc6a5d",
		"--code-number": "#d0bf69",
		"--code-keyword": "#fc5fa3",
		"--code-attribute": "#bf8555",
		"--code-directive": "#fd8f3f",
		"--code-type": "#5dd8ff",
		"--code-declaration": "#41a1c0",
		"--code-type-ref": "#9ef1dd",
		"--code-fn": "#67b7a4",
		"--code-type-system": "#d0a8ff",
		"--code-fn-system": "#a167e6",
		"--viewer-bg": "#060607",
		"--fg": "#f4f5f7",
		"--fg-muted": "#9ba1aa",
		"--fg-passive": "#646a73",
		"--accent": "#4d8dff",
		"--bg-1": "#15171b",
		"--bg-2": "#1b1d22",
		"--border-1": "#ffffff1a",
		"--scrollbar-thumb": "#ffffff29",
		"--scrollbar-thumb-hover": "#ffffff47",
		"--interactive-hover": "#ffffff0a",
		// The squiggle and the ruler mark, kept in step with the header's own
		// problem count (`--inbox-red` / `--inbox-amber` in `styles.css`) — one
		// file, two places saying the same thing, and they have to agree.
		"--diagnostic-error": "#ef6a63",
		"--diagnostic-warning": "#e0a544",
	},
	light: {
		"--code-plain": "#262626",
		"--code-comment": "#5d6c79",
		"--code-string": "#c41a16",
		"--code-number": "#1c00cf",
		"--code-keyword": "#9b2393",
		"--code-attribute": "#815f03",
		"--code-directive": "#643820",
		"--code-type": "#0b4f79",
		"--code-declaration": "#0f68a0",
		"--code-type-ref": "#1c464a",
		"--code-fn": "#326d74",
		"--code-type-system": "#3900a0",
		"--code-fn-system": "#6c36a9",
		"--viewer-bg": "#fcfcfc",
		"--fg": "#1a1a1a",
		"--fg-muted": "#666666",
		"--fg-passive": "#9a9a9a",
		"--accent": "#2563eb",
		"--bg-1": "#ffffff",
		"--bg-2": "#ededee",
		"--border-1": "#d4d4d6",
		"--scrollbar-thumb": "#0000002e",
		"--scrollbar-thumb-hover": "#0000004d",
		"--interactive-hover": "#0000000a",
		"--diagnostic-error": "#c0392b",
		"--diagnostic-warning": "#9a6b00",
	},
} as const;

/** The `--code-*` roles, in the order the themes declare their rules. */
export const SYNTAX_ROLES = [
	"--code-plain",
	"--code-comment",
	"--code-string",
	"--code-number",
	"--code-keyword",
	"--code-attribute",
	"--code-directive",
	"--code-type",
	"--code-declaration",
	"--code-type-ref",
	"--code-fn",
	"--code-type-system",
	"--code-fn-system",
] as const;

/**
 * The scopes the LSP semantic layer paints with, and the role each one takes.
 *
 * 🗝 Monaco standalone has NO separate semantic palette. `StandaloneTheme`
 * resolves a semantic token by joining its type and modifiers with dots and
 * matching THAT against the rules below (`standaloneThemeService.js:149`,
 * `tokenTheme._match([type].concat(modifiers).join('.'))`). So a semantic rule is
 * an ordinary theme rule, and shiki and the language server share one table
 * rather than fighting over two.
 *
 * 🗝 Which is exactly why these are namespaced `ao.`. The LSP type names collide
 * with TextMate scope names the theme already uses - a legend saying `function`
 * would match `entity.name.function`'s rule and paint every call the DECLARATION
 * colour, and `type` would take the `type` rule above. Nothing in any grammar
 * starts with `ao.`, so the two vocabularies cannot reach each other.
 *
 * The roles are Xcode's own identifier split: project vs system, type vs value.
 * `--code-fn-system` exists only here - no TextMate grammar can know that
 * `contentView` is UIKit's and `viewModel` is yours.
 */
export const SEMANTIC_SCOPES = [
	/** A name at its declaration site: `xcode.syntax.declaration.other`. */
	{ scope: "ao.declaration", role: "--code-declaration" },
	/** A type being referred to: `xcode.syntax.identifier.type`. */
	{ scope: "ao.type", role: "--code-type-ref" },
	/** …one the SDK declared: `xcode.syntax.identifier.type.system`. */
	{ scope: "ao.type.system", role: "--code-type-system" },
	/** A value being referred to: `xcode.syntax.identifier.function`/`.variable`. */
	{ scope: "ao.value", role: "--code-fn" },
	/** …one the SDK declared: `xcode.syntax.identifier.function.system`. */
	{ scope: "ao.value.system", role: "--code-fn-system" },
	/** `xcode.syntax.identifier.macro`, which Xcode paints with preprocessor. */
	{ scope: "ao.macro", role: "--code-directive" },
] as const satisfies readonly { scope: string; role: (typeof SYNTAX_ROLES)[number] }[];

export type SemanticScope = (typeof SEMANTIC_SCOPES)[number]["scope"];

export type EditorThemeName = "ao-dark" | "ao-light";

/** The theme id for an app theme. */
export function editorThemeName(theme: "dark" | "light"): EditorThemeName {
	return theme === "light" ? "ao-light" : "ao-dark";
}

type Tokens = Record<keyof (typeof EDITOR_THEME_TOKENS)["dark"], string>;

/** `#rrggbb` plus an alpha byte, so a tint stays a plain hex Monaco can parse. */
function tint(hex: string, alpha: number): string {
	const byte = Math.round(Math.min(1, Math.max(0, alpha)) * 255)
		.toString(16)
		.padStart(2, "0");
	return `${hex.slice(0, 7)}${byte}`;
}

/**
 * A shiki theme (`ThemeRegistrationRaw`) — `@shikijs/monaco` converts it to a
 * Monaco theme and keeps shiki's tokenizer and Monaco's colour map in step.
 */
function buildTheme(name: EditorThemeName, type: "dark" | "light", t: Tokens) {
	const accent = t["--accent"];
	return {
		name,
		type,
		bg: t["--viewer-bg"],
		fg: t["--code-plain"],
		colors: {
			"editor.background": t["--viewer-bg"],
			"editor.foreground": t["--code-plain"],
			"editorGutter.background": t["--viewer-bg"],
			"editorLineNumber.foreground": t["--fg-passive"],
			"editorLineNumber.activeForeground": t["--fg-muted"],
			"editorCursor.foreground": accent,
			"editor.selectionBackground": tint(accent, 0.28),
			"editor.inactiveSelectionBackground": tint(accent, 0.16),
			"editor.selectionHighlightBackground": tint(accent, 0.12),
			"editor.wordHighlightBackground": tint(accent, 0.12),
			"editor.wordHighlightStrongBackground": tint(accent, 0.18),
			"editor.lineHighlightBackground": t["--interactive-hover"],
			"editor.findMatchBackground": tint(accent, 0.38),
			"editor.findMatchHighlightBackground": tint(accent, 0.18),
			"editor.rangeHighlightBackground": tint(accent, 0.1),
			"editorIndentGuide.background1": t["--border-1"],
			"editorIndentGuide.activeBackground1": t["--fg-passive"],
			"editorWhitespace.foreground": t["--border-1"],
			"editorBracketMatch.background": tint(accent, 0.16),
			"editorBracketMatch.border": tint(accent, 0.45),
			// Diagnostics. Monaco draws the squiggle, the overview-ruler mark and
			// the minimap mark itself once `setModelMarkers` is called
			// (`markerDecorationsService.js`) — but only in whatever colour the
			// theme names, and a theme that names none leaves an error the same
			// colour as an information hint.
			"editorError.foreground": t["--diagnostic-error"],
			"editorWarning.foreground": t["--diagnostic-warning"],
			"editorInfo.foreground": accent,
			"editorHint.foreground": t["--fg-passive"],
			"editorOverviewRuler.errorForeground": tint(t["--diagnostic-error"], 0.8),
			"editorOverviewRuler.warningForeground": tint(t["--diagnostic-warning"], 0.8),
			"editorOverviewRuler.infoForeground": tint(accent, 0.8),
			"minimap.errorHighlight": tint(t["--diagnostic-error"], 0.8),
			"minimap.warningHighlight": tint(t["--diagnostic-warning"], 0.8),
			"editorOverviewRuler.border": "#00000000",
			"editorOverviewRuler.findMatchForeground": tint(accent, 0.6),
			"minimap.background": t["--viewer-bg"],
			"minimap.findMatchHighlight": tint(accent, 0.6),
			"minimapSlider.background": t["--scrollbar-thumb"],
			"minimapSlider.hoverBackground": t["--scrollbar-thumb-hover"],
			"minimapSlider.activeBackground": t["--scrollbar-thumb-hover"],
			"scrollbar.shadow": "#00000000",
			"scrollbarSlider.background": t["--scrollbar-thumb"],
			"scrollbarSlider.hoverBackground": t["--scrollbar-thumb-hover"],
			"scrollbarSlider.activeBackground": t["--scrollbar-thumb-hover"],
			"editorStickyScroll.background": t["--viewer-bg"],
			"editorStickyScrollHover.background": t["--interactive-hover"],
			"editorWidget.background": t["--bg-1"],
			"editorWidget.foreground": t["--fg"],
			"editorWidget.border": t["--border-1"],
			"editorWidget.resizeBorder": accent,
			"editorHoverWidget.background": t["--bg-1"],
			"editorHoverWidget.border": t["--border-1"],
			"editorLink.activeForeground": accent,
			"input.background": t["--bg-2"],
			"input.foreground": t["--fg"],
			"input.border": t["--border-1"],
			"inputOption.activeBorder": accent,
			"inputOption.activeBackground": tint(accent, 0.16),
			"inputOption.activeForeground": t["--fg"],
			"icon.foreground": t["--fg-muted"],
			focusBorder: accent,
			"widget.shadow": "#00000059",
		},
		/**
		 * Order matters — see the header comment. Beyond that, TextMate resolves a
		 * token by the MOST SPECIFIC selector that matches its own scope, so a
		 * three-segment rule below always beats the one-segment rule above it; that
		 * is how `keyword.operator.type-casting` climbs back out of the plain rule.
		 */
		settings: [
			{ scope: ["comment", "punctuation.definition.comment"], settings: { foreground: t["--code-comment"] } },
			{ scope: ["string", "attribute.value"], settings: { foreground: t["--code-string"] } },
			// xcode.syntax.regex is the string colour in both of Xcode's Default themes.
			{ scope: ["regexp"], settings: { foreground: t["--code-string"] } },
			// Xcode has NO operator role: `=`, `->`, `!`, `?`, `+` are plain text.
			{ scope: ["keyword.operator"], settings: { foreground: t["--code-plain"] } },
			{
				scope: [
					"keyword",
					"storage",
					"constant.language",
					// `self`/`super`/`this` — a keyword in Xcode, not an identifier.
					"variable.language",
					// …and the WORD operators, which Xcode does colour as keywords.
					"keyword.operator.word",
					"keyword.operator.logical",
					"keyword.operator.expression",
					"keyword.operator.new",
					"keyword.operator.type-casting",
					"entity.name.tag",
					"tag",
					"markup.heading",
				],
				settings: { foreground: t["--code-keyword"] },
			},
			{
				// xcode.syntax.character carries the same value as .number.
				scope: ["constant.numeric", "number", "constant.character"],
				settings: { foreground: t["--code-number"] },
			},
			{
				scope: [
					"storage.modifier.attribute",
					"punctuation.definition.attribute",
					"meta.attribute",
					"keyword.other.attribute",
					// The `iOS 15.0, *` inside `@available`, which Xcode paints with the attribute.
					"keyword.other.platform",
					"storage.type.annotation",
					"entity.other.attribute-name",
					"attribute.name",
				],
				settings: { foreground: t["--code-attribute"] },
			},
			{
				scope: [
					"meta.preprocessor",
					"punctuation.definition.preprocessor",
					"keyword.control.import.preprocessor",
					"keyword.control.directive",
					"entity.name.function.preprocessor",
					"entity.name.function.macro",
					// `#selector(…)` and `#available(…)`.
					"support.function.selector-reference",
					"support.function.availability-condition",
				],
				settings: { foreground: t["--code-directive"] },
			},
			{
				scope: [
					// A type being DECLARED: the name in `class Foo` / `enum Kind`.
					"entity.name.type",
					"entity.name.class",
					"entity.name.namespace",
					"support.type.property-name",
					"type",
					"type.identifier",
					"markup.underline.link",
				],
				settings: { foreground: t["--code-type"] },
			},
			{
				// Anything else being DECLARED: the name in `func foo`. Grammars scope a
				// CALL as `support.function`/`meta.function-call`, so the split is real.
				scope: ["entity.name.function", "function"],
				settings: { foreground: t["--code-declaration"] },
			},
			{
				// A type being USED. Xcode splits this again by whether the type is the
				// SDK's; a grammar cannot know that, so every non-builtin lands here.
				scope: ["meta.type-name", "entity.other.inherited-class", "variable.language.generic-parameter"],
				settings: { foreground: t["--code-type-ref"] },
			},
			{
				scope: ["support.function", "meta.function-call", "variable.function", "markup.inline.raw"],
				settings: { foreground: t["--code-fn"] },
			},
			{
				// Types the grammar itself ships a list for — `Int`, `String`, `Bool`.
				scope: ["support.type", "support.class"],
				settings: { foreground: t["--code-type-system"] },
			},
			{ scope: ["markup.bold"], settings: { fontStyle: "bold" } },
			{ scope: ["markup.italic"], settings: { fontStyle: "italic" } },
			// LAST, always: see SEMANTIC_SCOPES. Appending keeps every colour's
			// FIRST rule a grammar scope, which is the invariant the minimap's
			// `// MARK:` bands hang on.
			...SEMANTIC_SCOPES.map((rule) => ({
				scope: [rule.scope],
				settings: { foreground: t[rule.role] },
			})),
		],
	};
}

export const AO_DARK_THEME = buildTheme("ao-dark", "dark", EDITOR_THEME_TOKENS.dark);
export const AO_LIGHT_THEME = buildTheme("ao-light", "light", EDITOR_THEME_TOKENS.light);

export type SyntaxRole = (typeof SYNTAX_ROLES)[number];

const ROLE_BY_COLOUR = new Map<string, SyntaxRole>(
	SYNTAX_ROLES.map((role) => [EDITOR_THEME_TOKENS.dark[role].toLowerCase(), role]),
);

/**
 * Scope → role, derived from the theme's OWN rules so it cannot drift from them.
 * Both themes are built by the same function, so their scope lists are identical
 * and one table serves both.
 *
 * This is what reads `monaco.editor.tokenize`'s answer: under `@shikijs/monaco` a
 * token's reported "scope" IS the first rule carrying its colour, so every string
 * that API can return is a key here.
 */
export const SCOPE_ROLES: ReadonlyMap<string, SyntaxRole> = new Map(
	AO_DARK_THEME.settings.flatMap((rule) => {
		const foreground = "foreground" in rule.settings ? rule.settings.foreground : undefined;
		const role = foreground ? ROLE_BY_COLOUR.get(foreground.toLowerCase()) : undefined;
		// 🗝 `?? []`, because shiki NORMALISES the theme object it is handed IN
		// PLACE: `createHighlighterCore` prepends a scope-less default rule
		// (`{ settings: { foreground, background } }`) to this very array. This runs
		// at import, before the highlighter exists, but a lazy rebuild would then
		// throw on a rule that has no `scope` - and it would throw from a module
		// nobody would think to look at.
		return role ? (rule.scope ?? []).map((scope) => [scope, role] as [string, SyntaxRole]) : [];
	}),
);

/**
 * The role the GRAMMAR gave a token, `--code-plain` where it gave it none.
 *
 * 🗝 An unstyled token does NOT come back with an empty scope: `--code-plain` is
 * both the editor's default foreground and the `keyword.operator` rule's colour,
 * so shiki reverse-maps every unscoped token to `"keyword.operator"`. Going
 * through this table rather than testing for `""` is what makes that harmless.
 */
export function grammarRole(scope: string): SyntaxRole {
	return SCOPE_ROLES.get(scope) ?? "--code-plain";
}
