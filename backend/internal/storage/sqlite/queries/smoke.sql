-- name: ListSmokeChecksBySession :many
SELECT id, session_id, project_id, seq, name, why, steps, expected, pr_num, file_ref, verdict, note, decided_at, reported_at, created_at, updated_at, retired_at, retired_reason, authored_by, authored_by_role, authored_at, agreed_run_id
FROM smoke_check WHERE session_id = ? ORDER BY (retired_at IS NOT NULL), seq, created_at;

-- name: GetSmokeCheck :one
SELECT id, session_id, project_id, seq, name, why, steps, expected, pr_num, file_ref, verdict, note, decided_at, reported_at, created_at, updated_at, retired_at, retired_reason, authored_by, authored_by_role, authored_at, agreed_run_id
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
-- agreed_run_id is written on the SAME statement as the verdict, so a verdict and
-- the claim about how it was reached can never disagree: agreeing sets both, and
-- a hand-made call resets it to '' rather than leaving a previous agreement
-- standing over a verdict the user has since changed by hand.
UPDATE smoke_check SET verdict = ?, note = ?, decided_at = ?, agreed_run_id = ?, updated_at = ? WHERE id = ?;

-- name: ResetSmokeCheck :execrows
UPDATE smoke_check SET verdict = 'pending', note = '', decided_at = NULL, agreed_run_id = '', updated_at = ? WHERE id = ?;

-- name: MarkSmokeReported :execrows
UPDATE smoke_check SET reported_at = ?, updated_at = ? WHERE session_id = ?;

-- name: InsertSmokeEvidence :exec
INSERT INTO smoke_evidence (id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source, run_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSmokeEvidence :one
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source, run_id
FROM smoke_evidence WHERE id = ?;

-- name: ListSmokeEvidenceByCheck :many
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source, run_id
FROM smoke_evidence WHERE check_id = ? ORDER BY created_at;

-- name: ListSmokeEvidenceCreatedBefore :many
-- Age-based retention sweep: every evidence row whose created_at predates the
-- TTL cutoff, across all sessions. Ordered oldest-first so a batch purge is
-- deterministic.
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source, run_id
FROM smoke_evidence WHERE created_at < ? ORDER BY created_at;

-- name: DeleteUserSmokeEvidenceByCheck :exec
-- Scoped to the user's own attachments: Reset clears what the USER recorded
-- while playing a case, and the machine's artifacts are not theirs to drop.
DELETE FROM smoke_evidence WHERE check_id = ? AND source = 'user';

-- name: ListUserSmokeEvidenceByCheck :many
-- The rows Reset is about to delete, so the service can remove exactly those
-- blobs instead of wiping the case's whole evidence directory.
SELECT id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source, run_id
FROM smoke_evidence WHERE check_id = ? AND source = 'user' ORDER BY created_at;

-- name: DeleteSmokeEvidence :execrows
DELETE FROM smoke_evidence WHERE id = ?;

-- name: ListSmokeRunsByCheck :many
-- A case's machine runs, oldest first, so the caller reads them the way they
-- happened and the last one is the current result.
SELECT id, check_id, session_id, seq, verdict, note, sha, recorded_at, created_at, updated_at
FROM smoke_run WHERE check_id = ? ORDER BY seq, created_at;

-- name: GetOpenSmokeRun :one
-- The round the machine is in the middle of: opened by its first capture and not
-- yet concluded. At most one is open per case, because opening only ever happens
-- when this returns nothing.
SELECT id, check_id, session_id, seq, verdict, note, sha, recorded_at, created_at, updated_at
FROM smoke_run WHERE check_id = ? AND recorded_at IS NULL ORDER BY seq DESC LIMIT 1;

-- name: NextSmokeRunSeq :one
-- 1-based position of the run about to be inserted, so "RUN 3" keeps meaning the
-- third round even after an earlier one is read back out of order.
SELECT COALESCE(MAX(seq), 0) + 1 FROM smoke_run WHERE check_id = ?;

-- name: InsertSmokeRun :execrows
-- A run opens with no result: verdict/note/sha stay empty and recorded_at NULL
-- until the record lands. Inserting it is what gives the round's evidence
-- something to point at.
--
-- Guarded on the case being ACTIVE, so the frozen rule holds in the statement
-- and not only in the service's read-then-write: a retired case is not one
-- anything is asked to run, and nothing may open a round against it.
INSERT INTO smoke_run (id, check_id, session_id, seq, verdict, note, sha, recorded_at, created_at, updated_at)
SELECT ?, ?, ?, ?, '', '', '', NULL, ?, ?
WHERE EXISTS (SELECT 1 FROM smoke_check c WHERE c.id = ? AND c.retired_at IS NULL);

-- name: CloseSmokeRun :execrows
-- The machine's result, written only by `ao smoke record`. Disjoint from the
-- user-runtime fields by construction: this statement cannot reach verdict,
-- note, decided_at or the user's evidence rows - it cannot even reach the case
-- row. Guarded on recorded_at IS NULL so a result lands once and a later record
-- opens its own run rather than rewriting this one.
UPDATE smoke_run SET verdict = ?, note = ?, sha = ?, recorded_at = ?, updated_at = ?
WHERE id = ? AND recorded_at IS NULL;

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
