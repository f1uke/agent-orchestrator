-- Summary: worker-authored manual smoke-test checklists, keyed per worker
-- session (mirrors the Reviews data path). smoke_check holds one row per case
-- the user plays live; smoke_evidence holds 0..N screenshot/clip refs per case
-- (the bytes live on disk under <dataDir>/evidence, not in SQLite). reported_at
-- marks when the checklist's results were reported back to the worker.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE smoke_check (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id  TEXT NOT NULL REFERENCES projects (id),
    seq         INTEGER NOT NULL DEFAULT 0,   -- 1-based position => "CHECK N"
    name        TEXT NOT NULL,
    why         TEXT NOT NULL DEFAULT '',
    steps       TEXT NOT NULL DEFAULT '[]',   -- JSON array of strings
    expected    TEXT NOT NULL DEFAULT '',
    pr_num      INTEGER NOT NULL DEFAULT 0,
    file_ref    TEXT NOT NULL DEFAULT '',
    verdict     TEXT NOT NULL DEFAULT 'pending',
    note        TEXT NOT NULL DEFAULT '',
    decided_at  TIMESTAMP,
    reported_at TIMESTAMP,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_smoke_check_session ON smoke_check (session_id, seq);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE smoke_evidence (
    id          TEXT PRIMARY KEY,
    check_id    TEXT NOT NULL REFERENCES smoke_check (id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,                 -- image | video
    filename    TEXT NOT NULL DEFAULT '',
    mime        TEXT NOT NULL DEFAULT '',
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_smoke_evidence_check ON smoke_evidence (check_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS smoke_evidence;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS smoke_check;
-- +goose StatementEnd
