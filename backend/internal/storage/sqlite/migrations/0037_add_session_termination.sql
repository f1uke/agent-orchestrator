-- +goose Up
-- The account of HOW a session reached its terminal state.
--
-- Before this, a session that stopped left exactly two facts behind:
-- activity_state='exited' and is_terminated=1. That cannot distinguish a worker
-- whose agent process ended on its own mid-task from one AO tore down on
-- purpose, which is precisely the question asked when a session disappears with
-- no work product. The answer existed at termination time — the harness hands
-- AO a reason on its end-of-session hook, and AO knows which of its own
-- operations ordered a teardown — and was simply never written down.
--
-- termination_source is who ended it: 'agent' (the harness reported its own
-- exit), 'ao' (a teardown AO initiated) or 'runtime_gone' (the reaper inferred
-- it from a missing runtime). termination_reason is the harness's own reason
-- token for 'agent', or the named AO cause ('kill', 'auto_reclaim',
-- 'daemon_shutdown', …) otherwise — so a shared teardown path stays
-- attributable to the operation behind it. termination_last_state is what the
-- session was doing immediately before it stopped, and
-- termination_transcript_path is snapshotted rather than derived on read
-- because the worktree it is derived from may be reclaimed later.
--
-- Every column is TEXT with an empty default and terminated_at is nullable, so
-- existing rows stay valid with no backfill: an empty source reads as "this
-- session ended before AO kept an account", which is honest, rather than being
-- guessed at retroactively.
--
-- No CHECK constraint on the source/reason vocabularies on purpose: a harness
-- can grow a new end reason at any time and a row that records the unfamiliar
-- token it actually reported is far more useful than a write that fails.
--
-- No sessions_cdc_update trigger change: these columns are written in the same
-- statement as the is_terminated flip that already fires CDC, so the board is
-- already told the session ended and refetches the row for the detail.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN termination_source TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN termination_reason TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN termination_last_state TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN termination_transcript_path TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN terminated_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN terminated_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN termination_transcript_path;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN termination_last_state;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN termination_reason;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN termination_source;
-- +goose StatementEnd
