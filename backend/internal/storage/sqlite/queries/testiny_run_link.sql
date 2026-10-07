-- name: InsertTestinyRunLink :exec
-- A re-link keeps the first row, so its author and its place in the list stay.
INSERT INTO testiny_run_link (session_id, run_id, linked_by, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (session_id, run_id) DO NOTHING;

-- name: DeleteTestinyRunLink :exec
DELETE FROM testiny_run_link WHERE session_id = ? AND run_id = ?;

-- name: ListTestinyRunLinks :many
SELECT * FROM testiny_run_link WHERE session_id = ? ORDER BY created_at, rowid;
