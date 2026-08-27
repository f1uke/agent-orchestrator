import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { afterAll, describe, expect, test } from "vitest";
import { createLspRegistry, type LspRegistry } from "./lsp-registry";

/**
 * The bridge against a REAL gopls, on this repo's own Go module.
 *
 * Everything else in this directory runs against a fake server, which is right
 * for the supervisor's policy but proves nothing about whether a language server
 * actually answers. This file is the other half: it asserts that a cross-package
 * ⌘click resolves to the correct file and line, that `workspace/symbol` returns
 * hits, and that the process really is holding the memory the lifecycle exists
 * to bound.
 *
 * SKIPPED, not failed, when gopls or the backend module is missing - CI has
 * neither, and a test that cannot run is not a test that failed. When it skips,
 * it says so, because a silently-absent check is exactly the failure mode this
 * whole slice is about.
 */
const HERE = path.dirname(fileURLToPath(import.meta.url));
const BACKEND = path.resolve(HERE, "../../../../backend");

function goplsAvailable(): boolean {
	try {
		execFileSync("gopls", ["version"], { stdio: "ignore" });
		return true;
	} catch {
		return false;
	}
}

const CAN_RUN = goplsAvailable() && existsSync(path.join(BACKEND, "go.mod"));
if (!CAN_RUN) {
	console.warn(
		`[lsp] SKIPPING the real-gopls suite: gopls on PATH=${goplsAvailable()}, ${BACKEND}/go.mod exists=${existsSync(path.join(BACKEND, "go.mod"))}`,
	);
}

type PublishParams = { uri?: string; version?: number; diagnostics?: unknown[] };

/** A real cross-package reference in this repo, and where it is defined. */
const CALL_SITE = "internal/httpd/controllers/sessions.go";
const CALL_TEXT = "previewutil.ConfinedPath";
const DEFINITION_FILE = "internal/preview/entry.go";
const DEFINITION_SYMBOL = "ConfinedPath";

let registry: LspRegistry | null = null;
afterAll(async () => {
	await registry?.disposeAll();
});

describe.skipIf(!CAN_RUN)("gopls, for real", () => {
	test("a cross-package ⌘click resolves to the right file and line, and symbols answer", async () => {
		registry = createLspRegistry({
			dataDir: path.join(process.env.HOME ?? "/tmp", ".ao", "lsp-integration-test"),
			env: () => process.env,
			idleGraceMs: 1_000,
			onState: () => {},
			onMessage: (event) => {
				const id = event.message.id;
				if (typeof id === "number" && event.message.method === undefined) {
					inbox.get(id)?.(event.message);
					inbox.delete(id);
					return;
				}
				// 🗝 UNSOLICITED. `publishDiagnostics` answers no request, so a client
				// with no door for a notification drops every one and looks healthy
				// doing it - which is what this app did until this slice.
				if (event.message.method === "textDocument/publishDiagnostics") {
					published.push({ at: Date.now(), params: event.message.params as PublishParams });
				}
			},
		});
		const inbox = new Map<number, (m: Record<string, unknown>) => void>();
		const published: { at: number; params: PublishParams }[] = [];
		let nextId = 1;
		const attachment = await registry.attach({ root: BACKEND, languageId: "go" });
		// `attach` resolving at all is the readiness contract: main withholds it
		// until the handshake and the workspace load have settled.
		expect(attachment.state).toBe("ready");

		const request = (method: string, params: unknown) =>
			new Promise<Record<string, unknown>>((resolve) => {
				const id = nextId++;
				inbox.set(id, resolve);
				registry!.send(attachment.handleId, { jsonrpc: "2.0", id, method, params });
			});

		// Open the file, the way an editor pane does. gopls answers about
		// documents it knows; without this a definition request finds nothing.
		const callAbs = path.join(BACKEND, CALL_SITE);
		const text = readFileSync(callAbs, "utf8");
		registry.send(attachment.handleId, {
			jsonrpc: "2.0",
			method: "textDocument/didOpen",
			params: {
				textDocument: { uri: pathToFileURL(callAbs).href, languageId: "go", version: 1, text },
			},
		});

		// Find the reference by reading the file, so this does not rot into a
		// hardcoded line number the next time somebody edits sessions.go.
		const lines = text.split("\n");
		const lineIndex = lines.findIndex((l) => l.includes(CALL_TEXT));
		expect(lineIndex, `${CALL_SITE} no longer contains ${CALL_TEXT}`).toBeGreaterThanOrEqual(0);
		const character = lines[lineIndex].indexOf(CALL_TEXT) + "previewutil.".length + 1;

		const definition = await request("textDocument/definition", {
			textDocument: { uri: pathToFileURL(callAbs).href },
			position: { line: lineIndex, character },
		});

		// 🗝 The whole point of the slice: the editor MOVES. Assert on where to,
		// not merely on the absence of an error - a server that is up and
		// answering nothing looks identical to one that works, right up until
		// nobody can navigate.
		const result = definition.result as
			{ uri?: string; targetUri?: string; range?: unknown; targetSelectionRange?: unknown }[] | null;
		expect(Array.isArray(result) && result.length > 0, "definition returned nothing").toBe(true);
		const target = result![0];
		const targetUri = target.targetUri ?? target.uri ?? "";
		expect(targetUri).toContain(DEFINITION_FILE);

		const symbols = await request("workspace/symbol", { query: DEFINITION_SYMBOL });
		const hits = symbols.result as { name?: string }[] | null;
		expect(Array.isArray(hits) && hits.length > 0, "workspace/symbol returned nothing").toBe(true);
		expect(hits!.some((h) => h.name === DEFINITION_SYMBOL)).toBe(true);

		// The server is genuinely loaded, not a shell that answers from nothing.
		// A few hundred MB is the floor for a real Go dependency closure; this
		// asserts the lifecycle is bounding something real.
		const health = (await registry.health())[0];
		expect(health.state).toBe("ready");
		expect(health.rssMb ?? 0).toBeGreaterThan(200);

		// ── Completion, on the same connection. Slice 6 adds no plumbing; what it
		// adds is a policy, and the policy is only worth anything if the server
		// really answers with members of the receiver under the cursor.
		const completionAt = async (insert: string, label: string) => {
			const edited = [...lines];
			edited.splice(lineIndex, 0, insert);
			// A whole-document replacement, as a RANGE - both servers advertise
			// `textDocumentSync.change: 2`, so a full-text change event is not
			// something either of them agreed to accept.
			registry!.send(attachment.handleId, {
				jsonrpc: "2.0",
				method: "textDocument/didChange",
				params: {
					textDocument: { uri: pathToFileURL(callAbs).href, version: ++version },
					contentChanges: [
						{
							range: { start: { line: 0, character: 0 }, end: { line: previousLines, character: 0 } },
							text: edited.join("\n"),
						},
					],
				},
			});
			previousLines = edited.length - 1;
			const startedAt = Date.now();
			const answer = await request("textDocument/completion", {
				textDocument: { uri: pathToFileURL(callAbs).href },
				position: { line: lineIndex, character: insert.length },
				context: {
					triggerKind: insert.endsWith(".") ? 2 : 1,
					triggerCharacter: insert.endsWith(".") ? "." : undefined,
				},
			});
			const list = answer.result as { isIncomplete?: boolean; items?: { label: string }[] } | null;
			console.warn(
				`[measure] gopls completion "${label}": ${Date.now() - startedAt}ms, ${list?.items?.length ?? 0} items`,
			);
			return list;
		};
		let version = 1;
		let previousLines = lines.length - 1;

		// 🗝 gopls advertises `.` and NO resolveProvider - it ships detail and
		// documentation inline on every item. Asserted because Monaco reads the
		// trigger characters once, at registration, and because registering a
		// resolve handler against a server that has none swallows a MethodNotFound
		// per highlighted row.
		expect(attachment.completion?.triggerCharacters).toEqual(["."]);
		expect(attachment.completion?.resolveProvider).toBe(false);

		const members = await completionAt("\tstrings.", "strings.");
		// On WHAT it answered, never on the absence of an error.
		const names = (members?.items ?? []).map((i) => i.label);
		expect(names, "no members for `strings.`").toContain("TrimSpace");
		expect(names).toContain("Builder");
		// Both servers set this on every response, which is why the provider
		// re-requests per keystroke instead of filtering the previous list.
		expect(members?.isIncomplete).toBe(true);

		// The expensive shape on Go, and the one quick-suggestions fires on:
		// unqualified identifier completion measured 54-102 ms here against 1-3 ms
		// for a member, which is why the provider serialises rather than firing a
		// request per keystroke and cancelling the last.
		const qualified = await completionAt("\tpreviewutil.Confined", "previewutil.Confined");
		expect(
			(qualified?.items ?? []).map((i) => i.label),
			"no members for `previewutil.Confined`",
		).toContain(DEFINITION_SYMBOL);

		// 🗝 Put the buffer back the way it started before asking anything about a
		// POSITION. The completion probes above each inserted a line, so the call
		// site the line/character below name has moved - and a hover at a stale
		// position answers about whatever is there now, correctly and uselessly.
		// This is the same class of bug `document-sync.ts` exists to prevent, met
		// here in the harness rather than in the app.
		registry.send(attachment.handleId, {
			jsonrpc: "2.0",
			method: "textDocument/didChange",
			params: {
				textDocument: { uri: pathToFileURL(callAbs).href, version: ++version },
				contentChanges: [
					{ range: { start: { line: 0, character: 0 }, end: { line: previousLines, character: 0 } }, text },
				],
			},
		});
		previousLines = lines.length - 1;

		// ── Hover. The pacing decision this slice makes rests on ONE number: what
		// the first hover in a file costs against what a warm one costs. Measured
		// here through the shipping bridge rather than in a side harness.
		const hoverAt = async (label: string, line: number, character: number) => {
			const startedAt = Date.now();
			const answer = await request("textDocument/hover", {
				textDocument: { uri: pathToFileURL(callAbs).href },
				position: { line, character },
			});
			const contents = (answer.result as { contents?: { value?: string } } | null)?.contents;
			console.warn(`[measure] gopls hover ${label}: ${Date.now() - startedAt}ms`);
			return contents?.value ?? "";
		};
		// On WHAT it said, never on the absence of an error. gopls answers with
		// `MarkupContent` markdown whose first line is the declaration.
		const hovered = await hoverAt(DEFINITION_SYMBOL, lineIndex, character);
		expect(hovered, `hover over ${CALL_TEXT} said nothing`).toContain(DEFINITION_SYMBOL);
		await hoverAt("warm repeat", lineIndex, character);

		// ── References. The volume question the design turns on: 3-164 hits over
		// 1-35 files measured on this module, in 7-51 ms. The request is never the
		// expensive half; materialising a PREVIEW per file is.
		const referencesStartedAt = Date.now();
		const references = await request("textDocument/references", {
			textDocument: { uri: pathToFileURL(callAbs).href },
			position: { line: lineIndex, character },
			context: { includeDeclaration: true },
		});
		const hitList = (references.result ?? []) as { uri?: string }[];
		const files = new Set(hitList.map((h) => h.uri ?? ""));
		console.warn(
			`[measure] gopls references ${DEFINITION_SYMBOL}: ${Date.now() - referencesStartedAt}ms,` +
				` ${hitList.length} hits in ${files.size} files`,
		);
		// The declaration AND the call site, which is the difference between a
		// reference search and a definition jump.
		expect(hitList.length, "references returned nothing").toBeGreaterThan(1);
		expect(
			[...files].some((uri) => uri.includes(DEFINITION_FILE)),
			"references never reached the declaration",
		).toBe(true);
		expect([...files].some((uri) => uri.includes(CALL_SITE))).toBe(true);

		// ── Diagnostics. 🗝 gopls publishes TWICE after a file opens: an EMPTY set
		// at ~932 ms and the real one at ~5 010 ms. Which is why the app's header
		// never renders zero as a verdict — for four seconds it would be a lie.
		await new Promise((r) => setTimeout(r, 12_000));
		for (const message of published) {
			console.warn(
				`[measure] gopls publishDiagnostics: ${message.params.diagnostics?.length ?? 0} items,` +
					` version=${message.params.version}`,
			);
		}
		expect(published.length, "gopls published no diagnostics at all").toBeGreaterThan(0);
		// The version is what an out-of-order publish is judged on, and gopls is the
		// server that supplies one — sourcekit-lsp never does.
		expect(typeof published[published.length - 1].params.version).toBe("number");
		// Every publish addresses a document by URI, and the app matches on it. A
		// publish for a file nobody opened is normal; one for THIS file is what
		// makes the feature work at all.
		expect(published.some((m) => (m.params.uri ?? "").includes(CALL_SITE))).toBe(true);

		registry.detach(attachment.handleId);
	}, 240_000); // A cold GOPLSCACHE has to load this module's whole dependency closure.
});
