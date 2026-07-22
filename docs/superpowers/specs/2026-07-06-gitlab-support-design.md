# GitLab Support — Design Spec

Date: 2026-07-06
Status: Approved for planning

## Goal

Add GitLab as a first-class SCM/tracker provider **inside the existing
codebase**, running alongside the current GitHub provider. No hard fork: GitHub
and GitLab repositories are both observed by one daemon, selected per-repo.

Primary target is **self-hosted GitLab** (e.g. `gitlab.example.com`). The
gitlab.com SaaS path is designed for but not the initial validation target.

## Non-goals (deferred to a later round — YAGNI)

- Posting reviews/comments **back** to GitLab through the reviewer adapters
  (the `gh`-based code-review posting flow). Round 1 is read/observe only.
- Multi-host GitLab (several GitLab instances at once). Host config is modeled
  as a list so this is possible later, but only single self-hosted is validated.
- OAuth device flow. Round 1 uses a Personal Access Token (env) or the `glab`
  CLI login.
- SQLite migrations. None are required (see Data model).

## Why this fits the current architecture

The backend is already provider-neutral at the seams:

- `internal/observe/scm.Provider` — a 7-method interface consumed by the SCM
  observer. GitHub is one implementation under `internal/adapters/scm/github/`.
- `internal/ports/scm_observations.go` — provider-neutral DTOs whose doc
  comments explicitly say adapters "normalize their SCM-specific payloads ...
  so downstream code does not depend on raw GitHub payloads".
- `internal/ports.Tracker` — a 3-method read-only issue-tracker port. GitHub is
  `internal/adapters/tracker/github/`.
- `domain.TrackerProvider` enum + per-project `TrackerIntakeConfig.Provider`
  already exist; issue IDs are namespaced `provider:native`.

Only the **wiring** hardcodes GitHub as "v1" (`internal/daemon/scm_wiring.go`,
`internal/daemon/tracker_intake_wiring.go`). GitLab is therefore a new adapter
plus a small change to selection/wiring — the central interfaces do not change.

## Approach: composite dispatcher, deterministic selection (Approach A)

AO decides GitHub vs GitLab per-repo **without probing the network**:

- **SCM:** a new composite `Provider` wraps an ordered list of providers. A
  repo's host determines the owner. GitLab claims a repo only when its host is
  in the configured GitLab host set (`AO_GITLAB_HOST`, comma-separated allowed);
  GitHub is the default/fallback. `ParseRepository` returns a `ports.SCMRepo`
  with `Provider` set, and every later call routes on `repo.Provider` /
  `ref.Repo.Provider`. Deterministic, honors the daemon's lazy-credential
  startup rule.
- **Tracker:** replace `SingleTrackerResolver` with a multi-provider resolver
  keyed by `domain.TrackerProvider`, resolving on each project's existing
  `TrackerIntakeConfig.Provider`.

If no GitLab host is configured, the composite contains only GitHub and behavior
is byte-for-byte the current behavior.

## Package layout (mirrors `github/` one-to-one)

```
internal/adapters/scm/gitlab/
  client.go             REST v4 client: PRIVATE-TOKEN header, base https://<host>/api/v4,
                        conditional GET (If-None-Match) with graceful fallback.
  auth.go               TokenSource: env -> glab CLI fallback (mirror github/auth.go).
  provider.go           Observe() + SCMCredentialsAvailable().
  observer_provider.go  ParseRepository + the 6 observer methods.
  doc.go / *_test.go
internal/adapters/tracker/gitlab/
  tracker.go  auth.go  doc.go  *_test.go
internal/adapters/scm/composite/
  provider.go  *_test.go   Ordered dispatcher over observe/scm.Provider.
```

## SCM interface -> GitLab REST v4 mapping

The 7 methods of `observe/scm.Provider`:

| Method | GitLab REST v4 | Normalization notes |
|---|---|---|
| `ParseRepository(remote)` | derive from remote URL | Support **nested groups** (`group/sub/project`): `SCMRepo.Repo` = full path, `Owner` = everything before last `/`, `Name` = last segment. Project id for API calls = URL-encoded full path. |
| `ListOpenPRsByRepo` | `GET /projects/:id/merge_requests?state=opened&per_page=...` | `opened`->open, `merged`->merged, `closed`->closed; `draft`/`work_in_progress` -> Draft. `iid` is the MR number. |
| `RepoPRListGuard` | `If-None-Match` on the MR list | GitLab does not send ETag on every endpoint. When absent, report modified and fetch fully. 304 -> `NotModified`. |
| `CommitChecksGuard` | `If-None-Match` on pipelines for SHA | Same graceful-fallback rule. |
| `FetchPullRequests` | `GET /projects/:id/merge_requests/:iid` (+ `.../pipelines`) | Mergeability from `merge_status` (`can_be_merged`/`cannot_be_merged`) + `has_conflicts`; diff stats from `changes_count` / `diff_refs`; branches from `source_branch`/`target_branch`; `sha` -> HeadSHA. |
| `FetchFailedCheckLogTail` | `.../pipelines/:pid/jobs` then `GET /projects/:id/jobs/:job/trace` | CI summary: `success`->passing, `failed`->failing, `running`/`pending`/`created`->pending, else unknown. Log tail = last 20 lines of trace. `SCMCheckObservation.ProviderID` = job id. |
| `FetchReviewThreads` | `.../merge_requests/:iid/discussions` (+ `.../approvals`) | discussion -> thread, note -> comment, `resolvable`/`resolved` preserved, `position.new_path`/`new_line` -> Path/Line. Decision derived from approvals (approved when required approvals met). Bot detection from note author. |

### GitHub differences to absorb in the adapter

- GitLab has no native "approved / changes_requested review" object. Round 1
  maps `Decision` from **approvals**; unresolved discussions are surfaced as
  outstanding feedback via the existing thread facts.
- MR identifier is `iid` (per-project), not a global id — use `iid` as `Number`.
- Nested groups mean "owner/name" is not a safe 2-part split; keep the full path.

## Tracker (`ports.Tracker`) -> GitLab issues

| Method | GitLab REST v4 |
|---|---|
| `Get` | `GET /projects/:id/issues/:iid` -> `domain.Issue` |
| `List` | `GET /projects/:id/issues?...` with the `domain.ListFilter` mapped to GitLab query params |
| `Preflight` | cheap authenticated call (e.g. `GET /user`) to validate the credential |

Issue IDs namespace as `gitlab:<native>` via the existing scheme in
`trackerintake/observer.go` (no collision with `github:<native>`).

## Auth

Reuse `FallbackTokenSource` / `EnvTokenSource` / `StaticTokenSource` patterns
from `github/auth.go`. GitLab-specific token sourcing:

1. `EnvTokenSource{["AO_GITLAB_TOKEN"]}`, falling back to `GITLAB_TOKEN`.
2. `GlabTokenSource` — shells out to
   `glab auth status --show-token --hostname <host>` and parses the
   `Token found: <token>` line. Memoized like `GHTokenSource`; `InvalidateToken`
   on a 401 so a rotated token is picked up without a daemon restart. The exec
   hook is injectable so tests never touch the real binary; the parser is
   unit-tested against a captured sample. (`glab` 1.91 has no `glab auth token`
   subcommand, so we parse `auth status --show-token` rather than mirror
   `gh auth token` verbatim.)

Header: `PRIVATE-TOKEN: <token>`. `SkipTokenPreflight: true` at startup to honor
the daemon's lazy-credential readiness rule. API base URL and the composite host
matcher both come from `AO_GITLAB_HOST`.

## Wiring changes

- `internal/daemon/scm_wiring.go`: build the provider list — GitHub always;
  GitLab appended when `AO_GITLAB_HOST` is set — wrap in the composite, pass the
  composite to the unchanged `scmobserve.New`.
- `internal/daemon/tracker_intake_wiring.go`: swap `SingleTrackerResolver` for a
  multi-provider resolver holding both the lazy GitHub and lazy GitLab trackers,
  resolving per project `cfg.Provider`.

## Data model / config / API

- `domain/tracker.go`: add `TrackerProviderGitLab TrackerProvider = "gitlab"`.
- `domain/projectconfig.go`: accept `gitlab` in `TrackerIntakeConfig` validation
  (currently rejects unknown providers).
- OpenAPI: widen the tracker provider enum tag `enum:"github"` ->
  `enum:"github,gitlab"` in the DTO/specgen source, then run `npm run api` to
  regenerate the spec and frontend TS types.
- **No SQLite migration.** Provider lives in the project-config JSON; issue-ID
  namespacing already exists.

## Testing

Mirror `github/*_test.go` style — table tests, `httptest` fake GitLab server,
JSON fixtures for MR / pipeline / jobs / discussions / issues. No real network
(honors the repo rule against network in tests).

- Normalization: draft, merged, closed-not-merged, conflict/mergeability,
  pipeline state buckets, nested-group path parsing, 20-line log-tail truncation,
  approvals -> decision.
- Composite routing: GitLab host -> gitlab provider; other host -> github;
  no GitLab config -> github-only, unchanged behavior.
- `GlabTokenSource` parser: token extracted from a captured
  `glab auth status --show-token` sample; env precedence over CLI; invalidate on
  401.
- Tracker: `Get`/`List`/`Preflight` against the fake server; multi-resolver
  picks the right adapter per provider.

## Rollout / config surface

- `AO_GITLAB_HOST` — enables GitLab; sets the composite matcher and API base.
- `AO_GITLAB_TOKEN` / `GITLAB_TOKEN` — token, else `glab` login is used.
- Absent `AO_GITLAB_HOST` => zero behavior change for existing GitHub users.
