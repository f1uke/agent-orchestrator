/**
 * Monaco's own themes do not match this app, so the editor gets two themes built
 * from the app's OWN tokens: the seven `--code-*` syntax roles (Xcode "Midnight"
 * in dark, Xcode "Default" in light — the same palette `DiffRows` paints) plus
 * the surface/chrome tokens around them. `monaco-theme.test.ts` re-parses
 * `styles.css` and fails if any value here drifts from the token it claims.
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
		"--code-keyword": "#fc5fa3",
		"--code-string": "#fc6a5d",
		"--code-comment": "#6c7986",
		"--code-number": "#d0bf69",
		"--code-type": "#5dd8ff",
		"--code-fn": "#67b7a4",
		"--code-plain": "#e8e8ea",
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
	},
	light: {
		"--code-keyword": "#9b2393",
		"--code-string": "#c41a16",
		"--code-comment": "#5d6c79",
		"--code-number": "#1c00cf",
		"--code-type": "#0b4f79",
		"--code-fn": "#266b60",
		"--code-plain": "#1a1a1a",
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
	},
} as const;

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
		// Order matters — see the header comment.
		settings: [
			{ scope: ["comment", "punctuation.definition.comment"], settings: { foreground: t["--code-comment"] } },
			{ scope: ["string", "attribute.value"], settings: { foreground: t["--code-string"] } },
			{ scope: ["regexp"], settings: { foreground: t["--code-string"] } },
			{
				scope: ["keyword", "storage", "constant.language", "entity.name.tag", "tag", "markup.heading"],
				settings: { foreground: t["--code-keyword"] },
			},
			{
				scope: ["constant.numeric", "number", "constant.character.escape"],
				settings: { foreground: t["--code-number"] },
			},
			{
				scope: [
					"entity.name.type",
					"entity.name.class",
					"entity.name.namespace",
					"support.type",
					"support.class",
					"support.type.property-name",
					"entity.other.attribute-name",
					"type",
					"type.identifier",
					"attribute.name",
					"markup.underline.link",
				],
				settings: { foreground: t["--code-type"] },
			},
			{
				scope: [
					"entity.name.function",
					"support.function",
					"meta.function-call",
					"variable.function",
					"function",
					"markup.inline.raw",
				],
				settings: { foreground: t["--code-fn"] },
			},
			{ scope: ["markup.bold"], settings: { fontStyle: "bold" } },
			{ scope: ["markup.italic"], settings: { fontStyle: "italic" } },
		],
	};
}

export const AO_DARK_THEME = buildTheme("ao-dark", "dark", EDITOR_THEME_TOKENS.dark);
export const AO_LIGHT_THEME = buildTheme("ao-light", "light", EDITOR_THEME_TOKENS.light);
