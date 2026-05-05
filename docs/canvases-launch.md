# Canvases — what shipped, why it matters

> *Cursor-style interactive artifacts in the AO session detail view.* PR [#1653](https://github.com/ComposioHQ/agent-orchestrator/pull/1653)

## The pitch

When an agent emits a diff, a test summary, a cost breakdown, or any other structured output, today it scrolls past in the terminal and you lose it. Canvases give every session a right-hand rail where structured output **stays visible, stays interactive, and stays readable**.

Two ways to fill the rail:

1. **Free of charge** — AO synthesizes a `core-git-diff` canvas from the session's worktree against `origin/<default>`. Every session gets this without any agent integration.
2. **Agents opt in** — drop a JSON file at `{workspacePath}/.ao/canvases/{id}.json`. The dashboard picks it up within 5 seconds. No new SDK, no new APIs to call.

![Two canvases rendered in the rail next to the terminal — a markdown summary and a stats grid with green/red/amber tone colors](assets/canvases/canvases-hero.png)

## Try it locally in 30 seconds

```bash
# 1. Start the dashboard
pnpm dev

# 2. Open any session detail page in your browser

# 3. From a terminal, drop a JSON file in the session's worktree
WS=$(jq -r .worktree ~/.agent-orchestrator/projects/<your-project>/sessions/<session-id>.json)
mkdir -p "$WS/.ao/canvases"
cat > "$WS/.ao/canvases/hello.json" <<'EOF'
{
  "version": 1,
  "id": "hello",
  "type": "stats",
  "title": "Demo",
  "createdAt": "2026-05-05T00:00:00Z",
  "updatedAt": "2026-05-05T00:00:00Z",
  "payload": {
    "metrics": [
      { "label": "Tests", "value": 42, "tone": "good" },
      { "label": "Failures", "value": 0, "tone": "neutral" }
    ]
  }
}
EOF
```

Within 5 seconds the canvas appears. Edit the file, save, watch it update.

## What renders

Four built-in types, no plugins required:

| Type | What it's for | Payload shape |
|---|---|---|
| `markdown` | Notes, summaries, READMEs | `{ markdown: string }` — supports headings, **bold**, *italic*, `code`, fenced code blocks, lists |
| `diff` | File changes, patches | `{ files: [{ path, status, hunks }] }` |
| `table` | Test results, dependency lists | `{ columns, rows }` |
| `stats` | Cost, durations, pass/fail counts | `{ metrics: [{ label, value, tone, delta }] }` |

The `tone` field on `stats` maps to AO's existing status tokens — `good` is green, `bad` is red, `warn` is amber, `neutral` matches the surrounding text. No new design tokens.

## Empty state when there's nothing to show

The rail starts collapsed when a session has no canvases. A thin tab on the right edge expands it on click.

![Collapsed and expanded states of the canvas rail](assets/canvases/canvases-empty.png)

## Mobile gracefully hides

The rail is desktop-only. On viewports under the mobile breakpoint, the SessionDetail page renders without it — no overflow, no awkward stacking.

![Session detail on mobile viewport — rail hidden, content full-width](assets/canvases/canvases-mobile.png)

## How extensible is this?

Third parties can ship canvases without forking AO. The contract is **expressive data, constrained UI** — anyone can supply data in any of the 4 renderer types, but no third party ships React into the supervisor dashboard. Trade-off: new canvas *types* (e.g. `timeline`, `chart`) need a core PR; new canvas *content* needs nothing.

A `CanvasProducer` interface is declared in core for v0.2, when agent / SCM / tracker plugins will be invoked to synthesize canvases programmatically. v0.1 only invokes the file reader + the synthesized git-diff producer.

## What's deliberately *not* in v0.1

- Custom React renderers from third-party plugins (security boundary; never planned)
- Plugin-invoked `CanvasProducer.listCanvases` calls (queued for v0.2)
- Mux WebSocket push (queued for v0.3 — currently 5s REST poll)
- Write APIs from the dashboard back into canvases
- Action buttons that mutate session state

## Built with paranoia

The feature went through 12 codex review passes that surfaced 18 distinct corner-case bugs before merge:

- Polynomial-backtracking regex (CodeQL `js/redos`) replaced with `lastIndexOf`
- `lstat` instead of `stat` so a symlink to `/dev/zero` can't bypass the size cap
- Reserved `core-` id prefix so an agent can't shadow the trusted synthesized canvas
- Per-effect cancellation + sequence-guarded poll responses so an old response can't overwrite a newer one
- `.ao/` filtered out of synthesized diffs so AO's own metadata doesn't leak into agent diffs
- Untracked-file synthesis with file count + byte budget caps so a workspace with thousands of build artifacts can't make every poll slow
- `origin/<base>` preferred over stale local refs for merge-base
- Partial-stdout recovery so oversized diffs truncate instead of disappearing

End-to-end QA verified all paths in a real browser (full report in `.gstack/qa-reports/qa-report-canvases-2026-05-05.md` if you want to see the screenshots and per-test evidence).

## Where to read more

- [docs/canvases.md](canvases.md) — full design doc, schema, validation rules, producer guides, roadmap
- [PR #1653](https://github.com/ComposioHQ/agent-orchestrator/pull/1653) — implementation
- [packages/core/src/types.ts](../packages/core/src/types.ts) — `CanvasArtifact`, `CanvasProducer`, supporting types
- [packages/core/src/canvas-log.ts](../packages/core/src/canvas-log.ts) — file reader + git-diff synthesizer
- [packages/web/src/components/CanvasRail.tsx](../packages/web/src/components/CanvasRail.tsx) — the right-rail component

## Suggested first uses

- **Test runner agent** → emit a `table` after each test run with name / status / duration columns
- **Codex review agent** → emit a `markdown` canvas with the structured findings
- **Cost-tracking agent** → emit a `stats` canvas with token counts, request count, dollar estimates
- **Refactor agent** → the synthesized `core-git-diff` already shows the change set; nothing to do
