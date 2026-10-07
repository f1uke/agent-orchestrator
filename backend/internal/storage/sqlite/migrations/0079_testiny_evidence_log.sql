-- Summary: every QA evidence file AO uploaded to Google Drive, and every link
-- it posted on a case's result in a Testiny run.
--
-- One row per event: 'uploaded' is a file rclone transferred, 'linked' a file
-- whose Drive link AO posted, with the comment that holds it. Testiny shows a
-- comment's links but not which file each names, so the Testiny tab names a
-- link's file from its 'linked' row.
--
-- session_id is the TASK's id (dev's, for a crew). A row goes with its run's
-- link, and with the task.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE testiny_evidence_log (
    id          INTEGER   PRIMARY KEY,
    session_id  TEXT      NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id      INTEGER   NOT NULL,
    event       TEXT      NOT NULL CHECK (event IN ('uploaded', 'linked')),
    file        TEXT      NOT NULL CHECK (file <> ''),
    case_id     INTEGER   NOT NULL DEFAULT 0,   -- 0 for README.md
    drive_id    TEXT      NOT NULL DEFAULT '',  -- '' for an upload whose Drive id could not be read
    comment_id  INTEGER   NOT NULL DEFAULT 0,   -- the comment a linked file's link is in
    set_by      TEXT      NOT NULL DEFAULT '',  -- session id of the agent that ran it; '' = a person in the app
    created_at  TIMESTAMP NOT NULL,
    CHECK (event <> 'linked' OR (case_id > 0 AND drive_id <> '' AND comment_id > 0)),
    FOREIGN KEY (session_id, run_id) REFERENCES testiny_run_link (session_id, run_id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_testiny_evidence_log_run ON testiny_evidence_log (run_id, event);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS testiny_evidence_log;
-- +goose StatementEnd
