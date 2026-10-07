-- name: InsertTestinyEvidenceLog :exec
INSERT INTO testiny_evidence_log (session_id, run_id, event, file, case_id, drive_id, comment_id, set_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListTestinyEvidenceLinked :many
-- Every link AO posted in one run, from any task it is linked to, oldest
-- first: the name of a Drive file does not depend on which task posted it.
SELECT * FROM testiny_evidence_log
WHERE run_id = ? AND event = 'linked'
ORDER BY created_at, id;
