-- Summary: one row per CHILD WORKTREE of a worker - a subagent Claude Code
-- launched with `isolation: "worktree"`, whose worktree AO created through the
-- WorktreeCreate hook, outside the worker's folder, on a branch cut from the
-- worker's HEAD.
--
-- The row is the only record of the child: Claude Code never removes a worktree
-- a hook created, so AO owns merge-back and cleanup, and a child whose work has
-- not reached the worker's branch (state running/merging/held/conflict) keeps
-- its worker from being torn down.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE session_children (
    session_id        TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id        TEXT NOT NULL,
    agent_id          TEXT NOT NULL,
    parent_agent_id   TEXT NOT NULL DEFAULT '',
    agent_type        TEXT NOT NULL DEFAULT '',
    description       TEXT NOT NULL DEFAULT '',
    branch            TEXT NOT NULL,
    target_branch     TEXT NOT NULL,
    base_sha          TEXT NOT NULL,
    base_dirty        TEXT NOT NULL DEFAULT '[]',   -- JSON array of worker paths uncommitted at cut time
    worktree_path     TEXT NOT NULL,
    state             TEXT NOT NULL
        CHECK (state IN ('running', 'merging', 'held', 'conflict', 'merged', 'removed', 'preserved')),
    merge_head_before TEXT NOT NULL DEFAULT '',
    merged_sha        TEXT NOT NULL DEFAULT '',
    commits           INTEGER NOT NULL DEFAULT 0,
    files_changed     INTEGER NOT NULL DEFAULT 0,
    detail            TEXT NOT NULL DEFAULT '',
    stop_blocks       INTEGER NOT NULL DEFAULT 0,
    notified_state    TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMP NOT NULL,
    updated_at        TIMESTAMP NOT NULL,
    finished_at       TIMESTAMP,
    PRIMARY KEY (session_id, agent_id)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_session_children_state ON session_children (state);
-- +goose StatementEnd
-- +goose StatementBegin
-- A child appearing or changing state changes what the worker's card draws, so
-- both edges fan out a session_updated event on the worker's own row.
CREATE TRIGGER session_children_cdc_insert
AFTER INSERT ON session_children
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'childAgentId', NEW.agent_id, 'childState', NEW.state),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_children_cdc_update
AFTER UPDATE ON session_children
WHEN OLD.state <> NEW.state OR OLD.description <> NEW.description OR OLD.detail <> NEW.detail
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id, 'childAgentId', NEW.agent_id, 'childState', NEW.state),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_children_cdc_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_children_cdc_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_children;
-- +goose StatementEnd
