-- +goose Up
-- Recreate the sessions update CDC trigger so a CREW FORMING fans out a
-- session_updated event.
--
-- 0044 added crew_id/crew_role and deliberately did NOT watch them, on the
-- grounds that "no client watches it and nothing in the UI reads it". That was
-- true while the capability was switched off. It is not true now: the board
-- draws a task's crew strip from these columns, and a crew is formed by stamping
-- crew_id onto a dev row that is ALREADY on screen - a write that touches no
-- other watched column, so without this the card would keep drawing itself as a
-- solo task until something unrelated happened to it.
--
-- Only crew_id is watched. The pair is written together by one setter
-- (SetSessionCrew) and never toggled afterwards, so crew_id changing is exactly
-- the moment the relationship changes; adding crew_role would fan out no event
-- crew_id does not already carry.
--
-- The payload gains crewId and crewRole so a renderer can read the new
-- membership straight off the event, in the same shape every other field here
-- uses.
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_cdc_update;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
    OR OLD.preview_url <> NEW.preview_url
    OR OLD.preview_revision <> NEW.preview_revision
    OR OLD.is_todo <> NEW.is_todo
    OR OLD.is_suspended <> NEW.is_suspended
    OR OLD.keep_warm_on_merge <> NEW.keep_warm_on_merge
    OR OLD.crew_id <> NEW.crew_id
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END), 'keepWarmOnMerge', json(CASE WHEN NEW.keep_warm_on_merge THEN 'true' ELSE 'false' END), 'crewId', NEW.crew_id, 'crewRole', NEW.crew_role),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_cdc_update;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
    OR OLD.preview_url <> NEW.preview_url
    OR OLD.preview_revision <> NEW.preview_revision
    OR OLD.is_todo <> NEW.is_todo
    OR OLD.is_suspended <> NEW.is_suspended
    OR OLD.keep_warm_on_merge <> NEW.keep_warm_on_merge
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END), 'keepWarmOnMerge', json(CASE WHEN NEW.keep_warm_on_merge THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd
