-- Summary: a change to a session's `branch` or `pr_target` on its own now fans
-- out a session_updated event.
--
-- Both used to be written only at spawn and restore, in the same UPDATE as
-- columns the trigger already watched. Two writers now change them alone: the
-- branch-follow loop moves a session onto its branch's new name after a
-- `git branch -m` in its worktree, and `ao session set-branch` / the target
-- editor change one of them while the session runs. Without an event of their
-- own the board, the header and the Summary panel keep the old name until
-- something else about the session changes.
--
-- The payload is unchanged: the event only tells the UI to read the session
-- again.
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
    OR OLD.branch <> NEW.branch
    OR OLD.pr_target <> NEW.pr_target
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
    OR OLD.reactivated <> NEW.reactivated
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END), 'keepWarmOnMerge', json(CASE WHEN NEW.keep_warm_on_merge THEN 'true' ELSE 'false' END), 'crewId', NEW.crew_id, 'crewRole', NEW.crew_role, 'sleepReason', NEW.sleep_reason, 'wokenBy', NEW.woken_by),
        NEW.updated_at);
END;
-- +goose StatementEnd

