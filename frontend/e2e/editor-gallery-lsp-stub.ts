import { GALLERY_OTHER_PATH, GALLERY_PATH } from "./editor-gallery-api-stub";
import { SWIFT_FIXTURE } from "./editor-fixture";

/**
 * A language server for the harness page, speaking the real protocol over the
 * real bridge shape.
 *
 * Faked here: only the process. Everything the spec then measures is the app's
 * own - `useLanguageServer`'s attach, `createLspClient`'s framing and id
 * matching, the provider registration, Monaco asking for tokens, the mapping,
 * the theme resolving `ao.*` to a colour, and the browser painting it. That is
 * the chain five silent failures have been paid for on this feature, and the
 * only way to prove it is a COLOUR on screen.
 *
 * The legend is sourcekit-lsp's own, verbatim from its `initialize` reply on the
 * real iOS app, so the indices the spec encodes are the indices the real server
 * sends.
 */

export const SOURCEKIT_LEGEND = {
	tokenTypes: [
		"namespace",
		"type",
		"class",
		"enum",
		"interface",
		"struct",
		"typeParameter",
		"parameter",
		"variable",
		"property",
		"enumMember",
		"event",
		"function",
		"method",
		"macro",
		"keyword",
		"modifier",
		"comment",
		"string",
		"number",
		"regexp",
		"operator",
		"decorator",
		"bracket",
		"label",
		"concept",
		"unknown",
		"identifier",
	],
	tokenModifiers: [
		"declaration",
		"definition",
		"readonly",
		"static",
		"deprecated",
		"abstract",
		"async",
		"modification",
		"documentation",
		"defaultLibrary",
		"deduced",
		"virtual",
		"dependentName",
		"usedAsMutableReference",
		"usedAsMutablePointer",
		"constructorOrDestructor",
		"userDefined",
		"functionScope",
		"classScope",
		"fileScope",
		"globalScope",
	],
};

export const GALLERY_WORKSPACE_ROOT = "/gallery/workspace";

/**
 * What the fake server "knows" about the fixture, by TEXT rather than by line -
 * the fixture is edited for other reasons, and a spec that hardcoded line 27
 * would fail for a reason that has nothing to do with colour.
 *
 * Each entry is one occurrence: the first match wins, which is enough because
 * every word here appears in a distinct role.
 */
const KNOWN: { word: string; type: string; modifiers: string[]; occurrence?: number }[] = [
	// A property DECLARATION. The grammar leaves it plain - this is the gap the
	// whole slice exists to close.
	{ word: "reuseIdentifier", type: "identifier", modifiers: [] },
	// A property REFERENCE: `offers.count`, where `offers` was declared above.
	{ word: "offers", type: "property", modifiers: [], occurrence: 2 },
	// A local inside a STRING INTERPOLATION. The Swift grammar calls the whole
	// `"helper-\(total)"` a string; Xcode colours the expression as code.
	{ word: "total", type: "variable", modifiers: [], occurrence: 2 },
	// An SDK class. The grammar can only call it a project type.
	{ word: "UIViewController", type: "class", modifiers: ["defaultLibrary"] },
	// A TYPE declaration, which the grammar DOES know: it must keep its own
	// colour rather than take the declaration-other one.
	{ word: "PromotionHubViewController", type: "identifier", modifiers: [] },
	// An operator, which Xcode has no colour for. Reported as an SDK method.
	{ word: "=", type: "method", modifiers: ["static", "defaultLibrary"] },
];

function tokensForFixture(): number[] {
	const lines = SWIFT_FIXTURE.split("\n");
	const found: { line: number; character: number; length: number; type: number; modifiers: number }[] = [];
	for (const entry of KNOWN) {
		let seen = 0;
		const wanted = entry.occurrence ?? 1;
		for (let line = 0; line < lines.length; line++) {
			const pattern = new RegExp(`(?<![\\w$])${entry.word.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?![\\w$])`, "g");
			for (const match of lines[line].matchAll(pattern)) {
				if (++seen !== wanted) continue;
				found.push({
					line,
					character: match.index,
					length: entry.word.length,
					type: SOURCEKIT_LEGEND.tokenTypes.indexOf(entry.type),
					modifiers: entry.modifiers.reduce((bits, m) => bits | (1 << SOURCEKIT_LEGEND.tokenModifiers.indexOf(m)), 0),
				});
				break;
			}
			if (seen === wanted) break;
		}
	}
	// The wire format is relative, and relative to the PREVIOUS token - so the
	// server's answer has to be sorted, exactly as a real one is.
	found.sort((a, b) => a.line - b.line || a.character - b.character);
	const data: number[] = [];
	let line = 0;
	let character = 0;
	for (const token of found) {
		const deltaLine = token.line - line;
		data.push(
			deltaLine,
			deltaLine === 0 ? token.character - character : token.character,
			token.length,
			token.type,
			token.modifiers,
		);
		line = token.line;
		character = token.character;
	}
	return data;
}

type Message = Record<string, unknown>;

/**
 * sourcekit-lsp's own completion capability, verbatim from its `initialize`
 * reply on the real iOS app. `(` matters as much as `.`: it is what gives Swift
 * argument-label completion, and a client that guessed `["."]` would lose it
 * with nothing to see.
 */
export const SOURCEKIT_COMPLETION = { resolveProvider: true, triggerCharacters: [".", "("] };

/**
 * What the fake answers for `viewModel.`, shaped exactly like sourcekit-lsp's
 * real reply on the iOS app: a `textEdit` rather than an `insertText`, a numeric
 * `sortText` carrying the server's own ranking, `insertTextFormat: 2` on the
 * method, `labelDetails` on it too, and documentation withheld until resolved.
 */
const MEMBERS = [
	{ label: "offersCount", kind: 10, detail: "Int", sortText: "4998.10-offersCount" },
	{
		label: "configure(userDefaultManager:)",
		kind: 2,
		detail: "Void",
		labelDetails: { detail: "(userDefaultManager: any UserDefaultManagerProtocol)" },
		filterText: "configure(userDefaultManager:)",
		insertTextFormat: 2,
		insertText: "configure(userDefaultManager: ${1:any UserDefaultManagerProtocol})",
		sortText: "4998.20-configure",
	},
	{ label: "offersTitle", kind: 10, detail: "String", sortText: "4998.30-offersTitle" },
	// 🗝 The 200-item cap, in miniature. This one is ABSENT from the answer for
	// the bare `.` and appears only once the prefix narrows - which is what
	// `isIncomplete: true` means on the real server, and why the provider
	// re-requests instead of filtering the list it already has.
	{ label: "offersDeepCut", kind: 10, detail: "Bool", sortText: "4998.05-offersDeepCut", deepOnly: true },
];

/**
 * Where the fixture's own symbol is DECLARED and where it is USED, found by text
 * so the fixture can be edited for other reasons without breaking a spec.
 *
 * `offers` is the right needle: it is declared once and read once in this file,
 * which is what makes a two-file reference answer legible.
 */
function occurrencesOf(word: string): { line: number; character: number }[] {
	const lines = SWIFT_FIXTURE.split("\n");
	const found: { line: number; character: number }[] = [];
	for (let line = 0; line < lines.length; line++) {
		const pattern = new RegExp(`(?<![\\w$])${word}(?![\\w$])`, "g");
		for (const match of lines[line].matchAll(pattern)) found.push({ line, character: match.index });
	}
	return found;
}

/** A range that covers `word` at `at`. */
const rangeAt = (at: { line: number; character: number }, word: string) => ({
	start: at,
	end: { line: at.line, character: at.character + word.length },
});

/**
 * The diagnostic the spec looks for, placed on the fixture's own `page`
 * declaration by TEXT rather than by line number.
 *
 * Shaped exactly like a real sourcekit-lsp one, captured on the iOS app:
 * `severity: 2`, `source: "SourceKit"`, and `tags: []`.
 */
function fixtureDiagnostics(): unknown[] {
	const page = occurrencesOf("page")[0];
	const reuse = occurrencesOf("reuseIdentifier")[0];
	if (!page || !reuse) return [];
	return [
		{
			range: rangeAt(page, "page"),
			severity: 1,
			source: "SourceKit",
			tags: [],
			message: "cannot find type 'Paginator' in scope",
		},
		{
			range: rangeAt(reuse, "reuseIdentifier"),
			severity: 2,
			source: "SourceKit",
			tags: [],
			message: "variable 'reuseIdentifier' was never mutated",
		},
	];
}

/** Installs the bridge `useLanguageServer` looks for. Call before React mounts. */
export function installFakeLspBridge(
	options: {
		completionDelayMs?: number;
		failAttach?: string;
		/**
		 * How long after `didOpen` the first publish arrives. Real servers take
		 * seconds — gopls publishes an EMPTY set at ~932 ms and the real one at
		 * ~5 010 ms; sourcekit-lsp publishes once at ~3 325 ms — but a spec that
		 * waited that long would be measuring the clock.
		 */
		diagnosticsDelayMs?: number;
		/** Answer no hover / no references, so a spec can ask what is SAID about it. */
		features?: { hover: boolean; references: boolean };
	} = {},
): void {
	const listeners = new Set<(event: { handleId: string; message: Message }) => void>();
	let handles = 0;
	// Read by the spec: a request that was never made is a different failure from
	// one that was answered and thrown away.
	const asked: string[] = [];
	(globalThis as { __aoLspAsked?: string[] }).__aoLspAsked = asked;
	// How many completions are on the wire at once. The whole policy this slice
	// ships is "one", and a count is the only way to see it from outside.
	const wire = { inFlight: 0, peak: 0, sent: 0 };
	(globalThis as { __aoLspWire?: typeof wire }).__aoLspWire = wire;

	const answer = (handleId: string, id: unknown, result: unknown, afterMs: number) =>
		setTimeout(() => {
			for (const listener of listeners) listener({ handleId, message: { jsonrpc: "2.0", id, result } });
		}, afterMs);

	(globalThis as { ao?: unknown }).ao = {
		lsp: {
			attach: async () => {
				if (options.failAttach) throw new Error(options.failAttach);
				const handleId = `fake-${++handles}`;
				return {
					handleId,
					state: "ready" as const,
					detail: "fake sourcekit-lsp",
					documentRoot: GALLERY_WORKSPACE_ROOT,
					semanticTokens: SOURCEKIT_LEGEND,
					completion: SOURCEKIT_COMPLETION,
					features: options.features ?? { hover: true, references: true },
				};
			},
			detach: () => undefined,
			send: (handleId: string, message: Message) => {
				const method = message.method as string | undefined;
				if (method) asked.push(method);
				// 🗝 UNSOLICITED, exactly as both real servers do it: nothing asks for
				// diagnostics, so a client with no door for a notification drops every
				// one of them and looks perfectly healthy doing it.
				if (method === "textDocument/didOpen") {
					const uri = (message.params as { textDocument: { uri: string } }).textDocument.uri;
					setTimeout(() => {
						for (const listener of listeners) {
							listener({
								handleId,
								message: {
									jsonrpc: "2.0",
									method: "textDocument/publishDiagnostics",
									// No `version` — sourcekit-lsp sends none, ever.
									params: { uri, diagnostics: fixtureDiagnostics() },
								},
							});
						}
					}, options.diagnosticsDelayMs ?? 40);
					return;
				}
				if (message.id === undefined) return;
				if (method === "textDocument/hover") {
					const position = (message.params as { position: { line: number; character: number } }).position;
					answer(handleId, message.id, hoverAt(position), 5);
					return;
				}
				if (method === "textDocument/references") {
					const uri = (message.params as { textDocument: { uri: string } }).textDocument.uri;
					// Two files, deliberately: one hit in the file the reader is in, one
					// in a file that has no model yet. The second is the only one that
					// can prove the preview was materialised.
					const other = uri.replace(GALLERY_PATH, GALLERY_OTHER_PATH);
					answer(
						handleId,
						message.id,
						[
							...occurrencesOf("offers").map((at) => ({ uri, range: rangeAt(at, "offers") })),
							{
								uri: other,
								range: { start: { line: 8, character: 21 }, end: { line: 8, character: 27 } },
							},
						],
						5,
					);
					return;
				}
				if (method === "textDocument/definition") {
					// Into the OTHER file, so the peek preview has something to show that
					// the current pane could not have supplied.
					const uri = (message.params as { textDocument: { uri: string } }).textDocument.uri;
					answer(
						handleId,
						message.id,
						[
							{
								uri: uri.replace(GALLERY_PATH, GALLERY_OTHER_PATH),
								range: { start: { line: 2, character: 7 }, end: { line: 2, character: 12 } },
							},
						],
						5,
					);
					return;
				}
				if (method === "textDocument/semanticTokens/full") {
					// Asynchronously, like a real one: a synchronous answer would hide a
					// provider that only works when the reply is already in hand.
					answer(handleId, message.id, { data: tokensForFixture() }, 10);
					return;
				}
				if (method === "textDocument/completion") {
					const params = message.params as { position: { line: number; character: number } };
					const prefix = currentPrefix(params.position);
					const items = MEMBERS.filter(
						(m) => (!m.deepOnly || prefix.length >= 6) && m.label.toLowerCase().startsWith(prefix.toLowerCase()),
					).map(({ deepOnly: _deepOnly, ...item }) => ({
						...item,
						// A `textEdit`, not an `insertText`: sourcekit-lsp answers this
						// way, and honouring the range is what makes the insert correct.
						textEdit: {
							newText: item.insertText ?? item.label,
							range: {
								start: { line: params.position.line, character: params.position.character - prefix.length },
								end: { line: params.position.line, character: params.position.character },
							},
						},
					}));
					wire.sent++;
					wire.inFlight++;
					wire.peak = Math.max(wire.peak, wire.inFlight);
					const delay = options.completionDelayMs ?? 5;
					setTimeout(() => {
						wire.inFlight--;
						for (const listener of listeners) {
							listener({
								handleId,
								message: { jsonrpc: "2.0", id: message.id, result: { isIncomplete: true, items } },
							});
						}
					}, delay);
					return;
				}
				if (method === "completionItem/resolve") {
					const item = message.params as { label: string };
					answer(
						handleId,
						message.id,
						{ ...item, documentation: { kind: "markdown", value: `**${item.label}** — resolved on demand.` } },
						5,
					);
				}
			},
			noteResult: () => undefined,
			onMessage: (cb: (event: { handleId: string; message: Message }) => void) => {
				listeners.add(cb);
				return () => listeners.delete(cb);
			},
			onState: () => () => undefined,
		},
	};
}

/**
 * The type of the word at a position, read out of the LIVE model — so a hover
 * asked about an EDITED buffer answers about the text the reader can see, which
 * is what `document-sync.ts` exists to make true.
 *
 * Shaped like sourcekit-lsp's own reply: a `MarkupContent` whose value is
 * already a fenced Swift block.
 */
function hoverAt(position: { line: number; character: number }): unknown {
	const monaco = (globalThis as { __monaco?: { editor: { getModels(): { getLineContent(n: number): string }[] } } })
		.__monaco;
	const model = monaco?.editor.getModels()[0];
	if (!model) return null;
	const line = model.getLineContent(position.line + 1);
	const before = /[A-Za-z0-9_]*$/.exec(line.slice(0, position.character))?.[0] ?? "";
	const after = /^[A-Za-z0-9_]*/.exec(line.slice(position.character))?.[0] ?? "";
	const word = `${before}${after}`;
	// Nothing under the pointer is a legitimate answer, and the whole point of
	// hover's "not lying" rule is that it looks the same as a server that is
	// still starting.
	if (word === "") return null;
	return {
		contents: { kind: "markdown", value: `\`\`\`swift\nlet ${word}: PromotionOffer\n\`\`\`` },
		range: {
			start: { line: position.line, character: position.character - before.length },
			end: { line: position.line, character: position.character + after.length },
		},
	};
}

/**
 * The word the cursor is sitting after, read out of the LIVE model rather than
 * out of the fixture - the point of the spec is that the server sees the buffer
 * as it is being typed, so a fake that answered from the saved text would hide
 * exactly the bug `document-sync.ts` exists to prevent.
 */
function currentPrefix(position: { line: number; character: number }): string {
	const monaco = (globalThis as { __monaco?: { editor: { getModels(): { getLineContent(n: number): string }[] } } })
		.__monaco;
	const model = monaco?.editor.getModels()[0];
	if (!model) return "";
	const line = model.getLineContent(position.line + 1).slice(0, position.character);
	return /[A-Za-z0-9_]*$/.exec(line)?.[0] ?? "";
}
