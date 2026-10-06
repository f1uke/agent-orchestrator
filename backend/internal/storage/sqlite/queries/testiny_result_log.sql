-- name: InsertTestinyResultLog :exec
INSERT INTO testiny_result_log (session_id, run_id, case_id, status, comment, set_by, sha, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListLatestTestinyResults :many
-- The latest entry for each case of one run of a task. Entries written in one
-- batch share created_at, so the id breaks the tie.
SELECT l.* FROM testiny_result_log l
WHERE l.session_id = ? AND l.run_id = ?
  AND l.id = (
    SELECT m.id FROM testiny_result_log m
    WHERE m.session_id = l.session_id AND m.run_id = l.run_id AND m.case_id = l.case_id
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT 1
  )
ORDER BY l.case_id;
