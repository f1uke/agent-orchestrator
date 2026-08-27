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

/** Installs the bridge `useLanguageServer` looks for. Call before React mounts. */
export function installFakeLspBridge(options: { completionDelayMs?: number; failAttach?: string } = {}): void {
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
				return {
					handleId: `fake-${++handles}`,
					state: "ready" as const,
					detail: "fake sourcekit-lsp",
					documentRoot: GALLERY_WORKSPACE_ROOT,
					semanticTokens: SOURCEKIT_LEGEND,
					completion: SOURCEKIT_COMPLETION,
				};
			},
			detach: () => undefined,
			send: (handleId: string, message: Message) => {
				const method = message.method as string | undefined;
				if (method) asked.push(method);
				if (message.id === undefined) return;
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
