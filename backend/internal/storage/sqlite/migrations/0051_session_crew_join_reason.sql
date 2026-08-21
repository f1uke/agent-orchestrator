-- +goose Up
-- WHAT CREATED a crew member, now that membership is decided mid-task.
--
-- A qa is no longer formed at spawn. A task starts as dev alone and gains a qa
-- the first time dev touches a RUNTIME SURFACE - taking the simulator lease, or
-- pointing `ao preview` at the app (design §1.12.1). A backend-only task touches
-- neither, so it never gets a qa and never pays for one.
--
-- That makes crew membership something that CHANGES while a task runs, which is
-- what this column is for. There is exactly one transition - absent to present,
-- one way, once, never back - so one enum value on the member's own row is the
-- whole audit trail: WHEN it joined is already its created_at, and WHO it joined
-- is already crew_id/crew_role. The board turns the pair into one sentence
-- ("qa joined · dev opened the simulator"), which is what makes a card that moves
-- BACKWARD - ready-to-merge dropping to in-review as the smoke gate gains a real
-- input - legible rather than surprising.
--
-- '' = NOT RECORDED, which is dev, every solo session, and every qa created
-- before this existed. Written once with the INSERT and never toggled, so - like
-- crew_id/crew_role before 0046 - it needs no CDC trigger change: a member
-- appearing fires session_created for its own row and a sessions_cdc_update for
-- dev's crew_id, and both already carry the board what it needs to re-read.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN crew_join_reason TEXT NOT NULL DEFAULT ''
    CHECK (crew_join_reason IN ('', 'sim', 'preview', 'manual'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN crew_join_reason;
-- +goose StatementEnd
