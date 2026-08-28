-- NOTE: keep this file pure ASCII. sqlc 1.31's SQLite parser tracks statement
-- offsets in runes but slices the source in bytes, so one multi-byte character
-- in a comment shifts the tail of every generated query into the next one.

-- name: AssignSimDevice :execrows
-- Take a device for a session, as ONE statement. Both keys are conditional:
-- session_id is the primary key (a session already holding a device keeps the
-- one it has) and udid is UNIQUE (a device already assigned to somebody else
-- is not reassigned). 0 rows means one of those two was already true, and the
-- caller re-reads to learn which.
INSERT INTO sim_device_assignment (session_id, udid, assigned_at)
VALUES (?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetSimDeviceAssignment :one
SELECT session_id, udid, assigned_at FROM sim_device_assignment WHERE session_id = ?;

-- name: ListSimDeviceAssignments :many
-- Every row is live: the session-terminate trigger deletes them, so there is no
-- expiry to evaluate here. Ordered by udid so the listing is stable.
SELECT session_id, udid, assigned_at FROM sim_device_assignment ORDER BY udid;

-- name: ReleaseSimDeviceAssignment :execrows
DELETE FROM sim_device_assignment WHERE session_id = ?;
