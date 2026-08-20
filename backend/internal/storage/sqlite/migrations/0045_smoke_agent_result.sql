-- Summary: a smoke case carries a SECOND, independent result - the machine's -
-- beside the human's, and a case can be retired instead of deleted.
--
-- The two results answer different questions. A machine answers "did the steps
-- run"; a human answers "does this actually work for a person". Merging the
-- fields would declare those equivalent and would let a card read green with
-- nobody having touched it, so the agent gets its own disjoint set of columns
-- and its own evidence rows rather than writing into the user-runtime fields
-- (verdict/note/decided_at + the user's smoke_evidence rows).
--
-- Every column defaults, so every existing row stays valid without a backfill:
-- '' / NULL reads as "no machine has run this" and "not retired", which is true
-- of every checklist authored before this migration.
-- +goose Up

-- agent_verdict is deliberately NOT 'pending': the human's default means "the
-- user has not decided yet", while '' here means "no machine judgement, and
-- there may never be one" (a paint/focus/timing/feel case is the human's alone).
-- A row with agent_ran_at set and agent_verdict '' is "the machine ran it and
-- captured evidence but did not judge" - a real, distinct state.
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agent_verdict TEXT NOT NULL DEFAULT ''
    CHECK (agent_verdict IN ('', 'pass', 'fail', 'skip'));
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agent_note TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agent_ran_at TIMESTAMP;
-- +goose StatementEnd

-- agent_sha is the commit the machine ran the case against, so a recorded pass
-- can be told apart from a pass that predates the current head. Free-form
-- (never parsed) and empty when the caller could not resolve one.
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agent_sha TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- retired_at freezes a case: it stops being something the user is asked to play
-- without pretending it never existed. The row keeps its name, its steps, the
-- user's verdict/note and every evidence byte - retire is how the checklist
-- shrinks AUDITABLY, which is worth more than three cases quietly vanishing.
-- retired_reason is required by the service (non-empty) because the reason is
-- the whole trace: "now covered by tests" is the artifact.
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN retired_at TIMESTAMP;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN retired_reason TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- source keeps evidence provenance unambiguous at the ROW level, under the two
-- separate lists the API exposes. An evidence list where machine and human
-- artifacts are indistinguishable destroys the value of the list - evidence is
-- exactly what you go back to when you distrust a verdict. The 'user' default is
-- not a guess: every row that exists today was attached by a person in the
-- Tests tab, which was the only writer.
-- +goose StatementBegin
ALTER TABLE smoke_evidence ADD COLUMN source TEXT NOT NULL DEFAULT 'user'
    CHECK (source IN ('user', 'agent'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE smoke_evidence DROP COLUMN source;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN retired_reason;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN retired_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agent_sha;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agent_ran_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agent_note;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agent_verdict;
-- +goose StatementEnd
