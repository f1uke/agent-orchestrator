-- NOTE: keep this file pure ASCII. sqlc 1.31's SQLite parser tracks statement
-- offsets in runes but slices the source in bytes, so one multi-byte character
-- in a comment shifts the tail of every generated query into the next one.

-- name: AcquireSimLease :execrows
-- The whole exclusion rule, as ONE statement the database evaluates atomically.
-- The udid primary key means a second row for the same device cannot exist, and
-- the DO UPDATE only fires when the row is the caller's own (a renewal) or has
-- already expired. Anything else leaves the row untouched and reports 0 rows,
-- and that 0 is the contention signal. Written as a check (SELECT) followed by
-- an act (INSERT) this would reproduce exactly the race we are here to prevent.
INSERT INTO sim_lease (udid, session_id, acquired_at, expires_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (udid) DO UPDATE SET
    session_id = excluded.session_id,
    acquired_at = excluded.acquired_at,
    expires_at = excluded.expires_at,
    updated_at = excluded.updated_at
WHERE sim_lease.session_id = excluded.session_id
   OR sim_lease.expires_at <= excluded.acquired_at;

-- name: GetSimLease :one
SELECT udid, session_id, acquired_at, expires_at, updated_at, hold_token, hold_expires_at
FROM sim_lease WHERE udid = ?;

-- name: ListLiveSimLeases :many
-- Expiry is computed on read: a stale row is not a lease. Ordered by udid so
-- the listing is stable.
SELECT udid, session_id, acquired_at, expires_at, updated_at, hold_token, hold_expires_at
FROM sim_lease WHERE expires_at > ? ORDER BY udid;

-- name: AcquireSimHold :execrows
-- The gesture hold - the finger - as ONE statement. Three facts must all hold
-- and the database checks them together: the row exists, the caller's lease is
-- live, and no live hold is in flight. A caller that already holds the lease is
-- refused just the same while a gesture is running, which is the whole point:
-- one session running two commands is exactly how a finger ends up teleporting.
-- 0 rows affected is the contention signal; the caller then reads the row to
-- learn WHICH of the three failed.
UPDATE sim_lease
SET hold_token = sqlc.arg(hold_token),
    hold_expires_at = sqlc.arg(hold_expires_at),
    updated_at = sqlc.arg(now)
WHERE udid = sqlc.arg(udid)
  AND session_id = sqlc.arg(session_id)
  AND expires_at > sqlc.arg(now)
  AND (hold_token IS NULL OR hold_expires_at <= sqlc.arg(now));

-- name: ReleaseSimHold :execrows
-- Giving the finger back, never the device: the lease row survives. The token is
-- part of the predicate so a command that already lost its hold (killed, timed
-- out and taken over) cannot release the gesture that replaced it.
UPDATE sim_lease
SET hold_token = NULL, hold_expires_at = NULL, updated_at = sqlc.arg(now)
WHERE udid = sqlc.arg(udid) AND hold_token = sqlc.arg(hold_token);

-- name: ReleaseSimLease :execrows
-- Ownership is part of the predicate: a non-holder's release is a no-op the
-- caller can report, never a silent steal.
DELETE FROM sim_lease WHERE udid = ? AND session_id = ?;
