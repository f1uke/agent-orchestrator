-- Summary: learning collect - candidate lessons ("drafts") a model extracted
-- from captured turns, and the model runs that produced them.
--
-- Slice 1 (0062) keeps the human's turns as redacted excerpts. Collect hands a
-- batch of one session's uncollected excerpts to a model and keeps what it
-- found that should change how future agents work: a correction, a rule, a
-- procedure, a fact, the reason behind a decision. Nothing is proposed to the
-- human yet; a later stage consolidates drafts per task.
--
-- Every row cascades with its project, like 0062. `ao learn forget` deletes
-- these too.
--
-- No CDC triggers: nothing on screen watches these rows yet.
-- +goose Up

-- One model run. It is kept whether it succeeded or failed: the cost is real
-- either way, the daily budget sums it, and a failed run's error and stderr
-- are what `ao learn status` shows when collect stops working. rejected counts
-- the lessons the model returned that the grounding checks refused.
-- +goose StatementBegin
CREATE TABLE learn_job (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id    TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    session_id    TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL
        CHECK (state IN ('running', 'done', 'failed', 'abandoned')),
    model         TEXT NOT NULL DEFAULT '',
    turns         INTEGER NOT NULL DEFAULT 0,
    drafts        INTEGER NOT NULL DEFAULT 0,
    rejected      INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL NOT NULL DEFAULT 0,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT '',
    stderr_tail   TEXT NOT NULL DEFAULT '',
    started_at    TIMESTAMP NOT NULL,
    finished_at   TIMESTAMP
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_learn_job_started ON learn_job (started_at);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_learn_job_session ON learn_job (session_id, started_at);
-- +goose StatementEnd

-- One candidate lesson. quote is the human's own words, copied from the anchor
-- excerpt and checked in code to be a substring of it; a draft the model could
-- not ground in a human turn never gets a row. weak marks a draft whose only
-- evidence is a suggestion the human accepted, which may support a lesson but
-- never be its only basis.
-- +goose StatementBegin
CREATE TABLE learn_draft (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id        TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    session_id        TEXT NOT NULL DEFAULT '',
    task_key          TEXT NOT NULL DEFAULT '',
    job_id            INTEGER NOT NULL DEFAULT 0,
    kind              TEXT NOT NULL
        CHECK (kind IN ('correction', 'rule', 'procedure', 'fact', 'preference')),
    statement         TEXT NOT NULL,
    statement_hash    TEXT NOT NULL,
    applies_when      TEXT NOT NULL DEFAULT '',
    scope_hint        TEXT NOT NULL DEFAULT ''
        CHECK (scope_hint IN ('', 'global', 'project', 'repo')),
    confidence        REAL NOT NULL DEFAULT 0,
    quote             TEXT NOT NULL,
    anchor_excerpt_id INTEGER NOT NULL DEFAULT 0,
    evidence_json     TEXT NOT NULL DEFAULT '[]',
    agent_before      TEXT NOT NULL DEFAULT '',
    weak              INTEGER NOT NULL DEFAULT 0,
    supersedes_id     INTEGER NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'reversed', 'consumed', 'dropped')),
    created_at        TIMESTAMP NOT NULL,
    UNIQUE (session_id, statement_hash)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_learn_draft_project ON learn_draft (project_id, created_at);
-- +goose StatementEnd

-- Which excerpts a model run has already seen. Set in the same transaction
-- that stores the run's drafts, so a turn is never collected twice nor lost.
-- +goose StatementBegin
ALTER TABLE learn_excerpt ADD COLUMN collected_at TIMESTAMP;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_excerpt ADD COLUMN collected_job_id INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_learn_excerpt_uncollected ON learn_excerpt (project_id, session_id, collected_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_learn_excerpt_uncollected;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_excerpt DROP COLUMN collected_job_id;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_excerpt DROP COLUMN collected_at;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE learn_draft;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE learn_job;
-- +goose StatementEnd
