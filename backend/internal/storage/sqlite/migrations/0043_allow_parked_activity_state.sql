-- Add 'parked' to the sessions.activity_state vocabulary.
--
-- activity_state used to collapse two different situations into waiting_input:
-- an agent BLOCKED on a permission prompt (a dialog owns the pane's keyboard),
-- and an agent that had simply finished its turn and settled at an ordinary
-- prompt. Only the first may not be typed at, and conflating them meant every
-- actionable nudge - failing CI, a review verdict, review comments, a merge
-- conflict - was suppressed for sessions that were perfectly able to act on it.
-- 'parked' is the second case: the turn is over, nothing is blocked.
--
-- No backfill, and that is the safe default. Existing rows keep the value they
-- were written with: a stale 'waiting_input' keeps the strictly more
-- conservative reading (its messages are HELD rather than typed at a possibly
-- open dialog), and every other value already means what it says. Nothing can be
-- reclassified retroactively anyway - whether a row's waiting_input was a
-- permission prompt or a parked turn is not recoverable from the row, only from
-- the next hook the session reports, which arrives within one turn of a live
-- session and is irrelevant for a dead one. 'blocked' stays in the list although
-- nothing writes it any more: rows from before it was retired still carry it.
--
-- SQLite cannot ALTER a CHECK, so this follows 0007's pattern and surgically
-- rewrites the stored CREATE TABLE text in sqlite_master. writable_schema edits
-- must run outside a transaction, and RESET forces an immediate schema reparse
-- on the connection.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (activity_state IN (''active'', ''idle'', ''waiting_input'', ''blocked'', ''exited''))',
    'CHECK (activity_state IN (''active'', ''idle'', ''waiting_input'', ''parked'', ''blocked'', ''exited''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- A down-migration must not leave rows the CHECK rejects, so parked rows are
-- folded back onto idle first: it is the closest surviving state (a finished
-- turn), and the deriver ages it to the same needs_input reading.
-- +goose StatementBegin
UPDATE sessions SET activity_state = 'idle' WHERE activity_state = 'parked';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (activity_state IN (''active'', ''idle'', ''waiting_input'', ''parked'', ''blocked'', ''exited''))',
    'CHECK (activity_state IN (''active'', ''idle'', ''waiting_input'', ''blocked'', ''exited''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
