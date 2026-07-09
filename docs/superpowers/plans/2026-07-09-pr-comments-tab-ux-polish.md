# PR Comments Tab — UX Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make the Comments tab easier to scan and use — syntax-highlighted diffs, resolved threads collapsed by default, a collapsible reply composer, and a cleaner file header.

**Architecture:** Frontend-only (React renderer). Add `highlight.js` (core + a curated language set) for per-line diff highlighting behind a small `lib/highlight.ts` seam; give the highlight tokens their own CSS-variable palette in `styles.css` (dark + light), the sanctioned "code keeps its own palette" exception. Resolved-thread collapse and the reply-composer toggle are plain `useState` (no accordion primitive exists, and the Send-to-worker panel already sets this precedent).

**Tech Stack:** React + TanStack Query, Tailwind v4 (CSS-var tokens in `styles.css`), highlight.js, vitest + testing-library.

## Global Constraints

- **Design system:** renderer clones agent-orchestrator verbatim (DESIGN.md); build from `components/ui/*` primitives (`Button`, `Textarea`) and existing design tokens. Highlight token colors are the ONE new palette allowed (defined as `--hl-*` CSS vars in `styles.css`, NOT inline hex in components) — analogous to the terminal keeping its own colors.
- **Theming:** dark is `:root`, light is `:root[data-theme="light"]` in `frontend/src/renderer/styles.css`. Every `--hl-*` var MUST be defined in BOTH blocks.
- **Per-line highlighting** is intentional for v1 (multi-line strings/comments may not carry highlighting across lines). Do NOT attempt whole-hunk stateful highlighting — the add/del interleaving makes it unsafe. Note the limitation in a code comment.
- **`dangerouslySetInnerHTML` is used to render highlighted code.** This is safe ONLY because both code paths escape HTML: `hljs.highlight` escapes by default, and the no-language fallback MUST call an `escapeHtml`. The fallback returning raw text would be an XSS hole — the escape is mandatory, and a test must lock it.
- **No backend changes.** (Relative timestamps are deliberately out of scope — stored `createdAt` is observe-time, not authoring-time; doing it right needs a backend fetch.)
- Revert any `routeTree.gen.ts` / `pnpm-lock.yaml` churn; do not commit it.
- Keep the existing behavior intact: Send-to-worker button stays; reply/resolve still call the same hooks; optimistic cache still works.

---

### Task 1: Syntax highlighting infra + DiffHunk (highlight + copy button + tighter rows)

**Files:**
- Modify: `frontend/package.json` (add `highlight.js`)
- Create: `frontend/src/renderer/lib/highlight.ts`
- Create: `frontend/src/renderer/lib/highlight.test.ts`
- Modify: `frontend/src/renderer/styles.css` (add `--hl-*` vars + `.hljs-*` rules)
- Modify: `frontend/src/renderer/components/DiffHunk.tsx`
- Modify: `frontend/src/renderer/components/DiffHunk.test.tsx` (if it exists; else create)

**Interfaces:**
- Produces: `languageForPath(path: string): string | null`; `highlightLine(text: string, language: string | null): string` (returns HTML-escaped, hljs-marked-up string).

- [ ] **Step 1: Install highlight.js** — `cd frontend && npm install highlight.js`. Confirm it lands in `dependencies` (not dev). Revert any unrelated lockfile churn beyond the highlight.js addition.

- [ ] **Step 2: Write failing tests** `highlight.test.ts`:
  - `languageForPath("a/b/File.swift")` → `"swift"`; `"x.tsx"` → `"typescript"`; `"Makefile"` (no ext) → `null`; `".unknownext"` → `null`.
  - `highlightLine("let x = 1", "swift")` contains an `hljs-` class span and the text `x`.
  - **Escape test (security):** `highlightLine("<script>", null)` returns `"&lt;script&gt;"` (no raw `<script>`), and `highlightLine("a < b && c", null)` escapes `<` and `&`.
  Run → FAIL (module missing).

- [ ] **Step 3: Implement `highlight.ts`:**
  ```ts
  import hljs from "highlight.js/lib/core";
  import bash from "highlight.js/lib/languages/bash";
  import c from "highlight.js/lib/languages/c";
  import cpp from "highlight.js/lib/languages/cpp";
  import csharp from "highlight.js/lib/languages/csharp";
  import css from "highlight.js/lib/languages/css";
  import go from "highlight.js/lib/languages/go";
  import java from "highlight.js/lib/languages/java";
  import javascript from "highlight.js/lib/languages/javascript";
  import json from "highlight.js/lib/languages/json";
  import kotlin from "highlight.js/lib/languages/kotlin";
  import markdown from "highlight.js/lib/languages/markdown";
  import objectivec from "highlight.js/lib/languages/objectivec";
  import php from "highlight.js/lib/languages/php";
  import python from "highlight.js/lib/languages/python";
  import ruby from "highlight.js/lib/languages/ruby";
  import rust from "highlight.js/lib/languages/rust";
  import scss from "highlight.js/lib/languages/scss";
  import sql from "highlight.js/lib/languages/sql";
  import swift from "highlight.js/lib/languages/swift";
  import typescript from "highlight.js/lib/languages/typescript";
  import xml from "highlight.js/lib/languages/xml";
  import yaml from "highlight.js/lib/languages/yaml";

  const LANGS: Record<string, unknown> = {
    bash, c, cpp, csharp, css, go, java, javascript, json, kotlin, markdown,
    objectivec, php, python, ruby, rust, scss, sql, swift, typescript, xml, yaml,
  };
  for (const [name, def] of Object.entries(LANGS)) hljs.registerLanguage(name, def as never);

  // File extension → registered hljs language name.
  const EXT_LANG: Record<string, string> = {
    swift: "swift", ts: "typescript", tsx: "typescript", mts: "typescript", cts: "typescript",
    js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
    go: "go", py: "python", rb: "ruby", java: "java", kt: "kotlin", kts: "kotlin",
    m: "objectivec", mm: "objectivec", h: "cpp", hpp: "cpp", c: "c", cc: "cpp", cpp: "cpp", cxx: "cpp",
    cs: "csharp", rs: "rust", php: "php", json: "json", yaml: "yaml", yml: "yaml",
    sh: "bash", bash: "bash", zsh: "bash", sql: "sql", html: "xml", xml: "xml",
    css: "css", scss: "scss", sass: "scss", md: "markdown", markdown: "markdown",
  };

  export function languageForPath(path: string): string | null {
    const base = path.split("/").pop() ?? "";
    if (!base.includes(".")) return null;
    const ext = base.split(".").pop()?.toLowerCase() ?? "";
    return EXT_LANG[ext] ?? null;
  }

  function escapeHtml(s: string): string {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  // Returns HTML-safe markup for one code line. Both branches escape, so the
  // result is safe for dangerouslySetInnerHTML. Per-line: multi-line tokens
  // (block comments, multi-line strings) do not carry across lines.
  export function highlightLine(text: string, language: string | null): string {
    if (!language) return escapeHtml(text);
    try {
      return hljs.highlight(text, { language, ignoreIllegal: true }).value;
    } catch {
      return escapeHtml(text);
    }
  }
  ```
  Run tests → PASS.

- [ ] **Step 4: Add the highlight palette to `styles.css`.** In the `:root` (dark) block add:
  ```css
  --hl-keyword: #ff7b72;
  --hl-string: #a5d6ff;
  --hl-comment: #8b949e;
  --hl-number: #79c0ff;
  --hl-function: #d2a8ff;
  --hl-type: #ffa657;
  --hl-attr: #79c0ff;
  --hl-tag: #7ee787;
  ```
  In the `:root[data-theme="light"]` block add:
  ```css
  --hl-keyword: #cf222e;
  --hl-string: #0a3069;
  --hl-comment: #6e7781;
  --hl-number: #0550ae;
  --hl-function: #8250df;
  --hl-type: #953800;
  --hl-attr: #0550ae;
  --hl-tag: #116329;
  ```
  At the END of `styles.css` add the token rules:
  ```css
  .hljs-keyword, .hljs-selector-tag, .hljs-literal, .hljs-section, .hljs-doctag { color: var(--hl-keyword); }
  .hljs-string, .hljs-meta .hljs-string, .hljs-regexp, .hljs-addition { color: var(--hl-string); }
  .hljs-comment, .hljs-quote { color: var(--hl-comment); font-style: italic; }
  .hljs-number, .hljs-symbol, .hljs-bullet { color: var(--hl-number); }
  .hljs-title, .hljs-title.function_, .hljs-built_in { color: var(--hl-function); }
  .hljs-type, .hljs-class .hljs-title, .hljs-title.class_ { color: var(--hl-type); }
  .hljs-attr, .hljs-attribute, .hljs-variable, .hljs-template-variable, .hljs-property { color: var(--hl-attr); }
  .hljs-tag, .hljs-name, .hljs-selector-id, .hljs-selector-class { color: var(--hl-tag); }
  .hljs-meta, .hljs-params { color: var(--hl-comment); }
  .hljs-emphasis { font-style: italic; }
  .hljs-strong { font-weight: 600; }
  ```
  (No `.hljs` background/foreground rule — the diff row's own bg/fg and add/del tints are preserved; only token spans get color.)

- [ ] **Step 5: Update `DiffHunk.tsx`** — highlight each line, add a copy-code button, tighten rows. Key changes:
  - Compute `const lang = languageForPath(path);` once.
  - Replace the plain text span with highlighted markup:
    ```tsx
    <span
      className="whitespace-pre"
      dangerouslySetInnerHTML={{ __html: highlightLine(l.text, lang) }}
    />
    ```
  - Wrap the hunk in a `group relative` container and add a copy button (top-right, `opacity-0 group-hover:opacity-100 transition`), using `navigator.clipboard.writeText(code)` where `code = ctx.lines.map((l) => l.text).join("\n")`. Show a transient "Copied ✓" state (mirror SendToWorkerButton's `sent` timer pattern, or a 1.5s `useState`). Use `Copy`/`Check` from lucide-react.
  - Tighten row line-height/padding a touch (e.g. `leading-[1.45]`), keep `overflow-x-auto`.
  - Keep the existing "Expand full file" / truncated-note behavior.

- [ ] **Step 6: Tests for DiffHunk** — extend/write `DiffHunk.test.tsx`: mock the diff-context query to return lines; assert (a) a rendered line contains `hljs-` markup for a known language path, (b) clicking the copy button calls `navigator.clipboard.writeText` with the joined line text (mock `navigator.clipboard`). Run vitest → PASS. `npm run typecheck`.

- [ ] **Step 7: Commit** — `git commit -am "feat(web): syntax-highlight diff hunks + copy-code button"`

---

### Task 2: Resolved-collapse + filename-first header + tighter spacing (ThreadCard)

**Files:**
- Modify: `frontend/src/renderer/components/CommentsView.tsx`
- Create: `frontend/src/renderer/components/FileHeader.tsx` (filename-first header)
- Modify/Create: `frontend/src/renderer/components/CommentsView.test.tsx`
- Test: `frontend/src/renderer/components/FileHeader.test.tsx`

**Interfaces:**
- Consumes: `Thread` type (CommentsView), `Badge`, `ChevronRight`/`ChevronDown` from lucide-react.

- [ ] **Step 1: Write failing tests.**
  - `FileHeader.test.tsx`: given `path="a/b/c/File.swift"`, renders the basename `File.swift` prominently and the full path as the element's `title`. Given a path with no slash, renders it as-is.
  - `CommentsView.test.tsx` (extend the existing mock-dispatch test): a **resolved** thread renders COLLAPSED — its comments and DiffHunk are NOT in the document initially, and a header showing the "Resolved" badge + comment count IS; clicking the header expands (comments now present). An **unresolved** thread renders EXPANDED (comments present without clicking).
  Run → FAIL.

- [ ] **Step 2: Implement `FileHeader.tsx`** — filename-first, directory dimmed and left-truncated, full path in `title`:
  ```tsx
  export function FileHeader({ path, line }: { path: string; line: number }) {
    const slash = path.lastIndexOf("/");
    const dir = slash >= 0 ? path.slice(0, slash + 1) : "";
    const name = slash >= 0 ? path.slice(slash + 1) : path;
    return (
      <span className="flex min-w-0 items-baseline gap-0 font-mono text-[11.5px]" title={path}>
        {dir && <span className="truncate text-muted-foreground" dir="rtl">{dir}</span>}
        <span className="shrink-0 text-foreground">{name}</span>
        {line > 0 && <span className="shrink-0 text-muted-foreground">:{line}</span>}
      </span>
    );
  }
  ```
  (The `dir="rtl"` + `truncate` on the directory truncates it on the LEFT so the filename stays visible; verify visually — if `dir="rtl"` reverses punctuation oddly, fall back to a plain left-truncation via `text-overflow` on a max-width container. The test only asserts basename text + `title`.)

- [ ] **Step 3: Rework `ThreadCard`** in CommentsView.tsx:
  - `const [open, setOpen] = useState(!thread.resolved);` — resolved threads start collapsed, unresolved expanded.
  - Header row becomes a button (`w-full`, clickable) that toggles `open`: chevron (`ChevronDown` when open, `ChevronRight` when closed) + `<FileHeader path line />` + (when resolved) the `Resolved` Badge + a muted `· {thread.comments.length} comment(s)` count.
  - Render the DiffHunk, comment list, and footer (SendToWorkerButton + ThreadActions) ONLY when `open`.
  - Tighten spacing: reduce the comment list gap/padding (`gap-2` → `gap-1.5`, `py-2.5` → `py-2`) and card internal padding a touch. Keep it readable.
  - Preserve `thread.path && thread.line > 0` guard for the DiffHunk.

- [ ] **Step 4: Run tests → PASS.** `npm run typecheck`.

- [ ] **Step 5: Commit** — `git commit -am "feat(web): collapse resolved threads + filename-first header + tighter spacing"`

---

### Task 3: Collapsible reply composer + ⌘/Ctrl+Enter (ThreadActions)

**Files:**
- Modify: `frontend/src/renderer/components/ThreadActions.tsx`
- Modify: `frontend/src/renderer/components/ThreadActions.test.tsx`

**Interfaces:**
- Consumes: `useReplyToThread`, `useResolveThread` (unchanged).

- [ ] **Step 1: Write failing tests** (extend ThreadActions.test.tsx):
  - Initially the reply Textarea is NOT shown; a "Reply" button IS. Clicking "Reply" reveals the Textarea (+ a "Cancel" and a submit "Reply" button).
  - With the composer open and text typed, pressing **Cmd/Ctrl+Enter** in the textarea calls `reply.mutate({prUrl, threadId, body})`.
  - Clicking "Cancel" hides the composer and clears the text.
  - The "Resolve" button is still shown (when `!thread.resolved`) in the collapsed state.
  - Keep the existing tests (error line hoisted, resolve.mutate args, resolve hidden when resolved) working — adjust them for the new collapsed layout (Reply now toggles, so the submit assertions must open the composer first).
  Run → FAIL.

- [ ] **Step 2: Implement.** Restructure `ThreadActions`:
  - `const [composing, setComposing] = useState(false);`
  - **Collapsed footer** (when `!composing`): a row with `[Reply]` (opens composer) and, when `!thread.resolved`, `[Resolve]`. (SendToWorkerButton is rendered by ThreadCard alongside — leave it.)
  - **Composer** (when `composing`): the `Textarea` + a right-aligned `[Cancel]` (ghost) + `[Reply]` (submit, disabled while pending/empty). On the textarea add `onKeyDown`: if `(e.metaKey || e.ctrlKey) && e.key === "Enter"` and body non-empty and not pending → submit.
  - Submit handler: `reply.mutate({ prUrl, threadId: thread.threadId, body })`.
  - On reply success: clear body AND close the composer (`setComposing(false)`) — extend the existing `useEffect(reply.isSuccess)`.
  - Cancel: `setComposing(false); setBody("")`.
  - Keep the error line hoisted (unconditional when `reply.isError || resolve.isError`).
  - Extract the submit to a `submitReply()` helper so the button and the keydown share it.

- [ ] **Step 3: Run tests → PASS.** Then whole suite `cd frontend && npx vitest run && npm run typecheck`. Update `CommentsView.test.tsx` mock if the footer layout shift breaks it.

- [ ] **Step 4: Commit** — `git commit -am "feat(web): collapsible reply composer + Cmd/Ctrl+Enter to send"`

---

## Final verification
- [ ] `cd frontend && npm run typecheck && npx vitest run` — whole suite green.
- [ ] No `routeTree.gen.ts` / `pnpm-lock.yaml` churn committed; only the intended `package.json`/`package-lock` highlight.js addition.
- [ ] Manual visual pass after reinstall: Swift diff is colored; resolved thread starts collapsed and expands on click; Reply button reveals composer; ⌘+Enter sends; copy button copies code; filename readable.

## Self-Review notes
- **Ask coverage:** syntax highlighting → Task 1; resolved-collapse → Task 2; filename header → Task 2; collapsible composer → Task 3; ⌘/Ctrl+Enter → Task 3; tighter spacing → Tasks 1+2; copy-code button → Task 1.
- **Security:** the only `dangerouslySetInnerHTML` is fed by `highlightLine`, which escapes on BOTH paths (hljs default + explicit fallback) — locked by a test. Code text is the developer's own repo content rendered in their own app.
- **Type consistency:** `languageForPath`/`highlightLine` signatures identical across Task 1 usage; `Thread` type reused in Task 2/3.
