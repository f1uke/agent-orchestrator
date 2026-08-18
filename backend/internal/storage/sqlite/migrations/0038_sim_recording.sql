-- One recording per device, so `udid` is the primary key exactly as sim_lease
-- does it: two recordings on one device is a state the database refuses to
-- represent rather than a rule Go remembers to check.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE sim_recording (
    udid       TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP NOT NULL,
    stopped_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- Steps outlive the recording being stopped: a flow is emitted from them after
-- the fact, which is why stopping sets stopped_at rather than deleting.
-- +goose StatementBegin
CREATE TABLE sim_recording_step (
    udid          TEXT NOT NULL REFERENCES sim_recording(udid) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    at            TIMESTAMP NOT NULL,
    kind          TEXT NOT NULL,
    selector      TEXT NOT NULL DEFAULT '',
    selector_rung INTEGER NOT NULL DEFAULT 0,
    ambiguity     INTEGER NOT NULL DEFAULT 0,
    off_screen    INTEGER NOT NULL DEFAULT 0,
    screen_change INTEGER NOT NULL DEFAULT 0,
    x             REAL NOT NULL DEFAULT 0,
    y             REAL NOT NULL DEFAULT 0,
    to_x          REAL NOT NULL DEFAULT 0,
    to_y          REAL NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    text          TEXT NOT NULL DEFAULT '',
    detail        TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (udid, seq)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sim_recording_step;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS sim_recording;
-- +goose StatementEnd
