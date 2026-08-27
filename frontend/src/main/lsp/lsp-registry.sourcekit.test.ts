import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { documentUriForPath } from "../../renderer/lib/lsp/lsp-uri";
import { createLspRegistry, type LspRegistry } from "./lsp-registry";

/**
 * The bridge against a REAL sourcekit-lsp.
 *
 * 🗝 ⌘click is proved by WHERE IT LANDS, never by the absence of an error. This
 * whole slice exists because an unconfigured sourcekit-lsp answers `initialize`
 * in 60 ms, publishes diagnostics, answers `documentSymbol` and returns 0 hits
 * for every definition - so "no error" is exactly what total failure looks like,
 * and a test that asserts it would pass against a server doing nothing at all.
 *
 * SKIPPED, loudly, without a Swift toolchain. CI has none; a check that cannot
 * run is not a check that failed, but a check that is silently absent is the
 * same failure mode this slice is about.
 *
 * ⚠️ SCOPE. This drives the SwiftPM branch, because that is the only Swift
 * workspace that can be built from nothing inside a test. The Xcode
 * BUILD-SERVER branch - the one the human's iOS app uses - needs a real
 * `.xcodeproj`, a real Xcode build and `xcode-build-server`, none of which
 * exist here; it is covered by `scripts/measure-sourcekit.mjs` against a real
 * checkout, and by the smoke checklist. What this file proves is that the
 * catalogue, the readiness gate, the document mapping and the registry
 * lifecycle carry a real sourcekit-lsp end to end.
 */
function swiftToolchain(): boolean {
	try {
		execFileSync("sourcekit-lsp", ["--help"], { stdio: "ignore" });
		execFileSync("swift", ["--version"], { stdio: "ignore" });
		return true;
	} catch {
		return false;
	}
}

const CAN_RUN = swiftToolchain();
if (!CAN_RUN) {
	console.warn("[lsp] SKIPPING the real-sourcekit-lsp suite: no `sourcekit-lsp` and `swift` on PATH.");
}

/** A two-file package, so a definition has to CROSS a file to be right. */
const GREETING = `public struct Greeting {
    public let text: String
    public init(text: String) { self.text = text }
}
`;
const GREETER = `public enum Greeter {
    public static func make() -> Greeting {
        return Greeting(text: "hi")
    }
}
`;

let pkg: string;
let dataDir: string;
let registry: LspRegistry | null = null;

beforeAll(() => {
	if (!CAN_RUN) return;
	const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "ao-sourcekit-"));
	pkg = path.join(tmp, "Greeter");
	dataDir = path.join(tmp, "data");
	fs.mkdirSync(path.join(pkg, "Sources", "Greeter"), { recursive: true });
	fs.writeFileSync(
		path.join(pkg, "Package.swift"),
		'// swift-tools-version:5.9\nimport PackageDescription\nlet package = Package(name: "Greeter", targets: [.target(name: "Greeter")])\n',
	);
	fs.writeFileSync(path.join(pkg, "Sources", "Greeter", "Greeting.swift"), GREETING);
	fs.writeFileSync(path.join(pkg, "Sources", "Greeter", "Greeter.swift"), GREETER);
});

afterAll(async () => {
	await registry?.disposeAll();
});

describe.skipIf(!CAN_RUN)("sourcekit-lsp, for real", () => {
	test(
		"a cross-file ⌘click lands in the right file and line, and the package stays untouched",
		{ timeout: 120_000 },
		async () => {
			const before = fs.readdirSync(pkg).sort();
			registry = createLspRegistry({
				dataDir,
				env: () => process.env,
				idleGraceMs: 1_000,
				onState: () => {},
				onMessage: (event) => {
					const message = event.message as { id?: number; result?: unknown };
					if (typeof message.id === "number") answers.get(message.id)?.(message.result);
				},
			});
			const answers = new Map<number, (result: unknown) => void>();
			let nextId = 1;

			const attachment = await registry.attach({ root: pkg, languageId: "swift" });
			// A SwiftPM package is served in place, so the mapping is the identity -
			// but the renderer asks for it the same way either way, which is the point.
			expect(attachment.documentRoot).toBe(pkg);
			expect(attachment.warning).toMatch(/symbol search is off/i);

			const request = (method: string, params: unknown) =>
				new Promise<unknown>((resolve) => {
					const id = nextId++;
					answers.set(id, resolve);
					registry?.send(attachment.handleId, { jsonrpc: "2.0", id, method, params });
				});

			const callSite = path.join(pkg, "Sources", "Greeter", "Greeter.swift");
			const uri = documentUriForPath(callSite, { workspaceRoot: pkg, documentRoot: attachment.documentRoot });
			registry.send(attachment.handleId, {
				jsonrpc: "2.0",
				method: "textDocument/didOpen",
				params: { textDocument: { uri, languageId: "swift", version: 1, text: GREETER } },
			});

			// The readiness gate, exercised for real: `workspace/synchronize` is what
			// the process supervisor sends, and `initialized` resolving does not mean
			// the index has loaded. Waiting for `ready` here is the same wait the
			// palette does before it will show a single symbol row.
			await vi_waitForReady(registry, attachment.key);

			const lines = GREETER.split("\n");
			const line = lines.findIndex((l) => l.includes("Greeting(text:"));
			const result = (await request("textDocument/definition", {
				textDocument: { uri },
				position: { line, character: lines[line].indexOf("Greeting") + 2 },
			})) as { uri?: string; range?: { start?: { line?: number } } }[] | null;

			const locations = (result ?? []).map((l) => l.uri ?? "");
			// WHERE IT LANDS. Not "no error", not "a non-empty array".
			expect(locations.length).toBeGreaterThan(0);
			expect(locations.every((l) => l.endsWith("/Greeting.swift"))).toBe(true);
			expect(result?.[0]?.range?.start?.line).toBe(0);

			// ── Completion, on the same connection. Slice 6 adds no plumbing to
			// this seam; what it adds is a policy, and the policy is worth nothing
			// unless the server really answers with members of the receiver.
			expect(attachment.completion?.resolveProvider, "sourcekit-lsp advertises resolve").toBe(true);
			expect(attachment.completion?.triggerCharacters).toContain(".");
			// `(` as well as `.`, which is what gives Swift argument-label
			// completion. A renderer that guessed `["."]` would silently lose it.
			expect(attachment.completion?.triggerCharacters).toContain("(");

			const edited = GREETER.replace(
				'return Greeting(text: "hi")',
				'        Greeter.\n        return Greeting(text: "hi")',
			);
			registry.send(attachment.handleId, {
				jsonrpc: "2.0",
				method: "textDocument/didChange",
				params: {
					textDocument: { uri, version: 2 },
					// A whole-document replacement expressed as a RANGE: sourcekit-lsp
					// advertises `textDocumentSync.change: 2`, so a full-text change
					// event is not something it ever agreed to accept.
					contentChanges: [
						{
							range: {
								start: { line: 0, character: 0 },
								end: { line: GREETER.split("\n").length - 1, character: 0 },
							},
							text: edited,
						},
					],
				},
			});
			const dotLine = edited.split("\n").findIndex((l) => l.trim() === "Greeter.");
			const startedCompletion = Date.now();
			const completion = (await request("textDocument/completion", {
				textDocument: { uri },
				position: { line: dotLine, character: edited.split("\n")[dotLine].length },
				context: { triggerKind: 2, triggerCharacter: "." },
			})) as { isIncomplete?: boolean; items?: { label: string; data?: unknown }[] } | null;
			console.warn(
				`[measure] sourcekit completion "Greeter." → ${Date.now() - startedCompletion}ms, ${completion?.items?.length ?? 0} items`,
			);
			// On WHAT it answered. A misconfigured sourcekit-lsp returns an empty
			// list in 60 ms and logs nothing, which is what success looks like to
			// any assertion weaker than this one.
			expect(
				(completion?.items ?? []).map((i) => i.label),
				"no members for `Greeter.`",
			).toContain("make()");
			// Both servers set this on every response. The provider honours it and
			// re-requests, because a longer prefix really does return items the
			// shorter one omitted - measured, 6 of 9 on the real iOS app.
			expect(completion?.isIncomplete).toBe(true);

			const item = (completion?.items ?? []).find((i) => i.label === "make()");
			const resolved = (await request("completionItem/resolve", item)) as { detail?: string } | null;
			// Resolve is what keeps 200 doc comments from being fetched to show one.
			expect(resolved, "completionItem/resolve answered nothing").toBeTruthy();

			// 🗝 And nothing was written into the package. sourcekit-lsp's background
			// indexer writes `.build/index-build` straight into a SwiftPM checkout,
			// ignoring both `--scratch-path` and `swiftPM.scratchPath`, so this
			// assertion is the AO hard rule and "never touch the user's repo" at once.
			expect(fs.readdirSync(pkg).sort()).toEqual(before);
			expect(fs.existsSync(path.join(pkg, ".build"))).toBe(false);
		},
	);
});

/** Poll health until the server reports `ready`, or give up loudly. */
async function vi_waitForReady(registry: LspRegistry, key: string): Promise<void> {
	const deadline = Date.now() + 90_000;
	for (;;) {
		const entry = (await registry.health()).find((h) => h.key === key);
		if (entry?.state === "ready") return;
		if (entry?.state === "failed") throw new Error(`sourcekit-lsp failed: ${entry.detail}`);
		if (Date.now() > deadline) throw new Error(`sourcekit-lsp never became ready (last state ${entry?.state})`);
		await new Promise((r) => setTimeout(r, 250));
	}
}

/**
 * The BUILD-SERVER branch, against a real Xcode checkout, opt-in:
 *
 *   AO_LSP_SWIFT_PROJECT=/path/to/checkout \
 *   AO_LSP_XCODE_BUILD_SERVER=/path/to/xcode-build-server \
 *   npx vitest run src/main/lsp/lsp-registry.sourcekit
 *
 * 🗝 This is the measurement harness as well as the test, deliberately: the
 * numbers in the record come from the code that ships rather than from a
 * parallel harness. The editor spike published 246 MB for a Swift server and
 * 493 MB for gopls, both wrong, both from harnesses that differed from the app -
 * so this drives the real registry, opens a real document through the real
 * document mapping, and reports RSS the way the app's own health panel does.
 *
 * It needs the project to have been BUILT in Xcode at least once. It cannot
 * build one itself: `xcodebuild` does not run under the agent sandbox, so every
 * Swift number this produces assumes a pre-existing build.
 */
const SWIFT_PROJECT = process.env.AO_LSP_SWIFT_PROJECT;
const DEFINITION_TARGETS = (process.env.AO_LSP_SWIFT_TARGETS ?? "").split(";").filter(Boolean);

describe.skipIf(!CAN_RUN || !SWIFT_PROJECT)("a real Xcode project", () => {
	test("every ⌘click lands where it should, and the checkout is not touched", { timeout: 300_000 }, async () => {
		const root = SWIFT_PROJECT as string;
		const openFile = process.env.AO_LSP_SWIFT_FILE as string;
		if (!openFile || DEFINITION_TARGETS.length === 0) {
			throw new Error(
				"AO_LSP_SWIFT_FILE=<workspace-relative .swift> and AO_LSP_SWIFT_TARGETS='needle=>Expected.swift;…' are required",
			);
		}
		const answers = new Map<number, (result: unknown) => void>();
		let nextId = 1;
		const real = createLspRegistry({
			dataDir: path.join(os.homedir(), ".ao", "lsp-measure"),
			env: () => process.env,
			idleGraceMs: 1_000,
			onState: (e) => console.warn(`[measure] ${e.state}${e.detail ? ` — ${e.detail}` : ""}`),
			onMessage: (event) => {
				const message = event.message as { id?: number; result?: unknown };
				if (typeof message.id === "number") answers.get(message.id)?.(message.result);
			},
		});
		registry = real;

		const startedAt = Date.now();
		const attachment = await real.attach({ root, languageId: "swift" });
		console.warn(`[measure] attach → ${Date.now() - startedAt}ms, documentRoot=${attachment.documentRoot}`);
		// The shadow root, not the checkout: getting this wrong is the failure that
		// leaves symbol search working and every ⌘click silently empty.
		expect(attachment.documentRoot).not.toBe(root);

		const absolute = path.join(root, openFile);
		const text = fs.readFileSync(absolute, "utf8");
		const uri = documentUriForPath(absolute, { workspaceRoot: root, documentRoot: attachment.documentRoot });
		real.send(attachment.handleId, {
			jsonrpc: "2.0",
			method: "textDocument/didOpen",
			params: { textDocument: { uri, languageId: "swift", version: 1, text } },
		});

		await vi_waitForReady(real, attachment.key);
		console.warn(`[measure] ready → ${Date.now() - startedAt}ms`);

		const request = (method: string, params: unknown) =>
			new Promise<unknown>((resolve) => {
				const id = nextId++;
				answers.set(id, resolve);
				real.send(attachment.handleId, { jsonrpc: "2.0", id, method, params });
			});

		const lines = text.split("\n");
		for (const target of DEFINITION_TARGETS) {
			const [needle, expected] = target.split("=>");
			const line = lines.findIndex((l) => l.includes(needle));
			expect(line, `needle not found in ${openFile}: ${needle}`).toBeGreaterThanOrEqual(0);
			const at = Date.now();
			const result = (await request("textDocument/definition", {
				textDocument: { uri },
				position: { line, character: lines[line].indexOf(needle) + 2 },
			})) as { uri?: string }[] | null;
			const locations = (result ?? []).map((l) => l.uri ?? "");
			console.warn(`[measure] ⌘click ${needle} → ${Date.now() - at}ms  ${locations[0] ?? "(0 hits)"}`);
			// 0 hits is what a MISCONFIGURED server returns, in 60 ms, with no error.
			expect(
				locations.some((l) => l.endsWith(`/${expected}`)),
				`${needle} → ${locations.join(", ") || "0 hits"}`,
			).toBe(true);
		}

		const symbolQuery = process.env.AO_LSP_SWIFT_SYMBOL;
		if (symbolQuery) {
			const at = Date.now();
			const hits = ((await request("workspace/symbol", { query: symbolQuery })) ?? []) as {
				name?: string;
				location?: { uri?: string };
			}[];
			console.warn(
				`[measure] workspace/symbol "${symbolQuery}" → ${hits.length} hits in ${Date.now() - at}ms :: ` +
					hits.map((h) => `${h.name}@${(h.location?.uri ?? "").split("/").pop()}`).join(" | "),
			);
			expect(hits.length).toBeGreaterThan(0);
		}

		// ── Completion on the real project: the numbers this slice's whole design
		// rests on. `AO_LSP_SWIFT_COMPLETE='<line needle>=><member prefix ladder>'`,
		// e.g. `super.viewDidLoad()=>emailLabel.:n:nu:num:numb`.
		const completeSpec = process.env.AO_LSP_SWIFT_COMPLETE;
		if (completeSpec) {
			const [anchor, ladder] = completeSpec.split("=>");
			const anchorLine = lines.findIndex((l) => l.includes(anchor));
			expect(anchorLine, `completion anchor not found: ${anchor}`).toBeGreaterThanOrEqual(0);
			const [base, ...steps] = ladder.split(":");
			let version = 1;
			let previousLines = lines.length - 1;
			const firstList: string[] = [];

			for (const [index, suffix] of ["", ...steps].entries()) {
				const insert = `        ${base}${suffix}`;
				const edited = [...lines];
				edited.splice(anchorLine + 1, 0, insert);
				real.send(attachment.handleId, {
					jsonrpc: "2.0",
					method: "textDocument/didChange",
					params: {
						textDocument: { uri, version: ++version },
						contentChanges: [
							{
								range: { start: { line: 0, character: 0 }, end: { line: previousLines, character: 0 } },
								text: edited.join("\n"),
							},
						],
					},
				});
				previousLines = edited.length - 1;
				const at = Date.now();
				const list = (await request("textDocument/completion", {
					textDocument: { uri },
					position: { line: anchorLine + 1, character: insert.length },
					context: index === 0 ? { triggerKind: 2, triggerCharacter: "." } : { triggerKind: 1 },
				})) as { isIncomplete?: boolean; items?: { label: string; filterText?: string }[] } | null;
				const labels = (list?.items ?? []).map((i) => i.filterText ?? i.label);
				if (index === 0) firstList.push(...labels);
				// 🗝 THE measurement this slice turns on: how many of the items the
				// server returns for a LONGER prefix were absent from the list it
				// gave for the shorter one - i.e. how many a local filter over the
				// previous answer would have thrown away.
				const absent = labels.filter((l) => !firstList.includes(l)).length;
				console.warn(
					`[measure] completion "${base}${suffix}" → ${Date.now() - at}ms, ${labels.length} items` +
						(index === 0 ? "" : `, ${absent} of them absent from the first list`),
				);
				expect(labels.length, `completion "${base}${suffix}" answered nothing`).toBeGreaterThan(0);
				expect(list?.isIncomplete).toBe(true);
			}
		}

		const health = (await real.health()).find((h) => h.key === attachment.key);
		// The whole cost, not the visible third of it: server + build server + the
		// SourceKitService XPC that carries ppid=1 and no relationship to us.
		console.warn(`[measure] RSS ${health?.rssMb} MB (peak ${health?.peakRssMb} MB), state=${health?.state}`);
		expect(health?.rssMb ?? 0).toBeGreaterThan(0);
	});
});
