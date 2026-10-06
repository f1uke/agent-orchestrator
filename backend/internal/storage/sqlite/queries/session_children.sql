-- name: InsertSessionChild :exec
INSERT INTO session_children (session_id, project_id, agent_id, parent_agent_id, agent_type, description,
    branch, target_branch, base_sha, base_dirty, worktree_path, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSessionChild :one
SELECT * FROM session_children WHERE session_id = ? AND agent_id = ?;

-- name: ListSessionChildren :many
SELECT * FROM session_children WHERE session_id = ? ORDER BY created_at, agent_id;

-- name: ListSessionChildrenInStates :many
-- Every child in one of the given states, across sessions: daemon-start
-- reconciliation reads the rows a crash may have left mid-operation.
SELECT * FROM session_children WHERE state IN (sqlc.slice('states')) ORDER BY created_at, agent_id;

-- name: ListBoardSessionChildren :many
-- What the board draws: every child of a live worker, plus preserved children
-- of any worker (their kept branches need a person even after the worker ended).
SELECT c.* FROM session_children c
JOIN sessions s ON s.id = c.session_id
WHERE s.is_terminated = 0 OR c.state = 'preserved'
ORDER BY c.created_at, c.agent_id;

-- name: UpdateSessionChild :execrows
UPDATE session_children
SET agent_type = ?, description = ?, state = ?, merge_head_before = ?, merged_sha = ?, commits = ?,
    files_changed = ?, detail = ?, stop_blocks = ?, notified_state = ?, updated_at = ?, finished_at = ?
WHERE session_id = ? AND agent_id = ?;
