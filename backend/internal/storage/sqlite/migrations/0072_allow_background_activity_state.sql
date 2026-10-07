-- Add 'background' to the sessions.activity_state vocabulary.
--
-- A Claude Code turn can end with the agent's own work still running: a
-- background shell, a Monitor, a background subagent. Claude Code wakes the
-- agent with a new turn as each one reports, so the agent is not waiting on the
-- human, yet the turn's Stop used to report idle and the card aged into "Needs
-- you" for as long as the work ran. 'background' is that state: the turn is
-- over, nobody is blocked, and the agent will resume by itself.
--
-- No backfill: no existing row can be known to be waiting on background work.
--
-- SQLite cannot ALTER a CHECK, so this follows 0043's pattern and rewrites the
-- stored CREATE TABLE text in sqlite_master. writable_schema edits must run
-- outside a transaction, and RESET forces an immediate schema reparse on the
-- connection.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (activity_state IN (''active'', ''idle'', ''waiting_input'', ''parked'', ''blocked'', ''exited''))',
    'CHECK (activity_state IN (''active'', ''idle'', ''waiting_input'', ''parked'', ''background'', ''blocked'', ''exited''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- A down-migration must not leave rows the CHECK rejects, so background rows
-- are folded back onto idle first: it is what their Stop reported before this
-- state existed.
-- +goose StatementBegin
UPDATE sessions SET activity_state = 'idle' WHERE activity_state = 'background';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (activity_state IN (''active'', ''idle'', ''waiting_input'', ''parked'', ''background'', ''blocked'', ''exited''))',
    'CHECK (activity_state IN (''active'', ''idle'', ''waiting_input'', ''parked'', ''blocked'', ''exited''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
