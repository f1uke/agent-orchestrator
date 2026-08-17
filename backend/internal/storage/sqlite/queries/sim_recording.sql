-- NOTE: keep this file pure ASCII. sqlc 1.31's SQLite parser tracks statement
-- offsets in runes but slices the source in bytes, so one multi-byte character
-- in a comment shifts the tail of every generated query into the next one.

-- name: StartSimRecording :execrows
-- One statement, so two callers cannot both win. The predicate is the whole
-- rule: the caller holds a live lease on this device, and no recording is
-- already open on it. A recording that was stopped is not open, so starting
-- again after stopping is allowed and starting over a live one is not.
--
-- NOTE: uses sqlc.arg (named, reused) rather than bare "?" reused by numbered
-- position, unlike AcquireSimLease. sqlc 1.31's SQLite parser rejects mixing
-- bare "?" with numbered "?N" in one statement ("can not mix $1 format with ?
-- format"), which the udid/session_id/now reuse below requires.
INSERT INTO sim_recording (udid, session_id, name, started_at, stopped_at, updated_at)
SELECT sqlc.arg(udid), sqlc.arg(session_id), sqlc.arg(name), sqlc.arg(started_at), NULL, sqlc.arg(now)
WHERE EXISTS (
    SELECT 1 FROM sim_lease
    WHERE sim_lease.udid = sqlc.arg(udid)
      AND sim_lease.session_id = sqlc.arg(session_id)
      AND sim_lease.expires_at > sqlc.arg(now)
)
ON CONFLICT (udid) DO UPDATE SET
    session_id = excluded.session_id,
    name       = excluded.name,
    started_at = excluded.started_at,
    stopped_at = NULL,
    updated_at = excluded.updated_at
WHERE sim_recording.stopped_at IS NOT NULL;

-- name: DeleteSimRecordingSteps :exec
-- Clears a device's captured steps. StartSimRecording runs this in the SAME
-- transaction as the upsert above, and only when the upsert granted, because a
-- restart REUSES the device's one sim_recording row: without this, the second
-- recording inherits the first one's steps (AppendSimRecordingStep continues
-- from MAX(seq) and ListSimRecordingSteps is keyed by udid alone), and the flow
-- emitted from it silently contains gestures from a recording that ended
-- before it began. Re-recording is the sanctioned repair path for a flow that
-- came out wrong (spec 13.4), so that is the common case, not an edge one.
--
-- In the transaction rather than as a second round trip: a crash between the
-- two would leave a recording that reads as fresh over steps that are not.
DELETE FROM sim_recording_step WHERE udid = ?;

-- name: StopSimRecording :execrows
-- Ownership and openness are both in the predicate: a caller can only stop a
-- recording it started, and stopping an already-stopped recording is a no-op
-- the caller can see (0 rows) rather than a second stopped_at silently
-- overwriting the first.
UPDATE sim_recording
SET stopped_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE udid = sqlc.arg(udid)
  AND session_id = sqlc.arg(session_id)
  AND stopped_at IS NULL;

-- name: GetSimRecording :one
SELECT udid, session_id, name, started_at, stopped_at, updated_at
FROM sim_recording WHERE udid = ?;

-- name: AppendSimRecordingStep :one
-- The step number is assigned by the database, not the caller, and the whole
-- insert is conditional on the recording still being open, for the same
-- reason StartSimRecording is one statement: a caller that read "is it open"
-- and then inserted could append a step after another caller's StopSimRecording
-- had already closed it. RETURNING hands the assigned seq back in the same
-- round trip, and no rows (sql.ErrNoRows) is the refusal signal - no separate
-- SELECT to explain it, because there is only one reason: no recording is
-- open on this device.
-- Column lists below put selector_index LAST, matching where
-- 0039_sim_recording_step_index.sql's ALTER TABLE ADD COLUMN physically put
-- it: that keeps every explicit column list here in the table's own natural
-- order, so sqlc maps these queries onto the plain SimRecordingStep model
-- instead of minting a second, near-identical row type for one query.
INSERT INTO sim_recording_step (
    udid, seq, at, kind, selector, selector_rung, ambiguity, off_screen,
    screen_change, x, y, to_x, to_y, duration_ms, text, detail, selector_index
)
SELECT
    sqlc.arg(udid),
    COALESCE((SELECT MAX(seq) FROM sim_recording_step WHERE udid = sqlc.arg(udid)), 0) + 1,
    sqlc.arg(at),
    sqlc.arg(kind),
    sqlc.arg(selector),
    sqlc.arg(selector_rung),
    sqlc.arg(ambiguity),
    sqlc.arg(off_screen),
    sqlc.arg(screen_change),
    sqlc.arg(x),
    sqlc.arg(y),
    sqlc.arg(to_x),
    sqlc.arg(to_y),
    sqlc.arg(duration_ms),
    sqlc.arg(text),
    sqlc.arg(detail),
    sqlc.arg(selector_index)
WHERE EXISTS (
    SELECT 1 FROM sim_recording
    WHERE sim_recording.udid = sqlc.arg(udid) AND sim_recording.stopped_at IS NULL
)
RETURNING *;

-- name: ListSimRecordingSteps :many
-- Ordered by seq so the caller gets the flow back in the order it happened.
SELECT udid, seq, at, kind, selector, selector_rung, ambiguity, off_screen,
    screen_change, x, y, to_x, to_y, duration_ms, text, detail, selector_index
FROM sim_recording_step WHERE udid = ? ORDER BY seq;
