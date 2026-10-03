-- Summary: learning's standing-rules corpus - the rules agents are already
-- told, split into atomic statements so a candidate lesson can be checked
-- against them - and the rules the human pinned as protected.
--
-- Sources are the human's CLAUDE.md and skills, each learning project's repo
-- CLAUDE.md / AGENTS.md / skills, AO's own standing prompt for the project, and
-- the knowledge INDEX. Their text is split into chunks; each chunk is atomized
-- once by a model and cached by content hash, so an unchanged chunk is never
-- sent again and a chunk shared by two files is sent once. The source text
-- itself is not stored: the files are the source of truth.
--
-- No CDC triggers: nothing on screen watches these rows yet.
-- +goose Up

-- A content-addressed cache entry: what one chunk of text was split into, and
-- what the model run cost (the daily budget sums it with learn_job). model is
-- '' for a chunk split without a model (the knowledge INDEX).
-- +goose StatementBegin
CREATE TABLE learn_rule_chunk (
    hash          TEXT PRIMARY KEY,
    model         TEXT NOT NULL DEFAULT '',
    atoms_json    TEXT NOT NULL DEFAULT '[]',
    atoms         INTEGER NOT NULL DEFAULT 0,
    rejected      INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL NOT NULL DEFAULT 0,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX learn_rule_chunk_created ON learn_rule_chunk (created_at);
-- +goose StatementEnd

-- One source as of its last refresh: the ordered chunk hashes its content
-- splits into. key is stable (scope, project, kind, path). A global source has
-- no project; a project source goes with its project.
-- +goose StatementBegin
CREATE TABLE learn_rule_source (
    key          TEXT PRIMARY KEY,
    scope        TEXT NOT NULL CHECK (scope IN ('global', 'project')),
    project_id   TEXT REFERENCES projects (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL
        CHECK (kind IN ('claude_md', 'agents_md', 'skill', 'ao_prompt', 'knowledge_index')),
    label        TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    chunks_json  TEXT NOT NULL DEFAULT '[]',
    refreshed_at TIMESTAMP NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    CHECK ((scope = 'global') = (project_id IS NULL))
);
-- +goose StatementEnd

-- A rule the human pinned. It is the human's own text, not a corpus row, so a
-- re-atomize never touches it. patterns_json holds RE2 patterns a learned skill
-- must never match. No project means every project.
-- +goose StatementBegin
CREATE TABLE learn_protected_rule (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id    TEXT REFERENCES projects (id) ON DELETE CASCADE,
    text          TEXT NOT NULL CHECK (text <> ''),
    patterns_json TEXT NOT NULL DEFAULT '[]',
    note          TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE learn_protected_rule;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE learn_rule_source;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE learn_rule_chunk;
-- +goose StatementEnd
