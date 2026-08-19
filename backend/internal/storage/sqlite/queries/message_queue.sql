-- name: InsertQueuedMessage :one
INSERT INTO session_message_queue (session_id, body, state, attempts, last_error, queued_at, expires_at, updated_at)
VALUES (?, ?, 'pending', 0, '', ?, ?, ?)
RETURNING id, session_id, body, state, attempts, last_error, queued_at, expires_at, updated_at;

-- name: ListQueuedMessagesBySession :many
-- Whole inbox for one session, oldest first. Ordered by the rowid, which is the
-- insertion order the delivery loop replays.
SELECT id, session_id, body, state, attempts, last_error, queued_at, expires_at, updated_at
FROM session_message_queue WHERE session_id = ? ORDER BY id;

-- name: ListPendingQueuedMessages :many
SELECT id, session_id, body, state, attempts, last_error, queued_at, expires_at, updated_at
FROM session_message_queue WHERE session_id = ? AND state = 'pending' ORDER BY id;

-- name: ListSessionsWithPendingMessages :many
SELECT DISTINCT session_id FROM session_message_queue WHERE state = 'pending' ORDER BY session_id;

-- name: CountPendingQueuedMessages :one
SELECT COUNT(*) FROM session_message_queue WHERE session_id = ? AND state = 'pending';

-- name: CountQueuedMessagesBySession :many
-- One row per session that still holds messages, with how many are pending and
-- how many are failed, for the read model's per-session badge.
SELECT session_id,
       CAST(SUM(CASE WHEN state = 'pending' THEN 1 ELSE 0 END) AS INTEGER) AS pending,
       CAST(SUM(CASE WHEN state = 'failed' THEN 1 ELSE 0 END) AS INTEGER) AS failed
FROM session_message_queue GROUP BY session_id;

-- name: ClaimQueuedMessage :execrows
-- Delivered-once guard: pending -> delivering only if the row is still pending,
-- so two deliverers racing on the same row produce exactly one winner (the
-- loser sees 0 rows affected).
UPDATE session_message_queue
SET state = 'delivering', attempts = attempts + 1, updated_at = ?
WHERE id = ? AND state = 'pending';

-- name: DeleteQueuedMessage :execrows
DELETE FROM session_message_queue WHERE id = ?;

-- name: ReleaseQueuedMessage :execrows
-- Delivery failed but is worth retrying: back to pending, keeping the attempt
-- count and recording why.
UPDATE session_message_queue
SET state = 'pending', last_error = ?, updated_at = ?
WHERE id = ? AND state = 'delivering';

-- name: FailQueuedMessage :execrows
-- Terminal: this message will never be delivered. Kept (not deleted) so the
-- reader can see it was dropped and why.
UPDATE session_message_queue
SET state = 'failed', last_error = ?, updated_at = ?
WHERE id = ?;

-- name: FailDeliveringQueuedMessages :execrows
-- Boot recovery: a row still 'delivering' was in flight when the daemon died.
-- It is failed rather than re-queued, because the runtime may already have
-- typed it into the pane and a duplicate nudge in an agent's transcript is
-- worse than a visible drop.
UPDATE session_message_queue
SET state = 'failed', last_error = ?, updated_at = ?
WHERE state = 'delivering';

-- name: ListExpiredQueuedMessages :many
SELECT id, session_id, body, state, attempts, last_error, queued_at, expires_at, updated_at
FROM session_message_queue WHERE state = 'pending' AND expires_at <= ? ORDER BY id;

-- name: DeleteExpiredQueuedMessages :execrows
DELETE FROM session_message_queue WHERE state = 'pending' AND expires_at <= ?;

-- name: DeleteQueuedMessagesBySession :execrows
DELETE FROM session_message_queue WHERE session_id = ?;

-- name: DeleteOldestPendingQueuedMessages :execrows
-- Cap enforcement: drop the N oldest still-pending messages for a session. The
-- OLDEST go because a queue over its cap is a session nobody has opened for a
-- long time, where the newest message is the one still worth reading.
DELETE FROM session_message_queue WHERE id IN (
    SELECT q.id FROM session_message_queue AS q
    WHERE q.session_id = ? AND q.state = 'pending' ORDER BY q.id LIMIT ?
);

-- name: CountQueuedMessagesForSession :one
-- Both counts for ONE session in a single indexed lookup, so the sessions read
-- model can carry them without an N+1 scan.
SELECT CAST(COALESCE(SUM(CASE WHEN state = 'pending' THEN 1 ELSE 0 END), 0) AS INTEGER) AS pending,
       CAST(COALESCE(SUM(CASE WHEN state = 'failed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS failed
FROM session_message_queue WHERE session_id = ?;
