# Run xcodegen — "Open in…" menu action

**Date:** 2026-07-08
**Status:** Approved design (worker session `feature/openin-run-xcodegen`)

## Goal

Add a new **Run xcodegen** action to the existing session/project "Open in…"
menu. When triggered it must:

1. Recursively search the session's working directory (worktree root) —
   including nested subfolders — for files named exactly `project.yml`
   (xcodegen spec files).
2. Run `xcodegen generate` in **every** directory that contains a `project.yml`
   (a project can be multi-module with several specs), each with the directory
   as the working directory.
3. Surface output and errors: which directories ran, per-directory success /
   failure with the command output, and a clear, friendly message when
   `xcodegen` is not installed / not on `PATH`.

Noise/heavy directories are skipped during the search so it is fast and does not
pick up junk: any dot-directory (`.git`, `.claude/worktrees/*`, …) plus a
heavy-name set (`node_modules`, `Pods`, `Carthage`, `DerivedData`, `build`,
`.build`).

## Context: the existing "Open in…" menu (what we extend)

A macOS-only, terminal-toolbar `Share`-icon dropdown. Entirely a frontend
concern — the Go daemon is not involved. Detection + launching run in the
Electron **main** process; the renderer reaches them via `contextBridge` IPC.

- **UI:** `frontend/src/renderer/components/OpenInMenu.tsx` — shadcn
  `dropdown-menu` (unified `radix-ui` package), lucide icons. Items are
  individual `<DropdownMenuItem>`s. `directory` prop originates in
  `SessionView.tsx` as `session?.workspacePath ?? workspace?.path` (worker
  worktree, falling back to the project root for orchestrators).
- **Bridge:** `frontend/src/preload.ts` `openIn.*` → `ipcRenderer.invoke("openIn:…")`;
  channel names are inline string literals duplicated in `preload.ts` +
  `main.ts`. Renderer accessor `frontend/src/renderer/lib/bridge.ts` provides a
  browser-preview fallback stub (must satisfy `AoBridge`).
- **Main handlers:** `frontend/src/main.ts` `ipcMain.handle("openIn:…")` →
  launchers in `frontend/src/main/open-in.ts` (all via `spawn("open", …)`).
- **Pure/tested logic:** `frontend/src/main/open-in-targets.ts` (+ `.test.ts`);
  `OpenInMenu.test.tsx`.
- **PATH:** `main.ts` resolves and caches the login-shell env once at startup
  (`ensureShellEnv` → `cachedShellEnv`); `withFallbackPath` /`buildDaemonEnv` in
  `frontend/src/shared/shell-env.ts` produce a `PATH` that includes
  `/opt/homebrew/bin`. This is how a spawned `xcodegen` (installed via Homebrew)
  becomes resolvable from a Finder/Dock launch.

## Why a new module (not folded into `open-in.ts`)

`open-in.ts` launchers all go through `spawn("open", …)` for a single target.
"Run xcodegen" is different in kind: a recursive filesystem walk + a CLI spawn
(`xcodegen generate`, not `open`) run once **per** matching directory, plus
result aggregation and a "not installed" outcome. Folding that into `open-in.ts`
would mix concerns and bloat it. A dedicated, dependency-injected module keeps
each unit single-purpose and unit-testable without fs/spawn — matching the
existing `open-in-targets.ts` / `shell-env.ts` testing convention.

## Components

### 1. `frontend/src/main/run-xcodegen.ts` (new)

Pure, dependency-injected. Types:

```ts
export type XcodegenDirResult = {
  dir: string;           // path relative to the searched root ("." for root)
  ok: boolean;
  exitCode: number | null;
  output: string;        // combined stdout+stderr, trimmed
};

export type RunXcodegenResult =
  | { status: "not-installed" }
  | { status: "no-specs"; root: string }
  | { status: "ran"; root: string; results: XcodegenDirResult[] };
```

Functions:

- `shouldSkipDir(name: string): boolean` — `name.startsWith(".") ||
  IGNORED_DIR_NAMES.has(name)` where `IGNORED_DIR_NAMES = { node_modules, Pods,
  Carthage, DerivedData, build, .build }`. Exported + unit-tested.
- `findProjectSpecDirs(root, readdir): Promise<string[]>` — recursive walk;
  returns every directory (absolute) that directly contains a `project.yml`,
  skipping `shouldSkipDir` subdirs. `readdir` is injected (defaults to
  `fs.promises.readdir` with `withFileTypes`) so the walk is testable against an
  in-memory fake tree. Deterministic order (sorted).
- `runXcodegen(root, opts): Promise<RunXcodegenResult>` where
  `opts = { env, readdir?, runOne? }`:
  - `runOne(dir, env): Promise<{ notFound: true } | { ok, exitCode, output }>` —
    injected; default spawns `xcodegen generate` with `cwd: dir`, the given
    `env`, capturing stdout+stderr; resolves `{ notFound: true }` on an `ENOENT`
    spawn error.
  - Behaviour: find spec dirs → none ⇒ `{ status: "no-specs", root }`. Otherwise
    run **sequentially** in each dir; if any `runOne` reports `notFound`, abort
    and return `{ status: "not-installed" }` (the tool is globally missing, so
    remaining dirs would fail identically). Else aggregate
    `{ status: "ran", root, results }`. Non-zero exits are captured per dir
    (`ok: false`) — a failed generate is a surfaced result, not an exception.

Sequential (not parallel) because the real specs' `postGenCommand` runs
`pod install` / writes git config, which can contend if run concurrently. The
typical case is 1–2 specs, so latency is a non-issue.

### 2. Wiring (mirrors the existing `openIn:*` slice)

- `frontend/src/main.ts`: import `runXcodegen` + `withFallbackPath`; register
  `ipcMain.handle("openIn:xcodegen", async (_e, dir) => { await
  ensureShellEnv(); const env = { ...process.env, PATH:
  withFallbackPath(cachedShellEnv?.PATH ?? process.env.PATH) }; return
  runXcodegen(dir, { env }); })`. macOS-only guard consistent with the other
  handlers (return `{ status: "no-specs" }`-shaped early off darwin, or rely on
  the menu being hidden off-mac — the menu already hides off-mac).
- `frontend/src/preload.ts`: add
  `xcodegen: (dir: string) => ipcRenderer.invoke("openIn:xcodegen", dir) as
  Promise<RunXcodegenResult>` under `openIn`; import `RunXcodegenResult` type.
- `frontend/src/renderer/lib/bridge.ts`: add fallback stub
  `xcodegen: async () => ({ status: "no-specs", root: "" })`.

### 3. UI

- `OpenInMenu.tsx`: new `<DropdownMenuItem>` "Run xcodegen" (lucide `Wrench`),
  in its own separated section after the openers. **Always shown** when the menu
  shows (macOS + a `directory`) — NOT gated on root Xcode detection, because the
  real spec is nested (`NterApp/project.yml`) and root detection would wrongly
  hide it. Empty searches report "no project.yml found" in the results surface
  instead. Selecting it opens the results Sheet in a running state and invokes
  `aoBridge.openIn.xcodegen(directory)`.
- `frontend/src/renderer/components/XcodegenResultSheet.tsx` (new) — reuses the
  existing **`Sheet`** primitive (radix Dialog-based; zero new deps). States:
  - **running:** spinner + "Running xcodegen…".
  - **not-installed:** friendly copy — xcodegen isn't installed / on PATH;
    install with `brew install xcodegen`.
  - **no-specs:** "No `project.yml` found under `<root>`."
  - **ran:** header summary (e.g. "Generated N of M"), then a scrollable list;
    each dir shows ✓/✗, its relative path, and its combined output in a mono
    `<pre>` inside an `overflow-y-auto` region.
  - **unexpected error** (invoke rejected): a generic failure message.

  Chosen over the existing 4s toast because the requirement — multi-directory
  output + per-dir errors + missing-tool guidance — needs a roomy, scrollable,
  dismissible surface. Sheet is already in the design system (`components/ui/sheet.tsx`).

## Output / error surfacing summary

| Case | Surface |
| --- | --- |
| Search running / generate in progress | Sheet, spinner |
| Ran, all/some dirs | Sheet, per-dir ✓/✗ + scrollable output |
| No `project.yml` found | Sheet, friendly empty state |
| `xcodegen` not installed / not on PATH | Sheet, `brew install xcodegen` hint |
| IPC/unexpected failure | Sheet, generic error |

## Testing

- `frontend/src/main/run-xcodegen.test.ts` (new):
  - `shouldSkipDir` — skips dot-dirs + heavy names, keeps normal dirs.
  - `findProjectSpecDirs` — injected fake tree: finds nested spec
    (`NterApp/project.yml`), finds multiple, skips `.git`/`.claude`/`node_modules`/`Pods`.
  - `runXcodegen` — injected `readdir` + `runOne`: `ran` aggregation
    (success + non-zero mix), `no-specs`, `not-installed` (runOne ⇒ notFound).
- `frontend/src/renderer/components/OpenInMenu.test.tsx` (extend): "Run
  xcodegen" item present; selecting it calls `aoBridge.openIn.xcodegen(directory)`
  and opens the Sheet; assert a `ran` result renders dir + status, and the
  `not-installed` state renders the brew hint.

## Verification

- `cd frontend && npm run typecheck`
- `cd frontend && npm run test`
- Backend untouched (no daemon/API changes) — no Go regen needed.
- Demo the menu item + results Sheet via `ao preview` (renderer with mock/fallback
  data) per repo convention.

## Non-goals / deliberate omissions

- No live streaming of xcodegen stdout (single IPC invoke resolves with the full
  result; spinner in the meantime). Can add event-based streaming later if long
  runs warrant it.
- No daemon/API surface — consistent with the existing "Open in…" feature being
  a pure frontend/main-process concern.
- Item is not gated on detecting a spec (avoids an eager recursive walk on every
  menu open and the nested-spec hiding bug); could add cheap gating later.
