-- name: ListSmokeChecksBySession :many
SELECT id, session_id, project_id, seq, name, why, steps, expected, pr_num, file_ref, verdict, note, decided_at, reported_at, created_at, updated_at, agent_verdict, agent_note, agent_ran_at, agent_sha, retired_at, retired_reason, authored_by, authored_by_role, authored_at
FROM smoke_check WHERE session_id = ? ORDER BY (retired_at IS NOT NULL), seq, created_at;

-- name: GetSmokeCheck :one
SELECT id, session_id, project_id, seq, name, why, steps, expected, pr_num, file_ref, verdict, note, decided_at, reported_at, created_at, updated_at, agent_verdict, agent_note, agent_ran_at, agent_sha, retired_at, retired_reason, authored_by, authored_by_role, authored_at
FROM smoke_check WHERE id = ?;

-- name: InsertSmokeCheck :exec
-- A fresh case starts with BOTH results empty: 'pending' is the user's default
-- (nobody has played it) and '' the machine's (nothing has run it).
INSERT INTO smoke_check (id, session_id, project_id, seq, name, why, steps, expected, pr_num, file_ref, verdict, note, decided_at, reported_at, created_at, updated_at, authored_by, authored_by_role, authored_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', NULL, NULL, ?, ?, ?, ?, ?);

-- name: UpdateSmokeCheckAuthored :execrows
-- Re-author keeps the user's play results: only the worker-authored fields are
-- rewritten; verdict/note/decided_at/reported_at and the evidence rows are left
-- untouched.
UPDATE smoke_check SET seq = ?, name = ?, why = ?, steps = ?, expected = ?, pr_num = ?, file_ref = ?, updated_at = ?,
    authored_by = ?, authored_by_role = ?, authored_at = ?
WHERE id = ?;

-- name: DeleteSmokeCheck :execrows
DELETE FROM smoke_check WHERE id = ?;

-- name: SetSmokeVerdict :execrows
UPDATE smoke_check SET verdict = ?, note = ?, decided_at = ?, updated_at = ? WHERE id = ?;

-- name: ResetSmokeCheck :execrows
UPDATE smoke_check SET verdict = 'pending', note = '', decided_at = NULL, updated_at = ? WHERE id = ?;

-- name: MarkSmokeReported :execrows
UPDATE smoke_check SET reported_at = ?, updated_at = ? WHERE session_id = ?;

-- name: InsertSmokeEvidence :exec
INSERT INTO smoke_evidence (id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSmokeEvidence :one
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source
FROM smoke_evidence WHERE id = ?;

-- name: ListSmokeEvidenceByCheck :many
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source
FROM smoke_evidence WHERE check_id = ? ORDER BY created_at;

-- name: ListSmokeEvidenceCreatedBefore :many
-- Age-based retention sweep: every evidence row whose created_at predates the
-- TTL cutoff, across all sessions. Ordered oldest-first so a batch purge is
-- deterministic.
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source
FROM smoke_evidence WHERE created_at < ? ORDER BY created_at;

-- name: DeleteUserSmokeEvidenceByCheck :exec
-- Scoped to the user's own attachments: Reset clears what the USER recorded
-- while playing a case, and the machine's artifacts are not theirs to drop.
DELETE FROM smoke_evidence WHERE check_id = ? AND source = 'user';

-- name: ListUserSmokeEvidenceByCheck :many
-- The rows Reset is about to delete, so the service can remove exactly those
-- blobs instead of wiping the case's whole evidence directory.
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source
FROM smoke_evidence WHERE check_id = ? AND source = 'user' ORDER BY created_at;

-- name: DeleteSmokeEvidence :execrows
DELETE FROM smoke_evidence WHERE id = ?;

-- name: SetSmokeAgentResult :execrows
-- The machine's result, written only by `ao smoke record`. Disjoint from the
-- user-runtime fields by construction: this statement cannot reach verdict,
-- note, decided_at or the user's evidence rows.
UPDATE smoke_check SET agent_verdict = ?, agent_note = ?, agent_ran_at = ?, agent_sha = ?, updated_at = ?
WHERE id = ? AND retired_at IS NULL;

-- name: RetireSmokeCheck :execrows
-- Retire is not delete: nothing on the row is cleared. The case simply stops
-- being one the user is asked to play, and the reason it stopped is recorded.
-- Guarded on retired_at IS NULL so a second retire is a no-op rather than
-- overwriting the original reason and date.
UPDATE smoke_check SET retired_at = ?, retired_reason = ?, updated_at = ?
WHERE id = ? AND retired_at IS NULL;

-- name: GetSmokeChecklistState :one
SELECT session_id, stood_down_at, stood_down_by, stood_down_by_role, reason, created_at, updated_at
FROM smoke_checklist_state WHERE session_id = ?;

-- name: UpsertSmokeChecklistState :exec
-- Standing down twice re-states it: the later reason and author replace the
-- earlier ones, because the claim is about the checklist as it is NOW.
INSERT INTO smoke_checklist_state (session_id, stood_down_at, stood_down_by, stood_down_by_role, reason, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET
    stood_down_at = excluded.stood_down_at,
    stood_down_by = excluded.stood_down_by,
    stood_down_by_role = excluded.stood_down_by_role,
    reason = excluded.reason,
    updated_at = excluded.updated_at;

-- name: DeleteSmokeChecklistState :exec
-- Authoring a case retracts a stand-down: a case on the list contradicts "there
-- is nothing here for a person", so the claim goes rather than sitting beside
-- the thing that disproves it.
DELETE FROM smoke_checklist_state WHERE session_id = ?;
