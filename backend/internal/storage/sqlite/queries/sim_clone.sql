-- NOTE: keep this file pure ASCII. sqlc 1.31's SQLite parser tracks statement
-- offsets in runes but slices the source in bytes, so one multi-byte character
-- in a comment shifts the tail of every generated query into the next one.

-- name: InsertSimClone :execrows
-- 0 rows means the session already has a device under this label; the caller
-- reads that one back.
INSERT INTO sim_clone (udid, session_id, label, base, name, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetSimClone :one
SELECT udid, session_id, label, base, name, created_at FROM sim_clone
WHERE session_id = ? AND label = ?;

-- name: ListSimClones :many
SELECT udid, session_id, label, base, name, created_at FROM sim_clone
ORDER BY session_id, label;

-- name: ListOrphanSimClones :many
-- Clones whose session has ended, or no longer exists at all.
SELECT c.udid, c.session_id, c.label, c.base, c.name, c.created_at FROM sim_clone c
LEFT JOIN sessions s ON s.id = c.session_id
WHERE s.id IS NULL OR s.is_terminated
ORDER BY c.session_id, c.label;

-- name: DeleteSimClone :execrows
DELETE FROM sim_clone WHERE udid = ?;
