# AI-generated gitflow branch names Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When "New branch name" is left blank in the New Task dialog, name the worker branch by asking the session's own agent CLI (one-shot) for a `<type>/<JIRA-KEY>-<short-desc>` gitflow name, falling back to `ao/<id>/root` on any failure.

**Architecture:** A new optional `OneShotNamer` port lets an agent adapter answer a single non-interactive prompt (`claude -p`). At spawn, when an opt-in flag is set and the branch is blank, the manager asks the session's adapter for a name, sanitizes it, de-duplicates against existing refs, and uses it — otherwise it falls back to today's `defaultSessionBranch`. The dialog sets the opt-in flag whenever its branch field is blank and rearranges its fields into a full-width stack.

**Tech Stack:** Go (backend daemon, `internal/...`), code-first OpenAPI (`npm run api`), React 19 + Tailwind v4 (`frontend/src/renderer`).

## Global Constraints

- Spawning must NEVER fail or hang because of naming: every naming failure path resolves to `defaultSessionBranch(id, cfg.Kind, sessionPrefix(project))`.
- No network and no real agent CLI in Go tests — fake the `OneShotNamer`.
- App state stays under `~/.ao` only; the naming call's temp CWD is not app state (OS temp dir is fine).
- Do NOT change orchestrator branch naming (`ao/<prefix>-orchestrator`), `workerMultiPRPrompt`, or observer attribution — verified correct as-is.
- Generation runs ONLY when `AutoNameBranch == true` AND `cfg.Branch == ""` AND `cfg.Kind != domain.KindOrchestrator`.
- Timeout is bounded: default 20s, override via `AO_BRANCH_NAME_TIMEOUT` (Go duration string).
- Allowed gitflow types: `feature`, `bugfix`, `hotfix`, `chore`.
- Prefer `internal/process.CommandContext` over raw `os/exec` for the naming subprocess.

---

### Task 1: `OneShotNamer` port + claude-code adapter

**Files:**
- Modify: `backend/internal/ports/agent.go`
- Modify: `backend/internal/adapters/agent/claudecode/claudecode.go`
- Test: `backend/internal/adapters/agent/claudecode/claudecode_test.go`

**Interfaces:**
- Produces: `ports.OneShotNamer` interface; `*claudecode.Provider` implements `OneShotArgv(ctx, prompt) (argv []string, ok bool, err error)`.

- [ ] **Step 1: Write the failing test** in `claudecode_test.go`

Note: the provider type is `*Plugin`; existing tests construct it as `&Plugin{resolvedBinary: "claude"}` so `claudeBinary(ctx)` returns the cached `"claude"` without env resolution (no skip needed). `ports` is already imported in this test file.

```go
func TestOneShotArgv(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	var namer ports.OneShotNamer = p // compile-time proof the interface is satisfied
	argv, ok, err := namer.OneShotArgv(context.Background(), "name this branch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("claude-code must support one-shot naming")
	}
	if len(argv) != 5 {
		t.Fatalf("want 5-element argv, got %v", argv)
	}
	if argv[0] != "claude" {
		t.Fatalf("argv[0] must be the resolved binary, got %q", argv[0])
	}
	if argv[1] != "-p" || argv[2] != "name this branch" || argv[3] != "--output-format" || argv[4] != "text" {
		t.Fatalf("argv must be [binary -p <prompt> --output-format text], got %v", argv)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/agent/claudecode/ -run TestOneShotArgv`
Expected: FAIL — `OneShotArgv` undefined / does not implement `ports.OneShotNamer`.

- [ ] **Step 3: Add the port** to `internal/ports/agent.go` (near the `Agent` interface)

```go
// OneShotNamer is implemented by agent adapters that can answer a single
// non-interactive prompt (e.g. `claude -p`). Adapters that only run
// interactive sessions do not implement it; callers must handle ok == false.
type OneShotNamer interface {
	// OneShotArgv returns the argv to run the given prompt non-interactively.
	// ok == false means this harness has no one-shot mode (caller falls back).
	OneShotArgv(ctx context.Context, prompt string) (argv []string, ok bool, err error)
}
```

Ensure `context` is imported in `agent.go`.

- [ ] **Step 4: Implement `OneShotArgv`** on the claude-code provider in `claudecode.go`

```go
// OneShotArgv runs a single prompt non-interactively via `claude -p`. Used by the
// session manager to generate a branch name at spawn; failure is non-fatal there.
func (p *Plugin) OneShotArgv(ctx context.Context, prompt string) ([]string, bool, error) {
	binary, err := p.claudeBinary(ctx) // existing cached resolver (ResolveClaudeBinary)
	if err != nil {
		return nil, false, err
	}
	return []string{binary, "-p", prompt, "--output-format", "text"}, true, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/agent/claudecode/ -run TestOneShotArgv`
Expected: PASS (or SKIP if no claude binary — the compile-time `var namer ports.OneShotNamer = p` still proves the interface is satisfied).

- [ ] **Step 6: Build + commit**

Run: `cd backend && go build ./...`
```bash
git add backend/internal/ports/agent.go backend/internal/adapters/agent/claudecode/
git commit -m "feat(agent): add OneShotNamer port and claude-code claude -p impl"
```

---

### Task 2: Pure branch-name helpers

**Files:**
- Create: `backend/internal/session_manager/branchname.go`
- Test: `backend/internal/session_manager/branchname_test.go`

**Interfaces:**
- Produces (all in `package session_manager`):
  - `func extractJiraKey(texts ...string) string`
  - `func buildNamingPrompt(title, brief, jiraKeyHint string) string`
  - `func sanitizeBranchName(raw string) (string, bool)`
  - `func ensureUniqueBranch(existing map[string]bool, candidate string) string`

- [ ] **Step 1: Write the failing tests** in `branchname_test.go`

```go
package session_manager

import "testing"

func TestExtractJiraKey(t *testing.T) {
	cases := []struct {
		name  string
		texts []string
		want  string
	}{
		{"in title", []string{"STAR-2271 result UI", "brief"}, "STAR-2271"},
		{"in brief url", []string{"E-Coupon", "see https://x.atlassian.net/browse/ABC-42 now"}, "ABC-42"},
		{"none", []string{"no key here", "plain brief"}, ""},
		{"lowercase not matched", []string{"star-2271", ""}, ""},
		{"multi-letter project", []string{"PROJ12-9 thing", ""}, "PROJ12-9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractJiraKey(c.texts...); got != c.want {
				t.Fatalf("extractJiraKey(%v) = %q, want %q", c.texts, got, c.want)
			}
		})
	}
}

func TestSanitizeBranchName(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   string
		wantOK bool
	}{
		{"clean", "feature/STAR-2271-ecoupon-result", "feature/star-2271-ecoupon-result", true},
		{"backticked with prose", "`feature/STAR-2271-x`\nSure, here you go!", "feature/star-2271-x", true},
		{"label prefix", "branch: bugfix/ABC-1-fix-crash", "bugfix/abc-1-fix-crash", true},
		{"spaces and junk", "feature/STAR 2271  e coupon!!", "feature/star-2271-e-coupon", true},
		{"no gitflow prefix", "star-2271-result", "", false},
		{"bad type", "release/STAR-1-x", "", false},
		{"dotdot", "feature/STAR..1", "", false},
		{"empty", "", "", false},
		{"trailing slash trimmed then ok", "chore/cleanup/", "chore/cleanup", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := sanitizeBranchName(c.raw)
			if got != c.want || ok != c.wantOK {
				t.Fatalf("sanitizeBranchName(%q) = (%q,%v), want (%q,%v)", c.raw, got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestEnsureUniqueBranch(t *testing.T) {
	existing := map[string]bool{
		"feature/star-2271-x":   true,
		"feature/star-2271-x-2": true,
	}
	if got := ensureUniqueBranch(existing, "feature/star-2271-y"); got != "feature/star-2271-y" {
		t.Fatalf("free candidate changed: %q", got)
	}
	if got := ensureUniqueBranch(existing, "feature/star-2271-x"); got != "feature/star-2271-x-3" {
		t.Fatalf("collision suffix wrong: %q", got)
	}
}

func TestBuildNamingPromptMentionsKeyAndRules(t *testing.T) {
	p := buildNamingPrompt("E-Coupon Order Result", "make the UI", "STAR-2271")
	for _, want := range []string{"STAR-2271", "feature", "bugfix", "hotfix", "chore", "E-Coupon Order Result"} {
		if !contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd backend && go test ./internal/session_manager/ -run 'TestExtractJiraKey|TestSanitizeBranchName|TestEnsureUniqueBranch|TestBuildNamingPrompt'`
Expected: FAIL — helpers undefined.

- [ ] **Step 3: Implement** `branchname.go`

```go
package session_manager

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	jiraKeyRe        = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)
	allowedTypes     = map[string]bool{"feature": true, "bugfix": true, "hotfix": true, "chore": true}
	nonBranchChars   = regexp.MustCompile(`[^a-z0-9/-]+`)
	repeatedSlashDash = regexp.MustCompile(`[-]{2,}`)
)

// extractJiraKey returns the first Jira-style key (e.g. STAR-2271) found across
// the given texts, or "" when none is present.
func extractJiraKey(texts ...string) string {
	for _, t := range texts {
		if m := jiraKeyRe.FindString(t); m != "" {
			return m
		}
	}
	return ""
}

// buildNamingPrompt asks an agent to emit ONLY a gitflow branch name.
func buildNamingPrompt(title, brief, jiraKeyHint string) string {
	keyLine := "No Jira key detected — omit the key segment."
	if jiraKeyHint != "" {
		keyLine = fmt.Sprintf("Detected Jira key: %s — put it uppercase right after the type slash.", jiraKeyHint)
	}
	return fmt.Sprintf(`Generate ONE git branch name for the task below. Output ONLY the branch name on a single line — no backticks, no quotes, no explanation.

Format: <type>/<JIRA-KEY>-<short-desc>
- <type> is exactly one of: feature, bugfix, hotfix, chore (infer from the task's intent).
- %s
- <short-desc>: 2 to 4 words, kebab-case, lowercase, abbreviated.
- Total length <= 60 characters. Use only lowercase a-z, 0-9, hyphen and one slash.
- Example: feature/STAR-2271-ecoupon-result

Task title: %s

Task brief:
%s`, keyLine, title, brief)
}

// sanitizeBranchName cleans a raw agent response into a safe gitflow branch name.
// ok == false means the output could not be trusted and the caller must fall back.
func sanitizeBranchName(raw string) (string, bool) {
	line := ""
	for _, l := range strings.Split(raw, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			line = s
			break
		}
	}
	line = strings.Trim(line, "`\"' \t")
	// strip a leading "branch:"-style label
	if i := strings.IndexByte(line, ':'); i >= 0 && i < 12 && !strings.Contains(line[:i], "/") {
		line = strings.TrimSpace(line[i+1:])
	}
	line = strings.ToLower(line)
	if strings.Contains(line, "..") {
		return "", false
	}
	line = nonBranchChars.ReplaceAllString(line, "-")
	line = repeatedSlashDash.ReplaceAllString(line, "-")
	for strings.Contains(line, "//") {
		line = strings.ReplaceAll(line, "//", "/")
	}
	line = strings.Trim(line, "-/")
	if line == "" || len(line) > 80 || strings.HasSuffix(line, ".lock") {
		return "", false
	}
	slash := strings.IndexByte(line, '/')
	if slash <= 0 {
		return "", false
	}
	if !allowedTypes[line[:slash]] {
		return "", false
	}
	if strings.TrimSpace(line[slash+1:]) == "" {
		return "", false
	}
	return line, true
}

// ensureUniqueBranch returns candidate, or candidate-2, candidate-3, ... until it
// is not present in existing. Keys in existing are bare branch names (no refs/…).
func ensureUniqueBranch(existing map[string]bool, candidate string) string {
	if !existing[candidate] {
		return candidate
	}
	for n := 2; n < 1000; n++ {
		next := fmt.Sprintf("%s-%d", candidate, n)
		if !existing[next] {
			return next
		}
	}
	return candidate // pathological; caller still falls back on collision via workspace.Create error
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd backend && go test ./internal/session_manager/ -run 'TestExtractJiraKey|TestSanitizeBranchName|TestEnsureUniqueBranch|TestBuildNamingPrompt'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/session_manager/branchname.go backend/internal/session_manager/branchname_test.go
git commit -m "feat(session): add gitflow branch-name helpers (extract/sanitize/unique)"
```

---

### Task 3: Manager generation + wiring + `SpawnConfig.AutoNameBranch`

**Files:**
- Modify: `backend/internal/ports/session.go`
- Modify: `backend/internal/session_manager/branchname.go` (add manager methods)
- Modify: `backend/internal/session_manager/manager.go` (~L225-228)
- Test: `backend/internal/session_manager/manager_test.go` (or a new `branchname_manager_test.go`)

**Interfaces:**
- Consumes: `ports.OneShotNamer` (Task 1); `extractJiraKey`, `buildNamingPrompt`, `sanitizeBranchName`, `ensureUniqueBranch` (Task 2).
- Produces: `SpawnConfig.AutoNameBranch bool`; `(m *Manager) generateBranchName(ctx, agent ports.Agent, cfg ports.SpawnConfig, project domain.ProjectRecord) (string, bool)`; `(m *Manager) existingBranchNames(ctx, project domain.ProjectRecord) map[string]bool`.

- [ ] **Step 1: Add the config field** to `internal/ports/session.go`

```go
	// AutoNameBranch asks the manager to generate a gitflow branch name via the
	// session's agent (one-shot) when Branch is empty. Non-dialog callers leave
	// it false to keep the ao/<id>/root default. Best-effort: any failure falls
	// back to the default name.
	AutoNameBranch bool
```
(Add inside the `SpawnConfig` struct, after `Branch`/`BaseBranch`.)

- [ ] **Step 2: Write the failing manager test**

Model construction on the existing manager tests (fake agents resolver, fake workspace that records the `WorkspaceConfig` it receives). Add a fake namer to whatever fake agent the tests already use so it satisfies `ports.OneShotNamer`. The fake's `OneShotArgv` should return an argv that, when the manager runs it, is intercepted — but since the manager execs a real process, instead make `generateBranchName` testable WITHOUT exec by having the test drive the pieces it can: assert wiring via a fake agent whose `OneShotArgv` returns `ok=false` (forces fallback) and a separate direct test of `generateBranchName` is NOT possible without exec. Therefore test the WIRING through observable branch on `WorkspaceConfig`:

```go
func TestSpawnBranchNaming(t *testing.T) {
	// Uses the existing manager test harness. The fake workspace must capture
	// the WorkspaceConfig.Branch it is given.
	t.Run("flag off keeps ao default", func(t *testing.T) {
		h := newManagerHarness(t) // existing helper (match the real name in manager_test.go)
		rec, err := h.mgr.Spawn(ctx, ports.SpawnConfig{ProjectID: h.projectID, Kind: domain.KindWorker, Harness: h.harness})
		if err != nil { t.Fatal(err) }
		if !strings.HasPrefix(h.workspace.lastCfg.Branch, "ao/") {
			t.Fatalf("want ao/ default, got %q", h.workspace.lastCfg.Branch)
		}
		_ = rec
	})

	t.Run("orchestrator ignores flag", func(t *testing.T) {
		h := newManagerHarness(t)
		_, err := h.mgr.Spawn(ctx, ports.SpawnConfig{ProjectID: h.projectID, Kind: domain.KindOrchestrator, Harness: h.harness, AutoNameBranch: true})
		if err != nil { t.Fatal(err) }
		if !strings.Contains(h.workspace.lastCfg.Branch, "-orchestrator") {
			t.Fatalf("orchestrator name expected, got %q", h.workspace.lastCfg.Branch)
		}
	})

	t.Run("unsupported harness falls back", func(t *testing.T) {
		h := newManagerHarness(t) // fake agent does NOT implement OneShotNamer
		_, err := h.mgr.Spawn(ctx, ports.SpawnConfig{ProjectID: h.projectID, Kind: domain.KindWorker, Harness: h.harness, AutoNameBranch: true, IssueID: "STAR-1 thing"})
		if err != nil { t.Fatal(err) }
		if !strings.HasPrefix(h.workspace.lastCfg.Branch, "ao/") {
			t.Fatalf("want fallback ao/, got %q", h.workspace.lastCfg.Branch)
		}
	})
}
```

If the existing manager-test fakes do not expose `lastCfg`, extend the fake workspace to record it (smallest change). If there is no reusable harness helper, follow the construction already used by the nearest existing `Spawn` test in `manager_test.go`.

**Note for the implementer:** exercising the SUCCESS path (real name from a namer) requires executing a subprocess, which the no-network/no-CLI rule forbids in tests. Cover success at the unit level in Task 2 (`sanitizeBranchName`/`ensureUniqueBranch`) and cover the manager WIRING (flag off, orchestrator, unsupported→fallback) here. Do not add a test that execs a real agent.

- [ ] **Step 3: Run to verify it fails**

Run: `cd backend && go test ./internal/session_manager/ -run TestSpawnBranchNaming`
Expected: FAIL — `AutoNameBranch` unknown field / behavior missing.

- [ ] **Step 4: Implement the manager methods** in `branchname.go`

```go
import (
	"context"
	"os"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func branchNameTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("AO_BRANCH_NAME_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 20 * time.Second
}

// generateBranchName asks the session's agent for a gitflow branch name. It is
// best-effort: ok == false on any failure and the caller falls back.
func (m *Manager) generateBranchName(ctx context.Context, agent ports.Agent, cfg ports.SpawnConfig, project domain.ProjectRecord) (string, bool) {
	namer, isNamer := agent.(ports.OneShotNamer)
	if !isNamer {
		return "", false
	}
	key := extractJiraKey(string(cfg.IssueID), cfg.Prompt)
	prompt := buildNamingPrompt(string(cfg.IssueID), cfg.Prompt, key)

	cctx, cancel := context.WithTimeout(ctx, branchNameTimeout())
	defer cancel()
	argv, ok, err := namer.OneShotArgv(cctx, prompt)
	if !ok || err != nil || len(argv) == 0 {
		return "", false
	}
	tmpDir, err := os.MkdirTemp("", "ao-branchname-")
	if err != nil {
		return "", false
	}
	defer os.RemoveAll(tmpDir)

	cmd := aoprocess.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = tmpDir
	out, err := cmd.Output()
	if cctx.Err() != nil || err != nil {
		return "", false
	}
	name, ok := sanitizeBranchName(string(out))
	if !ok {
		return "", false
	}
	return name, true
}

// existingBranchNames lists local and origin branch short-names in the project
// repo so a generated name can be de-duplicated before worktree creation.
func (m *Manager) existingBranchNames(ctx context.Context, project domain.ProjectRecord) map[string]bool {
	set := map[string]bool{}
	if strings.TrimSpace(project.Path) == "" {
		return set
	}
	cmd := aoprocess.CommandContext(ctx, "git", "-C", project.Path,
		"for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes/origin")
	out, err := cmd.Output()
	if err != nil {
		return set
	}
	for _, l := range strings.Split(string(out), "\n") {
		s := strings.TrimSpace(l)
		if s == "" || s == "origin" { // git shortens refs/remotes/origin/HEAD to bare "origin"
			continue
		}
		set[strings.TrimPrefix(s, "origin/")] = true
	}
	return set
}
```

(Adjust the import path/alias to match how `internal/process` is imported elsewhere in the package, e.g. `github.com/.../internal/process`. Confirm the module path prefix with an existing import in `manager.go`.)

- [ ] **Step 5: Wire it into `Spawn`** — replace `manager.go` L225-228 with:

```go
	branch := cfg.Branch
	if branch == "" {
		if cfg.AutoNameBranch && cfg.Kind != domain.KindOrchestrator {
			if agent, ok := m.agents.Agent(cfg.Harness); ok {
				if name, ok := m.generateBranchName(ctx, agent, cfg, project); ok {
					branch = ensureUniqueBranch(m.existingBranchNames(ctx, project), name)
				}
			}
		}
		if branch == "" {
			branch = defaultSessionBranch(id, cfg.Kind, sessionPrefix(project))
		}
	}
```

- [ ] **Step 6: Run test + build**

Run: `cd backend && go test ./internal/session_manager/ -run TestSpawnBranchNaming && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/ports/session.go backend/internal/session_manager/
git commit -m "feat(session): generate gitflow branch name via session agent at spawn"
```

---

### Task 4: HTTP request field + codegen

**Files:**
- Modify: `backend/internal/httpd/controllers/dto.go`
- Modify: the create-session controller that maps the request DTO to `ports.SpawnConfig` (same file/handler that already maps `Branch`/`BaseBranch`)
- Regenerate: `openapi.yaml`, `frontend/src/api/schema.ts`
- Test: `backend/internal/httpd/controllers/*_test.go` (extend the existing create-session mapping test if present)

**Interfaces:**
- Consumes: `ports.SpawnConfig.AutoNameBranch` (Task 3).
- Produces: request JSON field `autoNameBranch` mapped into `SpawnConfig`.

- [ ] **Step 1: Add the DTO field** in `dto.go` (in the create-session request struct, next to `BaseBranch`)

```go
	AutoNameBranch bool `json:"autoNameBranch,omitempty"`
```

- [ ] **Step 2: Map it in the controller** where `SpawnConfig` is built from the request (alongside `Branch: in.Branch, BaseBranch: in.BaseBranch`)

```go
		AutoNameBranch: in.AutoNameBranch,
```

- [ ] **Step 3: Extend the controller mapping test** (if one exists) to assert the field is threaded

```go
	// given a request body with "autoNameBranch": true, the SpawnConfig passed to
	// the fake service has AutoNameBranch == true.
```
If no such test exists, add a minimal one following the nearest existing create-session handler test.

- [ ] **Step 4: Regenerate the API**

Run: `cd frontend && npm run api`
Expected: `openapi.yaml` gains `autoNameBranch` on the create-session request; `frontend/src/api/schema.ts` regenerates. Do not hand-edit generated files.

- [ ] **Step 5: Build + test**

Run: `cd backend && go build ./... && go test ./internal/httpd/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/httpd/controllers/ openapi.yaml frontend/src/api/schema.ts
git commit -m "feat(api): add autoNameBranch to create-session request"
```

---

### Task 5: NewTaskDialog — full-width layout + opt-in flag

**Files:**
- Modify: `frontend/src/renderer/components/NewTaskDialog.tsx`
- Test: `frontend/src/renderer/components/NewTaskDialog.test.tsx` (extend if present; else add)

**Interfaces:**
- Consumes: `autoNameBranch` request field (Task 4, via regenerated `schema.ts`).

- [ ] **Step 1: Write/extend the failing test** in `NewTaskDialog.test.tsx`

```tsx
// 1. Blank new-branch-name → POST body has autoNameBranch: true and no branch.
// 2. Typed new-branch-name → body has branch set and no autoNameBranch (or false/undefined).
// 3. Renders three stacked fields in order: "Start from", "New branch name", "Agent".
```
Follow the existing test's mocking of `apiClient.POST` to capture the request body. Assert on the captured `body.autoNameBranch` and `body.branch`, and on field order via `getAllByText`/label queries.

- [ ] **Step 2: Run to verify it fails**

Run: `cd frontend && npm test -- NewTaskDialog`
Expected: FAIL — `autoNameBranch` not sent / layout order differs.

- [ ] **Step 3: Rearrange the layout** — replace the `grid ... sm:grid-cols-[1fr_1fr]` block (`NewTaskDialog.tsx:204-240`) with three stacked full-width blocks. "Start from" already exists above at L189-202; keep it, then add "New branch name" and "Agent" each as their own `space-y-1.5` block (Agent keeps the "Refresh agents" button beneath it):

```tsx
	<div className="space-y-1.5">
		<label className="text-[12px] font-medium text-muted-foreground" htmlFor={branchId}>
			New branch name
		</label>
		<Input
			id={branchId}
			placeholder="optional — AI names it if blank"
			value={branch}
			onChange={(event) => setBranch(event.target.value)}
		/>
	</div>

	<div className="space-y-1.5">
		<RequiredAgentField
			id={agentId}
			label="Agent"
			placeholder="Project default"
			value={agent}
			authorized={agentCatalog?.authorized}
			installed={agentCatalog?.installed}
			supported={agentCatalog?.supported}
			disabled={agentsQuery.isFetching && agentCatalog === undefined}
			onChange={(value) => {
				setAgent(value);
				setAgentTouched(true);
			}}
		/>
		<button
			type="button"
			className="text-[12px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:pointer-events-none disabled:opacity-50"
			disabled={refreshAgentsMutation.isPending}
			onClick={() => refreshAgentsMutation.mutate()}
		>
			{refreshAgentsMutation.isPending ? "Refreshing agents..." : "Refresh agents"}
		</button>
	</div>
```

Order in the form: Title → Brief → Start from → New branch name → Agent.

- [ ] **Step 4: Send the opt-in flag** — in `submit`, update the POST body:

```tsx
					branch: cleanBranch || undefined,
					baseBranch: cleanBase || undefined,
					autoNameBranch: cleanBranch === "" ? true : undefined,
```

- [ ] **Step 5: Update the submit label** for the naming wait — where the button renders `"Starting..."`:

```tsx
	{isSubmitting ? (branch.trim() === "" ? "Naming branch…" : "Starting…") : "Start task"}
```
(Keep the existing spinner icon.)

- [ ] **Step 6: Run test + typecheck**

Run: `cd frontend && npm test -- NewTaskDialog && npm run -s typecheck` (or the project's lint/build check)
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/renderer/components/NewTaskDialog.tsx frontend/src/renderer/components/NewTaskDialog.test.tsx
git commit -m "feat(dialog): stack New Task fields full-width and opt into AI branch naming"
```

---

## Self-Review Notes

- **Spec coverage:** opt-in flag (T3/T4), OneShotNamer + claude impl (T1), helpers (T2), generate+wire+fallback (T3), full-width layout + flag + label (T5). Attribution/prompt unchanged (documented, no task needed).
- **Fallback guarantee:** every failure branch in `generateBranchName` returns `("", false)`; the wiring's inner `if branch == ""` always reaches `defaultSessionBranch`.
- **Test-reality constraint:** success path uses a subprocess, forbidden in tests → covered at unit level (T2) + wiring level (T3). Called out explicitly in T3 Step 2 so no one writes an exec-ing test.
- **Type consistency:** `AutoNameBranch` (Go) ↔ `autoNameBranch` (JSON/TS) consistent across T3/T4/T5; helper names identical across T2/T3.
