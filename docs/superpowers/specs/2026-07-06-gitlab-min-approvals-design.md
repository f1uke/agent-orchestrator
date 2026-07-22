# GitLab minimum-approvals threshold — design

**Date:** 2026-07-06
**Branch:** `feat/gitlab-min-approvals` (from `main-fluke`)
**Status:** approved design, pending spec review

## Problem

AO's board moves a session to **"Ready to merge"** when its PR/MR review decision
is `approved`. For GitLab, `approvalDecision()`
(`backend/internal/adapters/scm/gitlab/observer_provider.go`) returns `"approved"`
whenever `approvals_left == 0 && len(approved_by) > 0`. That value is derived from
GitLab's own configured **approval rules**.

When a GitLab project has **no approval rule** (`approvals_required: 0`,
`has_approval_rules: false` — e.g. `example-org/apps/demo-ios-app`), `approvals_left`
is always `0`, so a **single** approval already reads as `approved` in AO. The team's
real convention ("needs N human approvals") is invisible to AO.

We want AO to enforce its own **minimum-approvals floor** when — and only when — the
SCM has no rule of its own, configurable **per project** in the UI.

## Scope

- **GitLab only** in this version. The data model and config are built
  provider-aware so GitHub can be added later, but no GitHub behaviour changes now.
- Threshold applies **only as a fallback** when GitLab has no approval rule. If the
  project has an approval rule (`approvals_required > 0` / `has_approval_rules`), AO
  trusts GitLab's decision unchanged.
- Semantics: **`approvals_count >= minApprovals`** (at least N distinct approvers).
  Default **3**.

## Behaviour

For a GitLab MR with **no approval rule**:

| approvals_count   | minApprovals | AO review decision | board zone     |
| ----------------- | ------------ | ------------------ | -------------- |
| `>= minApprovals` | 3            | `approved`         | Ready to merge |
| `< minApprovals`  | 3            | `""` → `pr_open`   | In review      |

An MR that has an approval rule is unaffected — GitLab's `approved`/not decides, and
the per-project `minApprovals` is ignored for it.

The rest of the pipeline is unchanged: failing CI, draft, or **unresolved discussion
threads** still win over `approved` (worst-wins in `aggregatePRStatus`), so a
threshold-approved MR with an unresolved thread still surfaces as
`changes_requested` → "Needs you".

## Approach (chosen: B — policy in domain, adapter reports facts)

Adapters stay dumb fact-reporters; the min-approvals **policy** lives in the domain /
session-service layer, where per-project config is already available at
`deriveStatus` (`backend/internal/service/session/service.go:588`). Approach A
(plumbing per-project config down into the stateless GitLab provider) was rejected —
more plumbing and it puts policy in the adapter.

### 1. Domain facts — `backend/internal/domain/pr.go`

Add to `PullRequest` and `PRFacts`:

```go
ApprovalsCount         int  // number of distinct approvers reported by the SCM
ApprovalRuleConfigured bool // SCM enforces an approval rule of its own
```

Only the GitLab adapter populates these for now; GitHub leaves them zero-valued.

### 2. Per-project config — `backend/internal/domain/projectconfig.go`

```go
// MinApprovals is the minimum number of approvals AO treats as "ready" when the
// SCM has no approval rule of its own (ApprovalRuleConfigured == false). 0 = unset
// → resolves to the default (3). GitLab only in this version.
MinApprovals int `json:"minApprovals,omitempty"`
```

Resolution helper (default 3):

```go
const DefaultMinApprovals = 3

func (c ProjectConfig) ResolveMinApprovals() int {
    if c.MinApprovals <= 0 { return DefaultMinApprovals }
    return c.MinApprovals
}
```

A team that wants the old "1 approval is enough" behaviour sets `minApprovals: 1`.

### 3. GitLab adapter — `backend/internal/adapters/scm/gitlab/observer_provider.go`

- `restApprovals` gains `ApprovalsRequired int` (`approvals_required`) and
  `HasApprovalRules bool` (`has_approval_rules`).
- `approvalDecision`:
  - **rule present** (`ApprovalsRequired > 0 || HasApprovalRules`): unchanged —
    `"approved"` iff `ApprovalsLeft == 0 && len(ApprovedBy) > 0`, else `""`.
  - **no rule**: return `""` (defer to the domain threshold; no longer auto-approves
    on one approval).
- Emit `ApprovalsCount = len(ApprovedBy)` and `ApprovalRuleConfigured` on the
  observation so they persist onto the PR row.
- **Change detection:** include `ApprovalsCount` (and `ApprovalRuleConfigured`) in
  `reviewSemanticHash` so a poll that sees the count change (e.g. 2 → 3) marks the
  review changed and re-derives status. Without this the row would not update when
  only the approval count moves.

### 4. Status derivation — `backend/internal/service/session/status.go`

Plumb the resolved threshold through:

```go
deriveStatus(rec, prs, now, signalCapable, minApprovals)
  → aggregatePRStatus(open, minApprovals)
    → prPipelineStatus(pr, minApprovals)
```

In `prPipelineStatus`, add before the `default` arm:

```go
case !pr.ApprovalRuleConfigured && pr.ApprovalsCount >= minApprovals:
    return domain.StatusApproved
```

The caller at `service.go:588` resolves `minApprovals` from the session's project
config (`ProjectConfig.ResolveMinApprovals()`).

### 5. Frontend — `frontend/src/renderer/components/ProjectSettingsForm.tsx`

- New **"Minimum approvals"** number field (default 3), bound to
  `ProjectConfig.minApprovals` via the generated API schema.
- **Provider-aware:** the field is shown **only for GitLab projects**. Provider is
  detected from the repo origin host (contains `gitlab`). GitHub projects hide it in
  this version. (Planning task: confirm whether the backend already exposes a typed
  provider/host on the project record to use instead of host-substring detection.)
- Helper note under the field: _"Applies only when the GitLab repo has no approval
  rule of its own."_

## Open questions resolved

- **Interaction with GitLab rules:** fallback only when no rule.
- **Config location:** per-project, in the settings UI (not global).
- **Provider scope:** GitLab now; provider-aware groundwork for GitHub later.
- **Semantics/default:** `>= N`, default `3`.

## Testing

- `backend/internal/service/session/status_test.go`
  - no rule + `count >= 3` → `approved`
  - no rule + `count < 3` → `pr_open`
  - rule present → threshold ignored, GitLab decision wins
  - threshold-approved but unresolved thread → still `changes_requested`
- `backend/internal/adapters/scm/gitlab/observer_provider_test.go`
  - `approvalDecision`: no-rule → `""`; rule satisfied → `"approved"`
  - `ApprovalsCount` / `ApprovalRuleConfigured` emitted from the approvals payload
  - `reviewSemanticHash` changes when `ApprovalsCount` changes
- Frontend `ProjectSettingsForm.test.tsx`
  - field renders for a GitLab project, hidden for GitHub
  - value round-trips into `ProjectConfig.minApprovals`

## Out of scope

- GitHub min-approvals behaviour (data model is ready; logic + UI deferred).
- Changing how unresolved threads / CI / mergeability are derived.
- Any write-back to GitLab (AO stays read-only toward the SCM).
