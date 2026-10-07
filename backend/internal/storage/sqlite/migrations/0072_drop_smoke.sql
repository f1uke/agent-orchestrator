-- Summary: drop the smoke checklist. AO no longer keeps a manual test checklist
-- (projects that use Testiny keep their cases there), so smoke_check,
-- smoke_run, smoke_evidence and smoke_checklist_state go with their rows.
--
-- A learning excerpt classified 'smoke_report' was the human's own words that
-- AO typed into the pane, which is what 'app_send' means for every other
-- delivery, so those rows move there before the value stops existing.
-- +goose Up
-- +goose StatementBegin
UPDATE learn_excerpt SET source_class = 'app_send' WHERE source_class = 'smoke_report';
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE smoke_evidence;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE smoke_run;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE smoke_checklist_state;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE smoke_check;
-- +goose StatementEnd

-- Going back restores the empty tables. Cases, runs and evidence rows are gone,
-- and old smoke_report excerpts stay app_send: nothing records which rows they
-- were.
-- +goose Down
-- +goose StatementBegin
CREATE TABLE smoke_check (
    id               TEXT PRIMARY KEY,
    session_id       TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id       TEXT NOT NULL REFERENCES projects (id),
    seq              INTEGER NOT NULL DEFAULT 0,
    name             TEXT NOT NULL,
    why              TEXT NOT NULL DEFAULT '',
    steps            TEXT NOT NULL DEFAULT '[]',
    expected         TEXT NOT NULL DEFAULT '',
    pr_num           INTEGER NOT NULL DEFAULT 0,
    file_ref         TEXT NOT NULL DEFAULT '',
    verdict          TEXT NOT NULL DEFAULT 'pending',
    note             TEXT NOT NULL DEFAULT '',
    decided_at       TIMESTAMP,
    reported_at      TIMESTAMP,
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL,
    retired_at       TIMESTAMP,
    retired_reason   TEXT NOT NULL DEFAULT '',
    authored_by      TEXT NOT NULL DEFAULT '',
    authored_by_role TEXT NOT NULL DEFAULT ''
        CHECK (authored_by_role IN ('', 'dev', 'qa')),
    authored_at      TIMESTAMP,
    agreed_run_id    TEXT NOT NULL DEFAULT ''
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_smoke_check_session ON smoke_check (session_id, seq);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE smoke_evidence (
    id         TEXT PRIMARY KEY,
    check_id   TEXT NOT NULL REFERENCES smoke_check (id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    filename   TEXT NOT NULL DEFAULT '',
    mime       TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
    source     TEXT NOT NULL DEFAULT 'user'
        CHECK (source IN ('user', 'agent')),
    run_id     TEXT NOT NULL DEFAULT '',
    build      TEXT NOT NULL DEFAULT ''
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_smoke_evidence_check ON smoke_evidence (check_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE smoke_checklist_state (
    session_id         TEXT PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    stood_down_at      TIMESTAMP NOT NULL,
    stood_down_by      TEXT NOT NULL DEFAULT '',
    stood_down_by_role TEXT NOT NULL DEFAULT ''
        CHECK (stood_down_by_role IN ('', 'dev', 'qa')),
    reason             TEXT NOT NULL,
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE smoke_run (
    id          TEXT PRIMARY KEY,
    check_id    TEXT NOT NULL REFERENCES smoke_check (id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL DEFAULT 0,
    verdict     TEXT NOT NULL DEFAULT ''
        CHECK (verdict IN ('', 'pass', 'fail', 'skip')),
    note        TEXT NOT NULL DEFAULT '',
    sha         TEXT NOT NULL DEFAULT '',
    recorded_at TIMESTAMP,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_smoke_run_check ON smoke_run (check_id, seq);
-- +goose StatementEnd
