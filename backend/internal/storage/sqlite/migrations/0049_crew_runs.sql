-- Summary: one row per BRACKETED RUN - a build, a test suite or a device pass a
-- crew member wrapped in `ao crew run --start` / `--end`.
--
-- The bracket exists for the tree-write detector: gen_at_start and gen_at_end
-- are the worktree's write counter at each end, and equal readings are what make
-- the result trustworthy. It doubles as the only signal that can say "qa is
-- running a build" rather than merely "qa is awake" - an open row (ended_at IS
-- NULL) IS that signal, so there is no second mechanism to keep in step.
--
-- outcome is the DETECTOR's word (trusted | discarded | uncertified) and result
-- is what the build or test said (pass | fail). They are separate columns
-- because they answer different questions, and a discarded run must never be
-- rendered as the result it reported.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE crew_run (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id      TEXT NOT NULL,
    crew_id         TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL DEFAULT '',
    worktree_path   TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL,
    label           TEXT NOT NULL DEFAULT '',
    attempt         INTEGER NOT NULL DEFAULT 1,
    detector        TEXT NOT NULL DEFAULT 'live',   -- live | down
    detector_reason TEXT NOT NULL DEFAULT '',
    gen_at_start    INTEGER NOT NULL DEFAULT 0,
    gen_at_end      INTEGER NOT NULL DEFAULT 0,
    started_at      TIMESTAMP NOT NULL,
    ended_at        TIMESTAMP,
    outcome         TEXT NOT NULL DEFAULT '',       -- '' while open; trusted | discarded | uncertified
    result          TEXT NOT NULL DEFAULT '',       -- '' | pass | fail
    changed_paths   TEXT NOT NULL DEFAULT '[]',     -- JSON array of repo-relative paths
    head_sha        TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMP NOT NULL,
    updated_at      TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_crew_run_session ON crew_run (session_id, started_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
-- A run starting or ending changes what the board must draw (the "qa is running"
-- chip, and a card that has to move to NEEDS YOU after three discards), so both
-- edges fan out a session_updated event on the member's own row.
CREATE TRIGGER crew_run_cdc_insert
AFTER INSERT ON crew_run
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'crewRunId', NEW.id, 'crewRunKind', NEW.kind, 'crewRunOutcome', NEW.outcome),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER crew_run_cdc_update
AFTER UPDATE ON crew_run
WHEN OLD.outcome <> NEW.outcome OR (OLD.ended_at IS NULL AND NEW.ended_at IS NOT NULL)
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'crewRunId', NEW.id, 'crewRunKind', NEW.kind, 'crewRunOutcome', NEW.outcome),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS crew_run_cdc_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS crew_run_cdc_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS crew_run;
-- +goose StatementEnd
