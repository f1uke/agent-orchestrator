# Task Base-Branch Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a New Task choose the base branch it starts from (searchable dropdown of the repo's branches) in addition to naming the new working branch, instead of always branching off the project default.

**Architecture:** Backend gains a thin `GET /projects/{id}/branches` list endpoint (project service runs `git for-each-ref`) and a new `SpawnConfig.BaseBranch` threaded from the create-session request into `session_manager`'s worktree creation (which already accepts `BaseBranch`). Frontend New Task dialog fetches the branch list and adds a "Start from" combobox defaulting to the project default branch.

**Tech Stack:** Go (chi router, project service git-exec), sqlc unaffected (no schema change), code-first OpenAPI (`npm run api`), React 19 + Tailwind + react-query, shadcn primitives.

## Global Constraints

- Purely additive: with no base chosen, behavior is identical to today (falls back to `project.Config.WithDefaults().DefaultBranch`). CLI `ao start` unaffected.
- Base is used ONLY when creating a NEW branch. If the new-branch name matches an existing branch, gitworktree checks it out and base is ignored (git semantics) — do not try to change that.
- Do not change orchestrator branch behavior (`ao/<prefix>-orchestrator`).
- No new frontend dependency — build the combobox from existing primitives (`Input` + filtered list). Follow DESIGN.md (clone agent-orchestrator; mono, dense; reuse `components/ui/*`).
- No network in Go tests (fakes/temp git repos as existing project tests do). context.Context first arg.
- Generated artifacts (openapi.yaml, schema.ts) are produced by `npm run api` — edit Go source then regenerate; do not hand-edit generated files.
- Commit per task on branch `feat/gitlab-support`. Run `cd backend && go build ./... && go test ./...` (backend tasks) / `cd frontend && npm run typecheck && npm test` (frontend task) before committing.

---

### Task 1: Backend — list branches endpoint

**Files:**
- Modify: `backend/internal/service/project/` (add a `ListBranches` method following the existing git-exec + repo-path-resolution pattern used by workspace registration)
- Modify: `backend/internal/httpd/controllers/projects.go` (add route `r.Get("/projects/{id}/branches", c.branches)` in `Register`, and the `branches` handler)
- Modify: `backend/internal/httpd/controllers/dto.go` (add `ProjectBranchesResponse{ Branches []string }`)
- Modify: `backend/internal/httpd/apispec/specgen/build.go` (register the new operation + `ProjectBranchesResponse` schema name)
- Test: `backend/internal/service/project/*_test.go` (branch listing over a temp git repo), `backend/internal/httpd/controllers/projects_test.go` (route returns list / empty)

**Interfaces:**
- Produces: `projectsvc.Manager.ListBranches(ctx context.Context, id domain.ProjectID) ([]string, error)` — returns deduped short branch names from `refs/heads` + `refs/remotes/origin`, with `origin/HEAD` removed; returns an empty slice (nil, no error) when the repo is unavailable/unregistered so the UI degrades gracefully.
- Produces: `GET /api/v1/projects/{id}/branches` → `{"branches": ["develop","main","origin/PROJ-2270", ...]}`.

- [ ] **Step 1: Write the failing service test** — over a temp git repo with a couple of branches (mirror the temp-repo setup in `service/project/service_test.go`, which does `exec.Command("git","init","-b","main",dir)` etc.):

```go
func TestListBranches(t *testing.T) {
	// set up a temp repo registered as a project with branches main + feature/x,
	// following the existing helper style in this test file.
	got, err := svc.ListBranches(ctx, projectID)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	// expect the local branches present, deduped, origin/HEAD absent
	if !contains(got, "main") || !contains(got, "feature/x") {
		t.Fatalf("branches = %v", got)
	}
}
```

- [ ] **Step 2: Run it, verify RED**

Run: `cd backend && go test ./internal/service/project/ -run TestListBranches -v`
Expected: FAIL — `ListBranches` undefined.

- [ ] **Step 3: Implement `ListBranches`** — resolve the project's repo path the same way workspace registration does, then run
`git -C <repo> for-each-ref --format=%(refname:short) refs/heads refs/remotes/origin`, split lines, drop `origin/HEAD`, dedupe preserving order. On repo-not-available return `(nil, nil)`.

- [ ] **Step 4: Add the controller route + handler** — in `projects.go` `Register`, add `r.Get("/projects/{id}/branches", c.branches)`. Handler reads `projectID(r)` (chi param helper already used by `c.get`), calls `c.Mgr.ListBranches`, writes `ProjectBranchesResponse{Branches: names}` via the shared JSON envelope (mirror `c.get`'s response writing). Nil `Mgr` → the existing 501 pattern.

- [ ] **Step 5: Add controller route test** — assert 200 + JSON `{branches:[...]}` for a project, and empty list when the repo is unavailable (mirror existing `projects_test.go` httptest style).

- [ ] **Step 6: Regenerate API + verify**

Run: `npm run api && cd backend && go build ./... && go test ./internal/service/project/ ./internal/httpd/...`
Expected: PASS; openapi/schema gain the branches operation only.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/project backend/internal/httpd frontend/src/api backend/internal/httpd/apispec
git commit -m "feat(api): list project branches endpoint"
```

---

### Task 2: Backend — thread base branch through spawn

**Files:**
- Modify: `backend/internal/ports/session.go` (add `BaseBranch string` to `SpawnConfig`)
- Modify: `backend/internal/httpd/controllers/dto.go` (add `BaseBranch string json:"baseBranch,omitempty"` to `SpawnSessionRequest`, near the existing `Branch` field ~line 129)
- Modify: `backend/internal/httpd/controllers/sessions.go:137` (pass `BaseBranch: in.BaseBranch` into `ports.SpawnConfig`)
- Modify: `backend/internal/session_manager/manager.go:225-236` (use `cfg.BaseBranch` else project default for `WorkspaceConfig.BaseBranch`)
- Test: `backend/internal/session_manager/manager_test.go` (base threading), `backend/internal/httpd/controllers/sessions_test.go` (request decodes baseBranch)

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `ports.SpawnConfig.BaseBranch string`; `SpawnSessionRequest.BaseBranch string json:"baseBranch,omitempty"`.

- [ ] **Step 1: Write the failing manager test** — with a fake workspace capturing the `WorkspaceConfig` it receives, assert `cfg.BaseBranch` is honored and empty falls back to project default:

```go
func TestSpawnUsesBaseBranch(t *testing.T) {
	fake := &captureWorkspace{} // records last WorkspaceConfig.BaseBranch
	m := newManagerWithWorkspace(t, fake, projectWithDefaultBranch("develop"))
	_, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: pid, Kind: domain.SessionKindWorker, Harness: "claude-code", BaseBranch: "PROJ-2270"})
	if err != nil { t.Fatalf("spawn: %v", err) }
	if fake.lastBase != "PROJ-2270" { t.Fatalf("base = %q, want PROJ-2270", fake.lastBase) }

	_, _ = m.Spawn(ctx, ports.SpawnConfig{ProjectID: pid, Kind: domain.SessionKindWorker, Harness: "claude-code"})
	if fake.lastBase != "develop" { t.Fatalf("fallback base = %q, want develop", fake.lastBase) }
}
```

(Reuse whatever fake-workspace/manager construction the existing `manager_test.go` already provides; add a `lastBase` capture to that fake if needed.)

- [ ] **Step 2: Run it, verify RED**

Run: `cd backend && go test ./internal/session_manager/ -run TestSpawnUsesBaseBranch -v`
Expected: FAIL — `SpawnConfig.BaseBranch` undefined / base not threaded.

- [ ] **Step 3: Implement** — add the field to `SpawnConfig`; in `manager.go`:

```go
base := cfg.BaseBranch
if base == "" {
	base = project.Config.WithDefaults().DefaultBranch
}
ws, err := m.workspace.Create(ctx, ports.WorkspaceConfig{
	ProjectID: cfg.ProjectID, SessionID: id, Kind: cfg.Kind,
	SessionPrefix: sessionPrefix(project), Branch: branch, BaseBranch: base,
})
```

Add `BaseBranch` to `SpawnSessionRequest` and pass `BaseBranch: in.BaseBranch` at sessions.go:137.

- [ ] **Step 4: Add a controller decode test** — assert a POST body with `"baseBranch":"PROJ-2270"` reaches `Svc.Spawn` with `BaseBranch=="PROJ-2270"` (mirror the existing spawn handler test with a fake `Svc` capturing the `SpawnConfig`).

- [ ] **Step 5: Run + regenerate**

Run: `npm run api && cd backend && go build ./... && go test ./internal/session_manager/ ./internal/httpd/...`
Expected: PASS; schema gains `baseBranch` on the spawn request.

- [ ] **Step 6: Commit**

```bash
git add backend frontend/src/api
git commit -m "feat: choose base branch when spawning a session"
```

---

### Task 3: Frontend — "Start from" combobox in New Task

**Files:**
- Create: `frontend/src/renderer/hooks/useProjectBranches.ts` (react-query fetch of the branches endpoint)
- Create: `frontend/src/renderer/components/BranchCombobox.tsx` (searchable dropdown from `Input` + filtered list)
- Modify: `frontend/src/renderer/components/NewTaskDialog.tsx` (add base state + combobox, relabel Branch → "New branch name", send `baseBranch`)
- Test: `frontend/src/renderer/components/BranchCombobox.test.tsx`, extend `frontend/src/renderer/components/NewTaskDialog.test.tsx`

**Interfaces:**
- Consumes: `GET /api/v1/projects/{projectId}/branches` → `{branches: string[]}` (Task 1).
- Produces: POST `/api/v1/sessions` body now includes `baseBranch: cleanBase || undefined`.

- [ ] **Step 1: Write the failing BranchCombobox test**

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BranchCombobox } from "./BranchCombobox";

it("filters branches and selects one", async () => {
	const onChange = vi.fn();
	render(<BranchCombobox branches={["develop", "main", "origin/PROJ-2270"]} value="develop" onChange={onChange} />);
	await userEvent.click(screen.getByRole("textbox"));
	await userEvent.type(screen.getByRole("textbox"), "2270");
	await userEvent.click(screen.getByText("origin/PROJ-2270"));
	expect(onChange).toHaveBeenCalledWith("origin/PROJ-2270");
});
```

- [ ] **Step 2: Run it, verify RED**

Run: `cd frontend && npm test -- --run src/renderer/components/BranchCombobox.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `BranchCombobox`** — controlled `{ branches: string[]; value: string; onChange: (v: string) => void; id?: string }`. An `Input` bound to `value`; on focus/typing show a scrollable list of `branches` filtered by case-insensitive substring of the current text; clicking an item calls `onChange(item)` and closes. Typed free-text is allowed (value = what's typed) so an unknown ref still submits. Style with existing tokens (mirror the dense dropdown look in `components/ui/select.tsx` / DESIGN.md). Close on blur/escape.

- [ ] **Step 4: Run it, verify GREEN**

Run: `cd frontend && npm test -- --run src/renderer/components/BranchCombobox.test.tsx`
Expected: PASS.

- [ ] **Step 5: Implement `useProjectBranches`** — a react-query hook `useProjectBranches(projectId?: string)` that GETs the branches endpoint via the shared `apiClient` when `projectId` is set, returns `{ branches: string[] }` (default `[]`). Mirror an existing query hook's shape (e.g. `useSessionScmSummary`).

- [ ] **Step 6: Wire into NewTaskDialog** — add `const [base, setBase] = useState("")`; when the project or its default branch loads, initialize `base` to the project's default branch. Fetch `useProjectBranches(projectId)`. Render a "Start from" `BranchCombobox` (label above it, same style as the other fields) with `branches` from the hook (ensure the default branch is present in the list). Relabel the existing "Branch" field to **"New branch name"**, placeholder `optional — auto-named if blank`. In the submit body add `baseBranch: base.trim() || undefined`.

- [ ] **Step 7: Extend NewTaskDialog test** — assert the base combobox renders, and that submitting with a chosen base includes `baseBranch` in the POST body (mock `apiClient.POST` and assert the body). Keep the existing new-branch-name behavior asserted.

- [ ] **Step 8: Verify**

Run: `cd frontend && npm run typecheck && npm test -- --run src/renderer/components/BranchCombobox.test.tsx src/renderer/components/NewTaskDialog.test.tsx`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add frontend/src/renderer
git commit -m "feat(ui): choose base branch + new branch name in New Task"
```

---

## Self-Review Notes

- **Spec coverage:** list endpoint (T1) · SpawnConfig.BaseBranch + DTO + manager threading + regen (T2) · combobox + fetch hook + relabel + payload (T3) · semantics (base only on new branch; existing-name → checkout; blank → auto-name off base) enforced by manager+gitworktree unchanged; documented in T2/plan constraints · additive fallback (empty base → project default) in T2 Step 3 · testing per task. All covered.
- **Type consistency:** `ListBranches(ctx, domain.ProjectID) ([]string, error)`, `ProjectBranchesResponse{Branches []string}`, `SpawnConfig.BaseBranch`, `SpawnSessionRequest.BaseBranch`/json `baseBranch`, `BranchCombobox{branches,value,onChange}`, `useProjectBranches(projectId)` used consistently across tasks.
- **No placeholder git behavior:** base resolution reuses gitworktree's existing `BaseBranch` handling (`baseRefCandidates`), which already tries `origin/<base>` then local — no adapter change needed.
