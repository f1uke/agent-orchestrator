-- Summary: one row per WORKSPACE that has its own git worktree of the
-- project's mobile scripts store (mobileScripts.store). A task writes scripts
-- into that worktree on branch ao/<session_id> and publishes them into the
-- store's main checkout, instead of every session editing one shared checkout.
--
-- session_id is the workspace owner: a solo worker, or dev (the crew id) for a
-- crew, whose members share dev's worktree. There is no foreign key and no
-- delete-on-terminate trigger on purpose: a worktree teardown refused to remove
-- (state 'held') still holds work, and its row must outlive the session row.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE scripts_store_worktrees (
    session_id    TEXT NOT NULL PRIMARY KEY,
    project_id    TEXT NOT NULL,
    store         TEXT NOT NULL,
    path          TEXT NOT NULL,
    branch        TEXT NOT NULL,
    base_branch   TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('active', 'held', 'removed')),
    held_reason   TEXT NOT NULL DEFAULT ''
        CHECK (held_reason IN ('', 'uncommitted', 'publish_conflict', 'store_dirty_overlap', 'store_off_base', 'publish_failed')),
    held_files    TEXT NOT NULL DEFAULT '[]',   -- JSON array of paths the hold names
    uncommitted   TEXT NOT NULL DEFAULT '[]',   -- JSON array of uncommitted paths, as last read
    unpublished   INTEGER NOT NULL DEFAULT 0,   -- commits base_branch lacks, as last read
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    published_at  TIMESTAMP
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_scripts_store_worktrees_state ON scripts_store_worktrees (state);
-- +goose StatementEnd
-- +goose StatementBegin
-- What the owner's card draws (the unpublished-scripts chip) changes with the
-- state and the git facts, so those edges fan out a session_updated event on
-- the owner's row.
CREATE TRIGGER scripts_store_worktrees_cdc_insert
AFTER INSERT ON scripts_store_worktrees
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'scriptsStoreState', NEW.state),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER scripts_store_worktrees_cdc_update
AFTER UPDATE ON scripts_store_worktrees
WHEN OLD.state <> NEW.state OR OLD.held_reason <> NEW.held_reason OR OLD.held_files <> NEW.held_files
    OR OLD.uncommitted <> NEW.uncommitted OR OLD.unpublished <> NEW.unpublished
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'scriptsStoreState', NEW.state),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS scripts_store_worktrees_cdc_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS scripts_store_worktrees_cdc_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS scripts_store_worktrees;
-- +goose StatementEnd
