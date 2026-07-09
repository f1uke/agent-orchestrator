# Auto-Nudge Toggle (per-session override + global default) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Bring back the "auto-nudge the worker when a PR has unresolved review comments" behavior (removed in fb18eb2a), gated by a per-session on/off switch in the Comments tab whose default comes from a global setting.

**Architecture:** A global boolean default lives in a new file-backed `autonudge` settings store (modeled on `spawnconfirm`). Each session carries a nullable override (`*bool`, NULL = inherit the global default). The lifecycle Manager computes `effective = override ?? globalDefault` and re-fires the (restored) review-comment nudge only when effective is true. Frontend: a global toggle in the settings page and a per-session Switch in the Comments tab.

**Tech Stack:** Go (goose migrations, sqlc, chi, code-first OpenAPI via specgen), React + TanStack Query, `radix-ui` Switch, vitest.

## Global Constraints

- **Effective value = `session.AutoNudgeComments ?? globalDefault`.** `AutoNudgeComments` is `*bool`: `nil` = inherit; `true`/`false` = explicit override. Default of the global setting is **false** (manual stays the out-of-box behavior).
- **Restore the ORIGINAL nudge condition** (from fb18eb2a), now gated: `if effective && (o.Review == domain.ReviewChangesRequest || hasUnresolvedComments(o.Comments))`. Re-add helpers `hasUnresolvedComments` and `reviewContent` verbatim from the removed commit; keep the existing `sendOnce(..., reviewMaxNudge)` dedup and comment-body `SanitizeControlChars`.
- **Do NOT shoehorn the bool into `promptoverrides.Store`** (it's strictly templates/prompts). Use a NEW `autonudge` store modeled on `backend/internal/spawnconfirm/settings.go`.
- **Nullable migration:** `ALTER TABLE sessions ADD COLUMN auto_nudge_comments INTEGER` — NO `NOT NULL DEFAULT` (absent row = NULL = inherit). New migration file `0024_...` (goose format, StatementBegin/End + Down DROP).
- **OpenAPI is code-first:** edit `dto.go` + `specgen/build.go`, then run `npm run api` (repo root) → regenerates `openapi.yaml` + `frontend/src/api/schema.ts`. Never hand-edit generated files. Drift/parity guards under `go test ./internal/httpd/...`.
- **Manager construction ordering:** `startLifecycle` runs at `daemon.go:136`, BEFORE the spawn-confirm/reclaim stores are built. Build `autonudge.NewStore(cfg.DataDir)` next to `promptOverrides` (~daemon.go:124) and thread its getter into `startLifecycle`.
- **Frontend:** `Switch` from `radix-ui` (already a dep — do NOT add `@radix-ui/react-switch`). Design-clone rules: shadcn primitives + tokens. The per-session switch must sit ABOVE CommentsView's loading/error/empty early returns so it always renders.
- `npm run api` regen + `go test ./internal/httpd/...` (drift+parity) after any endpoint change. Revert `routeTree.gen.ts`/lockfile churn.

---

### Task 1: Global default — `autonudge` settings store + settings API

**Files:**
- Create: `backend/internal/autonudge/settings.go` (+ `settings_test.go`)
- Modify: `backend/internal/daemon/daemon.go` (build store + wire dep)
- Modify: `backend/internal/httpd/controllers/settings.go` (+ interface, routes, handlers)
- Modify: `backend/internal/httpd/controllers/dto.go` (DTOs)
- Modify: `backend/internal/httpd/api.go` (APIDeps field)
- Modify: `backend/internal/httpd/apispec/specgen/build.go` (ops + schemaNames)
- Regenerate: `openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**
- Produces: `autonudge.Store` with `Get() Settings` / `Set(Settings) error`, `Settings{ Enabled bool }`, `NewStore(dir string) (*Store, error)`. Consumed by Task 4 (lifecycle) and the settings API.

- [ ] **Step 1: Copy `spawnconfirm/settings.go` → `autonudge/settings.go`**, verbatim in structure, changing: package `autonudge`; file name const → `"auto-nudge-settings.json"`; `Settings{ Enabled bool }` with `Default()` returning `{Enabled: false}`. Copy its `settings_test.go` too (Get default, Set+Get roundtrip, persistence across NewStore). Run `go test ./internal/autonudge/` → PASS.

- [ ] **Step 2: Wire the store in `daemon.go`** — build `autoNudge, err := autonudge.NewStore(cfg.DataDir)` next to `promptOverrides` (~line 124, BEFORE the `startLifecycle` call at 136). Add it to `httpd.APIDeps` as `AutoNudge: autoNudge` (mirror `SpawnConfirm: spawnConfirmSettings` at ~203). Add the `AutoNudge` field to `APIDeps` in `httpd/api.go` (type: the controller's `AutoNudgeService` interface).

- [ ] **Step 3: Settings API** — in `controllers/settings.go` mirror the spawn-confirm handlers exactly:
  - Interface `AutoNudgeService { Get() autonudge.Settings; Set(autonudge.Settings) error }` (match the real store method shapes; if `Set` returns error, keep it).
  - `SettingsController.AutoNudge AutoNudgeService` field.
  - Routes in `Register`: `r.Get("/settings/auto-nudge", c.getAutoNudge)`, `r.Put("/settings/auto-nudge", c.setAutoNudge)`.
  - Handlers `getAutoNudge`/`setAutoNudge` mirroring `getSpawnConfirm`/`setSpawnConfirm` (99-124): nil-guard → NotImplemented; decode `SetAutoNudgeSettingsRequest`; `Set`; respond `AutoNudgeSettingsResponse{Enabled}`.
  - DTOs in `dto.go` (near line 744): `AutoNudgeSettingsResponse{ Enabled bool json:"enabled" }`, `SetAutoNudgeSettingsRequest{ Enabled bool json:"enabled" }`.

- [ ] **Step 4: specgen** — in `build.go` `settingsOperations()` add `getAutoNudgeSettings`/`setAutoNudgeSettings` ops mirroring the spawn-confirm pair (903-920); add the two DTO names to the schemaNames map (near 224): `"ControllersAutoNudgeSettingsResponse":"AutoNudgeSettingsResponse"`, `"ControllersSetAutoNudgeSettingsRequest":"SetAutoNudgeSettingsRequest"`.

- [ ] **Step 5: Regenerate + verify** — repo root `npm run api`; then `go build ./... && go test ./internal/httpd/... && gofmt -l internal/`. Drift/parity must pass.

- [ ] **Step 6: Commit** — `git commit -am "feat(settings): global auto-nudge-on-comments default"`

---

### Task 2: Per-session override persistence (migration + sqlc + store)

**Files:**
- Create: `backend/internal/storage/sqlite/migrations/0024_add_session_auto_nudge.sql`
- Modify: `backend/internal/storage/sqlite/queries/sessions.sql`
- Regenerate: `backend/internal/storage/sqlite/gen/*` (sqlc)
- Modify: `backend/internal/domain/session.go` (field)
- Modify: `backend/internal/storage/sqlite/store/session_store.go` (mapping + bridge helpers + setter)
- Test: `backend/internal/storage/sqlite/store/session_store_test.go` (roundtrip)

**Interfaces:**
- Produces: `domain.SessionRecord.AutoNudgeComments *bool` (json `autoNudgeComments`); store setter `SetSessionAutoNudge(ctx, id domain.SessionID, override *bool) (bool, error)` (returns found).

- [ ] **Step 1: Migration** `0024_add_session_auto_nudge.sql` (goose):
  ```sql
  -- +goose Up
  -- +goose StatementBegin
  ALTER TABLE sessions ADD COLUMN auto_nudge_comments INTEGER;
  -- +goose StatementEnd
  -- +goose Down
  -- +goose StatementBegin
  ALTER TABLE sessions DROP COLUMN auto_nudge_comments;
  -- +goose StatementEnd
  ```
  (Nullable: NULL = inherit. No CDC trigger change needed — the setter returns the value directly.)

- [ ] **Step 2: sqlc queries** in `sessions.sql` — add `auto_nudge_comments` to the column lists of `InsertSession`, `UpdateSession` (SET), `GetSession`, `ListSessionsByProject`, `ListAllSessions`. Add a dedicated setter mirroring `SetSessionPreviewURL` (42-46):
  ```sql
  -- name: SetSessionAutoNudge :execrows
  UPDATE sessions SET auto_nudge_comments = ?, updated_at = ? WHERE id = ?;
  ```
  Regenerate sqlc (find the generate command: `grep -rn "sqlc" Makefile* backend/**/*.go` — likely `go generate ./...` or a `sqlc generate`). The generated `Session` row gains `AutoNudgeComments sql.NullInt64`.

- [ ] **Step 3: Domain field** — `SessionRecord.AutoNudgeComments *bool json:"autoNudgeComments"` (near the Metadata field, session.go:67). Exposed in the API read model automatically (SessionRecord is embedded in `domain.Session`).

- [ ] **Step 4: Bridge helpers + mapping** in `session_store.go` (next to `nullTimeToTime`/`timeToNullTime`, 305-317):
  ```go
  func nullInt64ToBoolPtr(n sql.NullInt64) *bool {
      if !n.Valid { return nil }
      b := n.Int64 != 0
      return &b
  }
  func boolPtrToNullInt64(b *bool) sql.NullInt64 {
      if b == nil { return sql.NullInt64{} }
      v := int64(0)
      if *b { v = 1 }
      return sql.NullInt64{Int64: v, Valid: true}
  }
  ```
  Wire into `rowToRecord` (set `AutoNudgeComments: nullInt64ToBoolPtr(row.AutoNudgeComments)`), `recordToInsert`, `recordToUpdate` (use `boolPtrToNullInt64(rec.AutoNudgeComments)`). Add the store method `SetSessionAutoNudge(ctx, id, override *bool) (bool, error)` calling the sqlc `SetSessionAutoNudge` (rows affected → found), mirroring `SetSessionPreviewURL` (63-75).

- [ ] **Step 5: Test** roundtrip in `session_store_test.go`: insert a session (AutoNudgeComments nil), read back → nil; `SetSessionAutoNudge` true → read back `*v==true`; set false → false; set nil → nil. Run `go test ./internal/storage/sqlite/...` → PASS. `go build ./... && gofmt -l internal/`.

- [ ] **Step 6: Commit** — `git commit -am "feat(store): per-session auto-nudge override column"`

---

### Task 3: Session setter service + API + read-model exposure

**Files:**
- Modify: `backend/internal/service/session/service.go` (`SetAutoNudge` + interface `sessionStore` gains `SetSessionAutoNudge`)
- Test: `backend/internal/service/session/*_test.go`
- Modify: `backend/internal/httpd/controllers/sessions.go` (route + handler + `SessionService` iface)
- Modify: `backend/internal/httpd/controllers/dto.go` (request DTO)
- Modify: `backend/internal/httpd/apispec/specgen/build.go` (op)
- Modify: `frontend/src/renderer/lib/api-client.ts` (ROUTE_TEMPLATES)
- Regenerate: `openapi.yaml`, `schema.ts`

**Interfaces:**
- Consumes: `store.SetSessionAutoNudge` (Task 2); the preview-setter pattern (`SetPreview`).
- Produces: `Service.SetAutoNudge(ctx, id domain.SessionID, override *bool) (domain.Session, error)`; endpoint `PUT /sessions/{sessionId}/auto-nudge` `{override *bool}` → `SessionResponse`.

- [ ] **Step 1: Service method** `SetAutoNudge` in service.go mirroring `SetPreview` (523-532): call `s.store.SetSessionAutoNudge(ctx, id, override)`; if not found → the standard not-found (mirror SetPreview's handling); return `s.Get(ctx, id)`. Add `SetSessionAutoNudge` to the service's `sessionStore` interface and to the two fake stores used in session tests (compile-time). Test: `SetAutoNudge` returns a session whose `AutoNudgeComments` matches; unknown session → error.

- [ ] **Step 2: DTO** in dto.go: `SetAutoNudgeRequest{ Override *bool json:"override" }`. (Response reuses `SessionResponse` / `SessionView`, which now serializes `autoNudgeComments` from the embedded record — confirm the field surfaces in `SessionView` JSON; if `SessionView` embeds `domain.Session` it does automatically.)

- [ ] **Step 3: Route + handler** in sessions.go: `r.Put("/sessions/{sessionId}/auto-nudge", c.setAutoNudge)` in `Register`; handler mirrors `setPreview` (238-285) minus the preview resolution — decode `SetAutoNudgeRequest`, call `c.Svc.SetAutoNudge(r.Context(), sessionID(r), in.Override)`, respond `SessionResponse{Session: sessionView(updated)}`. Add `SetAutoNudge` to the `SessionService` interface (sessions.go:34). Update BOTH fake SessionServices (controllers test + `cli/dto_drift_e2e_test.go`).

- [ ] **Step 4: specgen** op `setSessionAutoNudge` mirroring `setSessionPreview` (626-638) with `http.MethodPut`, `pathParams: []any{controllers.SessionIDParam{}}`, `reqBody: controllers.SetAutoNudgeRequest{}`, resp `{200, SessionResponse{}}` + 400/404/500/501. Add `"ControllersSetAutoNudgeRequest":"SetAutoNudgeRequest"` to schemaNames.

- [ ] **Step 5: Regenerate + ROUTE_TEMPLATES** — `npm run api`; add `"/api/v1/sessions/{sessionId}/auto-nudge"` to `ROUTE_TEMPLATES` in api-client.ts. `go build ./... && go test ./internal/httpd/... ./internal/service/session/... && gofmt -l internal/`.

- [ ] **Step 6: Commit** — `git commit -am "feat(session): PUT /sessions/{id}/auto-nudge override + read-model field"`

---

### Task 4: Lifecycle gate — restore the gated nudge

**Files:**
- Modify: `backend/internal/lifecycle/manager.go` (Option + field)
- Modify: `backend/internal/lifecycle/reactions.go` (gated nudge block + helpers)
- Modify: `backend/internal/daemon/lifecycle_wiring.go` (thread the default getter)
- Modify: `backend/internal/daemon/daemon.go` (pass getter into startLifecycle)
- Test: `backend/internal/lifecycle/manager_test.go`

**Interfaces:**
- Consumes: `rec.AutoNudgeComments` (Task 2), `autonudge` getter (Task 1). Restores `hasUnresolvedComments`, `reviewContent`.

- [ ] **Step 1: Manager DI** — add field `autoNudgeDefault func() bool` and `Option` `WithAutoNudgeDefault(fn func() bool) Option { return func(m *Manager){ m.autoNudgeDefault = fn } }` (mirror `WithMessageRenderer`, manager.go:52-54). In `New`, default it (like the renderer default ~82): `m.autoNudgeDefault = func() bool { return false }` unless overridden.

- [ ] **Step 2: Write failing test** in manager_test.go (invert the current `TestPRObservation_UnresolvedReviewCommentsDoNotNudgeAgent`, or add new): with a session whose `AutoNudgeComments = ptr(true)` (or default getter returns true), an observation with an unresolved comment DOES nudge (msg captured). With override `ptr(false)` (default true) → NO nudge. With override nil + default false → NO nudge. Use the existing fake messenger/store harness. Run → FAIL.

- [ ] **Step 3: Restore the gated nudge** in `ApplyPRObservation` (reactions.go, at the placeholder comment ~160-164, between CI and merge-conflict blocks):
  ```go
  effective := m.autoNudgeDefault()
  if rec.AutoNudgeComments != nil {
      effective = *rec.AutoNudgeComments
  }
  if effective && (o.Review == domain.ReviewChangesRequest || hasUnresolvedComments(o.Comments)) {
      comments, sig := reviewContent(o.Comments)
      msg := m.renderNudge(messagetemplates.NameReviewCommentDispatch, messagetemplates.ReviewCommentData{Comments: comments})
      if sig == "" {
          sig = string(o.Review)
      }
      return m.sendOnce(ctx, id, o.URL, "review:"+o.URL, sig, msg, reviewMaxNudge)
  }
  ```
  Re-add the helpers verbatim from fb18eb2a:
  ```go
  func hasUnresolvedComments(comments []ports.PRCommentObservation) bool {
      for _, c := range comments {
          if !c.Resolved { return true }
      }
      return false
  }
  func reviewContent(comments []ports.PRCommentObservation) (string, string) {
      bodies := make([]string, 0, len(comments))
      ids := make([]string, 0, len(comments))
      for _, c := range comments {
          if c.Resolved { continue }
          bodies = append(bodies, domain.SanitizeControlChars(c.Body))
          ids = append(ids, c.ID)
      }
      return strings.Join(bodies, "\n\n"), strings.Join(ids, ",")
  }
  ```
  (Ensure `strings` import is present.)

- [ ] **Step 4: Wire the getter** — `startLifecycle` (lifecycle_wiring.go:54-59) gains a `autoNudgeDefault func() bool` param and passes `lifecycle.WithAutoNudgeDefault(autoNudgeDefault)`. `daemon.go` (call at ~136) passes `func() bool { return autoNudge.Get().Enabled }`. Update `startLifecycle`'s other callers/tests for the new param (e.g. `wiring_test.go`).

- [ ] **Step 5: Run tests → PASS** — `go test ./internal/lifecycle/... ./internal/daemon/...` (ignore the known `internal/cli` AO-session e2e artifact). `go build ./... && go vet ./internal/lifecycle/... && gofmt -l internal/`.

- [ ] **Step 6: Commit** — `git commit -am "feat(lifecycle): re-enable review-comment nudge gated by per-session toggle + global default"`

---

### Task 5: Frontend — Switch primitive + settings-page global toggle

**Files:**
- Create: `frontend/src/renderer/components/ui/switch.tsx` (+ `switch.test.tsx`)
- Create: `frontend/src/renderer/components/AutoNudgeSection.tsx` (+ test)
- Modify: `frontend/src/renderer/components/GlobalSettingsForm.tsx` (render the section)

**Interfaces:**
- Consumes: `GET/PUT /api/v1/settings/auto-nudge` (Task 1, now in schema.ts). Produces: `Switch` ui primitive.

- [ ] **Step 1: `switch.tsx`** — mirror `ui/select.tsx`'s structure: `import { Switch as SwitchPrimitive } from "radix-ui"`, `cn`, `data-slot`, forwardRef, styled with tokens (track `bg-input` off / `bg-primary` on, thumb `bg-background`, focus ring). Export `Switch`. Small test: renders, toggles `onCheckedChange` on click, reflects `checked`.

- [ ] **Step 2: `AutoNudgeSection.tsx`** — copy `SpawnConfirmSection.tsx` and swap: endpoint `/api/v1/settings/auto-nudge`, query key `["settings","autoNudge"]`, title/description ("Auto-send unresolved comments to the worker — the default for new sessions; each session can override it in its Comments tab"). Use the new `Switch` (not a Select) bound to `enabled` → `PUT { enabled }`. Handle loading/error via `apiErrorMessage`. Test mirrors SpawnConfirmSection's test (mock apiClient GET/PUT, toggle sends PUT).

- [ ] **Step 3: Render** `<AutoNudgeSection />` in `GlobalSettingsForm.tsx` alongside `<SpawnConfirmSection />`.

- [ ] **Step 4: Verify** — `cd frontend && npx vitest run src/renderer/components/switch.test.tsx src/renderer/components/AutoNudgeSection.test.tsx && npm run typecheck`, then whole suite.

- [ ] **Step 5: Commit** — `git commit -am "feat(web): Switch primitive + global auto-nudge default setting"`

---

### Task 6: Frontend — per-session switch in the Comments tab

**Files:**
- Modify: `frontend/src/renderer/components/CommentsView.tsx` (header switch)
- Create: `frontend/src/renderer/hooks/useAutoNudge.ts` (read effective + set override) — or inline
- Test: `frontend/src/renderer/components/CommentsView.test.tsx`

**Interfaces:**
- Consumes: session read model `autoNudgeComments` (`*bool` → `boolean|null`), `GET /settings/auto-nudge` (global default), `PUT /sessions/{id}/auto-nudge`.

- [ ] **Step 1: Effective value + mutation.** In CommentsView, fetch the global default (`useQuery ["settings","autoNudge"]` GET `/settings/auto-nudge`) and read the session's override. The session's `autoNudgeComments` is available via the sessions read model — fetch it with a `useQuery` on `GET /sessions/{sessionId}` (or reuse an existing session query if one is in scope) and read `data.session.autoNudgeComments`. Compute `effective = override ?? globalDefault.enabled`. A mutation PUTs `/sessions/{sessionId}/auto-nudge` `{override: nextValue}` and invalidates the session query + `["session-pr-comments", sessionId]` is unaffected.

- [ ] **Step 2: Header switch — hoist ABOVE the early returns.** Add a small header row (`Switch` + label "Auto-send unresolved comments to worker") rendered in ALL states (loading/error/empty/populated), so refactor CommentsView so the switch header renders first, then the state-specific body below. When `override !== null`, show a subtle "Reset to default" text button that PUTs `{override: null}`; when `null`, a muted "(following global default)" hint. Toggling the switch sets an explicit `{override: !effective}`.

- [ ] **Step 3: Tests** (extend CommentsView.test.tsx): the switch renders even when there are no comments; toggling it PUTs `/sessions/{id}/auto-nudge` with the negated effective value; reflects the effective value from override+default. Mock the two GETs + the PUT via the existing `getMock`/`putMock` dispatch pattern.

- [ ] **Step 4: Verify** — `cd frontend && npx vitest run src/renderer/components/CommentsView.test.tsx && npm run typecheck`, then whole suite. Revert any gen/lock churn.

- [ ] **Step 5: Commit** — `git commit -am "feat(web): per-session auto-nudge switch in the Comments tab"`

---

## Final verification
- [ ] Backend: `go build ./... && go test ./internal/autonudge/... ./internal/storage/sqlite/... ./internal/service/session/... ./internal/httpd/... ./internal/lifecycle/... ./internal/daemon/... && go vet ./... && gofmt -l internal/`
- [ ] Frontend: `cd frontend && npm run typecheck && npx vitest run`
- [ ] `npm run api` produced no unexpected drift; no `routeTree.gen.ts`/lockfile churn committed.
- [ ] Manual (post-reinstall): global default toggle in Settings persists; per-session switch in Comments tab reflects the default, overrides it, and "Reset to default" clears the override; with the toggle ON, an unresolved comment nudges the worker; OFF → no nudge.

## Self-Review notes
- Effective-value logic (`override ?? default`) appears in exactly two places — the lifecycle gate (Task 4) and the Comments-tab switch (Task 6); keep them consistent.
- The nudge restoration is a faithful revert of fb18eb2a's removed block + helpers, differing only by the `effective &&` gate — do not "improve" the condition.
- `AutoNudgeComments` is `*bool` end to end (NULL/nil = inherit); the migration column is nullable INTEGER; never coerce nil to false in the store.
- Security: comment bodies remain `SanitizeControlChars`'d before reaching the worker PTY (unchanged from the original).
