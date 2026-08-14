-- Summary: the gesture hold - the finger - alongside the device lease.
--
-- A lease answers "which SESSION may drive this device". It cannot answer the
-- question that actually keeps the device usable: "is a gesture in flight right
-- now". Two commands from the SAME session both hold the lease legitimately, so
-- two concurrent `ao sim tap` calls would interleave into Down(A) Down(B) Up(A)
-- Up(B) - one finger that teleports, where A's release lifts B's finger. The
-- smallest unit that must be exclusive is a whole gesture (begin..end), so the
-- lease row carries a second, much shorter claim scoped to exactly that.
--
-- Taking the hold is one conditional UPDATE (queries/sim_lease.sql): the
-- predicate requires the caller's lease to be live AND no live hold to exist, so
-- 0 rows affected is the contention signal straight from the database. As with
-- the lease itself, written as a check followed by an act this would reproduce
-- precisely the race it exists to prevent.
--
-- hold_expires_at is what stops a command killed between begin and end from
-- wedging the device forever: the hold lapses on its own and is read at query
-- time. There is no sweeper, no poller and no background watcher.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE sim_lease ADD COLUMN hold_token TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sim_lease ADD COLUMN hold_expires_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sim_lease DROP COLUMN hold_expires_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sim_lease DROP COLUMN hold_token;
-- +goose StatementEnd
