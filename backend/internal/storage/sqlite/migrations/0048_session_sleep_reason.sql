-- +goose Up
-- WHY a session is asleep, and WHAT woke it.
--
-- is_suspended was one boolean carrying two facts: "paused to free resources"
-- (the idle sweep - looking at the session SHOULD bring it back) and "not its
-- turn" (a crew member released by #225, or born asleep - looking at it must
-- NOT). The daemon could not tell them apart, so the user-open hook resumed
-- either, and a qa nobody had woken was found running twelve seconds after its
-- dev's PR merged.
--
-- sleep_reason annotates the existing state rather than adding a new one, on
-- purpose: every other reader of is_suspended (the message queue, the reaper,
-- boot reconciliation, the shutdown sweep, status derivation, and #225's Awake())
-- wants the same answer for both meanings - "there is no process here" - and
-- Awake() has to keep meaning exactly what it means to the crew exclusion.
--
-- woken_by is the audit trail: change_log recorded the is_suspended flip but not
-- the actor, so an unexplained wake could only be reasoned about from timestamps.
-- It is written only when the row being revived was actually asleep, and cleared
-- again on the next suspend, so at most one of the two columns is set at a time.
--
-- Both default to '' = NOT RECORDED, which behaves exactly as the code did
-- before. Only 'turn' changes any behaviour, which is what keeps a solo session -
-- every session on an ordinary board - byte-for-byte unchanged.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN sleep_reason TEXT NOT NULL DEFAULT ''
    CHECK (sleep_reason IN ('', 'idle', 'turn', 'merged'));
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN woken_by TEXT NOT NULL DEFAULT ''
    CHECK (woken_by IN ('', 'view', 'wake', 'restore', 'boot', 'spawn'));
-- +goose StatementEnd

-- The ONE backfill that is provable rather than guessed: a suspended crew member
-- that has never had a runtime was BORN asleep waiting for its turn (crew
-- formation inserts the row already suspended, with no handle, and only a launch
-- ever writes one). Every other suspended row is left '' - a crew member with a
-- handle could have got there by the idle sweep or by a handover, and inventing
-- an answer would be worse than the unknown that behaves as it always did.
-- +goose StatementBegin
UPDATE sessions SET sleep_reason = 'turn'
    WHERE is_suspended = 1 AND crew_id <> '' AND runtime_handle_id = '';
-- +goose StatementEnd

-- Recreate the sessions update CDC trigger so the payload carries both columns.
-- The WHEN clause is deliberately UNCHANGED: sleep_reason is only ever written in
-- the same UPDATE that sets is_suspended, and woken_by in the same UPDATE that
-- clears it, so every change to either already rides an event this trigger fires.
-- Watching them would add no event and could add a duplicate.
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
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision, 'isTodo', json(CASE WHEN NEW.is_todo THEN 'true' ELSE 'false' END), 'isSuspended', json(CASE WHEN NEW.is_suspended THEN 'true' ELSE 'false' END), 'keepWarmOnMerge', json(CASE WHEN NEW.keep_warm_on_merge THEN 'true' ELSE 'false' END), 'crewId', NEW.crew_id, 'crewRole', NEW.crew_role),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN woken_by;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN sleep_reason;
-- +goose StatementEnd
