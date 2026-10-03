-- Summary: learning writes lessons to Claude Code memory, not to AO-learned
-- skills (decision 11, 2026-10-04).
--
-- Claude Code's own memory (~/.claude/projects/<repo>/memory) already reaches
-- every session of a repo, worktrees included, and the backfill showed half of
-- the proposed lessons were already there. So memory becomes a source of the
-- standing-rules corpus and the destination of a project lesson.
--
-- - learn_rule_source accepts the kind 'memory'.
-- - skill_proposal becomes learn_proposal: proposals now create or update a
--   memory file (with its one-line pointer in MEMORY.md, index_line), update an
--   existing user skill, add to ~/.claude/CLAUDE.md, or are conflict cards.
--   Proposals made under the skills design target ~/.ao/learned, which no
--   longer exists, so they are dropped and their drafts reopened to be decided
--   again (none had been applied: there was no apply yet).
-- +goose Up

-- +goose StatementBegin
CREATE TABLE learn_rule_source_new (
    key          TEXT PRIMARY KEY,
    scope        TEXT NOT NULL CHECK (scope IN ('global', 'project')),
    project_id   TEXT REFERENCES projects (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL
        CHECK (kind IN ('claude_md', 'agents_md', 'skill', 'ao_prompt', 'knowledge_index', 'memory')),
    label        TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    chunks_json  TEXT NOT NULL DEFAULT '[]',
    refreshed_at TIMESTAMP NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    CHECK ((scope = 'global') = (project_id IS NULL))
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO learn_rule_source_new SELECT * FROM learn_rule_source;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE learn_rule_source;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_rule_source_new RENAME TO learn_rule_source;
-- +goose StatementEnd

-- +goose StatementBegin
-- Only decide ever sets these two statuses.
UPDATE learn_draft SET status = 'open' WHERE status IN ('consumed', 'dropped');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM learn_decided_task;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE skill_proposal_evidence;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE skill_proposal;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE learn_proposal (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id         TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    task_key           TEXT NOT NULL,
    action             TEXT NOT NULL
        CHECK (action IN ('create_memory', 'update_memory', 'update_skill', 'edit_rule_file', 'conflict')),
    target_path        TEXT NOT NULL,
    scope              TEXT NOT NULL,
    title              TEXT NOT NULL,
    rationale          TEXT NOT NULL DEFAULT '',
    base_sha256        TEXT NOT NULL DEFAULT '',
    new_content        TEXT NOT NULL DEFAULT '',
    -- The one-line pointer a new memory file adds to its MEMORY.md; written
    -- together with the file on apply.
    index_line         TEXT NOT NULL DEFAULT '',
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
-- +goose StatementBegin
CREATE UNIQUE INDEX learn_proposal_pending_target ON learn_proposal (target_path) WHERE status = 'pending';
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE learn_proposal_evidence (
    proposal_id INTEGER NOT NULL REFERENCES learn_proposal (id) ON DELETE CASCADE,
    draft_id    INTEGER NOT NULL REFERENCES learn_draft (id) ON DELETE CASCADE,
    PRIMARY KEY (proposal_id, draft_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE learn_proposal_evidence;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE learn_proposal;
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
