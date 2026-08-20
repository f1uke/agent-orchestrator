-- Summary: durable per-session inbox for messages that could not be typed into
-- a live agent pane at the moment they were sent - today only "the session is
-- suspended", i.e. the idle sweep reaped its tmux while keeping the record and
-- the worktree. The row survives a daemon restart so a message queued during a
-- long sleep is still delivered when the session comes back.
--
-- Ordering is the implicit rowid (id INTEGER PRIMARY KEY): SQLite hands out
-- max(rowid)+1, so insertion order is total and every reused value after a
-- delete is still greater than every surviving row. queued_at is for display
-- (and expiry), never for ordering, so a clock step cannot reorder an inbox.
--
-- state is the delivered-once guard: a deliverer moves pending -> delivering
-- with a conditional UPDATE (only one wins), and DELETEs the row once the
-- runtime has accepted it. A row still 'delivering' at daemon boot was in
-- flight when the process died; it is marked 'failed' rather than re-sent, so a
-- crash can never duplicate a message into an agent's transcript.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE session_message_queue (
    id         INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    state      TEXT NOT NULL DEFAULT 'pending', -- pending | delivering | failed
    attempts   INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    queued_at  TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_session_message_queue_session ON session_message_queue (session_id, state, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_session_message_queue_session;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_message_queue;
-- +goose StatementEnd
