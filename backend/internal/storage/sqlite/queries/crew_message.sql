-- name: InsertCrewMessage :exec
INSERT INTO crew_message (id, crew_id, project_id, from_session, to_session, subject, refused_reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountCrewMessagesOnSubject :one
-- Delivered messages one member has sent about one subject IN THE CURRENT ROUND.
-- Refused attempts are excluded: a refusal delivered nothing, so counting it
-- would spend the cap twice.
--
-- The round bound is what keeps the cap right across a qa that closed a round
-- and was brought back for another. The cap's refusal says "nothing has moved",
-- and a second round is asked for exactly when nothing has moved in the CODE -
-- new cases, a wiped device, a case that expected the wrong thing - so the same
-- commit SHA comes round again with its budget already spent. Rows from earlier
-- rounds stay exactly where they are; they simply stop being counted. A crew
-- that has never been restored passes the zero time and counts everything, which
-- is what this always did. See migration 0060.
SELECT COUNT(*) FROM crew_message
WHERE crew_id = ? AND subject = ? AND from_session = ? AND refused_reason = ''
  AND created_at >= ?;

-- name: CountCrewMessagesSince :one
-- Every delivered message inside this crew since a cutoff - the per-hour budget.
SELECT COUNT(*) FROM crew_message
WHERE crew_id = ? AND created_at >= ? AND refused_reason = '';

-- name: GetLatestCrewMessageBySender :one
SELECT id, crew_id, project_id, from_session, to_session, subject, refused_reason, created_at
FROM crew_message WHERE from_session = ? ORDER BY created_at DESC, rowid DESC LIMIT 1;
