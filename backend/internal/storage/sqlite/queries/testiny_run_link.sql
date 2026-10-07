-- name: InsertTestinyRunLink :exec
-- A re-link keeps the first row, so its author and its place in the list stay.
INSERT INTO testiny_run_link (session_id, run_id, project_id, project_key, project_name, linked_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, run_id) DO NOTHING;

-- name: DeleteTestinyRunLink :exec
DELETE FROM testiny_run_link WHERE session_id = ? AND run_id = ?;

-- name: ListTestinyRunLinks :many
SELECT * FROM testiny_run_link WHERE session_id = ? ORDER BY created_at, rowid;

-- name: FillTestinyRunLinkProject :exec
-- A link made before AO stored the run's project gets it the first time the
-- run is read again. A link that has its project keeps it.
UPDATE testiny_run_link SET project_id = ?, project_key = ?, project_name = ?
WHERE run_id = ? AND project_id = 0;
