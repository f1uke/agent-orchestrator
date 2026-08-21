-- name: InsertCrewMessage :exec
INSERT INTO crew_message (id, crew_id, project_id, from_session, to_session, subject, refused_reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountCrewMessagesOnSubject :one
-- Delivered messages one member has sent about one subject. Refused attempts are
-- excluded: a refusal delivered nothing, so counting it would spend the cap twice.
SELECT COUNT(*) FROM crew_message
WHERE crew_id = ? AND subject = ? AND from_session = ? AND refused_reason = '';

-- name: CountCrewMessagesSince :one
-- Every delivered message inside this crew since a cutoff - the per-hour budget.
SELECT COUNT(*) FROM crew_message
WHERE crew_id = ? AND created_at >= ? AND refused_reason = '';

-- name: GetLatestCrewMessageBySender :one
SELECT id, crew_id, project_id, from_session, to_session, subject, refused_reason, created_at
FROM crew_message WHERE from_session = ? ORDER BY created_at DESC, rowid DESC LIMIT 1;
