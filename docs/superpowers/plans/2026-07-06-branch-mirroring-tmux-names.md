# Branch-mirroring tmux Session Names Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Name a session's tmux session after its branch (prefixed by project id) so `tmux ls` lines up with the branch and the branch-mirroring worktree directory.

**Architecture:** Add `ProjectID`/`Branch` to `ports.RuntimeConfig`; the tmux adapter derives the session name from them via a new `SessionNameFor` helper (branch-based when both are present, else today's session-id naming). Everything downstream already uses the persisted `RuntimeHandleID`, so only the `ao spawn` attach hint needs updating to reuse the same helper.

**Tech Stack:** Go, standard library only. Tests via `go test`.

## Global Constraints

- All app state stays under `~/.ao` (unchanged by this work; no new paths).
- tmux session names must not contain `.` or `:` — the sanitizer collapses them (and every other non-`[A-Za-z0-9_-]` run) to a single `-`.
- Scope is tmux only. The ConPTY (Windows) runtime ignores the new config fields; no API schema change.
- Follow existing package conventions; no new dependencies.
- Run backend commands from the `backend/` directory (that is where `go.mod` lives).

---

### Task 1: tmux branch-based naming helper

**Files:**
- Modify: `backend/internal/adapters/runtime/tmux/tmux.go` (session-name helpers, ~lines 287–321)
- Test: `backend/internal/adapters/runtime/tmux/tmux_test.go`

**Interfaces:**
- Produces:
  - `func SessionNameFor(projectID, branch, sessionID string) (string, error)` — branch-based name when `projectID` and `branch` are both non-empty; otherwise falls back to `tmuxSessionName(domain.SessionID(sessionID))` (which errors on an empty session id).
  - `func sanitizeName(raw string, maxLen int) string` — collapses `raw` to `[A-Za-z0-9_-]`, other runs → single `-`, trims dashes, empty → `"session"`, caps at `maxLen`.
  - `func branchSessionName(projectID, branch string) string` = `sanitizeName(projectID+"/"+branch, branchNameMaxLen)`.
- Consumes: nothing new. `tmuxSessionName` and `SessionName` stay as they are.

- [ ] **Step 1: Write the failing tests**

Add to `backend/internal/adapters/runtime/tmux/tmux_test.go` (after the existing `TestSessionNameMatchesCreateNaming`, around line 137):

```go
func TestSessionNameForMirrorsBranch(t *testing.T) {
	cases := []struct {
		name      string
		projectID string
		branch    string
		sessionID string
		want      string
	}{
		{"gitflow branch", "mer", "feature/PROJ-2271-x", "mer-1", "mer-feature-PROJ-2271-x"},
		{"default ao branch", "mer", "ao/mer-1/root", "mer-1", "mer-ao-mer-1-root"},
		{"orchestrator branch", "mer", "ao/mer12-orchestrator", "mer-1", "mer-ao-mer12-orchestrator"},
		{"unsafe chars collapse to dashes", "mer", "feature/foo bar@baz.1", "mer-1", "mer-feature-foo-bar-baz-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SessionNameFor(tc.projectID, tc.branch, tc.sessionID)
			if err != nil {
				t.Fatalf("SessionNameFor: %v", err)
			}
			if got != tc.want {
				t.Fatalf("SessionNameFor = %q, want %q", got, tc.want)
			}
			if !sessionIDPattern.MatchString(got) {
				t.Fatalf("name %q fails tmux-safe pattern", got)
			}
		})
	}
}

func TestSessionNameForFallsBackToSessionID(t *testing.T) {
	// No branch: fall back to session-id naming (short conforming id passes through).
	got, err := SessionNameFor("mer", "", "mer-1")
	if err != nil {
		t.Fatalf("SessionNameFor: %v", err)
	}
	if got != "mer-1" {
		t.Fatalf("SessionNameFor = %q, want mer-1 fallback", got)
	}
	// Missing project id also falls back.
	got, err = SessionNameFor("", "feature/x", "mer-1")
	if err != nil {
		t.Fatalf("SessionNameFor: %v", err)
	}
	if got != "mer-1" {
		t.Fatalf("SessionNameFor = %q, want mer-1 fallback", got)
	}
}

func TestSessionNameForRejectsEmpty(t *testing.T) {
	if _, err := SessionNameFor("", "", ""); err == nil {
		t.Fatal("SessionNameFor with no branch and empty session id: want error, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/adapters/runtime/tmux/ -run TestSessionNameFor -v`
Expected: FAIL — `undefined: SessionNameFor`.

- [ ] **Step 3: Refactor the sanitizer and add the helpers**

In `backend/internal/adapters/runtime/tmux/tmux.go`, replace the existing `sanitizedSessionName` function (currently ~lines 297–321) with the following (this splits out `sanitizeName`, keeps `sanitizedSessionName`'s hashed behavior for the fallback path, and adds `branchSessionName` + `SessionNameFor`):

```go
// sanitizeName collapses raw into tmux's safe charset ([A-Za-z0-9_-]): every
// other run of characters (including "." and ":", which tmux treats as target
// syntax) becomes a single dash. Dashes are trimmed, an empty result becomes
// "session", and the result is capped at maxLen.
func sanitizeName(raw string, maxLen int) string {
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "session"
	}
	if len(base) > maxLen {
		base = strings.TrimRight(base[:maxLen], "-")
	}
	return base
}

func sanitizedSessionName(raw string) string {
	base := sanitizeName(raw, 32)
	sum := sha256.Sum256([]byte(raw))
	return base + "-" + hex.EncodeToString(sum[:4])
}

// branchNameMaxLen caps a branch-mirroring tmux name. It is looser than the 32
// used for hashed ids because these names carry no hash suffix and readability
// (the whole branch) is the point.
const branchNameMaxLen = 64

// branchSessionName mirrors a session's branch into a tmux session name: the
// project id joined to the branch, sanitized and flattened with dashes. tmux's
// namespace is flat and global, so the project id prefix keeps names unique
// across projects (a branch is unique within a project).
func branchSessionName(projectID, branch string) string {
	return sanitizeName(projectID+"/"+branch, branchNameMaxLen)
}

// SessionNameFor returns the tmux session name for a session. A session with a
// branch (worker or orchestrator) gets a branch-mirroring name so `tmux ls`
// lines up with the branch and its worktree directory. A session without a
// branch (e.g. the reviewer pane, which uses a synthetic id) falls back to
// session-id naming, which also carries the empty-id guard.
func SessionNameFor(projectID, branch, sessionID string) (string, error) {
	if projectID != "" && branch != "" {
		return branchSessionName(projectID, branch), nil
	}
	return tmuxSessionName(domain.SessionID(sessionID))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/adapters/runtime/tmux/ -run 'TestSessionNameFor|TestSessionName' -v`
Expected: PASS (new `TestSessionNameFor*` plus the untouched `TestSessionName*`).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/runtime/tmux/tmux.go backend/internal/adapters/runtime/tmux/tmux_test.go
git commit -m "feat(tmux): derive branch-mirroring session name via SessionNameFor"
```

---

### Task 2: Carry ProjectID/Branch through RuntimeConfig and name the tmux session with them

**Files:**
- Modify: `backend/internal/ports/outbound.go` (`RuntimeConfig`, ~lines 90–95)
- Modify: `backend/internal/adapters/runtime/tmux/tmux.go` (`Create`, ~line 105)
- Modify: `backend/internal/session_manager/manager.go` (two `RuntimeConfig{}` sites, ~lines 323 and 629)
- Test: `backend/internal/adapters/runtime/tmux/tmux_test.go`
- Test: `backend/internal/session_manager/manager_test.go`

**Interfaces:**
- Consumes: `SessionNameFor` from Task 1.
- Produces: `ports.RuntimeConfig` now has `ProjectID domain.ProjectID` and `Branch string`. `tmux.Runtime.Create` names the session via `SessionNameFor(string(cfg.ProjectID), cfg.Branch, string(cfg.SessionID))`.

- [ ] **Step 1: Add the fields to RuntimeConfig**

In `backend/internal/ports/outbound.go`, extend `RuntimeConfig`:

```go
type RuntimeConfig struct {
	SessionID     domain.SessionID
	ProjectID     domain.ProjectID
	Branch        string
	WorkspacePath string
	Argv          []string
	Env           map[string]string
}
```

- [ ] **Step 2: Write the failing tmux Create test**

Add to `backend/internal/adapters/runtime/tmux/tmux_test.go` (near `TestCreateIssuesNewSessionAndStatusOff`, ~line 211):

```go
func TestCreateNamesSessionAfterBranch(t *testing.T) {
	r, fr := newTestRuntime(0)
	fr.outputs = [][]byte{nil, nil, nil, nil}

	h, err := r.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     "mer-1",
		ProjectID:     "mer",
		Branch:        "feature/PROJ-2271-x",
		WorkspacePath: "/tmp/ws",
		Argv:          []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if h.ID != "mer-feature-PROJ-2271-x" {
		t.Fatalf("handle ID = %q, want mer-feature-PROJ-2271-x", h.ID)
	}
	if joined := strings.Join(fr.calls[0].args, " "); !strings.Contains(joined, "-s mer-feature-PROJ-2271-x") {
		t.Fatalf("new-session args missing branch-based -s: %v", fr.calls[0].args)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/runtime/tmux/ -run TestCreateNamesSessionAfterBranch -v`
Expected: FAIL — `handle ID = "mer-1"` (Create still names from the session id).

- [ ] **Step 4: Make Create use SessionNameFor**

In `backend/internal/adapters/runtime/tmux/tmux.go`, in `Create` (~line 105) replace:

```go
	id, err := tmuxSessionName(cfg.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
```

with:

```go
	id, err := SessionNameFor(string(cfg.ProjectID), cfg.Branch, string(cfg.SessionID))
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/runtime/tmux/ -run 'TestCreate|TestSessionName' -v`
Expected: PASS. (`TestCreateIssuesNewSessionAndStatusOff` still passes — its config has no branch, so it keeps the `sess-1` fallback name.)

- [ ] **Step 6: Populate the fields at both manager Create sites**

In `backend/internal/session_manager/manager.go`, the spawn site (~line 323):

```go
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		ProjectID:     cfg.ProjectID,
		Branch:        ws.Branch,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           m.runtimeEnv(id, cfg.ProjectID, cfg.IssueID, project.Config.Env),
	})
```

The restore site (~line 629):

```go
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		ProjectID:     rec.ProjectID,
		Branch:        ws.Branch,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           m.runtimeEnv(id, rec.ProjectID, rec.IssueID, project.Config.Env),
	})
```

- [ ] **Step 7: Write the failing manager assertion**

Add to `backend/internal/session_manager/manager_test.go` (a standalone test). `newManager` returns `(*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace)`; `fakeRuntime.lastCfg` records the last runtime config; and `fakeWorkspace.Create` echoes `cfg.Branch` back as `WorkspaceInfo.Branch` (manager_test.go ~line 299), so passing `Branch` in `SpawnConfig` is all that is needed:

```go
func TestSpawnPassesProjectAndBranchToRuntime(t *testing.T) {
	m, _, rt, _ := newManager()
	if _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "proj", Kind: domain.KindWorker, Branch: "feature/x"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rt.lastCfg.ProjectID != "proj" {
		t.Fatalf("runtime ProjectID = %q, want proj", rt.lastCfg.ProjectID)
	}
	if rt.lastCfg.Branch != "feature/x" {
		t.Fatalf("runtime Branch = %q, want feature/x", rt.lastCfg.Branch)
	}
}
```

- [ ] **Step 8: Run the manager test to verify it passes**

Run: `cd backend && go test ./internal/session_manager/ -run TestSpawnPassesProjectAndBranchToRuntime -v`
Expected: PASS.

- [ ] **Step 9: Run the affected package tests**

Run: `cd backend && go test ./internal/adapters/runtime/tmux/ ./internal/session_manager/ ./internal/ports/...`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/ports/outbound.go backend/internal/adapters/runtime/tmux/tmux.go backend/internal/adapters/runtime/tmux/tmux_test.go backend/internal/session_manager/manager.go backend/internal/session_manager/manager_test.go
git commit -m "feat(session): name tmux sessions after their branch"
```

---

### Task 3: Fix the `ao spawn` attach hint to reuse the branch-based name

**Files:**
- Modify: `backend/internal/cli/spawn.go` (`spawnResult`, ~lines 49–54; attach hint, ~lines 140–145)

**Interfaces:**
- Consumes: `tmux.SessionNameFor` from Task 1; `projectId` and `branch` fields already serialized on the session read model (`domain.SessionRecord.ProjectID`, `SessionView.Branch`).
- Produces: nothing new.

- [ ] **Step 1: Decode projectId and branch from the spawn response**

In `backend/internal/cli/spawn.go`, extend the `spawnResult.Session` struct (~lines 49–54):

```go
type spawnResult struct {
	Session struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		ProjectID string `json:"projectId"`
		Branch    string `json:"branch"`
	} `json:"session"`
}
```

- [ ] **Step 2: Reuse SessionNameFor for the attach hint**

In `backend/internal/cli/spawn.go`, replace the non-Windows attach branch (~line 141):

```go
			if runtime.GOOS != "windows" {
				attach = fmt.Sprintf("tmux attach -t %s", tmux.SessionName(res.Session.ID))
			} else {
```

with:

```go
			if runtime.GOOS != "windows" {
				// Reuse the runtime's own naming so the hint matches the actual
				// tmux session (branch-mirroring name; falls back to the id).
				name, nameErr := tmux.SessionNameFor(res.Session.ProjectID, res.Session.Branch, res.Session.ID)
				if nameErr != nil {
					name = res.Session.ID
				}
				attach = fmt.Sprintf("tmux attach -t %s", name)
			} else {
```

- [ ] **Step 3: Verify the CLI package builds and tests pass**

Run: `cd backend && go build ./internal/cli/ && go test ./internal/cli/`
Expected: PASS (no existing test asserts the attach hint string; this confirms the new signature compiles and nothing regresses).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/cli/spawn.go
git commit -m "fix(cli): attach hint reuses branch-based tmux session name"
```

---

### Task 4: Full backend build and test sweep

**Files:** none (verification only).

- [ ] **Step 1: Build the whole backend**

Run: `cd backend && go build ./...`
Expected: no output (success).

- [ ] **Step 2: Vet**

Run: `cd backend && go vet ./...`
Expected: no findings.

- [ ] **Step 3: Run the full backend test suite**

Run: `cd backend && go test ./...`
Expected: PASS across all packages. If any test outside the packages touched here fails, investigate whether it asserts a tmux session name derived from a session id for a branch-carrying session and update it to the branch-based expectation.

- [ ] **Step 4: Manual smoke (optional, if a tmux environment is available)**

Spawn a worker with an explicit branch, then confirm the tmux session and the attach hint agree:

```bash
ao spawn --project <proj> --branch feature/smoke-test ...   # note the printed "attach with:" line
tmux ls                                                     # expect <proj>-feature-smoke-test
```

Expected: the `tmux ls` entry and the `attach with:` hint both read `<proj>-feature-smoke-test`.
