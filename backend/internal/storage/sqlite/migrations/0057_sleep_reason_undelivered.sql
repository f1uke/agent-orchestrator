-- Add 'undelivered' to the sessions.sleep_reason vocabulary.
--
-- An agent that ends its OWN session while holding work that reached nobody -
-- commits in a worktree it still owns, no pull request ever opened - used to be
-- recorded as terminated, which files the task as finished and (for a crew dev)
-- reads as an ending its qa is expected to survive. It is parked instead:
-- activity_state 'parked' so the board reads needs_input, is_suspended so the
-- reaper's dead-runtime probe leaves the row alone, and the worktree kept.
--
-- That park needs a truthful reason. 'idle' would claim the idle sweep paused it
-- and 'merged' would claim a PR landed; both are false, and sleep_reason exists
-- precisely so the several facts is_suspended carries can be told apart.
--
-- No backfill: no row has ever been written with this value, and no earlier row
-- can be reclassified into it (whether a terminated session's work had been
-- delivered is not recoverable from the row).
--
-- SQLite cannot ALTER a CHECK, so this follows 0043's pattern and surgically
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
    'CHECK (sleep_reason IN ('''', ''idle'', ''turn'', ''merged''))',
    'CHECK (sleep_reason IN ('''', ''idle'', ''turn'', ''merged'', ''undelivered''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- A down-migration must not leave rows the CHECK rejects. An undelivered park is
-- a paused session with no process, which is what 'idle' also describes, so the
-- rows fold onto it rather than being woken or terminated: opening either one
-- resumes it, so nothing a human can do changes.
-- +goose StatementBegin
UPDATE sessions SET sleep_reason = 'idle' WHERE sleep_reason = 'undelivered';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (sleep_reason IN ('''', ''idle'', ''turn'', ''merged'', ''undelivered''))',
    'CHECK (sleep_reason IN ('''', ''idle'', ''turn'', ''merged''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
