-- Summary: a change to `reactivated` on its own now fans out a session_updated
-- event.
--
-- Until now reactivated was only ever written in the same UPDATE as columns the
-- trigger already watched: a restore or a resume flips is_terminated or
-- is_suspended and the activity state alongside it, so the event rode on those.
--
-- A keep-warm worker whose PR merges is the first writer that changes
-- reactivated ALONE. The worker keeps running through the merge - same tmux,
-- same activity, nothing suspended - and reactivated is the only thing that
-- moves, the fact that keeps the card on the board instead of letting "every PR
-- merged" file it in Done. Without an event of its own the board learns of it
-- only if its refetch for the PR's merge happens to land after this write, and
-- otherwise shows the card in Done until something else about the session
-- changes.
--
-- The payload is unchanged: reactivated is an internal fact, not part of the
-- wire object. The event only tells the board to read the session again, and
-- the status it derives is what changed.
-- +goose Up
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
    OR OLD.reactivated <> NEW.reactivated
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END), 'keepWarmOnMerge', json(CASE WHEN NEW.keep_warm_on_merge THEN 'true' ELSE 'false' END), 'crewId', NEW.crew_id, 'crewRole', NEW.crew_role, 'sleepReason', NEW.sleep_reason, 'wokenBy', NEW.woken_by),
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
    OR OLD.crew_id <> NEW.crew_id
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END), 'keepWarmOnMerge', json(CASE WHEN NEW.keep_warm_on_merge THEN 'true' ELSE 'false' END), 'crewId', NEW.crew_id, 'crewRole', NEW.crew_role, 'sleepReason', NEW.sleep_reason, 'wokenBy', NEW.woken_by),
        NEW.updated_at);
END;
-- +goose StatementEnd
