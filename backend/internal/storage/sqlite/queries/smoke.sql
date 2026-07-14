-- name: ListSmokeChecksBySession :many
SELECT id, session_id, project_id, seq, name, why, steps, expected, pr_num, file_ref, verdict, note, decided_at, reported_at, created_at, updated_at
FROM smoke_check WHERE session_id = ? ORDER BY seq, created_at;

-- name: GetSmokeCheck :one
SELECT id, session_id, project_id, seq, name, why, steps, expected, pr_num, file_ref, verdict, note, decided_at, reported_at, created_at, updated_at
FROM smoke_check WHERE id = ?;

-- name: InsertSmokeCheck :exec
INSERT INTO smoke_check (id, session_id, project_id, seq, name, why, steps, expected, pr_num, file_ref, verdict, note, decided_at, reported_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', NULL, NULL, ?, ?);

-- name: UpdateSmokeCheckAuthored :execrows
-- Re-author keeps the user's play results: only the worker-authored fields are
-- rewritten; verdict/note/decided_at/reported_at and the evidence rows are left
-- untouched.
UPDATE smoke_check SET seq = ?, name = ?, why = ?, steps = ?, expected = ?, pr_num = ?, file_ref = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteSmokeCheck :exec
DELETE FROM smoke_check WHERE id = ?;

-- name: SetSmokeVerdict :execrows
UPDATE smoke_check SET verdict = ?, note = ?, decided_at = ?, updated_at = ? WHERE id = ?;

-- name: ResetSmokeCheck :execrows
UPDATE smoke_check SET verdict = 'pending', note = '', decided_at = NULL, updated_at = ? WHERE id = ?;

-- name: MarkSmokeReported :execrows
UPDATE smoke_check SET reported_at = ?, updated_at = ? WHERE session_id = ?;

-- name: InsertSmokeEvidence :exec
INSERT INTO smoke_evidence (id, check_id, session_id, kind, filename, mime, size_bytes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSmokeEvidence :one
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at
FROM smoke_evidence WHERE id = ?;

-- name: ListSmokeEvidenceByCheck :many
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at
FROM smoke_evidence WHERE check_id = ? ORDER BY created_at;

-- name: ListSmokeEvidenceCreatedBefore :many
-- Age-based retention sweep: every evidence row whose created_at predates the
-- TTL cutoff, across all sessions. Ordered oldest-first so a batch purge is
-- deterministic.
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at
FROM smoke_evidence WHERE created_at < ? ORDER BY created_at;

-- name: DeleteSmokeEvidenceByCheck :exec
DELETE FROM smoke_evidence WHERE check_id = ?;

-- name: DeleteSmokeEvidence :execrows
DELETE FROM smoke_evidence WHERE id = ?;
