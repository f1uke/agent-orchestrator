-- +goose Up
-- A TODO is a session PREPARED BUT NOT STARTED (the board's TODO lane): no
-- branch/worktree/tmux exists yet, only the deferred spec below. is_todo=1 marks
-- it; Start materializes the row in place and clears is_todo. base_branch /
-- auto_name_branch / pr_target / created_by carry the deferred spec so Start can
-- replay it verbatim. All default to empty/false so existing rows stay valid
-- without backfill.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN is_todo BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN base_branch TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN auto_name_branch BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN pr_target TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- Recreate the sessions update CDC trigger so a is_todo change also fans out a
-- session_updated event: pressing Start flips is_todo 1->0 while activity stays
-- idle, which the prior trigger did not watch, so the board would not hear that
-- the card left the TODO lane until the next poll. The payload gains isTodo so
-- the renderer can read the new state straight from the event.
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
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN created_by;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN pr_target;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN auto_name_branch;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN base_branch;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN is_todo;
-- +goose StatementEnd
