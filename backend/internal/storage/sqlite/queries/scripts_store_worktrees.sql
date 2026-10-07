-- name: UpsertScriptsStoreWorktree :exec
-- One row per workspace owner: a recreated worktree overwrites the row of the
-- one it replaces, keeping its creation time.
INSERT INTO scripts_store_worktrees (session_id, project_id, store, path, branch, base_branch, state,
    held_reason, held_files, uncommitted, unpublished, created_at, updated_at, published_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET
    project_id = excluded.project_id,
    store = excluded.store,
    path = excluded.path,
    branch = excluded.branch,
    base_branch = excluded.base_branch,
    state = excluded.state,
    held_reason = excluded.held_reason,
    held_files = excluded.held_files,
    uncommitted = excluded.uncommitted,
    unpublished = excluded.unpublished,
    updated_at = excluded.updated_at,
    published_at = excluded.published_at;

-- name: GetScriptsStoreWorktree :one
SELECT * FROM scripts_store_worktrees WHERE session_id = ?;

-- name: ListScriptsStoreWorktreesInStates :many
SELECT * FROM scripts_store_worktrees WHERE state IN (sqlc.slice('states')) ORDER BY created_at, session_id;
