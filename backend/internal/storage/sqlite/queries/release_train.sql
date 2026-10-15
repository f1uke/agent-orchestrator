-- name: ListReleaseResources :many
SELECT * FROM release_resource ORDER BY kind, scope;

-- name: ListReleaseItems :many
SELECT kind, scope, body FROM release_item ORDER BY kind, scope, item_key;

-- name: UpsertReleaseResourceSpec :exec
-- A fact version change clears the cursor and completeness, so the kind is
-- refetched from scratch in the new shape.
INSERT INTO release_resource (kind, scope, app_id, sync_mode, fact_version, planned_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (kind, scope) DO UPDATE SET
    app_id = excluded.app_id,
    sync_mode = excluded.sync_mode,
    planned_at = excluded.planned_at,
    cursor = CASE WHEN release_resource.fact_version = excluded.fact_version THEN release_resource.cursor END,
    complete_at = CASE WHEN release_resource.fact_version = excluded.fact_version THEN release_resource.complete_at END,
    fact_version = excluded.fact_version;

-- name: UpsertReleaseItem :exec
INSERT INTO release_item (kind, scope, item_key, body, source_updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (kind, scope, item_key) DO UPDATE SET
    body = excluded.body,
    source_updated_at = excluded.source_updated_at;

-- name: ListReleaseItemKeys :many
SELECT item_key FROM release_item WHERE kind = ? AND scope = ?;

-- name: DeleteReleaseItem :exec
DELETE FROM release_item WHERE kind = ? AND scope = ? AND item_key = ?;

-- name: MarkReleaseResourceFetched :execrows
UPDATE release_resource SET
    cursor = ?,
    complete_at = COALESCE(sqlc.narg('complete_at'), complete_at),
    last_attempt_at = sqlc.arg('at'),
    last_success_at = sqlc.arg('at'),
    failures = 0,
    error_class = NULL,
    error_message = NULL,
    next_attempt_at = NULL,
    generation = generation + 1
WHERE kind = sqlc.arg('kind') AND scope = sqlc.arg('scope');

-- name: MarkReleaseResourceFailed :execrows
UPDATE release_resource SET
    last_attempt_at = ?,
    failures = ?,
    error_class = ?,
    error_message = ?,
    next_attempt_at = ?,
    generation = generation + 1
WHERE kind = ? AND scope = ?;

-- name: DeleteUnplannedReleaseResources :execrows
DELETE FROM release_resource WHERE planned_at < ?;

-- name: ListReleaseRuleSettings :many
SELECT * FROM release_rule_setting ORDER BY rule_id;

-- name: UpsertReleaseRuleSetting :exec
INSERT INTO release_rule_setting (rule_id, enabled, updated_at) VALUES (?, ?, ?)
ON CONFLICT (rule_id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at;

-- name: DeleteReleaseRuleSetting :exec
DELETE FROM release_rule_setting WHERE rule_id = ?;

-- name: ListReleaseHitHides :many
SELECT * FROM release_hit_hide ORDER BY hit_id;

-- name: UpsertReleaseHitHide :exec
INSERT INTO release_hit_hide (hit_id, evidence, severity, reason, hidden_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (hit_id) DO UPDATE SET
    evidence = excluded.evidence,
    severity = excluded.severity,
    reason = excluded.reason,
    hidden_at = excluded.hidden_at;

-- name: DeleteReleaseHitHide :execrows
DELETE FROM release_hit_hide WHERE hit_id = ?;

-- name: ListReleaseLlbPlans :many
SELECT * FROM release_llb_plan ORDER BY id;

-- name: ListReleaseLlbPlanBranches :many
SELECT * FROM release_llb_plan_branch ORDER BY plan_id, app_id, branch;

-- name: ListReleaseLlbPlanAccepted :many
SELECT * FROM release_llb_plan_accepted ORDER BY plan_id, mr_ref;

-- name: GetReleaseLlbPlanVersion :one
SELECT version FROM release_llb_plan WHERE id = ?;

-- name: UpsertReleaseLlbPlan :exec
INSERT INTO release_llb_plan (id, name, target_kind, target_train, note, version, updated_by, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    name = excluded.name,
    target_kind = excluded.target_kind,
    target_train = excluded.target_train,
    note = excluded.note,
    version = excluded.version,
    updated_by = excluded.updated_by,
    updated_at = excluded.updated_at;

-- name: ReleaseLlbBranchOwner :one
SELECT plan_id FROM release_llb_plan_branch WHERE app_id = ? AND branch = ? AND plan_id != ?;

-- name: DeleteReleaseLlbPlanBranches :exec
DELETE FROM release_llb_plan_branch WHERE plan_id = ?;

-- name: InsertReleaseLlbPlanBranch :exec
INSERT INTO release_llb_plan_branch (plan_id, app_id, branch) VALUES (?, ?, ?);

-- name: DeleteReleaseLlbPlanAccepted :exec
DELETE FROM release_llb_plan_accepted WHERE plan_id = ?;

-- name: InsertReleaseLlbPlanAccepted :exec
INSERT INTO release_llb_plan_accepted (plan_id, mr_ref, reason) VALUES (?, ?, ?);

-- name: GetLiveReleaseFix :one
SELECT * FROM release_fix
WHERE hit_id = ? AND plan_fingerprint = ? AND state IN ('dry_run', 'applying', 'applied');

-- name: GetReleaseFix :one
SELECT * FROM release_fix WHERE id = ?;

-- name: ListReleaseFixes :many
SELECT * FROM release_fix ORDER BY created_at, id;

-- name: InsertReleaseFix :exec
INSERT INTO release_fix (id, hit_id, rule_id, choice, plan, plan_fingerprint, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SetReleaseFixState :execrows
UPDATE release_fix SET state = ?, updated_at = ? WHERE id = ? AND state = ?;

-- name: InsertReleaseFixEvent :exec
INSERT INTO release_fix_event (fix_id, at, actor, kind, detail) VALUES (?, ?, ?, ?, ?);

-- name: ListReleaseFixEvents :many
SELECT * FROM release_fix_event ORDER BY fix_id, seq;
