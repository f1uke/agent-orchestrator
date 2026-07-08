# Orchestrator Spawn-Confirm Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a global "Confirm before spawning workers" setting (default ON) that makes the orchestrator present a confirmation summary in chat and wait for explicit approval before running `ao spawn`.

**Architecture:** A file-backed global settings store (`spawnconfirm`, cloning `reclaimsettings`) is exposed at `GET/PUT /api/v1/settings/spawn-confirm` and edited from a new Global Settings card. Its value is injected into the orchestrator system prompt in `buildSystemPrompt` via a Manager getter dependency; when ON a "Confirm before spawning" section is appended (reusing the git-convention feature's branch prefix/base/target), when OFF nothing is added.

**Tech Stack:** Go (backend daemon, chi router, go-generate OpenAPI), React + TanStack Query + shadcn/ui (renderer), Vitest, `go test`.

## Global Constraints

- Base branch / merge target: `main-fluke`. Branch name: `feature/spawn-confirm-gate` (already the current branch).
- All app state resolves under `~/.ao` (honor `AO_DATA_DIR`). Never write to OS-default app-data dirs.
- Frontend UI is built from shadcn primitives (`components/ui/*`); the renderer clones the agent-orchestrator web app. Follow `DESIGN.md`.
- Setting scope: **global** (app-wide). Default: **ON** (confirm). When OFF, inject **no** confirm text.
- The confirm text **reuses** the git-convention feature (PR #24): base + PR target = the project's `DefaultBranch`; the "New branch" line references the convention section already injected above it. Do not duplicate branch-naming rules.
- Tests: `go test ./...` for touched backend packages; `npm run test` + `npm run typecheck` in `frontend/`. After `npm run api`, revert any `routeTree.gen.ts` and `pnpm-lock.yaml` churn (commit only `openapi.yaml` + `schema.ts`).
- Commit message trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

## File Structure

- **Create** `backend/internal/spawnconfirm/settings.go` — file-backed global settings store (clone of `reclaimsettings`).
- **Create** `backend/internal/spawnconfirm/settings_test.go` — store unit tests.
- **Modify** `backend/internal/session_manager/manager.go` — add `SpawnConfirmEnabled` dep + `confirmBeforeSpawn()` wrapper + `orchestratorSpawnConfirmPrompt(...)` + wire into `buildSystemPrompt`.
- **Modify** `backend/internal/session_manager/manager_test.go` — add `TestSystemPrompt_SpawnConfirm`.
- **Modify** `backend/internal/httpd/controllers/settings.go` — add `SpawnConfirmService` + `/settings/spawn-confirm` routes.
- **Modify** `backend/internal/httpd/controllers/dto.go` — add spawn-confirm request/response DTOs.
- **Modify** `backend/internal/httpd/controllers/settings_test.go` — add spawn-confirm route tests.
- **Modify** `backend/internal/httpd/api.go` — add `SpawnConfirm` to `APIDeps` + wire into `SettingsController`.
- **Modify** `backend/internal/httpd/apispec/specgen/build.go` — add schema-name mappings + operations.
- **Regenerated** `backend/internal/httpd/apispec/openapi.yaml` + `frontend/src/api/schema.ts` (via `npm run api`).
- **Modify** `backend/internal/daemon/daemon.go` — construct the store before `startSession`, pass to `startSession` + `APIDeps`.
- **Modify** `backend/internal/daemon/lifecycle_wiring.go` — thread `spawnConfirmEnabled func() bool` through `startSession` into the Manager deps.
- **Create** `frontend/src/renderer/components/SpawnConfirmSection.tsx` — Global Settings card (clone of `AutoReclaimSection`).
- **Create** `frontend/src/renderer/components/SpawnConfirmSection.test.tsx` — card test.
- **Modify** `frontend/src/renderer/components/GlobalSettingsForm.tsx` — render the new card first.

---

### Task 1: Global spawn-confirm settings store

**Files:**
- Create: `backend/internal/spawnconfirm/settings.go`
- Test: `backend/internal/spawnconfirm/settings_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `spawnconfirm.Settings{ Enabled bool }`; `spawnconfirm.Default() Settings`; `spawnconfirm.NewStore(dir string) (*Store, error)`; `(*Store).Get() Settings`; `(*Store).Set(Settings) error`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/spawnconfirm/settings_test.go`:

```go
package spawnconfirm

import "testing"

func TestNewStore_AbsentFile_DefaultsToEnabled(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); !got.Enabled {
		t.Fatalf("defaults = %+v, want {Enabled:true}", got)
	}
}

func TestSet_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.Enabled {
		t.Fatalf("in-memory = %+v, want disabled", got)
	}
	// A fresh store over the same dir reloads the persisted value.
	st2, _ := NewStore(dir)
	if got := st2.Get(); got.Enabled {
		t.Fatalf("reloaded = %+v, want disabled", got)
	}
}

func TestNewStore_EmptyDir_Errors(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("want error for empty dir")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/spawnconfirm/...`
Expected: FAIL — package/symbols undefined (`NewStore`, `Settings`, etc.).

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/spawnconfirm/settings.go`:

```go
// Package spawnconfirm holds the global "confirm before spawning a worker"
// setting, persisted as a small JSON file under the data dir (~/.ao). The
// session manager reads Get().Enabled when it assembles the orchestrator system
// prompt; the REST layer edits via Set(). Modeled on reclaimsettings.
package spawnconfirm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const fileName = "spawn-confirm-settings.json"

// Settings is the single spawn-confirm toggle.
type Settings struct {
	// Enabled gates the orchestrator on human confirmation before it runs
	// `ao spawn`. Default true (confirm).
	Enabled bool `json:"enabled"`
}

// Default is the confirm gate ON.
func Default() Settings { return Settings{Enabled: true} }

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/spawn-confirm-settings.json. A missing or corrupt file
// degrades to Default() (gate ON) rather than erroring, so the daemon always
// boots with the safe default.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("spawnconfirm: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil {
			s.cur = loaded
		}
	}
	return s, nil
}

// Get returns the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Set persists (atomic write via temp+rename) and updates memory.
func (s *Store) Set(next Settings) error {
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("spawnconfirm: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("spawnconfirm: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("spawnconfirm: rename: %w", err)
	}
	s.cur = next
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/spawnconfirm/...`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/spawnconfirm/
git commit -m "feat(settings): global spawn-confirm settings store

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Inject the confirm gate into the orchestrator prompt

**Files:**
- Modify: `backend/internal/session_manager/manager.go`
- Test: `backend/internal/session_manager/manager_test.go`

**Interfaces:**
- Consumes: existing `orchestratorGitConventionPrompt`, `domain.GitConventionConfig.Active()`, `ProjectConfig.DefaultBranch`.
- Produces: `Deps.SpawnConfirmEnabled func() bool`; Manager method `confirmBeforeSpawn() bool` (nil getter → true); `orchestratorSpawnConfirmPrompt(enabled bool, conv domain.GitConventionConfig, baseBranch string) string`.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/session_manager/manager_test.go` (after `TestSystemPrompt_GitConvention`):

```go
// TestSystemPrompt_SpawnConfirm: when the global spawn-confirm gate is ON
// (default) the orchestrator prompt carries a confirmation section naming the
// source/new/PR-target branches; when OFF the section is absent. The gate never
// affects worker prompts.
func TestSystemPrompt_SpawnConfirm(t *testing.T) {
	newMgr := func(cfg domain.ProjectConfig, enabled func() bool) *Manager {
		st := newFakeStore()
		st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
		st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
		lookPath := func(string) (string, error) { return "/bin/true", nil }
		return New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath, SpawnConfirmEnabled: enabled})
	}
	build := func(m *Manager, kind domain.SessionKind) string {
		sp, err := m.buildSystemPrompt(ctx, kind, "mer")
		if err != nil {
			t.Fatalf("buildSystemPrompt: %v", err)
		}
		return sp
	}

	t.Run("default nil getter confirms (ON)", func(t *testing.T) {
		sp := build(newMgr(domain.ProjectConfig{DefaultBranch: "main-fluke"}, nil), domain.KindOrchestrator)
		for _, want := range []string{"Confirm before spawning", "wait for their explicit approval", "Source branch", "New branch", "PR target", "`main-fluke`"} {
			if !strings.Contains(sp, want) {
				t.Fatalf("ON orchestrator prompt missing %q:\n%s", want, sp)
			}
		}
	})

	t.Run("convention active adds the prefix clause", func(t *testing.T) {
		cfg := domain.ProjectConfig{DefaultBranch: "develop", GitConvention: domain.GitConventionConfig{Workflow: domain.GitWorkflowGitflow}}
		sp := build(newMgr(cfg, func() bool { return true }), domain.KindOrchestrator)
		if !strings.Contains(sp, "following the git branch convention above") {
			t.Fatalf("convention-active prompt missing prefix clause:\n%s", sp)
		}
	})

	t.Run("no convention keeps a generic new-branch line", func(t *testing.T) {
		sp := build(newMgr(domain.ProjectConfig{}, func() bool { return true }), domain.KindOrchestrator)
		if strings.Contains(sp, "following the git branch convention above") {
			t.Fatalf("no-convention prompt should not reference the convention:\n%s", sp)
		}
		if !strings.Contains(sp, "Confirm before spawning") {
			t.Fatalf("no-convention prompt still needs the confirm section:\n%s", sp)
		}
	})

	t.Run("OFF adds no confirm section", func(t *testing.T) {
		sp := build(newMgr(domain.ProjectConfig{}, func() bool { return false }), domain.KindOrchestrator)
		if strings.Contains(sp, "Confirm before spawning") {
			t.Fatalf("OFF orchestrator prompt should have no confirm section:\n%s", sp)
		}
	})

	t.Run("worker prompt never carries the confirm section", func(t *testing.T) {
		for _, enabled := range []func() bool{nil, func() bool { return true }} {
			sp := build(newMgr(domain.ProjectConfig{}, enabled), domain.KindWorker)
			if strings.Contains(sp, "Confirm before spawning") {
				t.Fatalf("worker prompt should have no confirm section:\n%s", sp)
			}
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/session_manager/ -run TestSystemPrompt_SpawnConfirm`
Expected: FAIL — `Deps` has no field `SpawnConfirmEnabled` (compile error).

- [ ] **Step 3: Write minimal implementation**

In `manager.go`, add the Manager field (inside `type Manager struct { ... }`, near `logger`):

```go
	// spawnConfirmEnabled reports whether the orchestrator must confirm before
	// spawning. Nil means "confirm" (the safe default).
	spawnConfirmEnabled func() bool
```

Add the Deps field (inside `type Deps struct { ... }`, near `Logger`):

```go
	// SpawnConfirmEnabled reports whether the orchestrator must present a
	// confirmation summary and wait for approval before running `ao spawn`.
	// Nil defaults to enabled (confirm) — the safe default.
	SpawnConfirmEnabled func() bool
```

In `func New(d Deps) *Manager`, add to the struct literal (near `logger: d.Logger,`):

```go
		spawnConfirmEnabled: d.SpawnConfirmEnabled,
```

Add the wrapper method (place it just above `orchestratorGitConventionPrompt`):

```go
// confirmBeforeSpawn reports whether the orchestrator prompt should carry the
// spawn-confirmation gate. A nil getter (e.g. a bare Manager in tests, or wiring
// that omits the store) defaults to true so the safe "confirm" behavior holds.
func (m *Manager) confirmBeforeSpawn() bool {
	if m.spawnConfirmEnabled == nil {
		return true
	}
	return m.spawnConfirmEnabled()
}
```

Wire it into `buildSystemPrompt`'s orchestrator branch — change:

```go
	case domain.KindOrchestrator:
		base = orchestratorPrompt(projectID) + orchestratorGitConventionPrompt(conv, cfg.DefaultBranch)
```

to:

```go
	case domain.KindOrchestrator:
		base = orchestratorPrompt(projectID) +
			orchestratorGitConventionPrompt(conv, cfg.DefaultBranch) +
			orchestratorSpawnConfirmPrompt(m.confirmBeforeSpawn(), conv, cfg.DefaultBranch)
```

Add the prompt function (place it just after `orchestratorGitConventionPrompt`):

```go
// orchestratorSpawnConfirmPrompt returns the confirmation-gate section injected
// into the orchestrator prompt, or "" when the gate is disabled. When enabled it
// tells the orchestrator to present a summary (task, source branch, new branch,
// PR target) and wait for explicit approval before running `ao spawn`. The
// new-branch line references the git-convention section injected just above when
// a convention is active, reusing that feature's prefix rather than repeating
// them. baseBranch is the project's DefaultBranch (base + PR target).
func orchestratorSpawnConfirmPrompt(enabled bool, conv domain.GitConventionConfig, baseBranch string) string {
	if !enabled {
		return ""
	}
	newBranch := "the branch that will be created"
	if conv.Active() {
		newBranch = "the branch that will be created, following the git branch convention above (e.g. `feature/<topic>`)"
	}
	return fmt.Sprintf("\n\n"+`## Confirm before spawning

Before you run `+"`ao spawn`"+`, present a short confirmation summary to the human and wait for their explicit approval. Do NOT spawn until they confirm. The summary must list:
- **Task** — one line on what the worker will do
- **Source branch** — the `+"`--from`"+` base branch (default `+"`%[1]s`"+`)
- **New branch** — %[2]s
- **PR target** — where the worker's pull request will merge (`+"`%[1]s`"+`)

If the human asks for changes, revise and re-confirm. Run `+"`ao spawn`"+` only after they approve. This confirmation is conversational — ask in chat and wait; there is no separate UI dialog.`, baseBranch, newBranch)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/session_manager/ -run 'TestSystemPrompt|TestSpawnOrchestrator_UsesCoordinatorPrompt'`
Expected: PASS. (Existing prompt tests use `strings.Contains`, so the default-ON section does not break them.)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/session_manager/manager.go backend/internal/session_manager/manager_test.go
git commit -m "feat(prompt): gate orchestrator on spawn confirmation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: REST surface + OpenAPI for spawn-confirm settings

**Files:**
- Modify: `backend/internal/httpd/controllers/dto.go`
- Modify: `backend/internal/httpd/controllers/settings.go`
- Modify: `backend/internal/httpd/api.go`
- Modify: `backend/internal/httpd/apispec/specgen/build.go`
- Test: `backend/internal/httpd/controllers/settings_test.go`
- Regenerated: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**
- Consumes: `spawnconfirm.Settings` (Task 1); existing `SettingsController`, `envelope`, `apispec.NotImplemented`, `decodeJSON`.
- Produces: `controllers.SpawnConfirmService` interface (`Get() spawnconfirm.Settings`, `Set(spawnconfirm.Settings) error`); `SpawnConfirmSettingsResponse{ Enabled bool }`; `SetSpawnConfirmSettingsRequest{ Enabled bool }`; `APIDeps.SpawnConfirm`; routes `GET/PUT /api/v1/settings/spawn-confirm`.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/httpd/controllers/settings_test.go`:

```go
type fakeSpawnConfirmSvc struct {
	cur   spawnconfirm.Settings
	saved spawnconfirm.Settings
	err   error
}

func (f *fakeSpawnConfirmSvc) Get() spawnconfirm.Settings { return f.cur }

func (f *fakeSpawnConfirmSvc) Set(s spawnconfirm.Settings) error {
	if f.err != nil {
		return f.err
	}
	f.saved = s
	f.cur = s
	return nil
}

func newSpawnConfirmTestServer(t *testing.T, svc *fakeSpawnConfirmSvc) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{SpawnConfirm: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSpawnConfirmRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/settings/spawn-confirm", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestSpawnConfirmController_GetReturnsCurrent(t *testing.T) {
	svc := &fakeSpawnConfirmSvc{cur: spawnconfirm.Settings{Enabled: true}}
	srv := newSpawnConfirmTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/spawn-confirm", "")
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got spawnConfirmSettingsBody
	mustJSON(t, body, &got)
	if !got.Enabled {
		t.Fatalf("got = %#v", got)
	}
}

func TestSpawnConfirmController_PutSaves(t *testing.T) {
	svc := &fakeSpawnConfirmSvc{cur: spawnconfirm.Settings{Enabled: true}}
	srv := newSpawnConfirmTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/spawn-confirm", `{"enabled":false}`)
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got spawnConfirmSettingsBody
	mustJSON(t, body, &got)
	if got.Enabled {
		t.Fatalf("response = %#v", got)
	}
	if svc.saved.Enabled {
		t.Fatalf("saved=%+v, want disabled", svc.saved)
	}
}

func TestSpawnConfirmController_PutInvalidJSON(t *testing.T) {
	srv := newSpawnConfirmTestServer(t, &fakeSpawnConfirmSvc{})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/spawn-confirm", `{`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

type spawnConfirmSettingsBody struct {
	Enabled bool `json:"enabled"`
}
```

Add the import to the test file's import block:

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/spawnconfirm"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run TestSpawnConfirm`
Expected: FAIL — `APIDeps` has no `SpawnConfirm`, DTO/interface undefined (compile error).

- [ ] **Step 3a: Add DTOs**

In `backend/internal/httpd/controllers/dto.go`, after `SetReclaimSettingsRequest`:

```go
// SpawnConfirmSettingsResponse mirrors spawnconfirm.Settings on the wire. It is
// the body of GET/PUT /api/v1/settings/spawn-confirm.
type SpawnConfirmSettingsResponse struct {
	Enabled bool `json:"enabled"`
}

// SetSpawnConfirmSettingsRequest is the body of PUT /api/v1/settings/spawn-confirm.
type SetSpawnConfirmSettingsRequest struct {
	Enabled bool `json:"enabled"`
}
```

- [ ] **Step 3b: Extend the controller**

In `backend/internal/httpd/controllers/settings.go`:

Add the import (in the import block):

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/spawnconfirm"
```

Add the service interface (after `SettingsService`):

```go
// SpawnConfirmService is the spawn-confirm settings store surface the controller
// needs. *spawnconfirm.Store satisfies this directly.
type SpawnConfirmService interface {
	Get() spawnconfirm.Settings
	Set(spawnconfirm.Settings) error
}
```

Add the field to `SettingsController`:

```go
type SettingsController struct {
	Svc          SettingsService
	SpawnConfirm SpawnConfirmService
}
```

Register the new routes (inside `Register`, after the reclaim routes):

```go
	r.Get("/settings/spawn-confirm", c.getSpawnConfirm)
	r.Put("/settings/spawn-confirm", c.setSpawnConfirm)
```

Add the handlers (after `set`):

```go
func (c *SettingsController) getSpawnConfirm(w http.ResponseWriter, r *http.Request) {
	if c.SpawnConfirm == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/spawn-confirm")
		return
	}
	s := c.SpawnConfirm.Get()
	envelope.WriteJSON(w, http.StatusOK, SpawnConfirmSettingsResponse{Enabled: s.Enabled})
}

func (c *SettingsController) setSpawnConfirm(w http.ResponseWriter, r *http.Request) {
	if c.SpawnConfirm == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/spawn-confirm")
		return
	}
	var in SetSpawnConfirmSettingsRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	next := spawnconfirm.Settings{Enabled: in.Enabled}
	if err := c.SpawnConfirm.Set(next); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SpawnConfirmSettingsResponse{Enabled: next.Enabled})
}
```

- [ ] **Step 3c: Wire APIDeps**

In `backend/internal/httpd/api.go`, add to `APIDeps` (after `Settings`):

```go
	SpawnConfirm       controllers.SpawnConfirmService
```

And in `NewAPI`, change the settings controller construction:

```go
		settings:      &controllers.SettingsController{Svc: deps.Settings, SpawnConfirm: deps.SpawnConfirm},
```

- [ ] **Step 3d: Declare the OpenAPI operations**

In `backend/internal/httpd/apispec/specgen/build.go`, add to `schemaNames` (after the reclaim settings entries):

```go
	"ControllersSpawnConfirmSettingsResponse":   "SpawnConfirmSettingsResponse",
	"ControllersSetSpawnConfirmSettingsRequest": "SetSpawnConfirmSettingsRequest",
```

And in `settingsOperations()`, add two operations inside the returned slice (after the reclaim PUT operation):

```go
		{
			method: http.MethodGet, path: "/api/v1/settings/spawn-confirm", id: "getSpawnConfirmSettings", tag: "settings",
			summary: "Fetch the spawn-confirmation gate setting",
			resps: []respUnit{
				{http.StatusOK, controllers.SpawnConfirmSettingsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/spawn-confirm", id: "setSpawnConfirmSettings", tag: "settings",
			summary: "Replace the spawn-confirmation gate setting",
			reqBody: controllers.SetSpawnConfirmSettingsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SpawnConfirmSettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
```

- [ ] **Step 4a: Run backend tests**

Run: `cd backend && go test ./internal/httpd/controllers/ -run TestSpawnConfirm && go test ./internal/httpd/...`
Expected: PASS.

- [ ] **Step 4b: Regenerate OpenAPI + TS types**

Run (from repo root): `npm run api`
Then revert unrelated churn:
Run: `cd frontend && git checkout -- src/routeTree.gen.ts 2>/dev/null; git checkout -- pnpm-lock.yaml 2>/dev/null; true`
Verify the new path is present:
Run: `grep -c "settings/spawn-confirm" frontend/src/api/schema.ts`
Expected: `>= 1`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/httpd frontend/src/api/schema.ts
git commit -m "feat(api): GET/PUT /settings/spawn-confirm

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Daemon wiring — construct the store and thread the getter

**Files:**
- Modify: `backend/internal/daemon/daemon.go`
- Modify: `backend/internal/daemon/lifecycle_wiring.go`

**Interfaces:**
- Consumes: `spawnconfirm.NewStore` (Task 1); `sessionmanager.Deps.SpawnConfirmEnabled` (Task 2); `httpd.APIDeps.SpawnConfirm` (Task 3).
- Produces: a live daemon whose Manager reads the store and whose `SettingsController` edits it.

- [ ] **Step 1: Thread the getter through `startSession`**

In `backend/internal/daemon/lifecycle_wiring.go`:

Add the import (in the import block):

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/spawnconfirm"
```

Change the `startSession` signature to accept the store:

```go
func startSession(cfg config.Config, runtime runtimeselect.Runtime, store *sqlite.Store, lcm *lifecycle.Manager, messenger ports.AgentMessenger, telemetry ports.EventSink, spawnConfirm *spawnconfirm.Store, log *slog.Logger) (*sessionsvc.Service, reviewsvc.Manager, sessionLifecycle, error) {
```

Add the getter to the Manager deps (inside `sessionmanager.New(sessionmanager.Deps{ ... })`, after `IdleCloseTTL: cfg.SessionIdleClose,`):

```go
		SpawnConfirmEnabled: func() bool { return spawnConfirm.Get().Enabled },
```

- [ ] **Step 2: Construct the store early and pass it in `daemon.go`**

In `backend/internal/daemon/daemon.go`:

Add the import if not already present (it is, for reclaimsettings; add spawnconfirm next to it):

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/spawnconfirm"
```

**Before** the `startSession` call, construct the store. Insert immediately above the `sessionSvc, reviewSvc, sessMgr, err := startSession(...)` line:

```go
	// The spawn-confirm gate is a global setting the orchestrator prompt reads at
	// spawn/restore time, so its store is built before the session manager and its
	// getter handed in. A missing/corrupt file degrades to ON (confirm).
	spawnConfirmSettings, err := spawnconfirm.NewStore(cfg.DataDir)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("spawn-confirm settings: %w", err)
	}
```

Update the `startSession` call to pass the store (add `spawnConfirmSettings` before `log`):

```go
	sessionSvc, reviewSvc, sessMgr, err := startSession(cfg, runtimeAdapter, store, lcStack.LCM, messenger, telemetrySink, spawnConfirmSettings, log)
```

Wire it into `httpd.APIDeps` (in the `httpd.NewWithDeps(...)` call, after `Settings: reclaimSettings,`):

```go
		SpawnConfirm:       spawnConfirmSettings,
```

- [ ] **Step 3: Build the daemon**

Run: `cd backend && go build ./... && go vet ./internal/daemon/...`
Expected: no errors.

- [ ] **Step 4: Run the touched backend packages**

Run: `cd backend && go test ./internal/daemon/... ./internal/session_manager/... ./internal/httpd/... ./internal/spawnconfirm/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/daemon/
git commit -m "feat(daemon): wire spawn-confirm store into manager + API

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Global Settings card

**Files:**
- Create: `frontend/src/renderer/components/SpawnConfirmSection.tsx`
- Test: `frontend/src/renderer/components/SpawnConfirmSection.test.tsx`
- Modify: `frontend/src/renderer/components/GlobalSettingsForm.tsx`

**Interfaces:**
- Consumes: `/api/v1/settings/spawn-confirm` (Task 3), `apiClient`, shadcn `Card`/`Select`/`Label`/`Button`.
- Produces: `SpawnConfirmSection` React component rendered in `GlobalSettingsForm`.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/renderer/components/SpawnConfirmSection.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { SpawnConfirmSection } from "./SpawnConfirmSection";

function renderSection() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<SpawnConfirmSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { enabled: true }, error: undefined });
	putMock.mockReset().mockResolvedValue({ data: { enabled: false }, error: undefined });
});

describe("SpawnConfirmSection", () => {
	it("loads the setting and saves a toggle to off", async () => {
		renderSection();
		const select = await screen.findByLabelText(/confirm before spawning/i);
		// Wait for the loaded value to seed the control before toggling it.
		await waitFor(() => expect(select).toHaveTextContent(/enabled/i));
		await userEvent.click(select);
		await userEvent.click(await screen.findByRole("option", { name: /disabled/i }));
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/spawn-confirm", {
				body: { enabled: false },
			}),
		);
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/renderer/components/SpawnConfirmSection.test.tsx`
Expected: FAIL — module `./SpawnConfirmSection` not found.

- [ ] **Step 3: Write the component**

Create `frontend/src/renderer/components/SpawnConfirmSection.tsx`:

```tsx
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type SpawnConfirmSettings = { enabled: boolean };
const spawnConfirmQueryKey = ["settings", "spawnConfirm"] as const;

// SpawnConfirmSection is the Global Settings card for the orchestrator's
// "confirm before spawning a worker" gate. When on, the orchestrator presents a
// confirmation summary in chat and waits for approval before running `ao spawn`.
// Daemon-backed state (GET/PUT /api/v1/settings/spawn-confirm), read when the
// orchestrator system prompt is assembled at spawn/restore.
export function SpawnConfirmSection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: spawnConfirmQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/spawn-confirm", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as SpawnConfirmSettings;
		},
	});
	const [form, setForm] = useState<SpawnConfirmSettings>({ enabled: true });
	const [savedAt, setSavedAt] = useState<number | null>(null);

	useEffect(() => {
		if (query.data) setForm(query.data);
	}, [query.data]);

	const save = useMutation({
		mutationFn: async (next: SpawnConfirmSettings) => {
			const { error } = await apiClient.PUT("/api/v1/settings/spawn-confirm", { body: next });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			setSavedAt(Date.now());
			void queryClient.invalidateQueries({ queryKey: spawnConfirmQueryKey });
		},
	});

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">Confirm before spawning workers</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-4">
				<p className="text-[12px] text-muted-foreground">
					When on, the orchestrator shows a summary — the task, the source branch, the new branch, and the pull-request
					target — and waits for your approval in chat before it runs `ao spawn`. When off, it spawns workers directly.
				</p>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="spawnConfirmEnabled" className="text-[12px] text-muted-foreground">
						Confirm before spawning
					</Label>
					<Select
						value={form.enabled ? "on" : "off"}
						onValueChange={(v) => {
							setSavedAt(null);
							setForm({ enabled: v === "on" });
						}}
					>
						<SelectTrigger id="spawnConfirmEnabled" className="h-8 w-full text-[13px]">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="on">Enabled</SelectItem>
							<SelectItem value="off">Disabled</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div className="flex items-center gap-3">
					<Button type="button" variant="primary" onClick={() => save.mutate(form)} disabled={save.isPending}>
						{save.isPending ? "Saving…" : "Save changes"}
					</Button>
					{save.isError && (
						<span className="text-[12px] text-error">
							{save.error instanceof Error ? save.error.message : "Save failed"}
						</span>
					)}
					{savedAt && !save.isPending && !save.isError && <span className="text-[12px] text-success">Saved.</span>}
				</div>
			</CardContent>
		</Card>
	);
}
```

- [ ] **Step 4a: Render it in GlobalSettingsForm**

In `frontend/src/renderer/components/GlobalSettingsForm.tsx`, add the import:

```tsx
import { SpawnConfirmSection } from "./SpawnConfirmSection";
```

And render it as the first card inside the `max-w-2xl` column (before `<AutoReclaimSection />`):

```tsx
					<SpawnConfirmSection />
					<AutoReclaimSection />
```

- [ ] **Step 4b: Run the test + typecheck**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/renderer/components/SpawnConfirmSection.test.tsx && npm run typecheck`
Expected: PASS, no type errors. (If `npm run typecheck` regenerated `routeTree.gen.ts`, revert it: `git checkout -- src/routeTree.gen.ts`.)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/renderer/components/SpawnConfirmSection.tsx frontend/src/renderer/components/SpawnConfirmSection.test.tsx frontend/src/renderer/components/GlobalSettingsForm.tsx
git commit -m "feat(web): Global Settings card for spawn-confirm gate

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Full verification + demo

**Files:** none (verification only).

- [ ] **Step 1: Full backend test sweep for touched packages**

Run: `cd backend && go test ./internal/spawnconfirm/... ./internal/session_manager/... ./internal/httpd/... ./internal/daemon/...`
Expected: PASS.

- [ ] **Step 2: Full frontend sweep**

Run: `cd frontend && npm run test && npm run typecheck`
Expected: PASS. Revert any `routeTree.gen.ts` / `pnpm-lock.yaml` churn (`git status` should show only intended files).

- [ ] **Step 3: Confirm no stray churn**

Run: `git status --short`
Expected: clean working tree (all changes committed); no `routeTree.gen.ts` / `pnpm-lock.yaml` in the diff.

- [ ] **Step 4: Demo the setting UI**

Use the `verify` skill / `ao preview` to open the desktop browser panel on Global settings, toggle **Confirm before spawning workers**, save, and confirm the value persists to `~/.ao/spawn-confirm-settings.json`:

Run: `cat ~/.ao/spawn-confirm-settings.json`
Expected: `{"enabled":false}` after toggling off (or `{"enabled":true}` after toggling back).

- [ ] **Step 5: Open the PR (do NOT auto-merge)**

Target `main-fluke`. In the PR body, document:
- The prompt-injection point: `buildSystemPrompt` → `orchestratorSpawnConfirmPrompt(m.confirmBeforeSpawn(), conv, cfg.DefaultBranch)`.
- The toggle wiring: `spawnconfirm.Store` (file under `~/.ao`) → Manager `SpawnConfirmEnabled` getter + `SettingsController` `/settings/spawn-confirm`.
- The exact confirmation-summary text the orchestrator produces (paste the ON-state section).
- The known limitation: a live orchestrator keeps its launch-time prompt until restart.

---

## Self-Review

**Spec coverage:**
- Requirement 1 (orchestrator prompt behavior: confirm summary with task / source / new-branch-with-prefix / PR target, conversational) → Task 2 (`orchestratorSpawnConfirmPrompt`, convention clause) + test.
- Requirement 2 (global toggle, default ON, conditions prompt ON/OFF) → Task 1 (store, default ON), Task 2 (OFF returns ""), Tasks 3–4 (persistence + wiring).
- Requirement 3 (wire into prompt assembly + tests: prompt branching, persistence, settings UI) → Task 2 (prompt test), Task 1 + Task 3 (persistence tests), Task 5 (UI + test).
- Reuse of PR #24 branch logic → Task 2 references `orchestratorGitConventionPrompt`/`conv.Active()`; base+target = `DefaultBranch`.
- Constraints (revert churn, run tests, demo, no auto-merge) → Tasks 3/5/6.

**Placeholder scan:** No TBD/TODO; every code step shows complete code; commands include expected output.

**Type consistency:** `Settings{Enabled bool}`, `Default()`, `NewStore`, `Get`/`Set` (Task 1) used verbatim in Tasks 3–4. `SpawnConfirmEnabled func() bool` (Deps) → `confirmBeforeSpawn()` wrapper → `orchestratorSpawnConfirmPrompt(bool, GitConventionConfig, string)` consistent across Tasks 2/4. `SpawnConfirmService` interface (`Get`/`Set`) satisfied by `*spawnconfirm.Store` and `fakeSpawnConfirmSvc`. Endpoint `/api/v1/settings/spawn-confirm` and body `{ enabled }` consistent across Tasks 3/5.
