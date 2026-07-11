-- +goose Up
-- is_suspended marks a session whose tmux runtime was torn down by the idle
-- sweep to free machine resources, but which STAYS ON THE BOARD in its current
-- lane (it is NOT terminated and does NOT archive to the Done bar) with its
-- worktree kept on disk. It is deliberately ORTHOGONAL to is_terminated: status
-- derivation never reads it, so attentionZone keeps the card in its real lane
-- and the flag only drives a "paused — click to resume" affordance. Opening the
-- session resumes it in place (recreate tmux, clear the flag). Defaults to false
-- so existing rows stay valid without backfill.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN is_suspended BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- Recreate the sessions update CDC trigger so an is_suspended change also fans
-- out a session_updated event: the idle sweep flips is_suspended 0->1 while
-- activity and is_terminated stay put, which the prior trigger did not watch, so
-- the board would not hear that the card was paused until the next poll. The
-- payload gains isSuspended so the renderer can read the new state straight from
-- the event.
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
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END)),
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
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN is_suspended;
-- +goose StatementEnd
