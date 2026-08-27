import type { editor, MarkerSeverity, MarkerTag } from "monaco-editor";

/**
 * Turning `textDocument/publishDiagnostics` into Monaco markers.
 *
 * Kept apart from `diagnostics.ts` because that file imports the monaco BARREL,
 * which boots the whole editor on import — so the arithmetic and the enum
 * translation, which is where this file's two real bugs would live, stay
 * testable without a DOM. Same split as `definition-mapping.ts`, and the reason
 * every monaco import here is `import type`.
 */

export type LspPosition = { line: number; character: number };
export type LspRange = { start: LspPosition; end: LspPosition };

export type LspDiagnostic = {
	range: LspRange;
	severity?: number;
	code?: string | number;
	source?: string;
	message: string;
	tags?: number[];
	relatedInformation?: { location?: { uri?: string; range?: LspRange }; message?: string }[];
};

export type PublishDiagnosticsParams = {
	uri?: string;
	/**
	 * 🗝 Only gopls fills this in. Measured on the real iOS app: every
	 * `publishDiagnostics` from sourcekit-lsp carries `version: undefined`, on
	 * open and on change alike.
	 */
	version?: number;
	diagnostics?: LspDiagnostic[];
};

/**
 * 🗝 THE TRAP OF THIS SLICE, and it is #258's completion-kind bug wearing a new
 * hat.
 *
 * LSP numbers its severities `1 Error, 2 Warning, 3 Information, 4 Hint` — a
 * dense list counting DOWN in urgency. Monaco's `MarkerSeverity` is a BITMASK
 * counting UP: `Hint 1, Info 2, Warning 4, Error 8`. The two lists overlap on
 * every value they use, so a plain cast type-checks, renders a complete and
 * entirely plausible set of squiggles, and gets every severity wrong — an LSP
 * error becomes a Monaco hint, which draws no squiggle at all.
 *
 * `satisfies SeverityMap` is the guard, and it is a real one: `MarkerSeverity.Error`
 * is reachable as a TYPE through a type-only import, so tsc proves each pairing
 * without the barrel being loaded. Verified by mutation — changing `1: 8` to
 * `1: 4` fails with "Type '4' is not assignable to type 'MarkerSeverity.Error'".
 * The run-time test additionally asserts that no LSP severity maps to ITSELF by
 * accident, without which a naive cast would have passed.
 */
type SeverityMap = {
	1: MarkerSeverity.Error;
	2: MarkerSeverity.Warning;
	3: MarkerSeverity.Info;
	4: MarkerSeverity.Hint;
};
const SEVERITY = { 1: 8, 2: 4, 3: 2, 4: 1 } as const satisfies SeverityMap;

/**
 * The tag lists happen to agree — LSP `1 Unnecessary, 2 Deprecated`, Monaco
 * `Unnecessary 1, Deprecated 2` — which is exactly why they are written out and
 * asserted rather than cast. An agreement nobody checked is the same thing as a
 * coincidence.
 */
type TagMap = { 1: MarkerTag.Unnecessary; 2: MarkerTag.Deprecated };
const TAG = { 1: 1, 2: 2 } as const satisfies TagMap;

/** Exported so the tests assert the tables rather than a description of them. */
export const SEVERITY_TABLE: Readonly<Record<number, MarkerSeverity>> = SEVERITY;
export const TAG_TABLE: Readonly<Record<number, MarkerTag>> = TAG;

/** Monaco's own `MarkerSeverity.Error`, named rather than spelled `8` at call sites. */
export const MARKER_ERROR: MarkerSeverity = SEVERITY[1];
export const MARKER_WARNING: MarkerSeverity = SEVERITY[2];

function severityOf(severity: number | undefined): MarkerSeverity {
	// The spec makes `severity` optional and leaves the reading to the client.
	// Anything but "treat it as an error" hides a real problem behind a colour
	// nobody looks at.
	return SEVERITY[severity as keyof typeof SEVERITY] ?? MARKER_ERROR;
}

/**
 * LSP is 0-based in both axes, Monaco is 1-based in both.
 *
 * 🗝 And a ZERO-WIDTH range has to be widened. Servers issue them — "expected
 * declaration" at the end of a line, an unterminated block — and Monaco draws
 * exactly nothing for a marker whose start equals its end, so the squiggle a
 * reader is meant to see would be absent while the marker is present and
 * counted: a diagnostic that exists in the header and nowhere on the line.
 * Widening by one column is what makes it visible; the message is unchanged.
 */
export function toMonacoMarkerRange(range: LspRange): {
	startLineNumber: number;
	startColumn: number;
	endLineNumber: number;
	endColumn: number;
} {
	const startLineNumber = range.start.line + 1;
	const startColumn = range.start.character + 1;
	let endLineNumber = range.end.line + 1;
	let endColumn = range.end.character + 1;
	if (endLineNumber < startLineNumber || (endLineNumber === startLineNumber && endColumn < startColumn)) {
		// A server is entitled to be wrong about its own ordering; Monaco is not
		// obliged to cope. Collapsing to the start keeps the marker on the line it
		// names instead of letting it select backwards across the file.
		endLineNumber = startLineNumber;
		endColumn = startColumn;
	}
	if (endLineNumber === startLineNumber && endColumn === startColumn) endColumn = startColumn + 1;
	return { startLineNumber, startColumn, endLineNumber, endColumn };
}

function isRange(value: unknown): value is LspRange {
	const r = value as LspRange | undefined;
	return (
		typeof r?.start?.line === "number" &&
		typeof r.start.character === "number" &&
		typeof r.end?.line === "number" &&
		typeof r.end.character === "number"
	);
}

/**
 * One `publishDiagnostics` payload as Monaco markers.
 *
 * `relatedUri` maps a related location's `file:` URI into whatever URI world the
 * caller's models live in; a related item whose file cannot be addressed is
 * dropped rather than pointed at a resource that will not resolve — an
 * unresolvable related-information entry renders as a row that goes nowhere.
 *
 * `codeDescription` (the clickable documentation link) is deliberately NOT
 * carried: Monaco's shape for it needs a `monaco.Uri`, which would drag the
 * barrel into this module, and neither gopls nor sourcekit-lsp sends one.
 */
export function toMonacoMarkers<TUri>(
	diagnostics: readonly LspDiagnostic[] | undefined,
	relatedUri: (uri: string) => TUri | null,
): editor.IMarkerData[] {
	const markers: editor.IMarkerData[] = [];
	for (const diagnostic of diagnostics ?? []) {
		if (!diagnostic || typeof diagnostic !== "object") continue;
		if (!isRange(diagnostic.range) || typeof diagnostic.message !== "string") continue;
		const tags = (diagnostic.tags ?? [])
			.map((tag) => TAG[tag as keyof typeof TAG])
			.filter((tag): tag is MarkerTag => tag !== undefined);
		const related = (diagnostic.relatedInformation ?? [])
			.map((item) => {
				const uri = item?.location?.uri;
				const range = item?.location?.range;
				if (typeof uri !== "string" || !isRange(range) || typeof item.message !== "string") return null;
				const resource = relatedUri(uri);
				if (resource === null) return null;
				return { resource, message: item.message, ...toMonacoMarkerRange(range) };
			})
			.filter((item): item is NonNullable<typeof item> => item !== null);
		markers.push({
			...toMonacoMarkerRange(diagnostic.range),
			severity: severityOf(diagnostic.severity),
			message: diagnostic.message,
			// `source` is what puts "gopls" or "swiftc" in front of the message in
			// the hover, which is the difference between "this app dislikes your
			// code" and "the compiler does".
			source: diagnostic.source,
			code: diagnostic.code === undefined ? undefined : String(diagnostic.code),
			...(tags.length > 0 ? { tags } : {}),
			...(related.length > 0 ? { relatedInformation: related as editor.IMarkerData["relatedInformation"] } : {}),
		});
	}
	return markers;
}

/**
 * Errors and warnings.
 *
 * 🗝 Counted, but never rendered as a zero. gopls's FIRST publish after opening
 * a file is empty and arrives ~932 ms in; the real one lands at ~5 010 ms. A
 * header that says "no problems" in between is lying for four seconds, and this
 * feature's whole job is to not do that.
 */
export function countMarkers(markers: readonly editor.IMarkerData[]): { errors: number; warnings: number } {
	let errors = 0;
	let warnings = 0;
	for (const marker of markers) {
		if (marker.severity === MARKER_ERROR) errors++;
		else if (marker.severity === MARKER_WARNING) warnings++;
	}
	return { errors, warnings };
}
