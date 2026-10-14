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
