-- +goose Up
-- crew_id / crew_role represent "these sessions belong to ONE task".
--
-- A task is one worktree and one or two long-lived sessions (dev + qa). The
-- sessions already share a worktree without any schema help: a worker's worktree
-- directory is derived from its BRANCH, not its session id, and workspace_path
-- carries no unique constraint. What is NOT representable today is the
-- RELATIONSHIP between the members, and which of them is dev — the member that
-- owns the PR and that every PR-driven nudge already goes to.
--
-- crew_id is the DEV member's session id, so the crew key and the id a human
-- types into `ao send` are the same string. crew_role is stored explicitly rather
-- than derived from `id = crew_id`: the role is a fact about the member, not
-- about which id happened to be allocated first.
--
-- Both default to '' — a SOLO session, which is every row that exists today and
-- every row a normal spawn still creates. Existing rows stay valid without
-- backfill, and every solo code path reads a zero value, which is what keeps the
-- solo lifetime (teardown, reclaim, idle sweep) byte-for-byte unchanged.
--
-- Set once when the crew is formed and never toggled, so (like task_size, and
-- unlike keep_warm_on_merge) it needs no sessions_cdc_update trigger change: no
-- client watches it and nothing in the UI reads it.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN crew_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN crew_role TEXT NOT NULL DEFAULT ''
    CHECK (crew_role IN ('', 'dev', 'qa'));
-- +goose StatementEnd

-- Exactly one dev per crew, enforced by the database rather than by remembering
-- to check in Go. Partial so the '' of every solo row is exempt: without the
-- crew_id <> '' clause every solo session would collide with every other one.
-- +goose StatementBegin
CREATE UNIQUE INDEX idx_sessions_crew_dev ON sessions (crew_id)
    WHERE crew_role = 'dev' AND crew_id <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sessions_crew_dev;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN crew_role;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN crew_id;
-- +goose StatementEnd
