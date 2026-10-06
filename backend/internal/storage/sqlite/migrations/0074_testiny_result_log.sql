-- Summary: every case result AO wrote to Testiny, and who asked for it.
-- Testiny cannot tell an agent's write from a person's (both use the person's
-- API key), so this log is what the Testiny tab shows as "set by qa" and what
-- the overwrite guard reads before an agent may change a case a person set.
--
-- session_id is the TASK's id (dev's, for a crew). A row goes with its run's
-- link, and with the task.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE testiny_result_log (
    id          INTEGER   PRIMARY KEY,
    session_id  TEXT      NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id      INTEGER   NOT NULL,
    case_id     INTEGER   NOT NULL,
    status      TEXT      NOT NULL CHECK (status IN ('PASSED', 'FAILED', 'BLOCKED', 'SKIPPED', 'NOTRUN')),
    comment     TEXT      NOT NULL DEFAULT '',
    set_by      TEXT      NOT NULL DEFAULT '',  -- session id of the agent that wrote it; '' = a person in the app
    sha         TEXT      NOT NULL DEFAULT '',  -- the commit the agent tested; '' from the app
    created_at  TIMESTAMP NOT NULL,
    FOREIGN KEY (session_id, run_id) REFERENCES testiny_run_link (session_id, run_id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_testiny_result_log_case ON testiny_result_log (session_id, run_id, case_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS testiny_result_log;
-- +goose StatementEnd
