-- name: InsertTestinyResultLog :exec
INSERT INTO testiny_result_log (session_id, run_id, case_id, status, comment, steps, set_by, sha, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListLatestTestinyResults :many
-- The latest entry that set a case status, for each case of one run of a
-- task. Entries written in one batch share created_at, so the id breaks the
-- tie.
SELECT l.* FROM testiny_result_log l
WHERE l.session_id = ? AND l.run_id = ? AND l.status <> ''
  AND l.id = (
    SELECT m.id FROM testiny_result_log m
    WHERE m.session_id = l.session_id AND m.run_id = l.run_id AND m.case_id = l.case_id AND m.status <> ''
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT 1
  )
ORDER BY l.case_id;

-- name: ListTestinyStepResultLog :many
-- Every entry that set step results in one run of a task, oldest first.
SELECT * FROM testiny_result_log
WHERE session_id = ? AND run_id = ? AND steps <> '[]'
ORDER BY created_at, id;
