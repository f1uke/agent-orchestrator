import type { editor, MarkerSeverity } from "monaco-editor";

/**
 * The whole-line tint behind a diagnostic - Xcode's band, from the markers
 * `diagnostics.ts` already publishes.
 *
 * ## What Monaco gives you, and the one line it stops at
 *
 * Checked against monaco-editor 0.56's own sources rather than assumed, because
 * this project has twice paid for building what the platform already had.
 *
 * `setModelMarkers` alone (`markerDecorationsService.js:_createDecorationOption`)
 * already draws the squiggle, the overview-ruler mark, the minimap mark and the
 * hover row. It builds its decoration with `className: "squiggly-error"` and
 * **no `isWholeLine`** - so everything it paints is bounded by the marker's own
 * range, which is one token wide.
 *
 * 🗝 And it goes one step further than the brief expected: `editor.css:84` gives
 * `.squiggly-error::before` a `background: var(--vscode-editorError-background)`,
 * so a theme naming `editorError.background` gets a tint **behind the offending
 * token** for free. That is the platform already covering the colour half of
 * this request - just not at line width. Deliberately left unset: a token-width
 * tint plus a line-width tint on the same characters is two colours stacked on
 * the code a reader is trying to read.
 *
 * Line width is a decoration with `isWholeLine`, and that is the whole of what
 * this module builds. The markers stay exactly as they were, so the ruler and
 * minimap marks #259 shipped are untouched.
 *
 * ## Density is the design problem, not the CSS
 *
 * The human's own Xcode screenshot showed 3 284 warnings. Three rules keep a
 * warning-heavy file readable, and all three are here rather than in CSS because
 * they are the part that can be wrong:
 *
 * 1. **One band per LINE, not per diagnostic.** Translucent bands stack: five
 *    warnings on one line would multiply into a near-opaque slab, and the line
 *    with the most diagnostics would be the least readable line in the file.
 * 2. **The worst severity on the line wins.** An error sharing a line with a
 *    warning must read as an error.
 * 3. **The line the diagnostic STARTS on, never the range it spans.** A server
 *    is entitled to report an unterminated block as a forty-line range; painting
 *    all forty makes the band's size a function of the construct rather than of
 *    the problem. The squiggle still spans the whole range - only the band is
 *    anchored.
 *
 * Info and hint carry no band at all. They are advisory, they are numerous, and
 * #259 already renders both in the ruler and the hover.
 */

/**
 * `TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges`, spelled as the literal
 * Monaco's own marker decorations use - `satisfies` proves the pairing through a
 * type-only import, so this module never loads the barrel. Same stickiness as
 * the squiggle it sits under: typing at the end of a line must not drag the band
 * onto the next one.
 */
const STICKINESS = 1 as const satisfies editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges;

/**
 * Monaco's `MarkerSeverity` is a BITMASK counting UP - `Hint 1, Info 2,
 * Warning 4, Error 8` - which is the trap `diagnostics-mapping.ts` documents at
 * length. Named here rather than spelled, and proved by `satisfies` the same
 * way, so a future reader cannot mistake the 8 for an LSP severity.
 */
const SEVERITY_ERROR = 8 as const satisfies MarkerSeverity.Error;
const SEVERITY_WARNING = 4 as const satisfies MarkerSeverity.Warning;

/** The two classes, so `styles.css` and the tests name the same strings. */
export const DIAGNOSTIC_LINE_CLASS = "ao-diagnostic-line";
export const DIAGNOSTIC_LINE_ERROR_CLASS = `${DIAGNOSTIC_LINE_CLASS} ${DIAGNOSTIC_LINE_CLASS}--error`;
export const DIAGNOSTIC_LINE_WARNING_CLASS = `${DIAGNOSTIC_LINE_CLASS} ${DIAGNOSTIC_LINE_CLASS}--warning`;

/**
 * The band for one set of markers: at most one per line, worst severity wins,
 * in ascending line order so a delta against the previous set is minimal.
 *
 * Sorted deliberately. `deltaDecorations` diffs positionally, so an unsorted
 * list churns ids on every publish for a file whose diagnostics did not move.
 */
export function diagnosticLineDecorations(markers: readonly editor.IMarkerData[]): editor.IModelDeltaDecoration[] {
	const worst = new Map<number, MarkerSeverity>();
	for (const marker of markers) {
		if (marker.severity !== SEVERITY_ERROR && marker.severity !== SEVERITY_WARNING) continue;
		const line = marker.startLineNumber;
		if (!Number.isFinite(line) || line < 1) continue;
		const seen = worst.get(line);
		// The bitmask counts up, so a plain `>` IS "worse".
		if (seen === undefined || marker.severity > seen) worst.set(line, marker.severity);
	}
	return [...worst.entries()]
		.sort((a, b) => a[0] - b[0])
		.map(([line, severity]) => ({
			range: { startLineNumber: line, startColumn: 1, endLineNumber: line, endColumn: 1 },
			options: {
				description: "ao-diagnostic-line",
				isWholeLine: true,
				className: severity === SEVERITY_ERROR ? DIAGNOSTIC_LINE_ERROR_CLASS : DIAGNOSTIC_LINE_WARNING_CLASS,
				// Under the selection and the find highlight, which a reader is
				// actively steering, and above nothing - the band is the quietest
				// thing on the line by design.
				zIndex: severity === SEVERITY_ERROR ? 2 : 1,
				stickiness: STICKINESS,
				// 🗝 No `overviewRuler` and no `minimap` entry. Monaco's own marker
				// decorations already put this diagnostic in both, in the colours
				// `monaco-theme.ts` names; a second entry would double every mark.
			},
		}));
}
