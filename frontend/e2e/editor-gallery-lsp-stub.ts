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

/** Installs the bridge `useLanguageServer` looks for. Call before React mounts. */
export function installFakeLspBridge(): void {
	const listeners = new Set<(event: { handleId: string; message: Message }) => void>();
	let handles = 0;
	// Read by the spec: a request that was never made is a different failure from
	// one that was answered and thrown away.
	const asked: string[] = [];
	(globalThis as { __aoLspAsked?: string[] }).__aoLspAsked = asked;

	(globalThis as { ao?: unknown }).ao = {
		lsp: {
			attach: async () => ({
				handleId: `fake-${++handles}`,
				state: "ready" as const,
				detail: "fake sourcekit-lsp",
				documentRoot: GALLERY_WORKSPACE_ROOT,
				semanticTokens: SOURCEKIT_LEGEND,
			}),
			detach: () => undefined,
			send: (handleId: string, message: Message) => {
				const method = message.method as string | undefined;
				if (method) asked.push(method);
				if (method !== "textDocument/semanticTokens/full" || message.id === undefined) return;
				// Asynchronously, like a real one: a synchronous answer would hide a
				// provider that only works when the reply is already in hand.
				setTimeout(() => {
					for (const listener of listeners) {
						listener({ handleId, message: { jsonrpc: "2.0", id: message.id, result: { data: tokensForFixture() } } });
					}
				}, 10);
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
