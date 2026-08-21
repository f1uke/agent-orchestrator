-- +goose Up
-- Exactly one member per ROLE per crew - so a task gets one qa, and it keeps it.
--
-- 0044 pinned "one dev per crew" and stopped there, which was enough while the
-- only way to gain a member was formCrew: it runs on a dev that is still SOLO,
-- so it could not produce a second anything. Attaching a qa to a task that
-- already exists is a second entry point, and a check-then-create in Go is a
-- rule one racing caller can step around. This makes it a fact about the data.
--
-- It counts TERMINATED rows too, deliberately. Standing qa down is how an attach
-- is undone (its runtime dies, its row terminates, dev's shared worktree is left
-- alone - #224), and its id stays referenced by smoke_check rows, their evidence
-- directories, review_run rows and its own transcript. Letting a task grow a
-- SECOND qa row afterwards would leave all of that pointing at a stranger. The
-- way back is `ao session restore <qa-id>`: the same id, returning.
--
-- Partial on the same two clauses 0044 used, so every solo row ('' , '') is
-- exempt and no backfill is needed. This subsumes idx_sessions_crew_dev, which
-- is kept: it is a merged migration, and an explicit "one dev" pin costs nothing.
-- +goose StatementBegin
CREATE UNIQUE INDEX idx_sessions_crew_role ON sessions (crew_id, crew_role)
    WHERE crew_id <> '' AND crew_role <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sessions_crew_role;
-- +goose StatementEnd
