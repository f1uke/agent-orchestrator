-- +goose Up
-- keep_warm_on_merge marks a WORKER that is expected to open MORE PRs after the
-- current one merges (e.g. an orchestrator-dispatched multi-slice worker). When
-- set, a PR merge that would otherwise finish the session SUSPENDS it in place
-- (card stays on the board, resumable via "Continue") instead of terminating it
-- to the Done bar (feature/merge-suspend-in-place). Default false, so an ordinary
-- single-PR worker still auto-archives to Done on merge; the flag is opt-in per
-- session (ao spawn --keep-warm, or the board card toggle). Existing rows stay
-- valid without backfill.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN keep_warm_on_merge BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- Recreate the sessions update CDC trigger so a keep_warm_on_merge change also
-- fans out a session_updated event: the board card toggle flips the flag while
-- activity/lane stay put, which the prior (0028) trigger did not watch, so other
-- clients would not hear the toggle until the next poll. The payload gains
-- keepWarmOnMerge so the renderer reads the new state straight from the event.
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
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN keep_warm_on_merge;
-- +goose StatementEnd
