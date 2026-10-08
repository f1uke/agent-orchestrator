-- Summary: sessions record the Claude profile they launch with and whether a
-- restart is waiting for the agent to go idle.
--
-- claude_profile is the canonical name of the Claude profile (a named Claude
-- Code settings file) a claude-code session launches with; '' on a row written
-- before profiles existed, and on every non-claude-code session, means the
-- Subscription default. restart_pending is set when a profile switch asked for
-- a restart while the agent was mid-turn; the daemon restarts it once it is idle.
-- A change to either fans out session_updated so the board redraws the chip.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN claude_profile TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN restart_pending BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd
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
    OR OLD.claude_profile <> NEW.claude_profile
    OR OLD.restart_pending <> NEW.restart_pending
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END), 'keepWarmOnMerge', json(CASE WHEN NEW.keep_warm_on_merge THEN 'true' ELSE 'false' END), 'crewId', NEW.crew_id, 'crewRole', NEW.crew_role, 'sleepReason', NEW.sleep_reason, 'wokenBy', NEW.woken_by, 'claudeProfile', NEW.claude_profile, 'restartPending', json(CASE WHEN NEW.restart_pending THEN 'true' ELSE 'false' END)),
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
    OR OLD.branch <> NEW.branch
    OR OLD.pr_target <> NEW.pr_target
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END), 'keepWarmOnMerge', json(CASE WHEN NEW.keep_warm_on_merge THEN 'true' ELSE 'false' END), 'crewId', NEW.crew_id, 'crewRole', NEW.crew_role, 'sleepReason', NEW.sleep_reason, 'wokenBy', NEW.woken_by),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN restart_pending;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN claude_profile;
-- +goose StatementEnd
