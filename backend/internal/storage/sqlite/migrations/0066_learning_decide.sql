-- Summary: learning decide - proposals a strong model drew from a finished
-- task's drafts, checked by a second adversarial call and by code gates.
--
-- A proposal is what the human will approve or reject (slice 5): create or
-- update a skill, edit a rule file, or a conflict card when the human's new
-- words contradict a standing rule. Nothing here is applied yet. The diff is
-- computed by AO against the target as it was (base_sha256), never by the
-- model. Every row cascades with its project.
--
-- No CDC triggers yet: nothing on screen watches these rows until slice 5.
-- +goose Up

-- learn_job now also records decide and verify runs; task_key names the task
-- they decided (collect runs keep session_id instead).
-- +goose StatementBegin
ALTER TABLE learn_job ADD COLUMN kind TEXT NOT NULL DEFAULT 'collect'
    CHECK (kind IN ('collect', 'decide', 'verify'));
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_job ADD COLUMN task_key TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE skill_proposal (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id         TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    task_key           TEXT NOT NULL,
    action             TEXT NOT NULL
        CHECK (action IN ('create_skill', 'update_skill', 'edit_rule_file', 'conflict')),
    target_path        TEXT NOT NULL,
    scope              TEXT NOT NULL,
    title              TEXT NOT NULL,
    rationale          TEXT NOT NULL DEFAULT '',
    base_sha256        TEXT NOT NULL DEFAULT '',
    new_content        TEXT NOT NULL DEFAULT '',
    diff               TEXT NOT NULL DEFAULT '',
    confidence         REAL NOT NULL DEFAULT 0,
    outcome            TEXT NOT NULL DEFAULT '',
    rule_verdicts_json TEXT NOT NULL DEFAULT '[]',
    verifier_json      TEXT NOT NULL DEFAULT '{}',
    status             TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'rejected', 'applied', 'stale', 'superseded', 'dropped')),
    drop_reason        TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- Two pending proposals never share a target: a new one amends the open one.
-- +goose StatementBegin
CREATE UNIQUE INDEX skill_proposal_pending_target ON skill_proposal (target_path) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE skill_proposal_evidence (
    proposal_id INTEGER NOT NULL REFERENCES skill_proposal (id) ON DELETE CASCADE,
    draft_id    INTEGER NOT NULL REFERENCES learn_draft (id) ON DELETE CASCADE,
    PRIMARY KEY (proposal_id, draft_id)
);
-- +goose StatementEnd

-- A task decided once. Drafts collected after that start a new decision.
-- +goose StatementBegin
CREATE TABLE learn_decided_task (
    task_key   TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    outcome    TEXT NOT NULL,
    proposals  INTEGER NOT NULL DEFAULT 0,
    decided_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE learn_decided_task;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE skill_proposal_evidence;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX skill_proposal_pending_target;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE skill_proposal;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_job DROP COLUMN task_key;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_job DROP COLUMN kind;
-- +goose StatementEnd
