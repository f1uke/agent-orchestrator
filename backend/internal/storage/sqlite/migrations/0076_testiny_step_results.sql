-- Summary: the result log also records step results, so the Testiny tab and
-- the overwrite guard know who set each step of a case.
--
-- steps is a JSON array of {"n": <step, from 1>, "status": <status>}: the
-- steps the writer asked for, '[]' for none. A write may set only steps and
-- keep the case's status, so status may now be '' (and then needs steps and
-- takes no comment). Changing a CHECK means rebuilding the table. Nothing
-- references testiny_result_log, so the rebuild cannot cascade anything away.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE testiny_result_log_new (
    id          INTEGER   PRIMARY KEY,
    session_id  TEXT      NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id      INTEGER   NOT NULL,
    case_id     INTEGER   NOT NULL,
    status      TEXT      NOT NULL CHECK (status IN ('', 'PASSED', 'FAILED', 'BLOCKED', 'SKIPPED', 'NOTRUN')),
    comment     TEXT      NOT NULL DEFAULT '',
    set_by      TEXT      NOT NULL DEFAULT '',  -- session id of the agent that wrote it; '' = a person in the app
    sha         TEXT      NOT NULL DEFAULT '',  -- the commit the agent tested; '' from the app
    created_at  TIMESTAMP NOT NULL,
    steps       TEXT      NOT NULL DEFAULT '[]' CHECK (json_valid(steps) AND json_type(steps) = 'array'),
    CHECK (status <> '' OR (steps <> '[]' AND comment = '')),
    FOREIGN KEY (session_id, run_id) REFERENCES testiny_run_link (session_id, run_id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO testiny_result_log_new (id, session_id, run_id, case_id, status, comment, set_by, sha, created_at)
SELECT id, session_id, run_id, case_id, status, comment, set_by, sha, created_at FROM testiny_result_log;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE testiny_result_log;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE testiny_result_log_new RENAME TO testiny_result_log;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_testiny_result_log_case ON testiny_result_log (session_id, run_id, case_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- A write that set only steps has no status the 0074 table can hold, so it is
-- dropped; every other row keeps its case result and loses its step results.
-- +goose StatementBegin
CREATE TABLE testiny_result_log_old (
    id          INTEGER   PRIMARY KEY,
    session_id  TEXT      NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id      INTEGER   NOT NULL,
    case_id     INTEGER   NOT NULL,
    status      TEXT      NOT NULL CHECK (status IN ('PASSED', 'FAILED', 'BLOCKED', 'SKIPPED', 'NOTRUN')),
    comment     TEXT      NOT NULL DEFAULT '',
    set_by      TEXT      NOT NULL DEFAULT '',
    sha         TEXT      NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL,
    FOREIGN KEY (session_id, run_id) REFERENCES testiny_run_link (session_id, run_id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO testiny_result_log_old (id, session_id, run_id, case_id, status, comment, set_by, sha, created_at)
SELECT id, session_id, run_id, case_id, status, comment, set_by, sha, created_at FROM testiny_result_log
WHERE status <> '';
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE testiny_result_log;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE testiny_result_log_old RENAME TO testiny_result_log;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_testiny_result_log_case ON testiny_result_log (session_id, run_id, case_id, created_at);
-- +goose StatementEnd
