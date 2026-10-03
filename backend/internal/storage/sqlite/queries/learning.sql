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
