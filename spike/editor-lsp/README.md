# SPIKE — in-app editor with real language intelligence

**Throwaway. Not production code. Nothing here is imported by `frontend/` or
`backend/`, and nothing here should be.** It exists to answer one question with
measurements instead of opinions: can we get Xcode-grade navigation — Open
Quickly, correct highlighting, ⌘click-to-definition — inside our own app, for the
languages the team actually works in?

The full write-up, with every number and what could not be resolved, lives
outside this worktree:
`~/.ao/knowledge/agent-orchestrator/plans/spike-in-app-editor-lsp--proposal.md`

## What it proves

|                      | how                                                                 |
| -------------------- | ------------------------------------------------------------------- |
| ⌘⇧O over **files**   | filesystem/git index in the bridge, fuzzy-ranked in the renderer    |
| ⌘⇧O over **symbols** | `workspace/symbol` against a real language server                   |
| correct highlighting | TextMate grammars via shiki, no WASM (see CSP note below)           |
| ⌘click → definition  | `textDocument/definition`, wired into Monaco's own goto             |
| autocompletion       | `textDocument/completion` + `didChange`, in Monaco's suggest widget |

## Two modes

**Browse** is a normal editor — the whole project as a tree, open a file, edit
it, ⌘S. **Changes** is `monaco.editor.createDiffEditor` against the merge base.

Both share **one model per file**, so an edit made while reviewing a diff is the
same buffer Browse has open; two models for one path is how an edit silently
disappears. The diff's _modified_ side is editable and the _original_ is not —
the original only exists at the merge base.

## Two levels of "changed", never merged

```
branch       merge base(target, HEAD) .. working tree   everything this branch did
uncommitted  HEAD .. working tree                       what Discard Change can undo
```

The second is a subset of the first, so they get separate gutter lanes: a
neutral grey hairline for the branch level (its question is only "is this line
part of my change") and the coloured bar for uncommitted. Colouring both by kind
put two same-coloured bars side by side that read as one thick bar.

`LSP_BASE` picks the target ref; it falls back through `origin/main-fluke`,
`main-fluke`, `origin/main`, `main`. The daemon derives this from the PR target
instead (`workspace_changes.go`).

## One server per language, on first use

Nothing spawns until a file of that language is opened, and nothing stays alive
once nobody is looking at that language. Each language gets its own process and
its own socket (`/lsp?lang=<id>`); `LSP_LANG` only decides which one is warmed at
startup, and `GET /stats` lists what is actually alive.

Shutdown is two-sided: the renderer stops a client unused for `spike.idleMs`
(localStorage, default 60 s) whose language is on screen in neither pane, and the
bridge stops the process a grace period after its last socket closes
(`LSP_IDLE_MS`, default 15 s) through a real `shutdown`/`exit` handshake. Coming
back to the language starts it again — the sweep self-heals whatever is in front
of you.

The per-server floor is small — gopls + sourcekit-lsp together are 261 MB on a
small mixed workspace, against ~880 MB live for gopls alone on this repo's Go
backend. **The cost is the dependency graph, not the server count.**

## Shape

```
renderer (Monaco)  ──ws://127.0.0.1──▶  bridge (server.mjs)  ──stdio──▶  gopls / sourcekit-lsp
```

`server.mjs` stands in for the **Electron main process**. The renderer is
sandboxed (`contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`)
so it can never spawn a language server itself; the loopback WebSocket is
already permitted by the shipped CSP (`connect-src ws://127.0.0.1:*`).
`index.html` carries a **verbatim copy of the packaged renderer's CSP** so
anything that would be blocked in the real app is blocked here too.

## Run it

```bash
npm install

# control case — this repo's Go backend, via gopls
LSP_LANG=go LSP_ROOT="$PWD/../../backend" PORT=8917 npm run bridge
npm run dev            # http://localhost:5199

# the case the spike exists for — a real .xcworkspace iOS app, via sourcekit-lsp
LSP_LANG=swift LSP_ROOT=/path/to/ios/checkout PORT=8917 npm run bridge
```

Swift needs a `buildServer.json` + `.compile` produced by
[`xcode-build-server`](https://github.com/SolaWing/xcode-build-server) from a
**previous Xcode build**. Without it sourcekit-lsp answers _nothing_ — measured,
and it is the single most important finding in the proposal.

`npm run measure:bundle dist` reports what a build costs on disk versus what a
cold page load actually pulls.

## Things learned here that the real feature must not re-learn

- `monaco-editor@0.56` moved its export map: the worker is
  `monaco-editor/editor/editor.worker`, **not** `monaco-editor/esm/vs/…`.
- Importing `editor.api` plus hand-picked contributions is 3.5× smaller but
  **⌘click silently does nothing** — `editor.action.revealDefinition` never
  registers. The barrel is the supported entry.
- shiki's default oniguruma engine is WebAssembly; `script-src 'self'` blocks
  WASM in Chromium unless `'wasm-unsafe-eval'` is added. The JS regex engine
  avoids the CSP change.
- Definition and symbol results routinely point **outside** the workspace root
  (Pods, SDK headers, DerivedData). The editor needs a way to open those, which
  is what `/open-external` is.
- The Xcode-style minimap with `// MARK: - Helpers` printed as a labelled band
  needs **no code at all** — `showMarkSectionHeaders` is on by default. But the
  defaults `scale: 1` / `maxColumn: 120` middle-truncate the label
  ("User...ction"); `scale: 2` renders it in full. A hand-rolled overlay was
  written first and deleted once the built-in was found.
- **CORS is real on the loopback hop.** The renderer and the bridge are
  different origins, so a `POST` with a JSON content-type is preflighted. Without
  an `OPTIONS` handler the write fails as a bare `TypeError: Failed to fetch`.
- Reverting a hunk has to work in **whole lines**: replacing "line 96..96" with
  the empty string leaves an empty line 96 behind. The range must reach the start
  of the following line so the newline goes with it.
- A popover parented to the editor gets its `mousedown` eaten by Monaco, which
  closes it before the button sees a `click` — `stopPropagation` on the box. And
  `#editor` needs `position: relative`, or the popover resolves against `<body>`.
- **Turning on every language wakes Monaco's own language services.** Once
  ts/js/css/html/json models exist, those services demand their _dedicated_
  workers; handing them the plain editor worker throws a stream of
  `Missing requestHandler or method: getSyntacticDiagnostics`. Ship the four
  workers (they are lazy chunks) or switch the services off.
- Completion needs `textDocument/didChange` or the server answers about the file
  as it was opened; and every response came back `isIncomplete: true`, so the
  client must re-request rather than filter locally.
- An Xcode index store also holds generated symbols (`ImageResource.…`, `_R.…`)
  and one copy per built arch. Unranked and undeduped, they bury the real
  results. See `rank()` and the dedupe in `src/main.ts`.
