-- Summary: which Testiny test runs belong to a task. One row per (task, run):
-- a task can have many runs (iOS and Android, re-runs). session_id is the
-- TASK's id (dev's, for a crew), so a run linked from qa lands on the task.
--
-- Only the link is stored. Titles, cases and results are read live from
-- Testiny, so nothing here can go stale.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE testiny_run_link (
    session_id  TEXT      NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id      INTEGER   NOT NULL CHECK (run_id > 0),
    linked_by   TEXT      NOT NULL DEFAULT '',  -- session id of the agent that linked it; '' = a person in the app
    created_at  TIMESTAMP NOT NULL,
    PRIMARY KEY (session_id, run_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS testiny_run_link;
-- +goose StatementEnd
