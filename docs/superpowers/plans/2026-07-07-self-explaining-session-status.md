# Self-Explaining Session Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose *why* a session shows its working/needs-you status — a machine-readable reason code per derivation branch plus a countdown to the next timeout-driven flip — in the Session Inspector and `ao session get`, without changing any status behavior.

**Architecture:** `deriveStatus` (the pure read-time derivation) gains a sibling `deriveStatusDetail` that returns the same status plus a `StatusReason` and an optional next-transition timestamp/target. `deriveStatus` becomes a thin wrapper so every existing caller and test is untouched. The three new facts ride out on `domain.Session` (already the API read model, embedded in `SessionView`), through the code-first OpenAPI regen, into the frontend session type, and render as a muted "why" caption + live countdown under the Inspector activity pill. Reason/countdown are pure derivations from columns that already exist — no persistence, no migration.

**Tech Stack:** Go (backend daemon, Cobra CLI), swaggest/openapi-typescript codegen, React + TypeScript + Vitest (Electron renderer).

## Global Constraints

- **Behavior-preserving.** No status value or transition-timing change. The existing `status_test.go` cases (`TestServiceDerivesStatusFromSessionFactsAndPR`, `TestAggregateStackedChildSignals`) MUST pass unchanged.
- **No new persistence / migration.** Reason and countdown derive from `activity_state`, `activity_last_at`, `first_signal_at`, and the existing grace constants only.
- **Code-first API.** After editing `domain/session.go` DTOs, regenerate with `npm run api`; never hand-edit `frontend/src/api/schema.ts` or `backend/internal/httpd/apispec/openapi.yaml`.
- **Do not store derived/display status** (AGENTS.md hard rule) — this feature only *derives and surfaces*, never stores.
- **Surgical changes only.** No drive-by refactors. Conventional commits (`feat:`/`test:`/`docs:`). Branch: `worktree-needs-input-detection` (already checked out, based on `main-fluke`).
- **Reason code value set (load-bearing, keep Go ↔ TS in sync):** `working`, `waiting_input`, `active_stale`, `idle_aged`, `idle`, `no_signal`, `pr_pipeline`, `terminated`, `merged` (TS adds `unknown` as the safe fallback).

---

### Task 1: Backend — reason + countdown in the derivation

**Files:**
- Modify: `backend/internal/domain/status.go` (add `StatusReason` type + constants)
- Modify: `backend/internal/domain/session.go:68-76` (three new optional fields on `Session`)
- Modify: `backend/internal/service/session/status.go` (add `deriveStatusDetail`, `statusResult`, `idleCountdown`; make `deriveStatus` a wrapper)
- Modify: `backend/internal/service/session/service.go:583-593` (`toSession` uses the detail)
- Test: `backend/internal/service/session/status_test.go` (new reason + countdown tests)

**Interfaces:**
- Produces: `domain.StatusReason` (string) with constants `domain.ReasonWorking`, `domain.ReasonWaitingInput`, `domain.ReasonActiveStale`, `domain.ReasonIdleAged`, `domain.ReasonIdle`, `domain.ReasonNoSignal`, `domain.ReasonPRPipeline`, `domain.ReasonTerminated`, `domain.ReasonMerged`.
- Produces: `domain.Session.StatusReason StatusReason`, `domain.Session.NextTransitionAt *time.Time`, `domain.Session.NextTransitionTo SessionStatus`.
- Produces: `deriveStatusDetail(rec domain.SessionRecord, prs []domain.PRFacts, now time.Time, signalCapable bool, minApprovals int) statusResult` where `statusResult{Status domain.SessionStatus; Reason domain.StatusReason; NextTransitionAt *time.Time; NextTransitionTo domain.SessionStatus}`.
- Consumes: existing `deriveStatus` signature stays identical (now delegating), so `status_test.go` and `toSession` keep compiling.

- [ ] **Step 1: Write the failing reason test**

Append to `backend/internal/service/session/status_test.go`:

```go
func TestDeriveStatusDetailReason(t *testing.T) {
	tests := []struct {
		name       string
		rec        domain.SessionRecord
		pr         []domain.PRFacts
		hookless   bool
		wantStatus domain.SessionStatus
		wantReason domain.StatusReason
		// wantNextTo is "" when no timed transition is pending.
		wantNextTo domain.SessionStatus
	}{
		{"working", statusRec(domain.ActivityActive, false), nil, false, domain.StatusWorking, domain.ReasonWorking, domain.StatusNeedsInput},
		{"active-stale", activeAgedRec(2 * activeStaleGrace), nil, false, domain.StatusNeedsInput, domain.ReasonActiveStale, ""},
		{"waiting-input", statusRec(domain.ActivityWaitingInput, false), nil, false, domain.StatusNeedsInput, domain.ReasonWaitingInput, ""},
		{"idle-aged", idleAgedRec(2 * waitingInputGrace), nil, false, domain.StatusNeedsInput, domain.ReasonIdleAged, ""},
		{"idle-fresh-signalled", idleAgedRec(waitingInputGrace / 2), nil, false, domain.StatusIdle, domain.ReasonIdle, domain.StatusNeedsInput},
		{"idle-fresh-never-signalled", silentRec(10 * time.Second), nil, false, domain.StatusIdle, domain.ReasonIdle, domain.StatusNoSignal},
		{"no-signal", silentRec(2 * noSignalGrace), nil, false, domain.StatusNoSignal, domain.ReasonNoSignal, ""},
		{"hookless-idle", silentRec(2 * noSignalGrace), nil, true, domain.StatusIdle, domain.ReasonIdle, ""},
		{"pr-open", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{}), false, domain.StatusPROpen, domain.ReasonPRPipeline, ""},
		{"terminated", statusRec(domain.ActivityExited, true), nil, false, domain.StatusTerminated, domain.ReasonTerminated, ""},
		{"merged", statusRec(domain.ActivityIdle, true), statusPR(domain.PRFacts{Merged: true}), false, domain.StatusMerged, domain.ReasonMerged, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveStatusDetail(tt.rec, tt.pr, statusNow, !tt.hookless, domain.DefaultMinApprovals)
			if got.Status != tt.wantStatus {
				t.Fatalf("status: got %q want %q", got.Status, tt.wantStatus)
			}
			if got.Reason != tt.wantReason {
				t.Fatalf("reason: got %q want %q", got.Reason, tt.wantReason)
			}
			if tt.wantNextTo == "" {
				if got.NextTransitionAt != nil {
					t.Fatalf("nextTransitionAt: got %v want nil", got.NextTransitionAt)
				}
				return
			}
			if got.NextTransitionAt == nil {
				t.Fatalf("nextTransitionAt: got nil want non-nil")
			}
			if got.NextTransitionTo != tt.wantNextTo {
				t.Fatalf("nextTransitionTo: got %q want %q", got.NextTransitionTo, tt.wantNextTo)
			}
		})
	}
}

func TestDeriveStatusDetailCountdownTimestamps(t *testing.T) {
	// active within grace flips to needs_input at last + activeStaleGrace.
	active := activeAgedRec(activeStaleGrace / 2)
	got := deriveStatusDetail(active, nil, statusNow, true, domain.DefaultMinApprovals)
	wantAt := active.Activity.LastActivityAt.Add(activeStaleGrace)
	if got.NextTransitionAt == nil || !got.NextTransitionAt.Equal(wantAt) {
		t.Fatalf("active nextTransitionAt: got %v want %v", got.NextTransitionAt, wantAt)
	}
	// idle-fresh (signalled) flips to needs_input at last + waitingInputGrace.
	idle := idleAgedRec(waitingInputGrace / 2)
	got = deriveStatusDetail(idle, nil, statusNow, true, domain.DefaultMinApprovals)
	wantAt = idle.Activity.LastActivityAt.Add(waitingInputGrace)
	if got.NextTransitionAt == nil || !got.NextTransitionAt.Equal(wantAt) {
		t.Fatalf("idle nextTransitionAt: got %v want %v", got.NextTransitionAt, wantAt)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails to compile**

Run: `cd backend && go test ./internal/service/session/ -run TestDeriveStatusDetail`
Expected: FAIL — build error `undefined: deriveStatusDetail` / `undefined: domain.ReasonWorking`.

- [ ] **Step 3: Add the `StatusReason` type and constants**

Append to `backend/internal/domain/status.go`:

```go
// StatusReason names which rule in the status derivation produced the display
// Status, so the UI can explain WHY a session reads working/needs_input/etc.
// It is derived on read alongside Status and never stored. A needs_input from a
// timeout guess (ReasonActiveStale/ReasonIdleAged) is thereby distinguishable
// from one the agent actually asked for (ReasonWaitingInput).
type StatusReason string

const (
	ReasonWorking      StatusReason = "working"       // active, heartbeat fresh
	ReasonWaitingInput StatusReason = "waiting_input" // agent reported a prompt (Notification hook)
	ReasonActiveStale  StatusReason = "active_stale"  // active aged past grace -> needs_input (timeout guess)
	ReasonIdleAged     StatusReason = "idle_aged"     // idle aged past grace -> needs_input (timeout guess)
	ReasonIdle         StatusReason = "idle"          // fresh idle within grace, or hook-less quiet
	ReasonNoSignal     StatusReason = "no_signal"     // hook-capable but never signalled
	ReasonPRPipeline   StatusReason = "pr_pipeline"   // status came from the open-PR aggregate
	ReasonTerminated   StatusReason = "terminated"    // session terminated
	ReasonMerged       StatusReason = "merged"        // merged branch / terminated with a merged PR
)
```

- [ ] **Step 4: Add the three optional fields to `domain.Session`**

In `backend/internal/domain/session.go`, replace the `Session` struct (lines 66-76) with:

```go
// Session is the read-model returned across the API boundary: a SessionRecord
// plus the derived display Status.
type Session struct {
	SessionRecord
	Status SessionStatus `json:"status" enum:"working,pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged,needs_input,idle,terminated,no_signal"`
	// StatusReason names the derivation rule that produced Status, so the UI can
	// explain WHY (e.g. a needs_input from a lost-hook timeout vs a real agent
	// prompt). Derived on read, never stored.
	StatusReason StatusReason `json:"statusReason,omitempty" enum:"working,waiting_input,active_stale,idle_aged,idle,no_signal,pr_pipeline,terminated,merged"`
	// NextTransitionAt is when the current timeout-based reading will flip if no
	// new signal arrives; nil when the status is sticky/terminal. NextTransitionTo
	// is what it becomes. Both derived on read.
	NextTransitionAt *time.Time    `json:"nextTransitionAt,omitempty"`
	NextTransitionTo SessionStatus `json:"nextTransitionTo,omitempty" enum:"working,pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged,needs_input,idle,terminated,no_signal"`
	TerminalHandleID string        `json:"terminalHandleId,omitempty"`
	// PRs are the session's attributed pull requests (one session can own many).
	// They feed status derivation and are surfaced on the API read model. Not
	// serialized here: the HTTP boundary maps them to the curated wire shape.
	PRs []PRFacts `json:"-"`
}
```

- [ ] **Step 5: Add `statusResult`, `deriveStatusDetail`, and `idleCountdown`; make `deriveStatus` a wrapper**

In `backend/internal/service/session/status.go`, replace the whole `deriveStatus` function (lines 38-98) with:

```go
// statusResult is the full outcome of the status derivation: the display Status
// plus WHY it was chosen and, for timeout-based readings, when/what it will flip
// to next. All fields are derived on read from durable facts; none is stored.
type statusResult struct {
	Status           domain.SessionStatus
	Reason           domain.StatusReason
	NextTransitionAt *time.Time
	NextTransitionTo domain.SessionStatus
}

// deriveStatus computes the display status. It delegates to deriveStatusDetail
// and drops the reason/countdown, preserving the original signature for callers
// and tests that only need the status.
func deriveStatus(rec domain.SessionRecord, prs []domain.PRFacts, now time.Time, signalCapable bool, minApprovals int) domain.SessionStatus {
	return deriveStatusDetail(rec, prs, now, signalCapable, minApprovals).Status
}

// deriveStatusDetail computes the display status AND the reason that produced it,
// plus the pending timeout transition where one applies. The Status it returns
// is identical to the historical deriveStatus for every input — it only adds the
// explanatory metadata. signalCapable says whether this session's harness has an
// activity hook pipeline at all; only then can prolonged silence mean the
// pipeline is broken (no_signal) rather than a hook-less harness's normal quiet.
//
// A session may own several PRs at once (independent or stacked). The PR-derived
// status is the worst-wins aggregate across its open PRs; stacked children whose
// parent is still open are exempt from the aggregation since they cannot merge
// until the parent does. Merged/closed PRs only matter once no open PR remains.
func deriveStatusDetail(rec domain.SessionRecord, prs []domain.PRFacts, now time.Time, signalCapable bool, minApprovals int) statusResult {
	if rec.IsTerminated {
		if anyMerged(prs) {
			return statusResult{Status: domain.StatusMerged, Reason: domain.ReasonMerged}
		}
		return statusResult{Status: domain.StatusTerminated, Reason: domain.ReasonTerminated}
	}

	if rec.Activity.State == domain.ActivityWaitingInput {
		return statusResult{Status: domain.StatusNeedsInput, Reason: domain.ReasonWaitingInput}
	}

	open := openPRs(prs)
	if len(open) > 0 {
		return statusResult{Status: aggregatePRStatus(open, minApprovals), Reason: domain.ReasonPRPipeline}
	}
	if anyMerged(prs) {
		return statusResult{Status: domain.StatusMerged, Reason: domain.ReasonMerged}
	}

	if rec.Activity.State == domain.ActivityActive {
		if now.Sub(rec.Activity.LastActivityAt) <= activeStaleGrace {
			at := rec.Activity.LastActivityAt.Add(activeStaleGrace)
			return statusResult{
				Status:           domain.StatusWorking,
				Reason:           domain.ReasonWorking,
				NextTransitionAt: &at,
				NextTransitionTo: domain.StatusNeedsInput,
			}
		}
		// active but no signal refreshed it within the grace: the turn's closing
		// Stop was lost and nothing else demoted it, so surface it as
		// waiting-for-human rather than a permanent false "working".
		return statusResult{Status: domain.StatusNeedsInput, Reason: domain.ReasonActiveStale}
	}

	if rec.Activity.State == domain.ActivityIdle && !rec.FirstSignalAt.IsZero() &&
		now.Sub(rec.Activity.LastActivityAt) > waitingInputGrace {
		return statusResult{Status: domain.StatusNeedsInput, Reason: domain.ReasonIdleAged}
	}

	if signalCapable && rec.FirstSignalAt.IsZero() && now.Sub(rec.Activity.LastActivityAt) > noSignalGrace {
		return statusResult{Status: domain.StatusNoSignal, Reason: domain.ReasonNoSignal}
	}

	// Fresh idle: report idle now, and where a promotion is pending compute when
	// and to what it will flip so the UI can count down to it.
	at, to := idleCountdown(rec, signalCapable)
	return statusResult{Status: domain.StatusIdle, Reason: domain.ReasonIdle, NextTransitionAt: at, NextTransitionTo: to}
}

// idleCountdown returns the pending transition for a fresh idle session (one the
// branches above did not already promote): a signalled idle will promote to
// needs_input at last+waitingInputGrace; an unsignalled but hook-capable idle
// will degrade to no_signal at last+noSignalGrace; a hook-less idle never flips.
func idleCountdown(rec domain.SessionRecord, signalCapable bool) (*time.Time, domain.SessionStatus) {
	if rec.Activity.State != domain.ActivityIdle {
		return nil, ""
	}
	if !rec.FirstSignalAt.IsZero() {
		at := rec.Activity.LastActivityAt.Add(waitingInputGrace)
		return &at, domain.StatusNeedsInput
	}
	if signalCapable {
		at := rec.Activity.LastActivityAt.Add(noSignalGrace)
		return &at, domain.StatusNoSignal
	}
	return nil, ""
}
```

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `cd backend && go test ./internal/service/session/ -run TestDeriveStatusDetail -v`
Expected: PASS (`TestDeriveStatusDetailReason`, `TestDeriveStatusDetailCountdownTimestamps`).

- [ ] **Step 7: Run the full session-service suite to confirm behavior is preserved**

Run: `cd backend && go test ./internal/service/session/`
Expected: PASS — the untouched `TestServiceDerivesStatusFromSessionFactsAndPR` and `TestAggregateStackedChildSignals` still pass, proving no status changed.

- [ ] **Step 8: Wire the detail into `toSession`**

In `backend/internal/service/session/service.go`, replace the return statement at line 592 with:

```go
	detail := deriveStatusDetail(rec, prs, s.now(), s.harnessSignals(rec.Harness), minApprovals)
	return domain.Session{
		SessionRecord:    rec,
		Status:           detail.Status,
		StatusReason:     detail.Reason,
		NextTransitionAt: detail.NextTransitionAt,
		NextTransitionTo: detail.NextTransitionTo,
		TerminalHandleID: rec.Metadata.RuntimeHandleID,
		PRs:              prs,
	}, nil
```

- [ ] **Step 9: Build and vet the backend**

Run: `cd backend && go build ./... && go vet ./internal/service/... ./internal/domain/...`
Expected: no output (success).

- [ ] **Step 10: Commit**

```bash
git add backend/internal/domain/status.go backend/internal/domain/session.go backend/internal/service/session/status.go backend/internal/service/session/service.go backend/internal/service/session/status_test.go
git commit -m "feat(session): derive a status reason + next-transition countdown

Add deriveStatusDetail returning the display status plus a StatusReason
for the deciding branch and, for timeout-based readings, when/what it
flips to next. deriveStatus delegates to it, so behavior is unchanged.
Surfaced on domain.Session for the API read model.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Regenerate the API contract + verify no drift

**Files:**
- Regenerate: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts` (via `npm run api` — do NOT hand-edit)
- Possibly modify: `backend/internal/httpd/apispec/specgen/build.go:125` (`schemaNames`) only if the drift test demands it

**Interfaces:**
- Consumes: the new `domain.Session` fields from Task 1.
- Produces: `components["schemas"]["Session"]` / `["SessionView"]` in `schema.ts` gain optional `statusReason?`, `nextTransitionAt?`, `nextTransitionTo?` — consumed by Task 4.

- [ ] **Step 1: Regenerate the spec and TS types**

Run: `npm run api`
Expected: `backend/internal/httpd/apispec/openapi.yaml` and `frontend/src/api/schema.ts` are rewritten; `git status` shows both modified.

- [ ] **Step 2: Confirm the new fields landed in the spec**

Run: `grep -n "statusReason\|nextTransitionAt\|nextTransitionTo" backend/internal/httpd/apispec/openapi.yaml`
Expected: three property definitions appear under the `Session` schema.

- [ ] **Step 3: Run the httpd drift + parity tests**

Run: `cd backend && go test ./internal/httpd/...`
Expected: PASS.

If it FAILS reporting an unmapped default component name `DomainStatusReason` (swaggest reflected the named type instead of inlining the enum), add this exact line to the `schemaNames` map in `backend/internal/httpd/apispec/specgen/build.go` (in the `// domain` block near line 132), then re-run `npm run api` and this test:

```go
	"DomainStatusReason": "StatusReason",
```

If it FAILS on a controller test that asserts an exact session JSON body, update that fixture to include the now-always-present `"statusReason"` field (its value is the reason for that fixture's activity/PR state). Re-run until green.

- [ ] **Step 4: Typecheck the frontend against the new schema**

Run: `cd frontend && npm run typecheck`
Expected: PASS (schema.ts is valid; no consumer breaks yet).

- [ ] **Step 5: Commit the generated artifacts**

```bash
git add backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts backend/internal/httpd/apispec/specgen/build.go
git commit -m "chore(api): regenerate spec + TS types for status reason fields

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: CLI — surface the reason in `ao session get`

**Files:**
- Modify: `backend/internal/cli/session.go:42-54` (`sessionDTO`), `:709-741` (`writeSessionDetails`)
- Test: `backend/internal/cli/session_test.go` (new focused test)

**Interfaces:**
- Consumes: the `statusReason` JSON field from the daemon (Task 2).
- Produces: `ao session get <id>` prints a `reason: <code>` line after `status:`.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/cli/session_test.go` (add `"strings"` is already imported; add `"github.com/spf13/cobra"` to the import block):

```go
func TestWriteSessionDetailsIncludesReason(t *testing.T) {
	var buf strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	sess := sessionDTO{
		ID:           "demo-1",
		ProjectID:    "demo",
		Kind:         "worker",
		Status:       "needs_input",
		StatusReason: "active_stale",
		Activity:     sessionActivity{State: "active"},
	}
	if err := writeSessionDetails(cmd, sess); err != nil {
		t.Fatalf("writeSessionDetails: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "reason: active_stale") {
		t.Fatalf("output missing reason line:\n%s", out)
	}
}

func TestWriteSessionDetailsOmitsEmptyReason(t *testing.T) {
	var buf strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	sess := sessionDTO{ID: "demo-1", ProjectID: "demo", Kind: "worker", Status: "working"}
	if err := writeSessionDetails(cmd, sess); err != nil {
		t.Fatalf("writeSessionDetails: %v", err)
	}
	if strings.Contains(buf.String(), "reason:") {
		t.Fatalf("empty reason should be omitted:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./internal/cli/ -run TestWriteSessionDetails`
Expected: FAIL — `sess.StatusReason undefined` (field not yet on `sessionDTO`).

- [ ] **Step 3: Add the field to `sessionDTO`**

In `backend/internal/cli/session.go`, add to the `sessionDTO` struct (after the `Status` field at line 53):

```go
	Status       string          `json:"status"`
	StatusReason string          `json:"statusReason,omitempty"`
```

- [ ] **Step 4: Print the reason in `writeSessionDetails`**

In `backend/internal/cli/session.go`, add a `reason` row to the `fields` slice in `writeSessionDetails` (immediately after the `{"status", sess.Status}` entry, line 716):

```go
		{"status", sess.Status},
		{"reason", sess.StatusReason},
```

(The existing loop already skips empty values, so an absent reason prints nothing.)

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/cli/ -run TestWriteSessionDetails -v`
Expected: PASS (both new tests).

- [ ] **Step 6: Run the full CLI suite (no regressions in existing session tests)**

Run: `cd backend && go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/cli/session.go backend/internal/cli/session_test.go
git commit -m "feat(cli): show status reason in 'ao session get'

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Frontend types — reason mapping, labels, countdown helper

**Files:**
- Modify: `frontend/src/renderer/types/workspace.ts` (new `StatusReason` type + helpers + `WorkspaceSession` fields)
- Modify: `frontend/src/renderer/hooks/useWorkspaceQuery.ts:5-12` (import), `:60-66` (map new fields)
- Test: `frontend/src/renderer/types/workspace.test.ts` (new describes)

**Interfaces:**
- Consumes: `session.statusReason`, `session.nextTransitionAt`, `session.nextTransitionTo` from the generated schema (Task 2).
- Produces: `StatusReason` type; `toStatusReason(reason?: string): StatusReason | undefined`; `statusReasonLabel: Record<StatusReason, string>`; `formatCountdown(ms: number): string`; `formatNextTransition(session, now: number): string`; `WorkspaceSession.statusReason?`, `.nextTransitionAt?`, `.nextTransitionTo?` — consumed by Task 5.

- [ ] **Step 1: Write the failing tests**

In `frontend/src/renderer/types/workspace.test.ts`, add `formatNextTransition`, `statusReasonLabel`, and `toStatusReason` to the import block (lines 2-24), then append:

```ts
describe("toStatusReason", () => {
	it("passes through known reasons, maps unknown to 'unknown', and undefined to undefined", () => {
		expect(toStatusReason("active_stale")).toBe("active_stale");
		expect(toStatusReason("waiting_input")).toBe("waiting_input");
		expect(toStatusReason("bogus")).toBe("unknown");
		expect(toStatusReason(undefined)).toBeUndefined();
	});
});

describe("statusReasonLabel", () => {
	it("has a non-empty label for every real reason code", () => {
		for (const r of [
			"working", "waiting_input", "active_stale", "idle_aged",
			"idle", "no_signal", "pr_pipeline", "terminated", "merged",
		] as const) {
			expect(statusReasonLabel[r].length).toBeGreaterThan(0);
		}
	});
});

describe("formatNextTransition", () => {
	const now = Date.parse("2026-01-01T00:00:00Z");

	it("formats a pending flip with target and duration", () => {
		expect(
			formatNextTransition({ nextTransitionAt: "2026-01-01T00:04:00Z", nextTransitionTo: "needs_input" }, now),
		).toBe("→ Needs input in 4m");
		expect(
			formatNextTransition({ nextTransitionAt: "2026-01-01T00:00:30Z", nextTransitionTo: "no_signal" }, now),
		).toBe("→ No signal in 30s");
	});

	it("is empty when already due, missing, or targeting a non-countdown status", () => {
		expect(formatNextTransition({ nextTransitionAt: "2025-12-31T23:59:00Z", nextTransitionTo: "needs_input" }, now)).toBe("");
		expect(formatNextTransition({}, now)).toBe("");
		expect(formatNextTransition({ nextTransitionAt: "2026-01-01T00:04:00Z", nextTransitionTo: "working" }, now)).toBe("");
	});
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd frontend && npx vitest run src/renderer/types/workspace.test.ts --config vite.renderer.config.ts`
Expected: FAIL — `toStatusReason`/`statusReasonLabel`/`formatNextTransition` are not exported.

- [ ] **Step 3: Add the `StatusReason` type, validator, labels, and countdown helpers**

In `frontend/src/renderer/types/workspace.ts`, add after the `toSessionActivity` block (after line 58):

```ts
export type StatusReason =
	| "working"
	| "waiting_input"
	| "active_stale"
	| "idle_aged"
	| "idle"
	| "no_signal"
	| "pr_pipeline"
	| "terminated"
	| "merged"
	| "unknown";

const statusReasons = new Set<StatusReason>([
	"working",
	"waiting_input",
	"active_stale",
	"idle_aged",
	"idle",
	"no_signal",
	"pr_pipeline",
	"terminated",
	"merged",
]);

/** Normalizes the daemon's reason code; undefined when absent (e.g. mock data). */
export function toStatusReason(reason?: string): StatusReason | undefined {
	if (!reason) return undefined;
	return statusReasons.has(reason as StatusReason) ? (reason as StatusReason) : "unknown";
}

/** Plain-language explanation of WHY a session shows its current status. */
export const statusReasonLabel: Record<StatusReason, string> = {
	working: "Agent active",
	waiting_input: "Agent requested input",
	active_stale: "No activity for a while — assumed waiting (a turn's Stop hook may have been lost)",
	idle_aged: "Turn ended and went quiet — assumed waiting",
	idle: "Recently active",
	no_signal: "No hook has reported since launch",
	pr_pipeline: "Status from the pull request pipeline",
	terminated: "Session ended",
	merged: "Work merged",
	unknown: "",
};

// Only timeout-based readings count down, and only ever to these targets.
const transitionTargetLabel: Partial<Record<SessionStatus, string>> = {
	needs_input: "Needs input",
	no_signal: "No signal",
};

/** Compact human duration for a countdown, e.g. "45s", "4m", "2h". */
export function formatCountdown(ms: number): string {
	const s = Math.round(ms / 1000);
	if (s < 60) return `${s}s`;
	const m = Math.round(s / 60);
	if (m < 60) return `${m}m`;
	return `${Math.round(m / 60)}h`;
}

/**
 * Countdown caption to the next status flip (e.g. "→ Needs input in 4m"), or ""
 * when there is no pending timed transition or it is already due. `now` (ms since
 * epoch) is passed in so the function stays pure and testable.
 */
export function formatNextTransition(
	session: Pick<WorkspaceSession, "nextTransitionAt" | "nextTransitionTo">,
	now: number,
): string {
	if (!session.nextTransitionAt || !session.nextTransitionTo) return "";
	const target = transitionTargetLabel[session.nextTransitionTo];
	if (!target) return "";
	const ms = Date.parse(session.nextTransitionAt) - now;
	if (Number.isNaN(ms) || ms <= 0) return "";
	return `→ ${target} in ${formatCountdown(ms)}`;
}
```

- [ ] **Step 4: Add the fields to `WorkspaceSession`**

In `frontend/src/renderer/types/workspace.ts`, add inside the `WorkspaceSession` type after `status: SessionStatus;` (line 126):

```ts
	status: SessionStatus;
	/** Machine reason for the current {@link status}, derived by the daemon. */
	statusReason?: StatusReason;
	/** ISO timestamp when the current timeout-based status will flip, if pending. */
	nextTransitionAt?: string;
	/** What {@link status} becomes at {@link nextTransitionAt} (needs_input / no_signal). */
	nextTransitionTo?: SessionStatus;
```

- [ ] **Step 5: Run the type tests to verify they pass**

Run: `cd frontend && npx vitest run src/renderer/types/workspace.test.ts --config vite.renderer.config.ts`
Expected: PASS.

- [ ] **Step 6: Map the new fields in `useWorkspaceQuery`**

In `frontend/src/renderer/hooks/useWorkspaceQuery.ts`, add `toStatusReason` to the import from `../types/workspace` (line 5-12 block):

```ts
	toAgentProvider,
	toSessionActivity,
	toSessionStatus,
	toStatusReason,
```

Then in the session `.map(...)` object, add after `status: toSessionStatus(session.status, session.isTerminated),` (line 60):

```ts
			status: toSessionStatus(session.status, session.isTerminated),
			statusReason: toStatusReason(session.statusReason),
			nextTransitionAt: session.nextTransitionAt ?? undefined,
			nextTransitionTo: session.nextTransitionTo ? toSessionStatus(session.nextTransitionTo) : undefined,
```

- [ ] **Step 7: Typecheck the frontend**

Run: `cd frontend && npm run typecheck`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/renderer/types/workspace.ts frontend/src/renderer/types/workspace.test.ts frontend/src/renderer/hooks/useWorkspaceQuery.ts
git commit -m "feat(ui): map status reason + next-transition into the session model

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Frontend UI — "why" caption + live countdown in the Inspector

**Files:**
- Modify: `frontend/src/renderer/components/SessionInspector.tsx:2` (import `useEffect`), `:11` (import helpers), `:245-299` (`ActivityTimeline`)

**Interfaces:**
- Consumes: `session.statusReason`, `session.nextTransitionAt`, `session.nextTransitionTo` (Task 4) and `statusReasonLabel`, `formatNextTransition` (Task 4).
- Produces: the Inspector activity "now" row renders a muted caption `"<why> · <countdown>"`, the countdown ticking every second while a transition is pending.

- [ ] **Step 1: Import `useEffect` and the workspace helpers**

In `frontend/src/renderer/components/SessionInspector.tsx`, change line 2 to:

```ts
import { useEffect, useState, type ReactNode } from "react";
```

and change line 11 to:

```ts
import { canonicalTrackerIssueId, formatNextTransition, sortedPRs, statusReasonLabel } from "../types/workspace";
```

- [ ] **Step 2: Add the ticking clock and captions in `ActivityTimeline`**

In `frontend/src/renderer/components/SessionInspector.tsx`, at the top of `ActivityTimeline` (immediately after `function ActivityTimeline({ session }: { session: WorkspaceSession }) {`, line 245), insert:

```tsx
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!session.nextTransitionAt) return;
		const id = setInterval(() => setNow(Date.now()), 1000);
		return () => clearInterval(id);
	}, [session.nextTransitionAt]);
	const why = session.statusReason ? statusReasonLabel[session.statusReason] : "";
	const countdown = formatNextTransition(session, now);
	const activityCaption = [why, countdown].filter(Boolean).join(" · ");
```

- [ ] **Step 3: Render the caption under the activity pill**

In the same file, replace the `node:` value of the `tone: "now"` event (lines 281-296) with a column layout that appends the caption:

```tsx
		node: (
			<span className="inline-flex flex-col gap-1">
				<span className="inline-flex flex-wrap items-center gap-1.5">
					<span className="inspector-timeline__badge">
						<InspectorActivityPill state={session.activity?.state ?? "unknown"} />
					</span>
					{session.status === "no_signal" ? (
						<span className="inspector-timeline__badge">
							<TimelinePill {...ACTIVITY_WARNING_PILL.no_signal} />
						</span>
					) : null}
					{scmTimelineStates(session).map((state) => (
						<span key={state} className="inspector-timeline__badge">
							<InspectorScmPill state={state} />
						</span>
					))}
				</span>
				{activityCaption ? (
					<span className="text-[11px] leading-snug text-[var(--fg-muted)]">{activityCaption}</span>
				) : null}
			</span>
		),
```

- [ ] **Step 4: Typecheck**

Run: `cd frontend && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Run the renderer test suite (no regressions in inspector/board tests)**

Run: `cd frontend && npm run test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/renderer/components/SessionInspector.tsx
git commit -m "feat(ui): explain session status with a why caption + live countdown

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Full verification + manual check

**Files:** none (verification only)

- [ ] **Step 1: Backend lint + full test**

Run: `npm run lint`
Expected: PASS (go test ./... + golangci-lint).

- [ ] **Step 2: Frontend typecheck + test + build**

Run: `cd frontend && npm run typecheck && npm run test && npm run build`
Expected: all PASS.

- [ ] **Step 3: Confirm no API drift remains**

Run: `git status --porcelain backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts`
Expected: empty (both already committed in Task 2; no stray regen diff).

- [ ] **Step 4: Manual check in the running app**

Launch the app (per the local build/run runbook), open the Session Inspector on a live session, and confirm:
- A `working` session shows *"Agent active · → Needs input in ~15m"* under the activity pill.
- A session that has sat idle shows *"Turn ended and went quiet — assumed waiting"* (reason `idle_aged`) — visibly different from a real prompt.
- `ao session get <id>` prints a `reason:` line matching the pill.

Confirm the countdown ticks down each second while the session is `working`/fresh-`idle`.

- [ ] **Step 5: Final commit (if the manual check required any tweak)**

```bash
git add -A
git commit -m "chore(session): finalize self-explaining status after manual verification

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Reason taxonomy (9 codes) → Task 1 (Go constants) + Task 4 (TS). ✓
- Countdown (`nextTransitionAt` + `nextTransitionTo`) → Task 1 (derive) + Task 4 (`formatNextTransition`) + Task 5 (render). ✓
- Backend no-migration derivation → Task 1. ✓
- API regen → Task 2. ✓
- CLI `ao session get` → Task 3. ✓
- Frontend Inspector "why" + countdown → Task 4/5. ✓
- Behavior-preserving (existing status_test.go unchanged) → Task 1 Step 7. ✓
- Testing (TDD, boundary cases, frontend unit) → Tasks 1/3/4 write tests first. ✓
- Deferred items (permission-vs-idle sub-split, history table, behavior changes) → not implemented, as specified. ✓

**Placeholder scan:** No TBD/TODO/"handle errors"/"similar to". Every code step shows full code. The two conditional remediations in Task 2 Step 3 give exact content and a concrete trigger (drift-test failure), not vague guidance. ✓

**Type consistency:** `deriveStatusDetail`/`statusResult`/`idleCountdown` names and the `statusResult` field names (`Status`/`Reason`/`NextTransitionAt`/`NextTransitionTo`) match across Tasks 1 and 8-step usage. `domain.Reason*` constants match between status.go (Task 1 Step 3) and the tests (Step 1). TS `StatusReason` union, `statusReasonLabel` keys, and `statusReasons` set list the same 9 codes; `toStatusReason` returns `"unknown"` fallback consistent with `toSessionStatus`. `WorkspaceSession` field names (`statusReason`/`nextTransitionAt`/`nextTransitionTo`) match the useWorkspaceQuery mapping and the SessionInspector usage. ✓
