-- name: UpsertSessionTranscript :exec
INSERT INTO session_transcript (session_id, transcript_path, claude_session_id, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (session_id, transcript_path) DO UPDATE SET
    claude_session_id = CASE WHEN excluded.claude_session_id <> '' THEN excluded.claude_session_id
                             ELSE session_transcript.claude_session_id END,
    last_seen_at = excluded.last_seen_at;

-- name: ListSessionTranscriptsByProject :many
SELECT st.session_id, st.transcript_path, st.claude_session_id, st.first_seen_at, st.last_seen_at
FROM session_transcript st
JOIN sessions s ON s.id = st.session_id
WHERE s.project_id = ?
ORDER BY st.first_seen_at;

-- name: InsertPromptFingerprint :exec
INSERT INTO prompt_fingerprint (session_id, project_id, claude_session_id, sha256, bytes, submitted_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: MarkPromptFingerprintMatched :execrows
-- Stamps ONE unmatched prompt with this fingerprint: the same text typed twice
-- is two prompts, and each typed turn read accounts for one of them.
UPDATE prompt_fingerprint SET matched_at = ?
WHERE id = (
    SELECT pf.id FROM prompt_fingerprint pf
    WHERE pf.session_id = ? AND pf.sha256 = ? AND pf.matched_at IS NULL
    ORDER BY pf.submitted_at LIMIT 1
);

-- name: CountUnmatchedPromptFingerprints :one
-- Prompts the hook saw before the cutoff that capture never found in a
-- transcript. The cutoff leaves capture time to reach a prompt it has not read
-- yet, so only a turn it read past and missed counts.
SELECT COUNT(*) FROM prompt_fingerprint
WHERE project_id = ? AND matched_at IS NULL AND submitted_at < ?;

-- name: CountPromptFingerprints :one
SELECT COUNT(*) FROM prompt_fingerprint WHERE project_id = ?;

-- name: InsertDeliveredFingerprint :exec
INSERT INTO delivered_fingerprint (session_id, project_id, sha256, bytes, trigger, author, delivered_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListDeliveredFingerprintsBySession :many
SELECT sha256, author, trigger
FROM delivered_fingerprint
WHERE session_id = ?
ORDER BY id;

-- name: GetLearnCursor :one
SELECT transcript_path, project_id, session_id, attribution, byte_offset, file_size, file_mtime,
    pending_carry, human_turns, machine_turns, last_error, updated_at
FROM learn_cursor WHERE transcript_path = ?;

-- name: UpsertLearnCursor :exec
INSERT INTO learn_cursor (transcript_path, project_id, session_id, attribution, byte_offset, file_size,
    file_mtime, pending_carry, human_turns, machine_turns, last_error, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (transcript_path) DO UPDATE SET
    project_id    = excluded.project_id,
    session_id    = excluded.session_id,
    attribution   = excluded.attribution,
    byte_offset   = excluded.byte_offset,
    file_size     = excluded.file_size,
    file_mtime    = excluded.file_mtime,
    pending_carry = excluded.pending_carry,
    human_turns   = excluded.human_turns,
    machine_turns = excluded.machine_turns,
    last_error    = excluded.last_error,
    updated_at    = excluded.updated_at;

-- name: SetLearnCursorError :exec
-- Records a failed pass without moving the cursor. The row is created when the
-- very first pass over a file fails, so the failure is visible in status.
INSERT INTO learn_cursor (transcript_path, project_id, session_id, attribution, last_error, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (transcript_path) DO UPDATE SET
    last_error = excluded.last_error,
    updated_at = excluded.updated_at;

-- name: ListLearnCursorsByProject :many
SELECT transcript_path, project_id, session_id, attribution, byte_offset, file_size, file_mtime,
    pending_carry, human_turns, machine_turns, last_error, updated_at
FROM learn_cursor WHERE project_id = ?
ORDER BY transcript_path;

-- name: InsertLearnExcerpt :execrows
-- A turn already captured from this transcript is left exactly as it is: the
-- first capture is the one that saw the turn's window close.
INSERT INTO learn_excerpt (project_id, session_id, transcript_path, turn_uuid, turn_at, source_class,
    cwd, git_branch, before_json, human_text, after_json, redactions_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (transcript_path, turn_uuid) DO NOTHING;

-- name: ListLearnExcerptsByProject :many
SELECT id, project_id, session_id, transcript_path, turn_uuid, turn_at, source_class, cwd, git_branch,
    before_json, human_text, after_json, redactions_json, created_at
FROM learn_excerpt
WHERE project_id = ?
ORDER BY turn_at DESC, id DESC
LIMIT ?;

-- name: CountLearnExcerptsByProject :one
SELECT COUNT(*) FROM learn_excerpt WHERE project_id = ?;

-- name: CountLearnExcerptsBySourceClass :many
SELECT source_class, COUNT(*) AS turns
FROM learn_excerpt
WHERE project_id = ?
GROUP BY source_class
ORDER BY source_class;

-- name: DeleteLearnExcerptsByProject :execrows
DELETE FROM learn_excerpt WHERE project_id = ?;

-- name: DeleteLearnCursorsByProject :execrows
DELETE FROM learn_cursor WHERE project_id = ?;

-- name: DeletePromptFingerprintsByProject :execrows
DELETE FROM prompt_fingerprint WHERE project_id = ?;

-- name: DeleteDeliveredFingerprintsByProject :execrows
DELETE FROM delivered_fingerprint WHERE project_id = ?;

-- name: ListUncollectedTurnTimes :many
-- Every captured turn no model has seen yet, as its session and time. Collect
-- groups them per session in Go: SQLite returns an aggregate of a TIMESTAMP
-- column as a bare number, which no longer scans as a time.
SELECT session_id, turn_at
FROM learn_excerpt
WHERE project_id = ? AND collected_at IS NULL
ORDER BY turn_at, id;

-- name: ListUncollectedExcerptsBySession :many
SELECT id, project_id, session_id, transcript_path, turn_uuid, turn_at, source_class, cwd, git_branch,
    before_json, human_text, after_json, redactions_json, created_at
FROM learn_excerpt
WHERE project_id = ? AND session_id = ? AND collected_at IS NULL
ORDER BY turn_at, id
LIMIT ?;

-- name: CountUncollectedExcerpts :one
SELECT COUNT(*) FROM learn_excerpt WHERE project_id = ? AND collected_at IS NULL;

-- name: MarkExcerptCollected :exec
UPDATE learn_excerpt SET collected_at = ?, collected_job_id = ?
WHERE id = ? AND collected_at IS NULL;

-- name: InsertLearnJob :one
INSERT INTO learn_job (project_id, session_id, state, model, turns, started_at)
VALUES (?, ?, 'running', ?, ?, ?)
RETURNING id;

-- name: FinishLearnJob :exec
UPDATE learn_job
SET state = ?, drafts = ?, rejected = ?, cost_usd = ?, input_tokens = ?, output_tokens = ?, duration_ms = ?,
    error = ?, stderr_tail = ?, finished_at = ?
WHERE id = ?;

-- name: AbandonRunningLearnJobs :execrows
-- A job still 'running' when the daemon starts died with the last daemon. Its
-- excerpts were never marked collected, so the next pass simply runs them again.
UPDATE learn_job SET state = 'abandoned', finished_at = ?
WHERE state = 'running';

-- name: ListRecentLearnJobsBySession :many
SELECT id, state, started_at, finished_at, error
FROM learn_job WHERE session_id = ?
ORDER BY id DESC LIMIT ?;

-- name: SumLearnJobCostSince :one
SELECT CAST(COALESCE(SUM(cost_usd), 0) AS REAL) FROM learn_job WHERE started_at >= ?;

-- name: LastFailedLearnJob :one
SELECT id, project_id, session_id, error, stderr_tail, started_at
FROM learn_job WHERE project_id = ? AND state = 'failed'
ORDER BY id DESC LIMIT 1;

-- name: LastFinishedLearnJob :one
SELECT id, state, finished_at FROM learn_job
WHERE project_id = ? AND state IN ('done', 'failed')
ORDER BY id DESC LIMIT 1;

-- name: InsertLearnDraft :one
-- A statement the session already has a draft for is not stored again; the
-- returned id is 0 then.
INSERT INTO learn_draft (project_id, session_id, task_key, job_id, kind, statement, statement_hash,
    applies_when, scope_hint, confidence, quote, anchor_excerpt_id, evidence_json, agent_before,
    weak, supersedes_id, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?)
ON CONFLICT (session_id, statement_hash) DO NOTHING
RETURNING id;

-- name: SetLearnDraftStatus :exec
UPDATE learn_draft SET status = ? WHERE id = ? AND session_id = ?;

-- name: ListOpenDraftsBySession :many
SELECT id, statement FROM learn_draft
WHERE session_id = ? AND status = 'open'
ORDER BY id;

-- name: ListLearnDraftsByProject :many
SELECT d.id, d.project_id, d.session_id, d.task_key, d.job_id, d.kind, d.statement, d.applies_when,
    d.scope_hint, d.confidence, d.quote, d.anchor_excerpt_id, d.evidence_json, d.agent_before, d.weak,
    d.supersedes_id, d.status, d.created_at,
    e.source_class AS anchor_source_class,
    e.turn_at AS anchor_turn_at
FROM learn_draft d
LEFT JOIN learn_excerpt e ON e.id = d.anchor_excerpt_id
WHERE d.project_id = ?
ORDER BY d.id DESC
LIMIT ?;

-- name: CountLearnDraftsByStatus :many
SELECT status, COUNT(*) AS drafts FROM learn_draft WHERE project_id = ? GROUP BY status ORDER BY status;

-- name: DeleteLearnDraftsByProject :execrows
DELETE FROM learn_draft WHERE project_id = ?;

-- name: DeleteLearnJobsByProject :execrows
DELETE FROM learn_job WHERE project_id = ?;
